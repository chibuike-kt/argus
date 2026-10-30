import { describe, expect, it } from "vitest";
import { forwardSpan } from "./geometry";

describe("forwardSpan", () => {
  it("spans from the anchor to the right edge for a normal in-range anchor", () => {
    expect(forwardSpan(100, 800)).toEqual({ x0: 100, width: 700 });
  });

  it("never produces a negative width when the anchor is beyond the right edge", () => {
    // This is exactly the shape of the "box fills to the left" bug: an
    // unclamped x0 greater than paneWidth makes paneWidth - x0 negative,
    // and a negative width draws backwards (left) from x0 in canvas.
    const { x0, width } = forwardSpan(900, 800);
    expect(x0).toBe(800);
    expect(width).toBe(0);
    expect(width).toBeGreaterThanOrEqual(0);
  });

  it("clamps a negative/off-screen anchor to the left edge rather than growing past it", () => {
    expect(forwardSpan(-50, 800)).toEqual({ x0: 0, width: 800 });
  });
});
