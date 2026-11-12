// live-socket.ts: one WebSocket per page (live-chart brief, phase 4,
// Part 2B), shared by every chart on the page rather than one socket
// per component -- React StrictMode mounts effects twice in
// development, and even outside StrictMode, more than one chart/widget
// subscribing to live data must never open a second connection.
//
// Prices and volume are never converted here (brief rule 2): every
// payload type below keeps them as the strings the wire sends.
// Converting to a number happens at exactly one place, downstream, when
// a bar is handed to lightweight-charts (chart-surface.tsx's own
// applyLiveCandle, not built in this phase's own checkpoint A-C) -- this
// module only parses, routes, and compares.
//
// Scope note: this is Part 2B/2C's own connection and subscription
// machinery (connect, subscribe/unsubscribe, snapshot, newer-than
// comparison) plus a minimal reconnect so a dropped socket does not
// strand a subscription forever. The full exponential-backoff-with-
// jitter schedule, the dead-connection-via-missed-heartbeats detection,
// and the policy-close handling (session invalid vs. rate limited) are
// Part 2E and are not built in this checkpoint -- see the report.

export type CandleInterval = "1m" | "5m" | "15m" | "1h" | "4h" | "1d";

export interface WireCandlePayload {
  open_time: string;
  close_time: string;
  open: string;
  high: string;
  low: string;
  close: string;
  volume: string;
  revision: number;
}

export interface WireSnapshotPayload {
  last_closed_open_time: string | null;
  last_closed_revision: number | null;
  candle?: WireCandlePayload;
}

export interface WireStatusPayload {
  state: "live" | "stale" | "degraded";
  provider?: string;
  last_event_at?: string;
}

export interface WireErrorPayload {
  code: string;
  message: string;
}

type ServerMessageType =
  | "snapshot"
  | "candle"
  | "candle_closed"
  | "candle_corrected"
  | "status"
  | "heartbeat"
  | "error";

interface Envelope {
  v: number;
  type: ServerMessageType | "subscribe" | "unsubscribe";
  id?: string;
  ts: string;
  sub?: string;
  seq?: number;
  payload?: unknown;
}

// subKey matches the server's own subString format exactly
// (internal/hub's subscriptionKey.subString: "<instrument_id>:<interval>")
// -- this is how an incoming message's own `sub` field is matched back
// to the subscription that should receive it.
function subKey(instrumentId: number, interval: CandleInterval): string {
  return `${instrumentId}:${interval}`;
}

export interface LiveCandleHandlers {
  onSnapshot: (payload: WireSnapshotPayload, seq: number) => void;
  onCandle: (payload: WireCandlePayload, seq: number) => void;
  onCandleClosed: (payload: WireCandlePayload, seq: number) => void;
  onCandleCorrected: (payload: WireCandlePayload, seq: number) => void;
  // A seq gap on this subscription (not exactly lastSeq+1): the review's
  // own rule is "refetch and resubscribe, never guess" -- this callback
  // is this module's own notification that it already resubscribed
  // (getting a fresh snapshot, delivered as a normal onSnapshot call
  // right after), so the caller's only job is to know a gap happened,
  // for its own UI/logging if it wants it; the resubscribe itself is
  // automatic and already done by the time this fires.
  onSeqGap?: (lastSeq: number, gotSeq: number) => void;
  onError?: (payload: WireErrorPayload) => void;
  // Connection-level status, not the server's own `status` message
  // (that one is per-subscription data, delivered via a later phase's
  // own badge wiring) -- "connecting"/"open"/"closed" is this socket's
  // own lifecycle, which every subscriber on the page shares.
  onConnectionState?: (state: ConnectionState) => void;
}

export type ConnectionState = "connecting" | "open" | "closed";

interface Subscription {
  instrumentId: number;
  interval: CandleInterval;
  handlers: LiveCandleHandlers;
  lastSeq: number;
}

function wsURL(): string {
  const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${window.location.host}/ws`;
}

// LiveSocket owns exactly one WebSocket for the whole page. Call
// getLiveSocket() to reach the one instance -- never `new LiveSocket()`
// directly outside this module.
class LiveSocket {
  private ws: WebSocket | null = null;
  private state: ConnectionState = "closed";
  private subs = new Map<string, Subscription>();
  private connectionStateListeners = new Set<(state: ConnectionState) => void>();

  // subscribe opens the connection if this is the first subscription on
  // the whole page, and sends a `subscribe` message for this
  // (instrument, interval) pair. Calling it again for the same pair
  // (e.g. a StrictMode double-invoke, or a genuine resubscribe) replaces
  // the handlers and sends `subscribe` again -- the server's own
  // Subscribe is idempotent for an already-held key (a fresh snapshot,
  // seq reset), so this never produces a second, independent
  // subscription slot.
  subscribe(instrumentId: number, interval: CandleInterval, handlers: LiveCandleHandlers): () => void {
    // Cancel a pending idle-close (see scheduleCloseIfIdle): any new
    // subscription means the socket is wanted again, including the
    // common case this guards -- React StrictMode's own synchronous
    // unsubscribe-then-resubscribe of the very last subscription,
    // which must reuse the existing connection, not tear it down and
    // open a second one in the same tick.
    if (this.closeTimer !== null) {
      clearTimeout(this.closeTimer);
      this.closeTimer = null;
    }

    const key = subKey(instrumentId, interval);
    this.subs.set(key, { instrumentId, interval, handlers, lastSeq: 0 });
    if (handlers.onConnectionState) {
      this.connectionStateListeners.add(handlers.onConnectionState);
      handlers.onConnectionState(this.state);
    }
    this.ensureConnected();
    this.sendSubscribe(key);

    return () => this.unsubscribe(instrumentId, interval, handlers);
  }

  private unsubscribe(instrumentId: number, interval: CandleInterval, handlers: LiveCandleHandlers): void {
    const key = subKey(instrumentId, interval);
    // Only remove this exact subscription if it is still the one
    // registered under this key -- a rapid unsubscribe-then-resubscribe
    // (e.g. StrictMode's own mount/unmount/mount, or a user flicking
    // between two instruments quickly) must not let the OLD cleanup
    // remove the NEW subscription that has since replaced it.
    const current = this.subs.get(key);
    if (current && current.handlers === handlers) {
      this.subs.delete(key);
      this.send({ v: 1, type: "unsubscribe", ts: new Date().toISOString(), sub: key });
    }
    if (handlers.onConnectionState) this.connectionStateListeners.delete(handlers.onConnectionState);
    this.scheduleCloseIfIdle();
  }

  // scheduleCloseIfIdle debounces the close-on-last-unsubscribe by one
  // tick instead of closing synchronously. Closing synchronously, then
  // letting the very next subscribe() call (StrictMode's own
  // unsubscribe-then-resubscribe happens within the same synchronous
  // cleanup/effect pass) see no connection and open a brand new one, is
  // exactly the bug this guards against -- proven by
  // live-socket.test.ts's own StrictMode test, which failed against an
  // earlier, synchronous-close version of this method.
  private closeTimer: ReturnType<typeof setTimeout> | null = null;
  private scheduleCloseIfIdle(): void {
    if (this.subs.size > 0 || this.closeTimer !== null) return;
    this.closeTimer = setTimeout(() => {
      this.closeTimer = null;
      if (this.subs.size === 0) this.close();
    }, 0);
  }

  // subscriptionCount/isOpen exist for tests (singleton proof) -- never
  // used to drive UI.
  subscriptionCount(): number {
    return this.subs.size;
  }

  isOpen(): boolean {
    return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }

  private ensureConnected(): void {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return;
    }
    this.setState("connecting");
    const ws = new WebSocket(wsURL());
    this.ws = ws;
    ws.onopen = () => {
      this.setState("open");
      // Every held subscription needs a fresh subscribe once the
      // socket is actually open -- covers both the very first connect
      // and a reconnect after a drop.
      for (const key of this.subs.keys()) this.sendSubscribe(key);
    };
    ws.onmessage = (event: MessageEvent<string>) => this.handleMessage(event.data);
    ws.onclose = () => {
      if (this.ws !== ws) return; // a newer socket already replaced this one
      this.ws = null;
      this.setState("closed");
      // Minimal reconnect (Part 2E's own full backoff-with-jitter
      // schedule is not built in this checkpoint): if subscriptions are
      // still held, something still wants this connection, so try again
      // once, shortly, rather than stranding every chart on the page.
      if (this.subs.size > 0) {
        window.setTimeout(() => {
          if (this.subs.size > 0) this.ensureConnected();
        }, 1000);
      }
    };
    ws.onerror = () => {
      // onclose always follows onerror for a WebSocket; the reconnect
      // above is handled there, not here.
    };
  }

  private sendSubscribe(key: string): void {
    const sub = this.subs.get(key);
    if (!sub) return;
    this.send({
      v: 1,
      type: "subscribe",
      ts: new Date().toISOString(),
      sub: key,
      payload: { instrument_id: sub.instrumentId, interval: sub.interval },
    });
  }

  private send(env: Envelope): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    this.ws.send(JSON.stringify(env));
  }

  private close(): void {
    this.ws?.close();
    this.ws = null;
    this.setState("closed");
  }

  private setState(state: ConnectionState): void {
    this.state = state;
    for (const listener of this.connectionStateListeners) listener(state);
  }

  private handleMessage(data: string): void {
    let env: Envelope;
    try {
      env = JSON.parse(data) as Envelope;
    } catch {
      return;
    }
    if (!env.sub) return; // heartbeat and a connection-level error carry no sub
    const sub = this.subs.get(env.sub);
    if (!sub) return; // already unsubscribed; a message in flight for it is not an error

    const seq = env.seq ?? 0;
    if (sub.lastSeq !== 0 && seq !== sub.lastSeq + 1 && env.type !== "error") {
      sub.handlers.onSeqGap?.(sub.lastSeq, seq);
      // "refetch and resubscribe, never guess" (review item 6/item 3):
      // resubscribing gets a fresh snapshot, which both resyncs state
      // and resets the sequence this module tracks for the pair.
      this.sendSubscribe(env.sub);
    }
    sub.lastSeq = seq;

    switch (env.type) {
      case "snapshot":
        sub.handlers.onSnapshot(env.payload as WireSnapshotPayload, seq);
        break;
      case "candle":
        sub.handlers.onCandle(env.payload as WireCandlePayload, seq);
        break;
      case "candle_closed":
        sub.handlers.onCandleClosed(env.payload as WireCandlePayload, seq);
        break;
      case "candle_corrected":
        sub.handlers.onCandleCorrected(env.payload as WireCandlePayload, seq);
        break;
      case "error":
        sub.handlers.onError?.(env.payload as WireErrorPayload);
        break;
      case "status":
      case "heartbeat":
        // Status badge wiring (Part 2D) and heartbeat-driven dead-
        // connection detection (Part 2E) are not built in this
        // checkpoint.
        break;
    }
  }
}

let singleton: LiveSocket | null = null;

export function getLiveSocket(): LiveSocket {
  if (!singleton) singleton = new LiveSocket();
  return singleton;
}

// resetLiveSocketForTests exists only so each test file gets its own
// singleton instead of leaking a real or mock WebSocket across test
// files that import this module -- never called from app code.
export function resetLiveSocketForTests(): void {
  singleton = null;
}

export { LiveSocket };
