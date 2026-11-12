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
// Scope note: Part 2B/2C built the connection and subscription
// machinery (connect, subscribe/unsubscribe, snapshot, newer-than
// comparison) plus a minimal flat-1s reconnect. Part 2E (this file,
// now) replaces that with: exponential backoff with full jitter,
// dead-connection-via-missed-heartbeats detection (the browser's own
// onclose never fires for a half-open connection -- a NAT/proxy path
// that silently stopped delivering data), and policy-close handling
// (session invalid vs. rate-limited vs. an ordinary transient drop).

export type CandleInterval = "1m" | "5m" | "15m" | "1h" | "4h" | "1d";

// [U] -- these three mirror internal/config/live_feed.go's own
// placeholder defaults exactly (ReconnectBackoffBase/Cap,
// HeartbeatInterval); they are not a separately decided frontend
// value, and must change together with that file's own defaults if
// the owner ever sets real ones. Full jitter itself (the backend
// config's own doc comment: delay chosen uniformly in
// [0, min(cap, base*2^attempt)]) is the algorithm, not a value.
const RECONNECT_BACKOFF_BASE_MS = 1_000; // [U] mirrors ARGUS_LIVE_RECONNECT_BACKOFF_BASE
const RECONNECT_BACKOFF_CAP_MS = 30_000; // [U] mirrors ARGUS_LIVE_RECONNECT_BACKOFF_CAP
// [U] the backend sends a heartbeat (and re-validates the session)
// every ARGUS_LIVE_HEARTBEAT_INTERVAL (default 15s); this client
// treats the connection as dead -- forcing a close and the normal
// reconnect path -- if NO message at all (heartbeat or otherwise)
// arrives within 2.5x that. Not a measured value: long enough to
// absorb one slow tick without false-triggering, short enough that a
// genuinely dead half-open connection does not sit silent for minutes.
const HEARTBEAT_INTERVAL_MS = 15_000; // [U] mirrors ARGUS_LIVE_HEARTBEAT_INTERVAL
const HEARTBEAT_TIMEOUT_MS = HEARTBEAT_INTERVAL_MS * 2.5; // [U]

// classifyClose maps a close code/reason from the server (internal/hub's
// own CloseSessionInvalid/CloseSubscribeFlood/CloseSlowConsumer, and
// ws.go's own "too many connections" -- all websocket.StatusPolicyViolation,
// 1008, distinguished only by reason text, since the hub package
// deliberately keeps one shared code for all of them) to what this
// client should do next:
//  - "fatal": the session itself is invalid. Reconnecting would just
//    get closed again immediately with the same reason -- surfaced to
//    the caller instead of retried.
//  - "rate-limited": the server is enforcing a connection or message-
//    rate limit. Worth retrying eventually (the limit is not
//    permanent), but the normal schedule's own early, short delays
//    would just get closed again right away -- skip ahead on the same
//    backoff curve instead of hammering it.
//  - "transient": anything else, including a clean 1000 close (e.g. a
//    deploy) or no close frame at all (a network drop) -- the normal
//    backoff schedule from attempt 0.
type ClosePolicy = "transient" | "rate-limited" | "fatal";

function classifyClose(reason: string): ClosePolicy {
  if (reason.includes("session no longer valid")) return "fatal";
  if (reason.includes("rate exceeded") || reason.includes("too many connections") || reason.includes("slow consumer")) {
    return "rate-limited";
  }
  return "transient";
}

// RATE_LIMITED_MIN_ATTEMPT: the backoff attempt index a rate-limited
// close jumps straight to, roughly halfway up the curve to the cap
// (base * 2^5 = 32s, already above the 30s cap) -- [U], the same
// standard-practice judgment call as the constants above, not a
// measured value.
const RATE_LIMITED_MIN_ATTEMPT = 5; // [U]

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
  // The server's own per-subscription `status` message (live/stale/
  // degraded, with the provider name) -- Part 2F's status badge reads
  // this directly; it is never derived or guessed client-side.
  onStatus?: (payload: WireStatusPayload) => void;
  // Connection-level status, not the server's own `status` message
  // above -- "connecting"/"open"/"closed"/"failed" is this socket's
  // own lifecycle, which every subscriber on the page shares. "failed"
  // (Part 2E) is terminal: a fatal policy close (the session itself is
  // invalid) that this module will not auto-retry.
  onConnectionState?: (state: ConnectionState) => void;
  // A fatal policy close (Part 2E): the session itself is invalid, so
  // reconnecting would just be closed again immediately with the same
  // reason. This subscription has already been dropped by the time
  // this fires -- the caller's job is to tell the user they need to
  // sign in again, never to resubscribe.
  onFatalClose?: (reason: string) => void;
}

export type ConnectionState = "connecting" | "open" | "closed" | "failed";

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
  // reconnectAttempt backs the exponential-backoff-with-full-jitter
  // schedule (Part 2E): reset to 0 on a successful open, incremented on
  // every scheduled reconnect so the delay grows attempt over attempt,
  // capped at RECONNECT_BACKOFF_CAP_MS.
  private reconnectAttempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  // heartbeatWatchdog backs dead-connection-via-missed-heartbeats
  // detection (Part 2E): reset on every message this socket receives
  // (not just `heartbeat` -- any message proves the connection is
  // alive), force-closing the socket if it ever fires. A WebSocket's
  // own onclose does not reliably fire for a half-open connection (a
  // NAT/proxy path that silently stopped delivering data without ever
  // sending a close frame), so nothing else would ever trigger a
  // reconnect in that case.
  private heartbeatWatchdog: ReturnType<typeof setTimeout> | null = null;

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
    // A fresh connect attempt (as opposed to the socket just sitting
    // there): cancel anything already scheduled so subscribe() calling
    // this directly cannot race a pending backoff timer into opening
    // two sockets.
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.setState("connecting");
    const ws = new WebSocket(wsURL());
    this.ws = ws;
    ws.onopen = () => {
      this.reconnectAttempt = 0; // a successful open resets the backoff curve
      this.setState("open");
      this.resetHeartbeatWatchdog();
      // Every held subscription needs a fresh subscribe once the
      // socket is actually open -- covers both the very first connect
      // and a reconnect after a drop. Resubscribing gets a fresh
      // snapshot, which is this brief's own reconnect rule: "refetch
      // from the last applied closed candle, resubscribe, apply a
      // fresh snapshot" -- the refetch-from-last-applied-closed-candle
      // half is use-live-candles.ts's own checkSnapshotGap, triggered
      // by the snapshot this subscribe produces.
      for (const key of this.subs.keys()) this.sendSubscribe(key);
    };
    ws.onmessage = (event: MessageEvent<string>) => {
      this.resetHeartbeatWatchdog();
      this.handleMessage(event.data);
    };
    ws.onclose = (event: CloseEvent) => {
      if (this.ws !== ws) return; // a newer socket already replaced this one
      this.ws = null;
      this.clearHeartbeatWatchdog();
      this.setState("closed");
      if (this.subs.size === 0) return; // nothing wants this connection anymore

      const policy = classifyClose(event.reason);
      if (policy === "fatal") {
        // The session itself is invalid -- reconnecting would just get
        // closed again immediately with the same reason. Tell every
        // held subscription's own caller and stop; only a fresh sign-in
        // (a new page load) can recover from this, never a retry loop.
        const subs = [...this.subs.values()];
        this.subs.clear();
        this.setState("failed"); // while the listeners are still registered
        for (const sub of subs) {
          if (sub.handlers.onConnectionState) this.connectionStateListeners.delete(sub.handlers.onConnectionState);
        }
        for (const sub of subs) sub.handlers.onFatalClose?.(event.reason);
        return;
      }
      if (policy === "rate-limited") {
        // Respect the server's own rate limit: jump ahead on the same
        // backoff curve instead of retrying at the normal schedule's
        // short early delays, which would just be closed again right
        // away for the same reason.
        this.reconnectAttempt = Math.max(this.reconnectAttempt, RATE_LIMITED_MIN_ATTEMPT);
      }
      this.scheduleReconnect();
    };
    ws.onerror = () => {
      // onclose always follows onerror for a WebSocket; the reconnect
      // above is handled there, not here.
    };
  }

  // scheduleReconnect is the exponential-backoff-with-full-jitter
  // schedule (Part 2E, config placeholders mirrored at the top of this
  // file): delay chosen uniformly at random in
  // [0, min(cap, base*2^attempt)], so many tabs reconnecting after the
  // same server restart do not all retry in lockstep.
  private scheduleReconnect(): void {
    if (this.reconnectTimer !== null) return; // already scheduled
    const exp = RECONNECT_BACKOFF_BASE_MS * 2 ** this.reconnectAttempt;
    const delay = Math.random() * Math.min(RECONNECT_BACKOFF_CAP_MS, exp);
    this.reconnectAttempt++;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (this.subs.size > 0) this.ensureConnected();
    }, delay);
  }

  private resetHeartbeatWatchdog(): void {
    this.clearHeartbeatWatchdog();
    this.heartbeatWatchdog = setTimeout(() => {
      this.heartbeatWatchdog = null;
      // Force-close so the normal onclose -> classifyClose -> reconnect
      // path runs, exactly as if the server had closed the connection
      // itself. There is no close frame to classify here (the whole
      // point is this connection went silently half-open), so this is
      // always a "transient" reconnect, never fatal/rate-limited.
      this.ws?.close();
    }, HEARTBEAT_TIMEOUT_MS);
  }

  private clearHeartbeatWatchdog(): void {
    if (this.heartbeatWatchdog !== null) {
      clearTimeout(this.heartbeatWatchdog);
      this.heartbeatWatchdog = null;
    }
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
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.clearHeartbeatWatchdog();
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
    if (!env.sub) {
      // heartbeat carries no sub by design. A connection-level error
      // (ws.go's own malformed-request path, hub.TypeError with no Sub
      // set) also has none -- it is not about any one subscription, so
      // every subscription this socket currently holds gets it, rather
      // than the message being silently dropped because it matches no
      // single sub key.
      if (env.type === "error") {
        for (const s of this.subs.values()) s.handlers.onError?.(env.payload as WireErrorPayload);
      }
      return;
    }
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
        sub.handlers.onStatus?.(env.payload as WireStatusPayload);
        break;
      case "heartbeat":
        // Unreachable in practice: heartbeat always carries no sub, so
        // the early return above already catches it. Its only real job
        // -- resetting the heartbeat watchdog -- is done in onmessage
        // for every message, not just this type.
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
