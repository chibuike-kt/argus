// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getLiveSocket, resetLiveSocketForTests, type LiveCandleHandlers } from "../live-socket";

// MockWebSocket stands in for the browser's own WebSocket -- jsdom (the
// environment this file does NOT even need to opt into; see
// vitest.config.ts's own comment on the default "node" environment)
// has no real networking, and these tests are about live-socket.ts's
// own routing/singleton/gap logic, not the wire protocol itself (the
// real end-to-end wire round trip is proven in Go, by
// internal/httpapi/ws_integration_test.go, and will be proven again
// here by Playwright once Part 2D-G exist).
class MockWebSocket {
  static instances: MockWebSocket[] = [];
  static readonly OPEN = 1;
  static readonly CONNECTING = 0;
  static readonly CLOSED = 3;

  readyState = MockWebSocket.CONNECTING;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: ((event: { code: number; reason: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  url: string;

  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }

  send(data: string) {
    this.sent.push(data);
  }

  // code/reason mirror the real WebSocket close event's own fields --
  // live-socket.ts's own classifyClose reads event.reason to tell a
  // fatal/rate-limited/transient close apart (internal/hub's own
  // CloseSessionInvalid etc. are all sent this way, never by code
  // alone, since several of them share websocket.StatusPolicyViolation).
  close(code = 1000, reason = "") {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.({ code, reason });
  }

  // Test helper, not part of the real WebSocket API.
  open() {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.();
  }

  // Test helper: deliver a server message.
  receive(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }
}

function handlers(overrides: Partial<LiveCandleHandlers> = {}): LiveCandleHandlers {
  return {
    onSnapshot: vi.fn(),
    onCandle: vi.fn(),
    onCandleClosed: vi.fn(),
    onCandleCorrected: vi.fn(),
    ...overrides,
  };
}

beforeEach(() => {
  MockWebSocket.instances = [];
  vi.stubGlobal("WebSocket", MockWebSocket);
  resetLiveSocketForTests();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("getLiveSocket", () => {
  it("returns the same instance across calls -- one socket manager per page", () => {
    expect(getLiveSocket()).toBe(getLiveSocket());
  });
});

describe("LiveSocket: one WebSocket per page", () => {
  it("opens exactly one WebSocket for two different (instrument, interval) subscriptions", () => {
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers());
    socket.subscribe(2, "5m", handlers());
    expect(MockWebSocket.instances.length).toBe(1);
  });

  it("survives a React StrictMode double-invoke (subscribe, unsubscribe, subscribe) without opening a second socket", () => {
    const socket = getLiveSocket();
    const h = handlers();
    const unsubscribeFirst = socket.subscribe(1, "1m", h);
    unsubscribeFirst(); // StrictMode's own synchronous effect-cleanup-then-rerun
    socket.subscribe(1, "1m", h);

    expect(MockWebSocket.instances.length).toBe(1);
    expect(socket.subscriptionCount()).toBe(1);
  });

  it("an unsubscribe for a key that has already been replaced by a newer subscription does not remove the newer one", () => {
    // This is exactly what StrictMode's own effect ordering can
    // produce if cleanup runs after the remount's own subscribe
    // (never guaranteed either way) -- the stale cleanup must not win.
    const socket = getLiveSocket();
    const first = handlers();
    const second = handlers();
    const unsubscribeFirst = socket.subscribe(1, "1m", first);
    socket.subscribe(1, "1m", second); // replaces, same key
    unsubscribeFirst(); // stale cleanup for the FIRST handlers object

    expect(socket.subscriptionCount()).toBe(1); // still held, under `second`
  });

  it("closes the socket once the last subscription is released and stays closed", async () => {
    const socket = getLiveSocket();
    const unsubscribe = socket.subscribe(1, "1m", handlers());
    const ws = MockWebSocket.instances[0];
    ws.open();
    unsubscribe();
    // The close is debounced by one tick (see scheduleCloseIfIdle's own
    // doc comment) specifically so a same-tick resubscribe -- the
    // StrictMode case the test above proves -- can cancel it instead of
    // the socket closing and reopening. Genuinely no resubscribe
    // follows here, so it closes once that tick elapses.
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(ws.readyState).toBe(MockWebSocket.CLOSED);
    expect(MockWebSocket.instances.length).toBe(1);
  });
});

describe("LiveSocket: message routing", () => {
  it("routes snapshot/candle/candle_closed/candle_corrected to the matching subscription's own handler, by sub key", () => {
    const socket = getLiveSocket();
    const h1 = handlers();
    const h2 = handlers();
    socket.subscribe(1, "1m", h1);
    socket.subscribe(2, "5m", h2);
    const ws = MockWebSocket.instances[0];
    ws.open();

    ws.receive({ v: 1, type: "snapshot", ts: "t", sub: "1:1m", seq: 1, payload: { last_closed_open_time: null, last_closed_revision: null } });
    ws.receive({ v: 1, type: "candle", ts: "t", sub: "2:5m", seq: 1, payload: { open_time: "a" } });

    expect(h1.onSnapshot).toHaveBeenCalledTimes(1);
    expect(h1.onCandle).not.toHaveBeenCalled();
    expect(h2.onCandle).toHaveBeenCalledTimes(1);
    expect(h2.onSnapshot).not.toHaveBeenCalled();
  });

  it("a message for a sub key this socket no longer holds is dropped, not an error", () => {
    const socket = getLiveSocket();
    const h = handlers();
    const unsubscribe = socket.subscribe(1, "1m", h);
    const ws = MockWebSocket.instances[0];
    ws.open();
    unsubscribe();

    expect(() => ws.receive({ v: 1, type: "candle", ts: "t", sub: "1:1m", seq: 5, payload: {} })).not.toThrow();
    expect(h.onCandle).not.toHaveBeenCalled();
  });

  it("ignores a message with no sub field (heartbeat) without throwing", () => {
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers());
    const ws = MockWebSocket.instances[0];
    ws.open();
    expect(() => ws.receive({ v: 1, type: "heartbeat", ts: "t" })).not.toThrow();
  });
});

describe("LiveSocket: sequence gap detection", () => {
  it("a seq that is exactly lastSeq+1 is applied with no gap callback and no resubscribe", () => {
    const socket = getLiveSocket();
    const onSeqGap = vi.fn();
    const h = handlers({ onSeqGap });
    socket.subscribe(1, "1m", h);
    const ws = MockWebSocket.instances[0];
    ws.open();
    ws.sent = []; // clear the initial subscribe message

    ws.receive({ v: 1, type: "candle", ts: "t", sub: "1:1m", seq: 1, payload: {} });
    ws.receive({ v: 1, type: "candle", ts: "t", sub: "1:1m", seq: 2, payload: {} });

    expect(onSeqGap).not.toHaveBeenCalled();
    expect(ws.sent.some((m) => (JSON.parse(m) as { type: string }).type === "subscribe")).toBe(false);
  });

  it("a seq gap (not lastSeq+1) fires onSeqGap and resends subscribe -- 'refetch and resubscribe, never guess'", () => {
    const socket = getLiveSocket();
    const onSeqGap = vi.fn();
    const h = handlers({ onSeqGap });
    socket.subscribe(1, "1m", h);
    const ws = MockWebSocket.instances[0];
    ws.open();
    ws.sent = [];

    ws.receive({ v: 1, type: "candle", ts: "t", sub: "1:1m", seq: 1, payload: {} });
    ws.receive({ v: 1, type: "candle", ts: "t", sub: "1:1m", seq: 5, payload: {} }); // 2,3,4 missing

    expect(onSeqGap).toHaveBeenCalledWith(1, 5);
    const resubscribed = ws.sent.some((m) => {
      const env = JSON.parse(m) as { type: string; sub?: string };
      return env.type === "subscribe" && env.sub === "1:1m";
    });
    expect(resubscribed).toBe(true);
  });

  it("a seq that goes backward (a repeat/redelivery) is also treated as a gap", () => {
    const socket = getLiveSocket();
    const onSeqGap = vi.fn();
    const h = handlers({ onSeqGap });
    socket.subscribe(1, "1m", h);
    const ws = MockWebSocket.instances[0];
    ws.open();

    ws.receive({ v: 1, type: "candle", ts: "t", sub: "1:1m", seq: 5, payload: {} });
    ws.receive({ v: 1, type: "candle", ts: "t", sub: "1:1m", seq: 3, payload: {} });

    expect(onSeqGap).toHaveBeenCalledWith(5, 3);
  });

  // A real stream-loss recovery (internal/hub's own handleCandleStreamLoss)
  // intentionally resets the hub's own per-subscription seq counter and
  // sends a fresh snapshot at seq 1 again -- without this exemption,
  // this module would see that snapshot's own seq as "not lastSeq+1",
  // call sendSubscribe in reaction, and get ANOTHER snapshot back at
  // seq 1 again (Subscribe always resets), perceived as yet another
  // gap, forever. A snapshot is a resync point by definition: its own
  // seq is accepted as the new baseline, never treated as evidence of
  // a gap needing another resubscribe.
  it("a snapshot's own seq is never treated as a gap, even when it is lower than lastSeq (a real stream-loss recovery resets the hub's own counter)", () => {
    const socket = getLiveSocket();
    const onSeqGap = vi.fn();
    const h = handlers({ onSeqGap });
    socket.subscribe(1, "1m", h);
    const ws = MockWebSocket.instances[0];
    ws.open();
    ws.sent = [];

    ws.receive({ v: 1, type: "candle", ts: "t", sub: "1:1m", seq: 5, payload: {} });
    // The recovery snapshot: seq 1 again, lower than lastSeq (5).
    ws.receive({
      v: 1,
      type: "snapshot",
      ts: "t",
      sub: "1:1m",
      seq: 1,
      payload: { last_closed_open_time: null, last_closed_revision: null },
    });

    expect(onSeqGap).not.toHaveBeenCalled();
    const resubscribedAfterSnapshot = ws.sent.some((m) => (JSON.parse(m) as { type: string }).type === "subscribe");
    expect(resubscribedAfterSnapshot).toBe(false); // no infinite resubscribe loop

    // The snapshot's own seq (1) is now the accepted baseline -- a
    // following message at seq 2 is normal, not a gap.
    ws.receive({ v: 1, type: "candle", ts: "t", sub: "1:1m", seq: 2, payload: {} });
    expect(onSeqGap).not.toHaveBeenCalled();
  });

  // Status shares its subscription's own seq space with candle/snapshot
  // messages on the hub side (internal/hub's own sendLiveStatus/degraded
  // notice send with no seq at all, Seq 0) -- it is advisory, never part
  // of the candle state machine, so it must never trigger a resubscribe
  // either.
  it("a status message's own seq (always 0) is never treated as a gap", () => {
    const socket = getLiveSocket();
    const onSeqGap = vi.fn();
    const onStatus = vi.fn();
    const h = handlers({ onSeqGap, onStatus });
    socket.subscribe(1, "1m", h);
    const ws = MockWebSocket.instances[0];
    ws.open();
    ws.sent = [];

    ws.receive({ v: 1, type: "candle", ts: "t", sub: "1:1m", seq: 5, payload: {} });
    ws.receive({ v: 1, type: "status", ts: "t", sub: "1:1m", payload: { state: "degraded" } });

    expect(onSeqGap).not.toHaveBeenCalled();
    expect(onStatus).toHaveBeenCalledWith({ state: "degraded" });
    const resubscribed = ws.sent.some((m) => (JSON.parse(m) as { type: string }).type === "subscribe");
    expect(resubscribed).toBe(false);
  });
});

describe("LiveSocket: reconnect on open resends every held subscription", () => {
  it("every (instrument, interval) pair gets a fresh subscribe message once the socket re-opens", () => {
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers());
    socket.subscribe(2, "5m", handlers());
    const ws = MockWebSocket.instances[0];
    ws.open();

    const subscribedKeys = ws.sent
      .map((m) => JSON.parse(m) as { type: string; sub?: string })
      .filter((env) => env.type === "subscribe")
      .map((env) => env.sub);
    expect(subscribedKeys.sort()).toEqual(["1:1m", "2:5m"]);
  });
});

// Part 2E: exponential backoff with full jitter, dead-connection-via-
// missed-heartbeats, and policy-close handling. These constants mirror
// live-socket.ts's own [U] placeholders (themselves a mirror of
// internal/config/live_feed.go's own defaults) -- asserted directly
// rather than exported, since the point is proving the OBSERABLE
// schedule an owner-set config value would also have to match.
const BACKOFF_BASE_MS = 1_000;
const BACKOFF_CAP_MS = 30_000;
const HEARTBEAT_TIMEOUT_MS = 15_000 * 2.5;
const RATE_LIMITED_MIN_ATTEMPT = 5;

describe("LiveSocket: Part 2E reconnect backoff", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("a transient close reconnects after a delay uniformly distributed in [0, base] for the first attempt", () => {
    const randomSpy = vi.spyOn(Math, "random").mockReturnValue(1); // picks the max of the range
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers());
    const ws = MockWebSocket.instances[0];
    ws.open();
    ws.close(1006, ""); // an ordinary drop, no close frame reason

    vi.advanceTimersByTime(BACKOFF_BASE_MS - 1);
    expect(MockWebSocket.instances.length).toBe(1); // not yet
    vi.advanceTimersByTime(1);
    expect(MockWebSocket.instances.length).toBe(2); // reconnected at exactly base*2^0*1(random)

    randomSpy.mockRestore();
  });

  it("each further transient close doubles the attempt exponent, capped", () => {
    const randomSpy = vi.spyOn(Math, "random").mockReturnValue(1);
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers());

    let ws = MockWebSocket.instances[0];
    ws.open();
    ws.close(1006, "");
    vi.advanceTimersByTime(BACKOFF_BASE_MS); // attempt 0 -> reconnect #2

    ws = MockWebSocket.instances[1];
    ws.close(1006, ""); // never reached "open" this time -- still schedules attempt 1
    vi.advanceTimersByTime(BACKOFF_BASE_MS * 2 - 1);
    expect(MockWebSocket.instances.length).toBe(2); // not yet -- needs base*2^1
    vi.advanceTimersByTime(1);
    expect(MockWebSocket.instances.length).toBe(3);

    randomSpy.mockRestore();
  });

  it("a successful open resets the backoff curve back to attempt 0", () => {
    const randomSpy = vi.spyOn(Math, "random").mockReturnValue(1);
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers());

    let ws = MockWebSocket.instances[0];
    ws.open();
    ws.close(1006, "");
    vi.advanceTimersByTime(BACKOFF_BASE_MS); // attempt 0 consumed -> reconnect

    ws = MockWebSocket.instances[1];
    ws.open(); // success -- attempt counter resets to 0
    ws.close(1006, "");
    // If the reset did not happen, this would need base*2^1 (2000ms);
    // the reset means it is base*2^0 (1000ms) again.
    vi.advanceTimersByTime(BACKOFF_BASE_MS - 1);
    expect(MockWebSocket.instances.length).toBe(2);
    vi.advanceTimersByTime(1);
    expect(MockWebSocket.instances.length).toBe(3);

    randomSpy.mockRestore();
  });

  it("the delay never exceeds the cap even at a high attempt count", () => {
    const randomSpy = vi.spyOn(Math, "random").mockReturnValue(1);
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers());
    let ws = MockWebSocket.instances[0];
    ws.open();

    // Run through enough closes that base*2^attempt would vastly exceed
    // the cap if it were not clamped.
    for (let i = 0; i < 10; i++) {
      ws.close(1006, "");
      vi.advanceTimersByTime(BACKOFF_CAP_MS);
      ws = MockWebSocket.instances[MockWebSocket.instances.length - 1];
    }
    expect(MockWebSocket.instances.length).toBe(11); // one reconnect per close, never stalled past the cap

    randomSpy.mockRestore();
  });

  it("'session no longer valid' is fatal: no reconnect is scheduled, onFatalClose fires, connection state becomes failed", () => {
    const onFatalClose = vi.fn();
    const onConnectionState = vi.fn();
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers({ onFatalClose, onConnectionState }));
    const ws = MockWebSocket.instances[0];
    ws.open();

    ws.close(1008, "session no longer valid");

    expect(onFatalClose).toHaveBeenCalledWith("session no longer valid");
    expect(onConnectionState).toHaveBeenCalledWith("failed");
    vi.advanceTimersByTime(BACKOFF_CAP_MS * 2); // long enough for any real schedule
    expect(MockWebSocket.instances.length).toBe(1); // never reconnected
  });

  it("a rate-limited close ('too many connections') skips ahead on the backoff curve instead of retrying immediately", () => {
    const randomSpy = vi.spyOn(Math, "random").mockReturnValue(1);
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers());
    const ws = MockWebSocket.instances[0];
    ws.open();

    ws.close(1008, "too many connections");

    const expectedDelay = Math.min(BACKOFF_CAP_MS, BACKOFF_BASE_MS * 2 ** RATE_LIMITED_MIN_ATTEMPT);
    vi.advanceTimersByTime(expectedDelay - 1);
    expect(MockWebSocket.instances.length).toBe(1); // not yet -- this is well past attempt 0's own short delay
    vi.advanceTimersByTime(1);
    expect(MockWebSocket.instances.length).toBe(2);

    randomSpy.mockRestore();
  });

  it("'subscribe/unsubscribe rate exceeded' is treated the same as a rate limit, not fatal", () => {
    const onFatalClose = vi.fn();
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers({ onFatalClose }));
    const ws = MockWebSocket.instances[0];
    ws.open();
    ws.close(1008, "subscribe/unsubscribe rate exceeded");

    expect(onFatalClose).not.toHaveBeenCalled();
    vi.advanceTimersByTime(BACKOFF_CAP_MS);
    expect(MockWebSocket.instances.length).toBeGreaterThan(1); // it did eventually retry
  });

  it("no message at all within the heartbeat timeout force-closes the socket and reconnects", () => {
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers());
    const ws = MockWebSocket.instances[0];
    ws.open();

    vi.advanceTimersByTime(HEARTBEAT_TIMEOUT_MS);
    expect(ws.readyState).toBe(MockWebSocket.CLOSED); // the watchdog force-closed it

    vi.advanceTimersByTime(BACKOFF_CAP_MS); // whatever the (randomized) delay is, this is long enough
    expect(MockWebSocket.instances.length).toBe(2); // and the normal reconnect flow picked it up
  });

  it("any message (not just heartbeat) resets the watchdog, so a quiet-but-alive connection is not force-closed", () => {
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers());
    const ws = MockWebSocket.instances[0];
    ws.open();

    // A message arrives just before the timeout would otherwise fire,
    // twice in a row -- if the watchdog did not reset, the second
    // advance alone would already exceed the original timeout.
    vi.advanceTimersByTime(HEARTBEAT_TIMEOUT_MS - 100);
    ws.receive({ v: 1, type: "heartbeat", ts: "t" });
    vi.advanceTimersByTime(HEARTBEAT_TIMEOUT_MS - 100);
    ws.receive({ v: 1, type: "heartbeat", ts: "t" });

    expect(ws.readyState).toBe(MockWebSocket.OPEN); // never force-closed
    expect(MockWebSocket.instances.length).toBe(1);
  });
});

describe("LiveSocket: Part 2F status routing", () => {
  it("a status message is routed to the matching subscription's own onStatus", () => {
    const onStatus = vi.fn();
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers({ onStatus }));
    const ws = MockWebSocket.instances[0];
    ws.open();

    ws.receive({
      v: 1,
      type: "status",
      ts: "t",
      sub: "1:1m",
      seq: 1,
      payload: { state: "stale", provider: "fixture" },
    });

    expect(onStatus).toHaveBeenCalledWith({ state: "stale", provider: "fixture" });
  });

  it("a connection-level error (no sub field) reaches every currently held subscription's own onError, not just one", () => {
    const onError1 = vi.fn();
    const onError2 = vi.fn();
    const socket = getLiveSocket();
    socket.subscribe(1, "1m", handlers({ onError: onError1 }));
    socket.subscribe(2, "5m", handlers({ onError: onError2 }));
    const ws = MockWebSocket.instances[0];
    ws.open();

    ws.receive({ v: 1, type: "error", ts: "t", payload: { code: "bad_request", message: "malformed message" } });

    expect(onError1).toHaveBeenCalledWith({ code: "bad_request", message: "malformed message" });
    expect(onError2).toHaveBeenCalledWith({ code: "bad_request", message: "malformed message" });
  });
});
