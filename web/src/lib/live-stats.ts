// live-stats.ts: the ONE source of live figures for the whole page
// (product overhaul brief, Phase 3). The watchlist, the side panel and
// the chart legend all read the same entry for an instrument, so the
// legend can never show a different 24h change than the panel.
//
// Load and update path, per instrument:
//   1. GET /instruments/stats once, when a consumer first asks for the
//      instrument (the initial load and the fallback; it also carries the
//      closed-candle figures: performance tiles and "finalized through").
//   2. ONE multiplexed `subscribe_stats` on the page's one socket for
//      every instrument any consumer holds (never one per row); every
//      `stats` message after that replaces the live figures.
//   3. One REST call again to resync: after a reconnect, when a hidden
//      tab becomes visible, and shortly after a new minute starts (the
//      closed-candle figures only change when a minute closes).
//
// The browser never computes a figure: every value is a decimal string
// from the server. The only comparisons here are exact string
// comparisons (compareDecimal) to pick a colour, and time comparisons to
// keep the newer of two figures.
//
// While the tab is hidden, updates are still recorded but nothing is
// rendered; on return, one REST call resyncs and the page renders once.
import { useEffect, useMemo, useSyncExternalStore } from "react";
import { ApiClientError } from "@/api/client";
import { listInstrumentStats, MAX_STATS_IDS } from "@/api/instruments";
import type { ApiInstrumentStats, ApiLiveFigures, ApiPerformancePeriod } from "@/api/types";
import { changeDirection } from "./decimal-display";
import { getLiveSocket, type ConnectionState, type LiveStatsHandlers, type WireStatsPayload } from "./live-socket";

// [U] after this long with no new tick (last_tick_at), the feed line says
// "No new ticks for Ns". A display threshold proposed in the Phase 3
// report, not a freshness verdict: ADR-006 and appendix C leave those
// thresholds open, so this only decides when to show the number.
export const NO_TICK_NOTICE_AFTER_MS = 10_000;

// [U] display value: how long after a new minute starts the closed-candle
// figures (performance tiles, finalized through) are re-read, so the
// closing minute has been stored by then. Batched for every instrument.
export const CLOSED_FIGURES_REFRESH_DELAY_MS = 1_500;

export type MovingField = "last_price" | "change_24h" | "change_24h_pct" | "volume_24h";
const MOVING_FIELDS: readonly MovingField[] = ["last_price", "change_24h", "change_24h_pct", "volume_24h"];

// Motion is the direction of a field's last change and a counter that
// increments on every change (what a flash keys on).
export interface Motion {
  dir: "up" | "down" | null;
  seq: number;
}

export interface ClosedFigures {
  last_close: string | null;
  last_closed_open_time: string | null;
  performance: Record<ApiPerformancePeriod, string | null>;
}

export interface LiveStatsEntry {
  // The newest live figures from either source; null until the first.
  figures: ApiLiveFigures | null;
  // The closed-candle figures, from REST only; null until the first.
  closed: ClosedFigures | null;
  motion: Record<MovingField, Motion>;
  // The server refused this instrument (unknown or disabled): it will
  // never have figures.
  rejected: boolean;
}

export interface LiveStatsSnapshot {
  entries: ReadonlyMap<number, LiveStatsEntry>;
  // A REST read is in flight for an instrument that has no figures yet.
  loading: boolean;
  // The last REST read failed (kept until one succeeds).
  error: ApiClientError | undefined;
  connection: ConnectionState;
  // True once the socket has been open at least once, so "connecting"
  // can be told apart from "reconnecting".
  everOpen: boolean;
  fatal: string | null;
}

function emptyMotion(): Record<MovingField, Motion> {
  return {
    last_price: { dir: null, seq: 0 },
    change_24h: { dir: null, seq: 0 },
    change_24h_pct: { dir: null, seq: 0 },
    volume_24h: { dir: null, seq: 0 },
  };
}

function timeOf(iso: string | null): number {
  if (iso === null) return Number.NEGATIVE_INFINITY;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? Number.NEGATIVE_INFINITY : t;
}

// isOlder reports whether incoming describes an earlier state than
// current: an earlier candle (as_of), or the same candle stored earlier
// (last_tick_at). Equal is not older: the later message wins.
export function isOlder(incoming: ApiLiveFigures, current: ApiLiveFigures): boolean {
  const a = timeOf(incoming.as_of);
  const b = timeOf(current.as_of);
  if (a !== b) return a < b;
  return timeOf(incoming.last_tick_at) < timeOf(current.last_tick_at);
}

function liveOf(row: ApiLiveFigures): ApiLiveFigures {
  return {
    instrument_id: row.instrument_id,
    last_price: row.last_price,
    last_price_closed: row.last_price_closed,
    as_of: row.as_of,
    last_tick_at: row.last_tick_at,
    change_24h: row.change_24h,
    change_24h_pct: row.change_24h_pct,
    change_24h_reference_open_time: row.change_24h_reference_open_time,
    volume_24h: row.volume_24h,
    volume_24h_minutes: row.volume_24h_minutes,
  };
}

type Listener = () => void;

class LiveStatsStore {
  private refs = new Map<number, number>();
  private entries = new Map<number, LiveStatsEntry>();
  private snapshot: LiveStatsSnapshot;
  private listeners = new Set<Listener>();
  private streamed: number[] = [];
  private unsubscribeSocket: (() => void) | null = null;
  private syncQueued = false;
  private inFlight = 0;
  private error: ApiClientError | undefined;
  private connection: ConnectionState = "closed";
  private everOpen = false;
  private fatal: string | null = null;
  private paused = false;
  private dirty = false;
  private closedRefreshTimer: ReturnType<typeof setTimeout> | null = null;
  private restToken = 0;
  private handlers: LiveStatsHandlers;
  private visibilityBound = false;
  private onVisibility: (() => void) | null = null;

  constructor() {
    this.snapshot = this.buildSnapshot();
    this.handlers = {
      onStats: (p) => this.onStats(p),
      onStatsError: (id) => {
        if (id === null) return;
        const e = this.entries.get(id);
        if (e) this.entries.set(id, { ...e, rejected: true });
        this.emit();
      },
      onConnectionState: (state) => this.onConnectionState(state),
      onFatalClose: (reason) => {
        this.fatal = reason;
        this.unsubscribeSocket = null;
        this.streamed = [];
        this.emit();
      },
    };
  }

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getSnapshot = (): LiveStatsSnapshot => this.snapshot;

  // retain asks for live figures for ids until the returned function is
  // called. Reference counted: the watchlist and the panel may both hold
  // the same instrument, and it stays streamed while either does.
  retain(ids: readonly number[]): () => void {
    this.bindVisibility();
    for (const id of ids) this.refs.set(id, (this.refs.get(id) ?? 0) + 1);
    this.queueSync();
    let released = false;
    return () => {
      if (released) return;
      released = true;
      for (const id of ids) {
        const n = (this.refs.get(id) ?? 0) - 1;
        if (n <= 0) this.refs.delete(id);
        else this.refs.set(id, n);
      }
      this.queueSync();
    };
  }

  // refresh forgets what is held for ids and asks again: one REST read and
  // the stats subscription sent again. For an instrument that has just been
  // enabled (a watchlist add, D15.1): its row appeared before the server
  // enabled it, so the first read and the subscription both refused it.
  refresh(ids: readonly number[]): void {
    let held = false;
    for (const id of ids) {
      if (this.entries.delete(id)) held = true;
    }
    if (!held) return;
    this.streamed = [];
    this.queueSync();
  }

  private queueSync(): void {
    if (this.syncQueued) return;
    this.syncQueued = true;
    queueMicrotask(() => {
      this.syncQueued = false;
      this.sync();
    });
  }

  private sync(): void {
    const wanted = [...this.refs.keys()];
    const wantedSet = new Set(wanted);
    for (const id of [...this.entries.keys()]) {
      if (!wantedSet.has(id)) this.entries.delete(id);
    }
    const fresh = wanted.filter((id) => !this.entries.has(id));
    for (const id of fresh) {
      this.entries.set(id, { figures: null, closed: null, motion: emptyMotion(), rejected: false });
    }

    // The server takes at most MAX_STATS_IDS per subscribe_stats. Anything
    // past that (only possible when the selected instrument is not on a
    // full watchlist) is still read by REST on every resync.
    const streamed = wanted.slice(0, MAX_STATS_IDS);
    const same = streamed.length === this.streamed.length && streamed.every((id, i) => id === this.streamed[i]);
    if (!same && this.fatal === null) {
      this.streamed = streamed;
      if (streamed.length === 0) {
        this.unsubscribeSocket?.();
        this.unsubscribeSocket = null;
      } else {
        this.unsubscribeSocket = getLiveSocket().setStats(streamed, this.handlers);
      }
    }
    if (fresh.length > 0) void this.readRest(fresh);
    this.emit();
  }

  // readRest is one GET /instruments/stats for ids (in chunks of the
  // server's cap). A row the server leaves out is an unknown or disabled
  // instrument: it is marked rejected.
  private async readRest(ids: readonly number[]): Promise<void> {
    if (ids.length === 0) return;
    const token = ++this.restToken;
    this.inFlight++;
    this.emit();
    try {
      for (let i = 0; i < ids.length; i += MAX_STATS_IDS) {
        const chunk = ids.slice(i, i + MAX_STATS_IDS);
        const rows = await listInstrumentStats(chunk);
        const seen = new Set<number>();
        for (const row of rows) {
          seen.add(row.instrument_id);
          this.mergeRest(row);
        }
        for (const id of chunk) {
          const e = this.entries.get(id);
          if (e && !seen.has(id)) this.entries.set(id, { ...e, rejected: true });
        }
      }
      if (token === this.restToken) this.error = undefined;
    } catch (err: unknown) {
      this.error = err instanceof ApiClientError ? err : new ApiClientError(String(err), 0);
    } finally {
      this.inFlight--;
      this.emit();
    }
  }

  private resyncAll(): void {
    void this.readRest([...this.entries.keys()]);
  }

  private mergeRest(row: ApiInstrumentStats): void {
    const e = this.entries.get(row.instrument_id);
    if (!e) return; // released while the request was in flight
    const closed: ClosedFigures = {
      last_close: row.last_close,
      last_closed_open_time: row.last_closed_open_time,
      performance: row.performance,
    };
    this.entries.set(row.instrument_id, this.applyLive({ ...e, closed, rejected: false }, liveOf(row)));
  }

  private onStats(p: WireStatsPayload): void {
    const e = this.entries.get(p.instrument_id);
    if (!e) return;
    const prevAsOf = e.figures?.as_of ?? null;
    const next = this.applyLive(e, liveOf(p));
    this.entries.set(p.instrument_id, next);
    // A new minute started: the minute before it has closed, so the
    // closed-candle figures (performance, finalized through) moved.
    if (prevAsOf !== null && next.figures?.as_of !== prevAsOf) this.scheduleClosedRefresh();
    this.emit();
  }

  // applyLive keeps the newer of e.figures and incoming, and records the
  // direction of every moving field that changed.
  private applyLive(e: LiveStatsEntry, incoming: ApiLiveFigures): LiveStatsEntry {
    const cur = e.figures;
    if (cur && isOlder(incoming, cur)) return e;
    const motion = { ...e.motion };
    for (const f of MOVING_FIELDS) {
      const prev = cur?.[f] ?? null;
      const next = incoming[f];
      if (next === null) {
        motion[f] = { dir: null, seq: e.motion[f].seq };
        continue;
      }
      const dir = changeDirection(prev, next);
      if (dir) motion[f] = { dir, seq: e.motion[f].seq + 1 };
    }
    return { ...e, figures: incoming, motion };
  }

  private scheduleClosedRefresh(): void {
    if (this.closedRefreshTimer !== null) return;
    this.closedRefreshTimer = setTimeout(() => {
      this.closedRefreshTimer = null;
      this.resyncAll();
    }, CLOSED_FIGURES_REFRESH_DELAY_MS);
  }

  private onConnectionState(state: ConnectionState): void {
    const wasOpen = this.connection === "open";
    this.connection = state;
    if (state === "open") {
      // A reconnect: the server sends fresh figures on the resubscribe,
      // and one REST read brings the closed-candle figures back in line.
      if (this.everOpen && !wasOpen) this.resyncAll();
      this.everOpen = true;
    }
    this.emit();
  }

  private bindVisibility(): void {
    if (this.visibilityBound || typeof document === "undefined") return;
    this.visibilityBound = true;
    this.onVisibility = () => {
      if (document.visibilityState === "hidden") {
        this.paused = true;
        return;
      }
      this.paused = false;
      this.resyncAll();
      if (this.dirty) this.emit();
    };
    document.addEventListener("visibilitychange", this.onVisibility);
  }

  // dispose detaches the store from the document and the socket (tests).
  dispose(): void {
    if (this.onVisibility) document.removeEventListener("visibilitychange", this.onVisibility);
    if (this.closedRefreshTimer !== null) clearTimeout(this.closedRefreshTimer);
    this.unsubscribeSocket?.();
    this.listeners.clear();
  }

  private buildSnapshot(): LiveStatsSnapshot {
    let loading = false;
    if (this.inFlight > 0) {
      for (const e of this.entries.values()) {
        if (e.figures === null && !e.rejected) loading = true;
      }
    }
    return {
      entries: new Map(this.entries),
      loading,
      error: this.error,
      connection: this.connection,
      everOpen: this.everOpen,
      fatal: this.fatal,
    };
  }

  private emit(): void {
    if (this.paused) {
      this.dirty = true;
      return;
    }
    this.dirty = false;
    this.snapshot = this.buildSnapshot();
    for (const l of this.listeners) l();
  }
}

let store: LiveStatsStore | null = null;

function getStore(): LiveStatsStore {
  if (!store) store = new LiveStatsStore();
  return store;
}

// resetLiveStatsForTests gives each test file its own store.
export function resetLiveStatsForTests(): void {
  store?.dispose();
  store = null;
}

// refreshLiveStats re-reads ids and subscribes to them again (see
// LiveStatsStore.refresh); a no-op for ids nothing holds.
export function refreshLiveStats(ids: readonly number[]): void {
  store?.refresh(ids);
}

// useLiveStats holds live figures for ids while the component is mounted
// and returns the page-wide snapshot.
export function useLiveStats(ids: readonly number[]): LiveStatsSnapshot {
  const s = getStore();
  const key = ids.join(",");
  useEffect(() => {
    if (key === "") return;
    return s.retain(key.split(",").map(Number));
  }, [s, key]);
  return useSyncExternalStore(s.subscribe, s.getSnapshot, s.getSnapshot);
}

// useLiveStat is useLiveStats for one instrument (undefined: none).
export function useLiveStat(id: number | undefined): { entry: LiveStatsEntry | undefined; snapshot: LiveStatsSnapshot } {
  const ids = useMemo(() => (id === undefined ? [] : [id]), [id]);
  const snapshot = useLiveStats(ids);
  return { entry: id === undefined ? undefined : snapshot.entries.get(id), snapshot };
}

// toInstrumentStats is the entry as the REST row shape: the newest live
// figures plus the REST-only closed-candle figures. undefined until both
// a live figure set and the closed figures exist.
export function toInstrumentStats(entry: LiveStatsEntry | undefined): ApiInstrumentStats | undefined {
  if (!entry || !entry.figures || !entry.closed) return undefined;
  return { ...entry.figures, ...entry.closed };
}
