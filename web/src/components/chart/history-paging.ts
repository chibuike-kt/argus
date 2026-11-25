// Scrolling back through stored history (L1.2): the rules the chart uses
// to read an older page when the view reaches the left edge, to keep the
// same bars in view when older ones are added in front, and to say where
// the stored history starts. Pure functions, so they are tested without a
// chart.
import { MAX_HISTORY_BARS, type HistoryEdge } from "@/lib/use-live-candles";

// [U] display value: how close to the left edge, in bars, the view must
// come before the next older page is read. Far enough that the page is
// usually there before the trader reaches the edge, near enough that
// merely opening the chart (about 120 bars in view at the newest end of
// 1,000) never reads one.
export const LOAD_OLDER_EDGE_BARS = 20;

// prependShift is how many bars were added in front of the series when
// next replaced prev (both ascending bar times, in seconds): k when every
// bar of prev is still there, in order, after k new older bars; 0 for
// anything else (a switch, a reload, a gap fill in the middle), so the
// view is only ever shifted for a pure prepend. prev's last bar may be the
// live forming bar, which next may not have yet.
export function prependShift(prev: readonly number[], next: readonly number[]): number {
  if (prev.length === 0 || next.length <= prev.length - 1) return 0;
  const k = next.indexOf(prev[0]);
  if (k <= 0) return 0;
  const n = Math.min(prev.length, next.length - k);
  if (n < prev.length - 1) return 0;
  for (let i = 0; i < n; i++) {
    if (next[k + i] !== prev[i]) return 0;
  }
  return k;
}

// shouldLoadOlder: the view's first logical index is within
// LOAD_OLDER_EDGE_BARS of the oldest held bar, and the history is neither
// at its stored start nor at the chart's bar cap.
export function shouldLoadOlder(from: number | null | undefined, edge: HistoryEdge | undefined): boolean {
  if (from === null || from === undefined || !Number.isFinite(from)) return false;
  if (edge && (edge.exhausted || edge.capped)) return false;
  return from < LOAD_OLDER_EDGE_BARS;
}

const DATE = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" });

// historyStartNote is the quiet note at the oldest stored bar.
export function historyStartNote(oldestOpenTime: string): string {
  return `History starts ${DATE.format(new Date(oldestOpenTime))}`;
}

// historyCapNote is shown instead when the chart's own bar cap, not the
// stored history, stopped the scrolling.
export function historyCapNote(): string {
  return `This chart holds ${MAX_HISTORY_BARS.toLocaleString("en-US")} bars; older history is not loaded`;
}
