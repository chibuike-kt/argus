import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConflictError, RejectedError, VersionedSync, type Remote, type SyncState } from "../versioned-sync";
import { prefsAdapter, type Prefs } from "../preferences";

// A fake server holding one versioned preferences document, with switches
// for failure modes. It is the "other tab / other device" too: write()
// changes the stored copy behind the client's back.
class FakeServer implements Remote<Prefs> {
  doc: Prefs = {};
  version = 0;
  puts: { data: Prefs; version: number }[] = [];
  fail: "none" | "500" | "offline" | "reject" = "none";

  write(doc: Prefs) {
    this.doc = doc;
    this.version++;
  }
  async get() {
    if (this.fail === "offline") throw new TypeError("Failed to fetch");
    return { data: structuredClone(this.doc), version: this.version };
  }
  async put(data: Prefs, version: number) {
    this.puts.push({ data: structuredClone(data), version });
    if (this.fail === "offline") throw new TypeError("Failed to fetch");
    if (this.fail === "500") throw new Error("500 internal_error");
    if (this.fail === "reject") throw new RejectedError("400 invalid_argument");
    if (version !== this.version) throw new ConflictError("409");
    this.doc = structuredClone(data);
    this.version++;
    return { data: structuredClone(this.doc), version: this.version };
  }
}

function client(server: FakeServer, initial: Partial<SyncState<Prefs>> = {}) {
  const states: SyncState<Prefs>[] = [];
  const sync = new VersionedSync(prefsAdapter, server, { data: {}, baseVersion: 0, dirty: [], ...initial }, {
    debounceMs: 1000,
    maxBackoffMs: 30_000,
    onChange: (s) => states.push(s),
  });
  return { sync, states };
}

describe("VersionedSync", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("debounces: several quick changes become one write after about 1 s", async () => {
    const server = new FakeServer();
    const { sync } = client(server);
    sync.set("timeframe", "1h");
    sync.set("right_panel_tab", "watchlist");
    sync.set("timeframe", "4h");
    await vi.advanceTimersByTimeAsync(900);
    expect(server.puts).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(200);
    expect(server.puts).toHaveLength(1);
    expect(server.doc).toEqual({ timeframe: "4h", right_panel_tab: "watchlist" });
    expect(sync.state.dirty).toEqual([]);
  });

  it("two tabs, different keys: the 409 is handled and neither change is lost", async () => {
    const server = new FakeServer();
    const { sync } = client(server);
    await sync.pull();
    // The other tab changes the timeframe first...
    server.write({ timeframe: "1d" });
    // ...while this tab changes the panel tab, based on the old version.
    sync.set("right_panel_tab", "watchlist");
    await vi.advanceTimersByTimeAsync(1100);
    expect(server.puts.length).toBeGreaterThanOrEqual(2); // a 409, then the retry
    expect(server.doc).toEqual({ timeframe: "1d", right_panel_tab: "watchlist" });
    expect(sync.state.data).toEqual(server.doc);
  });

  it("two tabs, the SAME key: this tab's intentional change wins (last change per key)", async () => {
    const server = new FakeServer();
    const { sync } = client(server);
    await sync.pull();
    server.write({ timeframe: "1d" });
    sync.set("timeframe", "5m");
    await vi.advanceTimersByTimeAsync(1100);
    expect(server.doc.timeframe).toBe("5m");
  });

  it("map entries merge per entry: two tabs saving views for different charts keep both", async () => {
    const server = new FakeServer();
    const { sync } = client(server);
    await sync.pull();
    const v = { from_time: 1, to_time: 2, bars_visible: 10, bar_spacing: 6, right_offset: 0, pinned_to_latest: false };
    server.write({ chart_views: { "4:1m": v } });
    sync.set("chart_views/5:1h", { ...v, bars_visible: 20 });
    await vi.advanceTimersByTimeAsync(1100);
    expect(Object.keys(server.doc.chart_views ?? {}).sort()).toEqual(["4:1m", "5:1h"]);
  });

  it("server 500: retries with backoff, never loses the local value, succeeds when the server returns", async () => {
    const server = new FakeServer();
    const { sync } = client(server);
    server.fail = "500";
    sync.set("timeframe", "4h");
    await vi.advanceTimersByTimeAsync(1100); // attempt 1 fails
    await vi.advanceTimersByTimeAsync(1100); // retry after 1 s fails
    await vi.advanceTimersByTimeAsync(2100); // retry after 2 s fails
    expect(server.puts.length).toBe(3);
    expect(sync.state.data.timeframe).toBe("4h");
    expect(sync.state.dirty).toEqual(["timeframe"]);
    server.fail = "none";
    await vi.advanceTimersByTimeAsync(4100); // next backoff step
    expect(server.doc.timeframe).toBe("4h");
    expect(sync.state.dirty).toEqual([]);
  });

  it("offline then online: the change is held locally and pushed on nudge", async () => {
    const server = new FakeServer();
    const { sync } = client(server);
    server.fail = "offline";
    sync.set("tool_rail_collapsed", true);
    await vi.advanceTimersByTimeAsync(1100);
    expect(sync.state.data.tool_rail_collapsed).toBe(true);
    server.fail = "none";
    sync.nudge(); // the browser's "online" event
    await vi.advanceTimersByTimeAsync(10);
    expect(server.doc.tool_rail_collapsed).toBe(true);
  });

  it("a newer server copy (another device) is adopted, with this client's unsynced keys kept on top", async () => {
    const server = new FakeServer();
    server.write({ timeframe: "1d", right_panel_tab: "watchlist" });
    const { sync } = client(server, { data: { tool_rail_collapsed: true }, dirty: ["tool_rail_collapsed"] });
    await sync.pull();
    expect(sync.state.data).toEqual({ timeframe: "1d", right_panel_tab: "watchlist", tool_rail_collapsed: true });
  });

  it("an older server copy never replaces a newer local one", async () => {
    const server = new FakeServer(); // version 0
    const { sync } = client(server, { data: { timeframe: "4h" }, baseVersion: 3 });
    await sync.pull();
    expect(sync.state.data.timeframe).toBe("4h");
  });

  it("a key edited again while its write is in flight stays dirty and is sent next", async () => {
    const server = new FakeServer();
    let release!: () => void;
    const slowPut = server.put.bind(server);
    server.put = async (d, v) => {
      await new Promise<void>((r) => (release = r));
      return slowPut(d, v);
    };
    const { sync } = client(server);
    sync.set("timeframe", "1h");
    await vi.advanceTimersByTimeAsync(1100); // write #1 in flight
    sync.set("timeframe", "4h");
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(sync.state.dirty).toEqual(["timeframe"]);
    await vi.advanceTimersByTimeAsync(1100);
    release();
    await vi.advanceTimersByTimeAsync(0);
    expect(server.doc.timeframe).toBe("4h");
  });

  it("a document the server rejects (400/413) is dropped, not retried forever", async () => {
    const server = new FakeServer();
    server.write({ timeframe: "1d" });
    const { sync } = client(server);
    await sync.pull();
    server.fail = "reject";
    sync.set("timeframe", "4h");
    await vi.advanceTimersByTimeAsync(1100);
    server.fail = "none";
    await vi.advanceTimersByTimeAsync(60_000);
    expect(server.puts).toHaveLength(1);
    expect(sync.state.data.timeframe).toBe("1d"); // the server's copy
    expect(sync.state.dirty).toEqual([]);
  });
});
