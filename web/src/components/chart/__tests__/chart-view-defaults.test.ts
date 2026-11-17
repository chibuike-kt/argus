import { describe, expect, it } from "vitest";
import type { CandlestickData, Time } from "lightweight-charts";
import { defaultVisibleRange, isSavedViewUsable } from "../default-view";
import { withGapWhitespace } from "../gap-whitespace";
import { capAxisLabels } from "../system-axis-labels";

describe("defaultVisibleRange", () => {
  // Window = 120 bars + 5 bars right margin = 125 logical units wide.
  it("1000 bars: the newest 120, ending at the newest bar, plus the right margin", () => {
    expect(defaultVisibleRange(1000)).toEqual({ from: 880, to: 1004 });
  });

  it("40 bars: all of them from the left, at the same width as 120 (never stretched)", () => {
    expect(defaultVisibleRange(40)).toEqual({ from: -2, to: 122 });
  });

  it("3 bars: same normal-width window from the left, not three fat bars", () => {
    expect(defaultVisibleRange(3)).toEqual({ from: -2, to: 122 });
  });

  it("1 bar: same normal-width window, the bar at the left", () => {
    expect(defaultVisibleRange(1)).toEqual({ from: -2, to: 122 });
  });

  it("exactly 120 bars: the long-series rule", () => {
    expect(defaultVisibleRange(120)).toEqual({ from: 0, to: 124 });
  });

  it("no bars: nothing to position", () => {
    expect(defaultVisibleRange(0)).toBeNull();
  });
});

describe("isSavedViewUsable", () => {
  it("a view over existing bars is kept", () => {
    expect(isSavedViewUsable({ from: 10, to: 60 }, 100)).toBe(true);
  });
  it("a view entirely past the data (saved on a longer series) falls back", () => {
    expect(isSavedViewUsable({ from: 800, to: 950 }, 40)).toBe(false);
  });
  it("a view entirely before the data falls back", () => {
    expect(isSavedViewUsable({ from: -50, to: -2 }, 40)).toBe(false);
  });
  it("a degenerate view falls back", () => {
    expect(isSavedViewUsable({ from: 5, to: 5.5 }, 40)).toBe(false);
    expect(isSavedViewUsable({ from: Number.NaN, to: 5 }, 40)).toBe(false);
  });
  it("no data: nothing fits", () => {
    expect(isSavedViewUsable({ from: 0, to: 10 }, 0)).toBe(false);
  });
});

describe("withGapWhitespace", () => {
  const bar = (iso: string, p: number): CandlestickData<Time> => ({
    time: (Date.parse(iso) / 1000) as Time,
    open: p,
    high: p,
    low: p,
    close: p,
  });

  it("golden: a 5-hour hole in 1m data becomes 299 whitespace points, no prices invented", () => {
    // 09:58, 09:59, 10:00, then nothing until 15:00, 15:01.
    const bars = [
      bar("2026-11-16T09:58:00Z", 1),
      bar("2026-11-16T09:59:00Z", 2),
      bar("2026-11-16T10:00:00Z", 3),
      bar("2026-11-16T15:00:00Z", 4),
      bar("2026-11-16T15:01:00Z", 5),
    ];
    const out = withGapWhitespace(bars, 60);
    // 10:01 .. 14:59 inclusive = 5h * 60 - 1 = 299 slots.
    expect(out).toHaveLength(5 + 299);
    const ws = out.filter((d) => !("open" in d));
    expect(ws).toHaveLength(299);
    expect(ws[0]).toEqual({ time: Date.parse("2026-11-16T10:01:00Z") / 1000 });
    expect(ws[298]).toEqual({ time: Date.parse("2026-11-16T14:59:00Z") / 1000 });
    // Every whitespace point is time only.
    expect(ws.every((d) => Object.keys(d).length === 1)).toBe(true);
    // Real bars untouched, in order, at their own indices.
    expect(out[2]).toBe(bars[2]);
    expect(out[3 + 299]).toBe(bars[3]);
    // Strictly ascending times (lightweight-charts requires it).
    for (let i = 1; i < out.length; i++) expect(out[i].time as number).toBeGreaterThan(out[i - 1].time as number);
  });

  it("no hole: data unchanged", () => {
    const bars = [bar("2026-11-16T10:00:00Z", 1), bar("2026-11-16T10:01:00Z", 2)];
    expect(withGapWhitespace(bars, 60)).toEqual(bars);
  });

  it("4h data: one missing 4h bar becomes one whitespace point", () => {
    const bars = [bar("2026-11-16T00:00:00Z", 1), bar("2026-11-16T08:00:00Z", 2)];
    const out = withGapWhitespace(bars, 14_400);
    expect(out).toHaveLength(3);
    expect(out[1]).toEqual({ time: Date.parse("2026-11-16T04:00:00Z") / 1000 });
  });
});

describe("capAxisLabels", () => {
  const L = (key: string, y: number) => ({ key, price: y, y, color: "#fff" });

  it("well-separated labels all show", () => {
    const r = capAxisLabels([L("a", 100), L("b", 200), L("c", 300)], 0, { labelHeight: 18, maxVisible: 4 });
    expect(r.shown.map((l) => l.key)).toEqual(["a", "b", "c"]);
    expect(r.groupY).toBeNull();
  });

  it("six labels stacked within a few pixels: one shows, the rest collapse into one group label that overlaps nothing", () => {
    // The 4h/1d case: order block high/low, entry, stop, two targets all
    // within 10px of each other.
    const labels = [L("ob-high", 202), L("ob-low", 204), L("entry", 205), L("stop", 208), L("t1", 210), L("t2", 212)];
    const r = capAxisLabels(labels, 500, { labelHeight: 18, maxVisible: 4 });
    expect(r.shown).toHaveLength(1);
    expect(r.hidden).toHaveLength(5);
    expect(r.groupY).not.toBeNull();
    // No two drawn labels (shown + group + the last-price anchor) overlap.
    const drawn = [500, ...r.shown.map((l) => l.y), r.groupY!];
    for (let i = 0; i < drawn.length; i++)
      for (let j = i + 1; j < drawn.length; j++) expect(Math.abs(drawn[i] - drawn[j])).toBeGreaterThanOrEqual(18);
  });

  it("keeps the labels nearest the current price, at most maxVisible", () => {
    const labels = [L("far", 10), L("near1", 480), L("near2", 450), L("mid", 300), L("near3", 420), L("x", 100)];
    const r = capAxisLabels(labels, 500, { labelHeight: 18, maxVisible: 4 });
    expect(r.shown.map((l) => l.key).sort()).toEqual(["mid", "near1", "near2", "near3"]);
    expect(r.hidden.map((l) => l.key).sort()).toEqual(["far", "x"]);
  });

  it("never places a label on top of the last-price label", () => {
    const r = capAxisLabels([L("entry", 505)], 500, { labelHeight: 18, maxVisible: 4 });
    expect(r.shown).toHaveLength(0);
    expect(r.groupY).not.toBeNull();
    expect(Math.abs(r.groupY! - 500)).toBeGreaterThanOrEqual(18);
  });
});
