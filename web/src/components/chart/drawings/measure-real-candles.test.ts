// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import type { IChartApi, ISeriesApi, SeriesType, Time } from "lightweight-charts";
import type { Candle } from "@/types/domain";
import { DrawingController } from "./controller";
import { MeasurePrimitive } from "./primitives";
import type { DrawingsStore } from "./store";

// The measure tool must count the chart's OWN candles -- the selected
// instrument at the selected timeframe -- not a fixed sample set. Driven
// through the real MeasurePrimitive draw path with a fake chart/series and
// a canvas context that records the label it writes.

const t = (hh: number, mm: number) => Date.UTC(2026, 10, 16, hh, mm) / 1000;
const iso = (sec: number) => new Date(sec * 1000).toISOString();

function candlesAt(times: number[]): Candle[] {
  return times.map((s, i) => ({
    openTime: iso(s),
    open: 100 + i,
    high: 101 + i,
    low: 99 + i,
    close: 100.5 + i,
  })) as unknown as Candle[];
}

function fakeChartAndSeries() {
  const series = {
    priceToCoordinate: (p: number) => 500 - p,
    createPriceLine: () => ({}),
    removePriceLine: () => {},
  } as unknown as ISeriesApi<SeriesType, Time>;
  const chart = {
    timeScale: () => ({ timeToCoordinate: (time: Time) => ((time as number) % 100_000) / 10 }),
  } as unknown as IChartApi;
  return { chart, series };
}

// drawMeasureLabel attaches a MeasurePrimitive to series, draws it once,
// and returns the label text it wrote.
function drawMeasureLabel(chart: IChartApi, series: ISeriesApi<SeriesType, Time>, p1: { time: Time; price: number }, p2: { time: Time; price: number }): string {
  const prim = new MeasurePrimitive({ p1, p2, style: { color: "#fff", lineWidth: 1, lineStyle: "solid" } });
  prim.attached({ chart, series, requestUpdate: () => {} } as never);
  const texts: string[] = [];
  const ctx = new Proxy(
    {},
    {
      get: (_target, prop) => {
        if (prop === "fillText") return (text: string) => texts.push(text);
        if (prop === "measureText") return (text: string) => ({ width: text.length * 6 });
        return () => {};
      },
      set: () => true,
    },
  );
  const target = { useMediaCoordinateSpace: (fn: (s: { context: unknown }) => void) => fn({ context: ctx }) };
  prim.paneViews()[0].renderer()!.draw(target as never);
  return texts.join(" | ");
}

function chartWithCandles(candles: Candle[]) {
  const { chart, series } = fakeChartAndSeries();
  const controller = new DrawingController(chart, series, document.createElement("div"), {} as DrawingsStore, {} as never);
  controller.setCandles(candles);
  return { chart, series, controller };
}

describe("measure tool uses the chart's own candles", () => {
  // Measure from 00:02 (price 100) to 00:07 (price 104.5).
  const p1 = { time: t(0, 2) as Time, price: 100 };
  const p2 = { time: t(0, 7) as Time, price: 104.5 };

  it("real 1m series: hand-worked +4.5 (shown rounded as +5), +4.50%, 5 bars, 5m", () => {
    // 1m candles 00:00 .. 00:09. Bars in (00:02, 00:07] = 00:03,04,05,06,07 = 5.
    const { chart, series } = chartWithCandles(candlesAt([0, 1, 2, 3, 4, 5, 6, 7, 8, 9].map((m) => t(0, m))));
    const label = drawMeasureLabel(chart, series, p1, p2);
    expect(label).toContain("+5  (+4.50%)  5 bars  5m");
  });

  it("after switching instrument: the new instrument's candles are counted (a missing minute counts as no bar)", () => {
    const { chart, series, controller } = chartWithCandles(candlesAt([0, 1, 2, 3, 4, 5, 6, 7, 8, 9].map((m) => t(0, m))));
    // The other instrument has no candle at 00:04: bars in (00:02, 00:07] = 03,05,06,07 = 4.
    controller.setCandles(candlesAt([0, 1, 2, 3, 5, 6, 7, 8, 9].map((m) => t(0, m))));
    expect(drawMeasureLabel(chart, series, p1, p2)).toContain("4 bars");
  });

  it("after switching timeframe to 5m: 5m candles are counted", () => {
    const { chart, series, controller } = chartWithCandles(candlesAt([0, 1, 2, 3, 4, 5, 6, 7, 8, 9].map((m) => t(0, m))));
    // 5m candles 00:00, 00:05, 00:10: bars in (00:02, 00:07] = 00:05 only = 1.
    controller.setCandles(candlesAt([0, 5, 10].map((m) => t(0, m))));
    expect(drawMeasureLabel(chart, series, p1, p2)).toContain("1 bars");
  });

  it("two charts never see each other's candles", () => {
    const a = chartWithCandles(candlesAt([0, 1, 2, 3, 4, 5, 6, 7].map((m) => t(0, m))));
    const b = chartWithCandles(candlesAt([0, 5].map((m) => t(0, m))));
    expect(drawMeasureLabel(a.chart, a.series, p1, p2)).toContain("5 bars");
    expect(drawMeasureLabel(b.chart, b.series, p1, p2)).toContain("1 bars");
  });
});
