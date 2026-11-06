// Persists the chart's visible logical range (which bars are in view, and
// how spread out they are) across a reload -- scoped the same way
// drawings/store.ts already scopes its own localStorage keys, by
// instrument+timeframe, so switching either naturally switches the saved
// view too. A per-viewer convenience, not data: reads/writes are wrapped,
// degrading to "no saved view" (the caller falls back to its own default)
// rather than throwing in a private window, with cleared site data, or
// with storage blocked outright.
const STORAGE_PREFIX = "argus:chart-view:v1:";

export interface ChartView {
  from: number;
  to: number;
}

export function loadChartView(scopeKey: string): ChartView | null {
  try {
    const raw = window.localStorage.getItem(STORAGE_PREFIX + scopeKey);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    return typeof parsed?.from === "number" && typeof parsed?.to === "number" ? parsed : null;
  } catch {
    return null;
  }
}

export function saveChartView(scopeKey: string, view: ChartView): void {
  try {
    window.localStorage.setItem(STORAGE_PREFIX + scopeKey, JSON.stringify(view));
  } catch {
    // Not persisted this session; the view still updates in memory.
  }
}
