import { describe, expect, it } from "vitest";
import { forwardSpan } from "./geometry";

describe("forwardSpan", () => {
  it("spans from the left anchor to the right boundary for a normal in-range pair", () => {
    expect(forwardSpan(100, 800)).toEqual({ x0: 100, width: 700 });
  });

  it("bounds a finite box between two anchors, not just to the pane edge", () => {
    // The position tool's own right time-anchor, not the pane's width --
    // a genuinely bounded box with a finite forward width.
    expect(forwardSpan(100, 300)).toEqual({ x0: 100, width: 200 });
  });

  it("never produces a negative width when the left anchor is beyond the right boundary", () => {
    // This is exactly the shape of the "box fills to the left" bug: an
    // unclamped x0 greater than xRight makes xRight - x0 negative, and a
    // negative width draws backwards (left) from x0 in canvas.
    const { x0, width } = forwardSpan(900, 800);
    expect(x0).toBe(800);
    expect(width).toBe(0);
    expect(width).toBeGreaterThanOrEqual(0);
  });

  it("clamps a negative/off-screen left anchor to 0 rather than growing past it", () => {
    expect(forwardSpan(-50, 800)).toEqual({ x0: 0, width: 800 });
  });
});
