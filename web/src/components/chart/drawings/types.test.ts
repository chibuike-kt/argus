import { describe, expect, it } from "vitest";
import type { Time } from "lightweight-charts";
import { DEFAULT_STYLE, FIB_LEVELS, migrateDrawing, type HLineDrawing, type PositionDrawing } from "./types";

describe("FIB_LEVELS", () => {
  it("is exactly the three anchors + equilibrium levels, not the full retracement ladder", () => {
    expect(FIB_LEVELS).toEqual([0, 0.5, 1]);
  });
});

describe("migrateDrawing", () => {
  it("backfills rightTime on a legacy position drawing that predates the field", () => {
    const legacy = {
      id: "p1",
      kind: "position-long",
      time: 1000 as unknown as Time,
      entry: 100,
      stop: 90,
      targets: [110],
      style: DEFAULT_STYLE,
      createdAt: 0,
      updatedAt: 0,
      // rightTime deliberately absent, as a pre-migration save would be.
    } as unknown as PositionDrawing;

    const migrated = migrateDrawing(legacy) as PositionDrawing;
    expect(migrated.rightTime).toBe((1000 + 6000) as unknown as Time);
  });

  it("leaves a position drawing that already has rightTime untouched", () => {
    const d: PositionDrawing = {
      id: "p1",
      kind: "position-long",
      time: 1000 as unknown as Time,
      rightTime: 1500 as unknown as Time,
      entry: 100,
      stop: 90,
      targets: [110],
      style: DEFAULT_STYLE,
      createdAt: 0,
      updatedAt: 0,
    };
    expect(migrateDrawing(d)).toBe(d);
  });

  it("leaves non-position drawings untouched", () => {
    const d: HLineDrawing = { id: "h1", kind: "hline", price: 100, style: DEFAULT_STYLE, createdAt: 0, updatedAt: 0 };
    expect(migrateDrawing(d)).toBe(d);
  });
});
