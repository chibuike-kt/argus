import { describe, expect, it } from "vitest";
import { layoutLabels, LABEL_PRIORITY, type LabelRequest, type PlacedLabel } from "../label-layout";

const overlap = (a: PlacedLabel, b: PlacedLabel) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;

function noOverlaps(out: PlacedLabel[]) {
  const vis = out.filter((p) => p.visible);
  for (let i = 0; i < vis.length; i++) for (let j = i + 1; j < vis.length; j++) expect(overlap(vis[i], vis[j]), `${vis[i].id} / ${vis[j].id}`).toBe(false);
}

// Ten labels on almost the same anchor: the owner's screenshot case (SOL
// #275: swing, sweep, protected high, entry, TP3, premium, EQ and the zone
// label all within a few pixels).
const crowded: LabelRequest[] = [
  { id: "zone", ax: 300, ay: 200, w: 90, h: 14, priority: LABEL_PRIORITY.zone },
  { id: "entry", ax: 400, ay: 202, w: 80, h: 14, priority: LABEL_PRIORITY.plan, place: "right" },
  { id: "stop", ax: 400, ay: 196, w: 80, h: 14, priority: LABEL_PRIORITY.plan, place: "right" },
  { id: "tp1", ax: 400, ay: 204, w: 100, h: 14, priority: LABEL_PRIORITY.plan, place: "right" },
  { id: "tp3", ax: 400, ay: 206, w: 100, h: 14, priority: LABEL_PRIORITY.plan, place: "right" },
  { id: "bos", ax: 305, ay: 199, w: 30, h: 14, priority: LABEL_PRIORITY.break },
  { id: "sweep", ax: 302, ay: 201, w: 40, h: 14, priority: LABEL_PRIORITY.sweep },
  { id: "struct", ax: 298, ay: 200, w: 60, h: 14, priority: LABEL_PRIORITY.structural },
  { id: "protected", ax: 301, ay: 198, w: 80, h: 14, priority: LABEL_PRIORITY.swing },
  { id: "eq", ax: 290, ay: 203, w: 20, h: 14, priority: LABEL_PRIORITY.equilibrium },
];

describe("layoutLabels", () => {
  it("places ten crowded labels with no two visible ones overlapping", () => {
    const out = layoutLabels(crowded, { width: 800, height: 500 });
    noOverlaps(out);
    expect(out.filter((p) => p.visible).length).toBe(10);
  });

  it("keeps the most important label at its anchor and moves the less important ones, with a leader line", () => {
    const out = layoutLabels(crowded, { width: 800, height: 500 });
    const by = Object.fromEntries(out.map((p) => [p.id, p]));
    expect(by.zone.leader).toBeNull();
    expect(by.zone.y).toBe(200 - 14 - 3);
    expect(by.bos.leader).not.toBeNull();
    // A moved label's leader starts at its anchor.
    expect(by.bos.leader).toMatchObject({ x1: 305, y1: 199 });
  });

  it("respects priority: on one contested spot the higher priority keeps it", () => {
    const a: LabelRequest = { id: "eq", ax: 10, ay: 50, w: 40, h: 14, priority: LABEL_PRIORITY.equilibrium };
    const b: LabelRequest = { id: "ob", ax: 10, ay: 50, w: 40, h: 14, priority: LABEL_PRIORITY.zone };
    const out = layoutLabels([a, b], { width: 200, height: 100 });
    expect(out.find((p) => p.id === "ob")!.leader).toBeNull();
    expect(out.find((p) => p.id === "eq")!.leader).not.toBeNull();
  });

  it("hides a label when there is no room, rather than overlapping", () => {
    // A pane one label tall: only one fits.
    const out = layoutLabels(
      [
        { id: "a", ax: 0, ay: 17, w: 50, h: 14, priority: 0 },
        { id: "b", ax: 0, ay: 17, w: 50, h: 14, priority: 1 },
        { id: "c", ax: 0, ay: 17, w: 50, h: 14, priority: 2 },
      ],
      { width: 60, height: 16 },
    );
    expect(out.map((p) => p.visible)).toEqual([true, false, false]);
    noOverlaps(out);
  });

  it("hides labels beyond the allowed number of moves", () => {
    const many: LabelRequest[] = Array.from({ length: 30 }, (_, i) => ({ id: `l${i}`, ax: 100, ay: 250, w: 50, h: 14, priority: 5 }));
    const out = layoutLabels(many, { width: 400, height: 500, maxSteps: 3 });
    expect(out.filter((p) => p.visible).length).toBe(7); // home plus three up and three down
    noOverlaps(out);
  });

  it("is deterministic and independent of input order", () => {
    const a = layoutLabels(crowded, { width: 800, height: 500 });
    const b = layoutLabels([...crowded].reverse(), { width: 800, height: 500 });
    const key = (o: PlacedLabel[]) => [...o].sort((x, y) => (x.id < y.id ? -1 : 1)).map((p) => [p.id, p.visible, p.x, p.y]);
    expect(key(b)).toEqual(key(a));
    expect(layoutLabels(crowded, { width: 800, height: 500 })).toEqual(a);
  });

  it("never places a label in the reserved top band (the chart legend)", () => {
    const out = layoutLabels(
      [
        { id: "a", ax: 10, ay: 30, w: 50, h: 14, priority: 0 },
        { id: "b", ax: 10, ay: 30, w: 50, h: 14, priority: 1 },
      ],
      { width: 200, height: 200, top: 48 },
    );
    for (const p of out) if (p.visible) expect(p.y).toBeGreaterThanOrEqual(48);
    expect(out.every((p) => p.visible)).toBe(true);
  });

  it("keeps a label near the right edge inside the pane", () => {
    const [p] = layoutLabels([{ id: "tp", ax: 790, ay: 50, w: 100, h: 14, priority: 1, place: "right" }], { width: 800, height: 100 });
    expect(p.visible).toBe(true);
    expect(p.x + p.w).toBeLessThanOrEqual(800);
  });
});
