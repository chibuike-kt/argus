import { RIGHT_MARGIN_BARS } from "./default-view";
import { preferences, type ChartViewPref } from "@/lib/preferences";

// The chart's saved visible range, per instrument and timeframe, stored as
// a preference (D1: on the server per user, localStorage only as a cache).
//
// Saved as TIME plus geometry, never raw bar indexes: a logical index
// means "the n-th point currently loaded", which shifts whenever history
// grows, is trimmed or gains a gap. A saved view records the open times of
// the first and last real bars in view, how many bars wide it was, the bar
// spacing, and -- when the newest bar was in view -- how far past it the
// view reached, so a view that was following the latest bar keeps
// following it after new candles arrive.

export type SavedChartView = ChartViewPref;

export interface LogicalRange {
  from: number;
  to: number;
}

// Only "<instrument_id>:<timeframe>" scopes are stored (the server's own
// key rule); anything else (no instrument resolved yet) is never saved.
const STORABLE_SCOPE = /^[1-9][0-9]*:(1m|5m|15m|1h|4h|1d)$/;

export function loadChartView(scope: string): SavedChartView | null {
  return preferences.get().chart_views?.[scope] ?? null;
}

export function saveChartView(scope: string, view: SavedChartView): void {
  if (!STORABLE_SCOPE.test(scope)) return;
  preferences.setEntry("chart_views", scope, view);
}

// toSavedView converts the visible logical range into a saved view.
// times[i] is the open time (unix seconds) of the i-th data point
// (whitespace points included -- they occupy indexes too). null when the
// range shows no data point at all.
export function toSavedView(range: LogicalRange, times: readonly number[], barSpacing: number): SavedChartView | null {
  const n = times.length;
  if (n === 0 || !(range.to > range.from) || range.to < 0 || range.from > n - 1) return null;
  const first = Math.min(n - 1, Math.max(0, Math.ceil(range.from)));
  const last = Math.min(n - 1, Math.max(0, Math.floor(range.to)));
  if (first > last) return null;
  const pinned = range.to >= n - 1;
  return {
    from_time: times[first],
    to_time: times[last],
    bars_visible: range.to - range.from,
    bar_spacing: barSpacing > 0 ? barSpacing : 1,
    right_offset: pinned ? range.to - (n - 1) : 0,
    pinned_to_latest: pinned,
  };
}

// fromSavedView converts a saved view back into a logical range over the
// data now loaded, or null when it no longer fits (the caller then shows
// the default view):
//   - pinned to latest: end the same distance past the newest bar (at most
//     RIGHT_MARGIN_BARS), same width;
//   - otherwise: start at the first bar at or after from_time, same width --
//     null when the saved time span no longer overlaps the loaded data.
export function fromSavedView(saved: SavedChartView, times: readonly number[]): LogicalRange | null {
  const n = times.length;
  if (n === 0 || !(saved.bars_visible > 0)) return null;
  if (saved.pinned_to_latest) {
    // The saved zoom is kept, but the margin right of the newest bar is at
    // most the default one. A pan into the empty space (to draw there) was
    // saved as a right offset and then restored on every load: 83 bars of
    // 217 on the owner's 1h BTC view, a third of the plot empty (polish
    // pass, item 3). The pan itself still works within a session.
    const to = n - 1 + Math.min(saved.right_offset, RIGHT_MARGIN_BARS);
    return { from: to - saved.bars_visible, to };
  }
  if (saved.from_time > times[n - 1] || saved.to_time < times[0]) return null;
  // First index whose time is >= from_time (times ascending).
  let lo = 0;
  let hi = n - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (times[mid] < saved.from_time) lo = mid + 1;
    else hi = mid;
  }
  return { from: lo, to: lo + saved.bars_visible };
}
