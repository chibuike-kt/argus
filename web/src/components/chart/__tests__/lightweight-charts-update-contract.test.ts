// @vitest-environment jsdom
//
// Part 1 fact-finding (live-chart brief, phase 4): exactly what
// series.update(bar) does in lightweight-charts 5.2.1 for the same
// time as the last bar, a newer time, and an older time -- verified
// against the real library, not its doc comments (which do say most
// of this, but the brief asks for a test, not a reading). jsdom cannot
// render an actual canvas (HTMLCanvasElement.getContext() logs "not
// implemented" and returns undefined), but the library tolerates that
// and still runs its own data-model logic -- series.data() reads back
// the series' own internal data array regardless of whether anything
// was ever actually painted, which is all this contract needs.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { CandlestickSeries, createChart, type IChartApi, type ISeriesApi, type UTCTimestamp } from "lightweight-charts";

function bar(time: number, close: number) {
  return { time: time as UTCTimestamp, open: close - 1, high: close + 1, low: close - 2, close };
}

// jsdom does not implement matchMedia at all; the library's own device-
// pixel-ratio watcher (fancy-canvas) calls it unconditionally on chart
// creation. A minimal stub is enough -- this contract never resizes or
// changes zoom, so the listener it registers is never exercised.
if (typeof window.matchMedia !== "function") {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
}

describe("lightweight-charts 5.2.1 series.update() contract", () => {
  let chart: IChartApi;
  let series: ISeriesApi<"Candlestick">;

  beforeEach(() => {
    const el = document.createElement("div");
    document.body.appendChild(el);
    // jsdom has no working 2D canvas context (getContext() logs "not
    // implemented" and returns undefined); the library's own async,
    // requestAnimationFrame-driven redraw then throws trying to
    // measure text on it. That redraw is irrelevant to this contract
    // (series.data() reads the data model directly, never the
    // canvas), so autoSize/animation is left on but every test
    // disposes the chart immediately in afterEach to stop further
    // redraws from firing after the assertions that matter are done.
    chart = createChart(el, { width: 400, height: 300 });
    series = chart.addSeries(CandlestickSeries);
    series.setData([bar(1000, 100), bar(1060, 101), bar(1120, 102)]);
  });

  afterEach(() => {
    chart.remove();
  });

  it("same time as the last bar: replaces it in place, length unchanged", () => {
    series.update(bar(1120, 999));
    const data = series.data();
    expect(data.length).toBe(3);
    expect(data[2]).toMatchObject({ time: 1120, close: 999 });
  });

  it("a newer time: appends a new bar, length grows by one", () => {
    series.update(bar(1180, 103));
    const data = series.data();
    expect(data.length).toBe(4);
    expect(data[3]).toMatchObject({ time: 1180, close: 103 });
  });

  it("an older time, historicalUpdate omitted: throws, and must not be swallowed", () => {
    // The doc comment says "time of the new item must be greater or
    // equal to the latest existing time point" but does not say what
    // happens on violation -- this is the exact ambiguity the brief
    // asks to resolve empirically.
    expect(() => series.update(bar(1060, 555))).toThrow();
    // And the series must be left exactly as it was before the
    // rejected call -- a thrown, ignored update must not have partially
    // applied.
    const data = series.data();
    expect(data.length).toBe(3);
    expect(data[1]).toMatchObject({ time: 1060, close: 101 });
  });

  it("an older time, historicalUpdate omitted: the error message names the real constraint", () => {
    try {
      series.update(bar(1000, 1));
      expect.unreachable("expected update() to throw for an older time without historicalUpdate");
    } catch (err) {
      expect(String(err)).toMatch(/time|data|order/i);
    }
  });

  it("an older time, historicalUpdate=true: the documented safe path actually works, updating that bar in place without disturbing the others", () => {
    series.update(bar(1060, 555), true);
    const data = series.data();
    expect(data.length).toBe(3);
    expect(data[0]).toMatchObject({ time: 1000, close: 100 });
    expect(data[1]).toMatchObject({ time: 1060, close: 555 });
    expect(data[2]).toMatchObject({ time: 1120, close: 102 });
  });

  it("an older time, historicalUpdate=true, but the time does not match any existing bar: still throws (it is an update, not an insert)", () => {
    expect(() => series.update(bar(1090, 1), true)).toThrow();
  });
});
