// figure-reasons.ts: why a figure is a dash, and the per-instrument feed
// line, in plain words (Phase 3). A dash without a reason is not
// acceptable: every absent figure gets one sentence a trader can act on,
// built only from fields the server sent (never a guess at the cause
// beyond what those fields show).
import type { ApiPerformancePeriod } from "@/api/types";
import { formatUTC } from "./format";
import { NO_TICK_NOTICE_AFTER_MS, type LiveStatsEntry, type LiveStatsSnapshot, type MovingField } from "./live-stats";

// PERIOD_WORDS names each performance period's exact reference offset
// (the server's fixed durations, marketdata.PerformancePeriods).
const PERIOD_WORDS: Record<ApiPerformancePeriod, string> = {
  "1D": "24 hours",
  "1W": "7 days",
  "1M": "30 days",
  "3M": "90 days",
  "6M": "180 days",
  "1Y": "365 days",
};

const PERIOD_MS: Record<ApiPerformancePeriod, number> = {
  "1D": 24 * 3600_000,
  "1W": 7 * 24 * 3600_000,
  "1M": 30 * 24 * 3600_000,
  "3M": 90 * 24 * 3600_000,
  "6M": 180 * 24 * 3600_000,
  "1Y": 365 * 24 * 3600_000,
};

// requestReason is the reason shared by every figure while the figures
// themselves are not there yet; null once they are.
function requestReason(entry: LiveStatsEntry | undefined, snapshot: LiveStatsSnapshot): string | null {
  if (!entry) return "No instrument selected";
  if (entry.rejected) return "Not available: this instrument is not enabled for live figures";
  if (entry.figures) return null;
  if (snapshot.error) return `Could not load figures: ${snapshot.error.message}`;
  return "Loading live figures";
}

// liveReason is why a live figure (last price, 24h change, 24h volume)
// is a dash, or null when it is present.
export function liveReason(field: MovingField, entry: LiveStatsEntry | undefined, snapshot: LiveStatsSnapshot): string | null {
  const r = requestReason(entry, snapshot);
  if (r) return r;
  const f = entry!.figures!;
  if (f[field] !== null) return null;
  if (f.last_price === null) return "No candle stored for this instrument yet";
  switch (field) {
    case "last_price":
      return null;
    case "change_24h":
    case "change_24h_pct": {
      if (field === "change_24h_pct" && f.change_24h !== null) return "Reference close is zero: the percent is undefined";
      const ref = f.change_24h_reference_open_time;
      const at = ref ? ` (${formatUTC(ref)})` : "";
      return `24h reference minute missing${at}: data gap or less than 24h of history stored`;
    }
    case "volume_24h": {
      const n = f.volume_24h_minutes;
      const have = n === null || n === undefined ? "Not all" : `Only ${n.toLocaleString("en-US")} of 1,440`;
      return `${have} minutes of the last 24h are stored: data gap or less than 24h of history`;
    }
  }
}

// performanceReason is why a performance tile is a dash, or null.
export function performanceReason(
  period: ApiPerformancePeriod,
  entry: LiveStatsEntry | undefined,
  snapshot: LiveStatsSnapshot,
): string | null {
  const r = requestReason(entry, snapshot);
  if (r) return r;
  const closed = entry!.closed;
  if (!closed) return snapshot.error ? `Could not load figures: ${snapshot.error.message}` : "Loading";
  if (closed.performance[period] !== null) return null;
  if (!closed.last_closed_open_time) return "No closed candle stored yet";
  // Time arithmetic (not price): the exact reference minute.
  const ref = new Date(Date.parse(closed.last_closed_open_time) - PERIOD_MS[period]).toISOString();
  return `No stored candle exactly ${PERIOD_WORDS[period]} before the latest closed minute (${formatUTC(ref)}): data gap or not enough history`;
}

// finalizedReason is why "finalized through" is a dash, or null.
export function finalizedReason(entry: LiveStatsEntry | undefined, snapshot: LiveStatsSnapshot): string | null {
  const r = requestReason(entry, snapshot);
  if (r) return r;
  if (!entry!.closed) return "Loading";
  return entry!.closed.last_closed_open_time ? null : "No closed candle stored yet";
}

// formatSpan renders an elapsed duration as a plain number with a unit
// ("12s", "4m 05s", "3h 10m"). A number, never a verdict.
export function formatSpan(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h ${String(m % 60).padStart(2, "0")}m`;
  return `${Math.floor(h / 24)}d`;
}

export interface FeedLine {
  text: string;
  // No new tick for at least NO_TICK_NOTICE_AFTER_MS, or no live feed at
  // all: the caller may mute the price. Not a freshness verdict.
  quiet: boolean;
}

// feedLine is the instrument's live feed state in plain words: the
// socket's own state first, then whether this instrument is streaming,
// then how long since its last stored tick (ingestion time against this
// browser's clock).
export function feedLine(entry: LiveStatsEntry | undefined, snapshot: LiveStatsSnapshot, now: number): FeedLine {
  if (snapshot.fatal) return { text: "Price feed stopped: sign in again", quiet: true };
  if (snapshot.connection === "failed") return { text: "Price feed stopped: sign in again", quiet: true };
  if (snapshot.connection !== "open") {
    return {
      text: snapshot.everOpen ? "Price feed disconnected, reconnecting" : "Connecting to the price feed",
      quiet: true,
    };
  }
  if (!entry) return { text: "No instrument selected", quiet: true };
  if (entry.rejected) return { text: "Not streaming: instrument not enabled", quiet: true };
  if (!entry.figures) return { text: "Waiting for the first figures", quiet: false };
  const tick = entry.figures.last_tick_at;
  if (!tick) return { text: "No candle stored yet", quiet: true };
  const age = now - Date.parse(tick);
  if (age >= NO_TICK_NOTICE_AFTER_MS) return { text: `No new ticks for ${formatSpan(age)}`, quiet: true };
  return { text: `Receiving ticks, last ${formatSpan(age)} ago`, quiet: false };
}
