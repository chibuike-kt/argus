// SetupLayersPrimitive draws ARGUS's setup layers (setup-layers.ts) on the
// chart: one primitive for every element, so hundreds of zones cost one
// draw pass, and so hit testing sees exactly what was painted. ARGUS
// output, never a user drawing: no handles, never in the drawing store,
// cannot be moved or deleted (ADR-011). Anchored by time and price, so it
// sits correctly on every timeframe (a 1m zone on a 1h chart falls inside
// its hour, at least MIN_* pixels in size).
//
// Two passes per paint: shapes, then labels. Labels go through
// layoutLabels (label-layout.ts) so no two visible labels overlap; the
// library paints at most once per animation frame, so the layout re-runs
// on pan and zoom at that rate and no more.
//
// Hierarchy: the selected setup shows its whole story and its labels;
// every other setup is a thin translucent zone at OTHER_SETUP_ALPHA with
// one short label (kept at every zoom, so a long active zone can always be
// told apart), brought up while hovered. Level of detail: below
// LABEL_MIN_BAR_SPACING_PX of bar spacing only markers and the zone label
// are drawn; swing point text needs SWING_TEXT_MIN_BAR_SPACING_PX.
import type { IChartApi, ISeriesApi, ISeriesPrimitive, SeriesAttachedParameter, SeriesType, Time } from "lightweight-charts";
import { timeToX } from "./drawings/geometry";
import { labelColors } from "@/lib/label-contrast";
import { layoutLabels, LABEL_PRIORITY, type LabelRequest } from "./label-layout";
import type { LayerElement } from "./setup-layers";

// [U] display values.
export const MIN_ZONE_HEIGHT_PX = 4;
export const MIN_ZONE_WIDTH_PX = 6;
export const OTHER_SETUP_ALPHA = 0.35;
export const LABEL_MIN_BAR_SPACING_PX = 4;
export const SWING_TEXT_MIN_BAR_SPACING_PX = 8;
// The chart's HTML legend (symbol, OHLC, status chips) covers the pane's top.
export const LEGEND_RESERVED_PX = 48;
const HIT_SLOP_PX = 4;
const FONT = "500 10.5px Onest, sans-serif";
const LABEL_H = 15;
const PAD_X = 4;

interface MediaScope {
  context: CanvasRenderingContext2D;
  mediaSize: { width: number; height: number };
}
interface RenderTarget {
  useMediaCoordinateSpace(f: (scope: MediaScope) => void): void;
}

export interface HitBox {
  id: string;
  left: number;
  top: number;
  width: number;
  height: number;
}

// Colours from the theme tokens (theme.css), read once when attached; the
// fallbacks are the same tokens' values.
interface Palette {
  accent: string;
  up: string;
  down: string;
  fg: string;
  muted: string;
  surface: string;
}
const FALLBACK: Palette = { accent: "#6d7bff", up: "#2ebd85", down: "#f0616d", fg: "#f3f5f7", muted: "#9aa1ab", surface: "#0a0a0a" };

function readPalette(): Palette {
  if (typeof document === "undefined" || typeof getComputedStyle !== "function") return FALLBACK;
  const cs = getComputedStyle(document.documentElement);
  const v = (name: string, fb: string) => cs.getPropertyValue(name).trim() || fb;
  return {
    accent: v("--color-accent", FALLBACK.accent),
    up: v("--color-up", FALLBACK.up),
    down: v("--color-down", FALLBACK.down),
    fg: v("--color-fg", FALLBACK.fg),
    muted: v("--color-fg-muted", FALLBACK.muted),
    surface: v("--color-surface", FALLBACK.surface),
  };
}

// alpha applies an opacity to a #rrggbb token.
export function alpha(hex: string, a: number): string {
  const h = hex.replace("#", "").slice(0, 6);
  if (h.length !== 6) return hex;
  return `rgba(${parseInt(h.slice(0, 2), 16)},${parseInt(h.slice(2, 4), 16)},${parseInt(h.slice(4, 6), 16)},${a})`;
}

// dashFor shows how a setup was found: live solid, replay dashed, unknown
// (not recorded) dotted.
export function dashFor(mode: LayerElement["foundMode"]): number[] {
  if (mode === "live") return [];
  if (mode === "replay") return [5, 3];
  return [1, 3];
}

// minSize grows a span to at least min pixels around its centre.
export function minSize(a: number, b: number, min: number): [number, number] {
  let lo = Math.min(a, b);
  let len = Math.abs(b - a);
  if (len < min) {
    lo -= (min - len) / 2;
    len = min;
  }
  return [lo, len];
}

// A label to lay out, with how to draw it once placed.
interface PendingLabel extends LabelRequest {
  text: string;
  elementId: string;
  // A pill (plan prices) has a solid colour back; a text label a subtle backing.
  pill?: string;
  color: string;
  alpha: number;
}

export class SetupLayersPrimitive implements ISeriesPrimitive<Time> {
  private chart?: IChartApi;
  private series?: ISeriesApi<SeriesType, Time>;
  private requestUpdate?: () => void;
  private elements: LayerElement[] = [];
  private palette: Palette = FALLBACK;
  // "Play the story": the selected setup's elements with storyStep above
  // this are not drawn; null draws everything.
  private storyLimit: number | null = null;
  private focusedId: string | null = null;
  private hoveredSetup: number | null = null;
  // Pixels at the top the chart's legend covers (measured by the chart).
  private reservedTop = LEGEND_RESERVED_PX;
  // What the last paint drew (shapes and visible labels), for hitElement.
  hitBoxes: HitBox[] = [];
  // Labels the last paint had to hide (no room): their elements keep a
  // marker and get a tooltip.
  hiddenLabels: { elementId: string; text: string }[] = [];
  // The labels the last paint showed, where (for checks and tooltips).
  visibleLabels: (HitBox & { text: string })[] = [];

  attached(p: SeriesAttachedParameter<Time, SeriesType>) {
    this.chart = p.chart;
    this.series = p.series;
    this.requestUpdate = p.requestUpdate;
    this.palette = readPalette();
  }
  detached() {
    this.chart = undefined;
    this.series = undefined;
    this.requestUpdate = undefined;
    this.hitBoxes = [];
  }

  setElements(els: LayerElement[]) {
    this.elements = els;
    this.requestUpdate?.();
  }
  setStoryLimit(n: number | null) {
    this.storyLimit = n;
    this.requestUpdate?.();
  }
  setFocused(id: string | null) {
    this.focusedId = id;
    this.requestUpdate?.();
  }
  setReservedTop(px: number) {
    if (Math.abs(px - this.reservedTop) < 0.5) return;
    this.reservedTop = px;
    this.requestUpdate?.();
  }
  setHoveredSetup(id: number | null) {
    if (id === this.hoveredSetup) return;
    this.hoveredSetup = id;
    this.requestUpdate?.();
  }

  // hitElement returns the topmost element under (x, y) in host pixels:
  // small elements (lines, markers, labels) win over the zones they sit on.
  hitElement(x: number, y: number): string | null {
    let best: HitBox | null = null;
    for (const b of this.hitBoxes) {
      if (x >= b.left - HIT_SLOP_PX && x <= b.left + b.width + HIT_SLOP_PX && y >= b.top - HIT_SLOP_PX && y <= b.top + b.height + HIT_SLOP_PX) {
        if (!best || b.width * b.height <= best.width * best.height) best = b;
      }
    }
    return best?.id ?? null;
  }

  // labelOf is the label text for an element (for a tooltip), or "".
  elementLabel(id: string): string {
    return this.elements.find((e) => e.id === id)?.label ?? "";
  }

  private x(t: number): number | null {
    if (!this.chart || !this.series) return null;
    return timeToX({ chart: this.chart, series: this.series }, t as Time);
  }
  private y(p: number): number | null {
    return this.series?.priceToCoordinate(p) ?? null;
  }

  paneViews() {
    return [{ renderer: () => ({ draw: (target: RenderTarget) => this.draw(target) }) }];
  }

  private draw(target: RenderTarget) {
    const boxes: HitBox[] = [];
    const pending: PendingLabel[] = [];
    const spacing = this.chart?.timeScale().options().barSpacing ?? 6;
    const detailed = spacing >= LABEL_MIN_BAR_SPACING_PX;
    const swingText = spacing >= SWING_TEXT_MIN_BAR_SPACING_PX;
    target.useMediaCoordinateSpace(({ context: ctx, mediaSize }) => {
      ctx.font = FONT;
      const measure = (t: string) => Math.ceil(ctx.measureText(t).width) + PAD_X * 2;
      for (const el of this.elements) {
        if (el.selected && this.storyLimit !== null && el.storyStep > this.storyLimit) continue;
        const hovered = !el.selected && el.setupId === this.hoveredSetup;
        // Hierarchy: other setups recede; invalidated ones further.
        const a = el.selected ? (el.dimmed ? 0.55 : 1) : hovered ? 0.9 : el.dimmed ? OTHER_SETUP_ALPHA * 0.6 : OTHER_SETUP_ALPHA;
        ctx.save();
        ctx.globalAlpha = a;
        const box = this.drawShape(ctx, el, el.id === this.focusedId);
        ctx.restore();
        if (!box) continue;
        boxes.push({ id: el.id, ...box });
        // An element entirely off-screen gets no label: before, the layout
        // pulled such labels into view at the pane's edge (polish pass).
        if (box.left + box.width < 0 || box.left > mediaSize.width || box.top + box.height < 0 || box.top > mediaSize.height) continue;
        this.labelsFor(el, box, { detailed, swingText, hovered, alpha: a }, measure, pending);
      }

      const placed = layoutLabels(pending, { width: mediaSize.width, height: mediaSize.height, gap: 2, maxSteps: 6, top: this.reservedTop });
      const byId = new Map(pending.map((p) => [p.id, p]));
      this.hiddenLabels = [];
      this.visibleLabels = [];
      for (const pl of placed) {
        const req = byId.get(pl.id)!;
        if (!pl.visible) {
          this.hiddenLabels.push({ elementId: req.elementId, text: req.text });
          continue;
        }
        ctx.save();
        ctx.globalAlpha = req.alpha;
        if (pl.leader) {
          ctx.strokeStyle = alpha(this.palette.muted, 0.6);
          ctx.lineWidth = 1;
          ctx.beginPath();
          ctx.moveTo(pl.leader.x1, pl.leader.y1);
          ctx.lineTo(pl.leader.x2, pl.leader.y2);
          ctx.stroke();
        }
        if (req.pill) {
          const c = labelColors(req.pill);
          ctx.fillStyle = c.back;
          ctx.beginPath();
          ctx.roundRect(pl.x, pl.y, pl.w, pl.h, 3);
          ctx.fill();
          ctx.fillStyle = c.text;
        } else {
          // A subtle backing so text reads over candles.
          ctx.fillStyle = alpha(this.palette.surface, 0.72);
          ctx.beginPath();
          ctx.roundRect(pl.x, pl.y, pl.w, pl.h, 3);
          ctx.fill();
          ctx.fillStyle = req.color;
        }
        ctx.font = FONT;
        ctx.textAlign = "left";
        ctx.textBaseline = "middle";
        ctx.fillText(req.text, pl.x + PAD_X, pl.y + pl.h / 2 + 0.5);
        ctx.restore();
        boxes.push({ id: req.elementId, left: pl.x, top: pl.y, width: pl.w, height: pl.h });
        this.visibleLabels.push({ id: req.elementId, left: pl.x, top: pl.y, width: pl.w, height: pl.h, text: req.text });
      }
    });
    this.hitBoxes = boxes;
  }

  // labelsFor queues an element's labels, by the hierarchy and level-of-detail rules.
  private labelsFor(
    el: LayerElement,
    box: Omit<HitBox, "id">,
    lod: { detailed: boolean; swingText: boolean; hovered: boolean; alpha: number },
    measure: (t: string) => number,
    out: PendingLabel[],
  ) {
    const P = this.palette;
    const push = (id: string, text: string, ax: number, ay: number, priority: number, place: LabelRequest["place"], color: string, pill?: string) =>
      out.push({ id, elementId: el.id, text, ax, ay, w: measure(text), h: LABEL_H, priority, place, color, pill, alpha: lod.alpha });
    if (!el.selected) {
      // Other setups: one short label on the zone, when there is detail to spare or it is hovered.
      if (el.kind === "zone") {
        push(el.id, el.label, box.left, box.top, lod.hovered ? LABEL_PRIORITY.zone : LABEL_PRIORITY.otherZone, "above-right", P.muted);
      }
      return;
    }
    const g = el.geometry;
    switch (el.kind) {
      case "zone":
        // The selected zone's label is always shown.
        push(el.id, el.label, box.left, box.top, LABEL_PRIORITY.zone, "above-right", P.fg);
        return;
      case "plan":
        if (!lod.detailed || g.type !== "plan") return;
        {
          // Right gutter: one pill per line, at the plan's end.
          const right = box.left + box.width;
          const ye = this.y(g.entry);
          const ys = this.y(g.stop);
          if (ye != null) push(`${el.id}:entry`, g.entryText, right, ye, LABEL_PRIORITY.plan, "right", P.fg, P.accent);
          if (ys != null) push(`${el.id}:stop`, g.stopText, right, ys, LABEL_PRIORITY.plan, "right", P.fg, P.down);
          g.targets.forEach((t, i) => {
            const yt = this.y(t.price);
            if (yt != null) push(`${el.id}:tp${i}`, t.label, right, yt, LABEL_PRIORITY.plan, "right", P.fg, P.up);
          });
        }
        return;
      case "break":
      case "sweep":
        if (lod.detailed && g.type === "segment") {
          const x2 = this.x(g.t2);
          const y2 = this.y(g.p2);
          if (x2 != null && y2 != null) push(el.id, el.label, x2, y2, el.kind === "break" ? LABEL_PRIORITY.break : LABEL_PRIORITY.sweep, "right", P.fg);
        }
        return;
      case "structural":
        if (lod.detailed) push(el.id, el.label, box.left, box.top + 3, LABEL_PRIORITY.structural, "above-right", P.muted);
        return;
      case "swing":
        if (lod.swingText && g.type === "marker") {
          const y = this.y(g.p);
          const x = this.x(g.t);
          if (x != null && y != null) push(el.id, el.label, x + 5, y, LABEL_PRIORITY.swing, g.above ? "above-right" : "below-right", P.muted);
        }
        return;
      case "range":
        if (lod.detailed && g.type === "range") {
          const yh = this.y(g.high);
          const yl = this.y(g.low);
          const ye = this.y(g.eq);
          // Labelled once, at the left edge of the range.
          if (yh != null && ye != null && g.side === "premium") push(`${el.id}:side`, "Premium", box.left, Math.min(yh, ye), LABEL_PRIORITY.range, "below-right", P.down);
          if (yl != null && ye != null && g.side === "discount") push(`${el.id}:side`, "Discount", box.left, Math.max(yl, ye), LABEL_PRIORITY.range, "above-right", P.up);
          if (ye != null) push(`${el.id}:eq`, "EQ", box.left, ye, LABEL_PRIORITY.equilibrium, "left-of", P.muted);
        }
        return;
      default:
        return;
    }
  }

  private drawShape(ctx: CanvasRenderingContext2D, el: LayerElement, focused: boolean): Omit<HitBox, "id"> | null {
    const P = this.palette;
    const g = el.geometry;
    const lw = focused ? 2 : 1;
    switch (g.type) {
      case "rect": {
        const x1 = this.x(g.t1);
        const x2 = this.x(g.t2);
        const y1 = this.y(g.high);
        const y2 = this.y(g.low);
        if (x1 == null || x2 == null || y1 == null || y2 == null) return null;
        const [left, w] = minSize(x1, x2, MIN_ZONE_WIDTH_PX);
        const [top, h] = minSize(y1, y2, MIN_ZONE_HEIGHT_PX);
        const formation = el.kind === "formation";
        ctx.fillStyle = alpha(P.accent, formation ? 0.45 : el.selected ? 0.2 : 0.14);
        ctx.fillRect(left, top, w, h);
        ctx.strokeStyle = alpha(P.accent, el.selected || focused ? 0.95 : 0.7);
        ctx.lineWidth = lw;
        ctx.setLineDash(formation ? [] : dashFor(el.foundMode));
        ctx.strokeRect(left + 0.5, top + 0.5, w - 1, h - 1);
        ctx.setLineDash([]);
        return { left, top, width: w, height: h };
      }
      case "segment": {
        const x1 = this.x(g.t1);
        const x2 = this.x(g.t2);
        const y1 = this.y(g.p1);
        const y2 = this.y(g.p2);
        if (x1 == null || x2 == null || y1 == null || y2 == null) return null;
        ctx.strokeStyle = el.kind === "structural" ? alpha(P.accent, 0.7) : P.accent;
        ctx.lineWidth = lw;
        ctx.setLineDash(el.kind === "sweep" ? [4, 3] : el.kind === "structural" ? [2, 3] : []);
        ctx.beginPath();
        ctx.moveTo(x1, y1 + 0.5);
        ctx.lineTo(x2, y2 + 0.5);
        ctx.stroke();
        ctx.setLineDash([]);
        if (el.kind === "sweep" || el.kind === "break") {
          ctx.fillStyle = P.accent;
          ctx.beginPath();
          ctx.arc(x2, y2, 2.5, 0, Math.PI * 2);
          ctx.fill();
        }
        const left = Math.min(x1, x2);
        return { left, top: Math.min(y1, y2) - 3, width: Math.max(Math.abs(x2 - x1), 6), height: Math.abs(y2 - y1) + 6 };
      }
      case "marker": {
        const x = this.x(g.t);
        const y = this.y(g.p);
        if (x == null || y == null) return null;
        ctx.fillStyle = P.accent;
        ctx.beginPath();
        // A small triangle pointing away from price: above a high, below a low.
        const d = g.above ? -1 : 1;
        ctx.moveTo(x, y + d * 2);
        ctx.lineTo(x - 3.5, y + d * 8);
        ctx.lineTo(x + 3.5, y + d * 8);
        ctx.closePath();
        ctx.fill();
        if (focused) {
          ctx.strokeStyle = P.fg;
          ctx.lineWidth = 1;
          ctx.stroke();
        }
        return { left: x - 5, top: Math.min(y, y + d * 9) - 1, width: 10, height: 11 };
      }
      case "range": {
        const x1 = this.x(g.t1);
        const x2 = this.x(g.t2);
        const yh = this.y(g.high);
        const yl = this.y(g.low);
        const ye = this.y(g.eq);
        if (x1 == null || x2 == null || yh == null || yl == null || ye == null) return null;
        const [left, w] = minSize(x1, x2, MIN_ZONE_WIDTH_PX);
        // Premium above equilibrium, discount below (spec section 8); the
        // setup's own side is the stronger band.
        ctx.fillStyle = alpha(P.down, g.side === "premium" ? 0.08 : 0.03);
        ctx.fillRect(left, Math.min(yh, ye), w, Math.abs(ye - yh));
        ctx.fillStyle = alpha(P.up, g.side === "discount" ? 0.08 : 0.03);
        ctx.fillRect(left, Math.min(ye, yl), w, Math.abs(yl - ye));
        ctx.strokeStyle = alpha(P.muted, 0.7);
        ctx.lineWidth = 1;
        ctx.setLineDash([4, 4]);
        ctx.beginPath();
        ctx.moveTo(left, ye + 0.5);
        ctx.lineTo(left + w, ye + 0.5);
        ctx.stroke();
        ctx.setLineDash([]);
        return { left, top: Math.min(yh, yl), width: w, height: Math.abs(yl - yh) };
      }
      case "plan": {
        const x1 = this.x(g.t1);
        const x2 = this.x(g.t2);
        const ye = this.y(g.entry);
        const ys = this.y(g.stop);
        if (x1 == null || x2 == null || ye == null || ys == null) return null;
        const [left, w] = minSize(x1, x2, MIN_ZONE_WIDTH_PX);
        const tps = g.targets.map((t) => this.y(t.price)).filter((y): y is number => y != null);
        const far = tps.length ? tps[tps.length - 1] : null;
        if (far != null) {
          ctx.fillStyle = alpha(P.up, 0.08);
          ctx.fillRect(left, Math.min(ye, far), w, Math.abs(far - ye));
        }
        ctx.fillStyle = alpha(P.down, 0.08);
        ctx.fillRect(left, Math.min(ye, ys), w, Math.abs(ys - ye));
        // Lines stop at the plan's end time; their pills sit in the gutter.
        const line = (y: number, color: string) => {
          ctx.strokeStyle = color;
          ctx.lineWidth = lw;
          ctx.beginPath();
          ctx.moveTo(left, y + 0.5);
          ctx.lineTo(left + w, y + 0.5);
          ctx.stroke();
        };
        line(ye, P.accent);
        line(ys, P.down);
        for (const y of tps) line(y, alpha(P.up, 0.85));
        const ys2 = [ye, ys, ...tps];
        const top = Math.min(...ys2);
        return { left, top, width: w, height: Math.max(...ys2) - top };
      }
    }
  }
}
