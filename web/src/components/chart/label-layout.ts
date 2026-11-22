// label-layout.ts: where ARGUS's chart labels go (Phase 6, label layout).
// Pure and deterministic: screen-space anchors, measured sizes and a
// priority in, a position or "hidden" out. No two visible labels overlap.
//
// Each label is tried at its preferred spot next to its anchor. If that
// spot collides with a label already placed (higher priority first), it is
// moved to the nearest free slot up or down the column (stacked, with a
// short leader line back to the anchor). If no slot within reach is free,
// or the label would leave the pane, it is hidden; the caller keeps the
// element's marker and gives it a tooltip.

export interface LabelRequest {
  id: string;
  // Anchor in pane pixels: the point the label describes.
  ax: number;
  ay: number;
  // Measured size of the label box (text plus padding).
  w: number;
  h: number;
  // Lower is more important (LABEL_PRIORITY).
  priority: number;
  // Where the label sits relative to its anchor before any move.
  place?: "above-right" | "right" | "below-right" | "left-of";
}

export interface PlacedLabel {
  id: string;
  visible: boolean;
  // Top-left of the label box.
  x: number;
  y: number;
  w: number;
  h: number;
  // Set when the label was moved away from its anchor.
  leader: { x1: number; y1: number; x2: number; y2: number } | null;
}

// Priority order (owner instruction for Phase 6): lower draws first and wins.
export const LABEL_PRIORITY = {
  zone: 0,
  plan: 1,
  break: 2,
  sweep: 3,
  structural: 4,
  swing: 5,
  range: 6,
  equilibrium: 7,
  // Other setups' one-line zone labels come after the selected setup's story.
  otherZone: 8,
} as const;

export interface LayoutOptions {
  width: number;
  height: number;
  // Space kept between stacked labels, px.
  gap?: number;
  // How many label heights a label may be moved up or down before it is hidden.
  maxSteps?: number;
  // Pixels at the top kept free (the chart's own legend sits there).
  top?: number;
}

type Rect = { x: number; y: number; w: number; h: number };

function overlaps(a: Rect, b: Rect): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

function home(l: LabelRequest): Rect {
  switch (l.place ?? "above-right") {
    case "right":
      return { x: l.ax + 4, y: l.ay - l.h / 2, w: l.w, h: l.h };
    case "below-right":
      return { x: l.ax + 2, y: l.ay + 3, w: l.w, h: l.h };
    case "left-of":
      return { x: l.ax - l.w - 4, y: l.ay - l.h / 2, w: l.w, h: l.h };
    default:
      return { x: l.ax + 2, y: l.ay - l.h - 3, w: l.w, h: l.h };
  }
}

export function layoutLabels(labels: readonly LabelRequest[], opts: LayoutOptions): PlacedLabel[] {
  const gap = opts.gap ?? 2;
  const maxSteps = opts.maxSteps ?? 6;
  // Deterministic order: priority, then id.
  const order = [...labels].sort((a, b) => a.priority - b.priority || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  const placed: Rect[] = [];
  const out = new Map<string, PlacedLabel>();
  const top = opts.top ?? 0;
  const inPane = (r: Rect) => r.x >= 0 && r.y >= top && r.x + r.w <= opts.width && r.y + r.h <= opts.height;

  for (const l of order) {
    const h0 = home(l);
    // Keep the column inside the pane horizontally (labels near the right
    // edge slide left rather than vanish).
    if (h0.x + h0.w > opts.width) h0.x = Math.max(0, opts.width - h0.w);
    if (h0.x < 0) h0.x = 0;
    let found: Rect | null = null;
    let moved = false;
    for (let k = 0; k <= maxSteps && !found; k++) {
      const offsets = k === 0 ? [0] : [-k, k];
      for (const o of offsets) {
        const r = { ...h0, y: h0.y + o * (l.h + gap) };
        if (inPane(r) && !placed.some((p) => overlaps(p, r))) {
          found = r;
          moved = k > 0;
          break;
        }
      }
    }
    if (!found) {
      out.set(l.id, { id: l.id, visible: false, x: h0.x, y: h0.y, w: l.w, h: l.h, leader: null });
      continue;
    }
    placed.push(found);
    const leader = moved
      ? {
          x1: l.ax,
          y1: l.ay,
          x2: Math.min(Math.max(l.ax, found.x), found.x + found.w),
          y2: found.y + found.h / 2 < l.ay ? found.y + found.h : found.y,
        }
      : null;
    out.set(l.id, { id: l.id, visible: true, ...found, leader });
  }
  return labels.map((l) => out.get(l.id)!);
}
