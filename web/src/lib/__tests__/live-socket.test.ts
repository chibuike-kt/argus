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
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  url: string;

  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.();
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
