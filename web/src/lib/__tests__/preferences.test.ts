// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createPreferencesStore, prefsAdapter, sanitizePrefs, type Prefs } from "../preferences";
import type { Remote } from "../versioned-sync";

const noServer: Remote<Prefs> = { get: () => new Promise(() => {}), put: (d, v) => Promise.resolve({ data: d, version: v + 1 }) };

describe("preferences local cache", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  it("corrupt JSON in storage is ignored, never a crash", () => {
    localStorage.setItem("argus:prefs:v1:user:7", "{not json");
    const store = createPreferencesStore(noServer);
    expect(() => store.attach(7)).not.toThrow();
    expect(store.get()).toEqual({});
  });

  it("an oversized (70 KB) local copy is ignored", () => {
    const big = JSON.stringify({ data: { timeframe: "1h", junk: "x".repeat(140 * 1024) }, baseVersion: 1, dirty: [] });
    localStorage.setItem("argus:prefs:v1:user:7", big);
    const store = createPreferencesStore(noServer);
    store.attach(7);
    expect(store.get()).toEqual({});
  });

  it("unknown and wrongly typed keys are dropped from a cached copy (so they are never pushed into a 400 loop)", () => {
    localStorage.setItem(
      "argus:prefs:v1:user:7",
      JSON.stringify({ data: { timeframe: "1h", theme: "dark", right_panel_tab: "news", tool_rail_collapsed: "yes" }, baseVersion: 2, dirty: [] }),
    );
    const store = createPreferencesStore(noServer);
    store.attach(7);
    expect(store.get()).toEqual({ timeframe: "1h" });
  });

  it("a change is cached per user immediately and survives a reload (new store, same user)", () => {
    const a = createPreferencesStore(noServer);
    a.attach(7);
    a.set("right_panel_tab", "watchlist");
    a.setEntry("drawing_tools", "magnet", true);
    const b = createPreferencesStore(noServer);
    b.attach(7);
    expect(b.get()).toEqual({ right_panel_tab: "watchlist", drawing_tools: { magnet: true } });
  });

  it("another user's cache is never shown, and sign-out clears memory", () => {
    const a = createPreferencesStore(noServer);
    a.attach(7);
    a.set("timeframe", "1d");
    a.attach(8);
    expect(a.get()).toEqual({});
    a.attach(7);
    expect(a.get()).toEqual({ timeframe: "1d" });
    a.detach();
    expect(a.get()).toEqual({});
  });
});

describe("sanitizePrefs and the path adapter", () => {
  it("keeps a valid chart view and drops an invalid one", () => {
    const ok = { from_time: 10, to_time: 20, bars_visible: 5, bar_spacing: 6, right_offset: 0, pinned_to_latest: false };
    const p = sanitizePrefs({ chart_views: { "4:1m": ok, "BTC:1m": ok, "5:1m": { ...ok, to_time: 1 } } });
    expect(p).toEqual({ chart_views: { "4:1m": ok } });
  });

  it("paths address entries of map-valued keys", () => {
    let d: Prefs = {};
    d = prefsAdapter.set(d, "drawing_tools/magnet", true);
    d = prefsAdapter.set(d, "timeframe", "4h");
    expect(prefsAdapter.keys(d).sort()).toEqual(["drawing_tools/magnet", "timeframe"]);
    expect(prefsAdapter.get(d, "drawing_tools/magnet")).toBe(true);
    d = prefsAdapter.set(d, "drawing_tools/magnet", undefined);
    expect(d).toEqual({ timeframe: "4h" });
  });
});
