// The chart's bottom range buttons (Phase 3): each sets the visible range
// to that span ending at the newest bar. Time arithmetic only (seconds),
// never price arithmetic.
import type { CandleInterval } from "@/lib/live-socket";
import { MAX_HISTORY_BARS } from "@/lib/use-live-candles";

export const RANGES = ["1D", "5D", "1M", "3M", "6M", "YTD", "1Y", "5Y", "All"] as const;
export type RangeKey = (typeof RANGES)[number];

const DAY = 86_400;

// Fixed day counts, the same convention as the performance tiles (1M = 30
// days, not a calendar month), so a range never depends on the month.
const RANGE_DAYS: Record<Exclude<RangeKey, "YTD" | "All">, number> = {
  "1D": 1,
  "5D": 5,
  "1M": 30,
  "3M": 90,
  "6M": 180,
  "1Y": 365,
  "5Y": 5 * 365,
};

// rangeStartSec is the first second (unix) range key should show, for a
// newest bar at lastSec and an oldest held bar at firstSec. YTD starts at
// 1 January 00:00 UTC of the newest bar's year; All is the oldest held bar
// (so "All" only ever needs what is loaded, plus older pages if any).
export function rangeStartSec(key: RangeKey, lastSec: number, firstSec: number): number {
  if (key === "All") return Number.NEGATIVE_INFINITY;
  if (key === "YTD") {
    const year = new Date(lastSec * 1000).getUTCFullYear();
    return Date.UTC(year, 0, 1) / 1000;
  }
  void firstSec;
  return lastSec - RANGE_DAYS[key] * DAY;
}

// formatShownSpan renders a span of seconds in the largest sensible unit,
// one decimal ("3.5 days", "16.7 hours", "45 minutes").
export function formatShownSpan(seconds: number): string {
  const s = Math.max(0, seconds);
  if (s >= 2 * DAY) return `${trim1(s / DAY)} days`;
  if (s >= 2 * 3600) return `${trim1(s / 3600)} hours`;
  return `${Math.round(s / 60)} minutes`;
}

function trim1(v: number): string {
  return (Math.round(v * 10) / 10).toFixed(1).replace(/\.0$/, "");
}

// describeShown is the notice under the chart after a range button, or
// null when the range is fully shown. It says plainly when stored history
// is shorter than the range ("showing 3.5 days, the most history stored")
// or the chart's own bar cap stopped the paging.
export function describeShown(
  key: RangeKey,
  outcome: "covered" | "exhausted" | "capped" | "cancelled",
  fromSec: number,
  startSec: number,
  lastSec: number,
  timeframe: CandleInterval,
): string | null {
  const shown = formatShownSpan(lastSec - fromSec);
  if (key === "All") {
    if (outcome === "capped") {
      return `Showing ${shown}, the most this chart loads at ${timeframe} (${MAX_HISTORY_BARS.toLocaleString("en-US")} bars); a longer timeframe shows more`;
    }
    return `Showing all ${shown} of stored history`;
  }
  if (fromSec <= startSec) return null;
  switch (outcome) {
    case "exhausted":
    case "covered":
      return `Showing ${shown}, the most history stored`;
    case "capped":
      return `Showing ${shown}, the most this chart loads at ${timeframe} (${MAX_HISTORY_BARS.toLocaleString("en-US")} bars); a longer timeframe shows more`;
    case "cancelled":
      return `Showing ${shown}; older candles could not be loaded`;
  }
}
