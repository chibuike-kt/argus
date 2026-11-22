import { describe, expect, it } from "vitest";
import { fromSavedView, toSavedView } from "../chart-view-persistence";
import { RIGHT_MARGIN_BARS } from "../default-view";

// 1m bars at t = 1000, 1060, ... (unix seconds).
const series = (n: number, start = 1000) => Array.from({ length: n }, (_, i) => start + i * 60);

describe("chart view saved as time, not indexes", () => {
  it("a view in the middle of history is saved by the times of its first and last bars", () => {
    const times = series(100);
    const v = toSavedView({ from: 20, to: 50 }, times, 6.5)!;
    expect(v).toEqual({ from_time: 1000 + 20 * 60, to_time: 1000 + 50 * 60, bars_visible: 30, bar_spacing: 6.5, right_offset: 0, pinned_to_latest: false });
  });

  it("restores onto the SAME bars after history grows at the front (indexes shifted by 40)", () => {
    const before = series(100);
    const saved = toSavedView({ from: 20, to: 50 }, before, 6)!;
    // 40 older bars were loaded in front: the same candles now sit at 60..90.
    const after = series(140, 1000 - 40 * 60);
    expect(fromSavedView(saved, after)).toEqual({ from: 60, to: 90 });
  });

  it("a view following the newest bar keeps following it after new candles arrive", () => {
    const before = series(100);
    const saved = toSavedView({ from: 80, to: 104 }, before, 6)!; // newest is 99, 5 bars of margin
    expect(saved.pinned_to_latest).toBe(true);
    expect(saved.right_offset).toBe(5);
    const after = series(130); // 30 new candles
    expect(fromSavedView(saved, after)).toEqual({ from: 110, to: 134 });
  });

  it("falls back (null) when the saved time span no longer overlaps the loaded data", () => {
    const saved = toSavedView({ from: 20, to: 50 }, series(100), 6)!;
    expect(fromSavedView(saved, series(100, 1_000_000))).toBeNull();
  });

  it("a gap at the saved start: restores from the first bar after it", () => {
    const saved = { from_time: 1000 + 10 * 60, to_time: 1000 + 30 * 60, bars_visible: 20, bar_spacing: 6, right_offset: 0, pinned_to_latest: false };
    const withHole = series(100).filter((t) => t < 1000 + 5 * 60 || t >= 1000 + 15 * 60); // minutes 5..14 missing
    // first bar at or after minute 10 is minute 15, now at index 5.
    expect(fromSavedView(saved, withHole)).toEqual({ from: 5, to: 25 });
  });

  it("nothing in view, or no data: nothing saved", () => {
    expect(toSavedView({ from: 200, to: 230 }, series(100), 6)).toBeNull();
    expect(toSavedView({ from: 0, to: 10 }, [], 6)).toBeNull();
  });
});

describe("restoring a view pinned to the latest bar (polish pass, item 3)", () => {
  const times = Array.from({ length: 206 }, (_, i) => 1_000 + i * 3600);
  it("caps a saved right margin at the default, keeping the saved zoom", () => {
    // The owner's real saved 1h BTC view: 83 empty bars of 217.
    const saved = { from_time: times[70], to_time: times[205], bars_visible: 217.28, bar_spacing: 3.64, right_offset: 83.2, pinned_to_latest: true };
    const r = fromSavedView(saved, times)!;
    expect(r.to - (times.length - 1)).toBe(RIGHT_MARGIN_BARS);
    expect(r.to - r.from).toBeCloseTo(217.28);
  });
  it("keeps a smaller saved margin as it was", () => {
    const saved = { from_time: times[100], to_time: times[205], bars_visible: 100, bar_spacing: 6, right_offset: 2, pinned_to_latest: true };
    expect(fromSavedView(saved, times)!.to).toBe(times.length - 1 + 2);
  });
});
