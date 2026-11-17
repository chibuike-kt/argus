// The chart's default visible range: what a first load, or a switch to
// an instrument/timeframe with no saved view, shows. Expressed as a
// lightweight-charts logical range (bar indices, which count whitespace
// points too), so it is independent of time and of the chart's pixel width.
//
// Replaces fitContent() + a fixed bar spacing, which pinned a short series
// (a few 4h or 1d bars) to the right edge as thin or stretched bars.

// [U] display values, not ADR decisions.
// About 120 bars at a normal width ends at the newest bar.
export const DEFAULT_VISIBLE_BARS = 120;
// A small gap to the right of the newest bar, in bars.
export const RIGHT_MARGIN_BARS = 5;
// A small gap before the first bar when a short series starts at the left.
export const LEFT_PAD_BARS = 2;

export interface LogicalRange {
  from: number;
  to: number;
}

// defaultVisibleRange returns the logical range to show for a series of
// barCount points (null for an empty series -- nothing to position).
//
//   - barCount >= DEFAULT_VISIBLE_BARS: the newest DEFAULT_VISIBLE_BARS
//     bars, ending at the newest one, plus RIGHT_MARGIN_BARS of space;
//   - fewer: every bar, starting from the left (after LEFT_PAD_BARS), in a
//     window of the SAME total width -- so each bar has the same normal
//     width as in the long case and is never stretched to fill the chart.
export function defaultVisibleRange(barCount: number): LogicalRange | null {
  if (barCount <= 0) return null;
  const width = DEFAULT_VISIBLE_BARS + RIGHT_MARGIN_BARS;
  if (barCount >= DEFAULT_VISIBLE_BARS) {
    const to = barCount - 1 + RIGHT_MARGIN_BARS;
    return { from: to - width + 1, to };
  }
  const from = -LEFT_PAD_BARS;
  return { from, to: from + width - 1 };
}
