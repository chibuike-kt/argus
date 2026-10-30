import type {
  ISeriesPrimitive,
  IChartApi,
  ISeriesApi,
  SeriesType,
  Time,
  SeriesAttachedParameter,
} from "lightweight-charts";
import { candles as fixtureCandles } from "@/mocks/fixtures";
import { barsBetween, forwardSpan, timeSeconds, type ChartAccessors } from "./geometry";
import { extendPoint, getHandles, type Handle } from "./hit-test";
import {
  FIB_LEVELS,
  type Drawing,
  type DrawingStyle,
  type FibDrawing,
  type MeasureDrawing,
  type PositionDrawing,
  type RectangleDrawing,
  type TextDrawing,
  type VLineDrawing,
  type HLineDrawing,
  type TrendLineDrawing,
} from "./types";

// Minimal view of the canvas target the library hands to draw(). We only need
// media-space (CSS pixel) drawing, which keeps coordinates aligned with
// priceToCoordinate / timeToCoordinate without pixel-ratio math.
interface MediaScope {
  context: CanvasRenderingContext2D;
  mediaSize: { width: number; height: number };
}
interface RenderTarget {
  useMediaCoordinateSpace(f: (scope: MediaScope) => void): void;
}

const ACCENT = "#d7fe62";
const DOWN = "#ff5b5b";
const INK = "#0a0a0a";

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath();
  ctx.roundRect(x, y, w, h, r);
}

function label(ctx: CanvasRenderingContext2D, text: string, x: number, y: number, bg: string, fg: string) {
  ctx.font = "600 11px Poppins, sans-serif";
  const padX = 5;
  const w = ctx.measureText(text).width + padX * 2;
  const h = 16;
  ctx.fillStyle = bg;
  roundRect(ctx, x, y - h / 2, w, h, 3);
  ctx.fill();
  ctx.fillStyle = fg;
  ctx.textBaseline = "middle";
  ctx.fillText(text, x + padX, y);
}

function applyLine(ctx: CanvasRenderingContext2D, style: DrawingStyle) {
  ctx.strokeStyle = style.color;
  ctx.lineWidth = style.lineWidth;
  ctx.setLineDash(style.lineStyle === "dashed" ? [5, 4] : []);
}

function withAlpha(hex: string, alpha: number): string {
  const h = hex.replace("#", "");
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `rgba(${r},${g},${b},${alpha})`;
}

function formatElapsed(seconds: number): string {
  const m = Math.round(seconds / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  const d = Math.floor(h / 24);
  return `${d}d ${h % 24}h`;
}

const fmt = (p: number) => Math.round(p).toLocaleString("en-US");

// Base plumbing: holds the attached chart/series and a repaint request, and
// exposes the chart/series pair as ChartAccessors for the geometry/hit-test
// helpers so renderer code never duplicates coordinate-conversion logic.
//
// paneViews()'s renderer/draw are arrow functions throughout (never method
// shorthand), so `this` inside them is always the primitive instance that
// the charting library calls draw() on, not whatever object the library
// happens to invoke it against -- the same effect the previous `self = this`
// alias achieved, without aliasing this.
abstract class BasePrimitive implements ISeriesPrimitive<Time> {
  protected chart?: IChartApi;
  protected series?: ISeriesApi<SeriesType, Time>;
  protected requestUpdate?: () => void;

  attached(param: SeriesAttachedParameter<Time, SeriesType>) {
    this.chart = param.chart;
    this.series = param.series;
    this.requestUpdate = param.requestUpdate;
  }
  detached() {
    this.chart = undefined;
    this.series = undefined;
    this.requestUpdate = undefined;
  }
  protected x(time: Time): number | null {
    return this.chart?.timeScale().timeToCoordinate(time) ?? null;
  }
  protected y(price: number): number | null {
    return this.series?.priceToCoordinate(price) ?? null;
  }
  protected accessors(): ChartAccessors | null {
    return this.chart && this.series ? { chart: this.chart, series: this.series } : null;
  }
  repaint() {
    this.requestUpdate?.();
  }
  abstract paneViews(): readonly { renderer(): { draw(t: RenderTarget): void } | null }[];
}

// ---- Trend line / ray / extended line ----
export class LinePrimitive extends BasePrimitive {
  constructor(private d: Pick<TrendLineDrawing, "kind" | "p1" | "p2" | "style">) {
    super();
  }
  update(d: Pick<TrendLineDrawing, "kind" | "p1" | "p2" | "style">) {
    this.d = d;
    this.repaint();
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const x1 = this.x(this.d.p1.time);
            const x2 = this.x(this.d.p2.time);
            const y1 = this.y(this.d.p1.price);
            const y2 = this.y(this.d.p2.price);
            if (x1 == null || x2 == null || y1 == null || y2 == null) return;
            target.useMediaCoordinateSpace(({ context: ctx, mediaSize }) => {
              let ex = x2;
              let ey = y2;
              if (this.d.kind !== "trendline") {
                const far = extendPoint({ x: x1, y: y1 }, { x: x2, y: y2 }, mediaSize.width);
                ex = far.x;
                ey = far.y;
              }
              applyLine(ctx, this.d.style);
              ctx.beginPath();
              ctx.moveTo(x1, y1);
              ctx.lineTo(ex, ey);
              ctx.stroke();
              ctx.setLineDash([]);
            });
          },
        }),
      },
    ];
  }
}

// ---- Rectangle (zones, order block) ----
export class RectanglePrimitive extends BasePrimitive {
  constructor(private d: Pick<RectangleDrawing, "p1" | "p2" | "style" | "label">) {
    super();
  }
  update(d: Pick<RectangleDrawing, "p1" | "p2" | "style" | "label">) {
    this.d = d;
    this.repaint();
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const x1 = this.x(this.d.p1.time);
            const x2 = this.x(this.d.p2.time);
            const y1 = this.y(this.d.p1.price);
            const y2 = this.y(this.d.p2.price);
            if (x1 == null || x2 == null || y1 == null || y2 == null) return;
            target.useMediaCoordinateSpace(({ context: ctx }) => {
              const left = Math.min(x1, x2);
              const top = Math.min(y1, y2);
              const w = Math.abs(x2 - x1);
              const h = Math.abs(y2 - y1);
              ctx.fillStyle = withAlpha(this.d.style.color, 0.08);
              ctx.fillRect(left, top, w, h);
              applyLine(ctx, this.d.style);
              ctx.strokeRect(left + 0.5, top + 0.5, w, h);
              ctx.setLineDash([]);
              if (this.d.label) {
                ctx.font = "600 10px Poppins, sans-serif";
                ctx.fillStyle = this.d.style.color;
                ctx.textBaseline = "top";
                ctx.fillText(this.d.label.toUpperCase(), left + 6, top + 5);
              }
            });
          },
        }),
      },
    ];
  }
}

// ---- Fibonacci retracement ----
export class FibPrimitive extends BasePrimitive {
  constructor(private d: Pick<FibDrawing, "p1" | "p2" | "style">) {
    super();
  }
  update(d: Pick<FibDrawing, "p1" | "p2" | "style">) {
    this.d = d;
    this.repaint();
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const x1 = this.x(this.d.p1.time);
            const x2 = this.x(this.d.p2.time);
            const y1 = this.y(this.d.p1.price);
            const y2 = this.y(this.d.p2.price);
            if (x1 == null || x2 == null || y1 == null || y2 == null) return;
            const { p1, p2 } = this.d;
            target.useMediaCoordinateSpace(({ context: ctx, mediaSize }) => {
              const left = Math.min(x1, x2);
              const right = mediaSize.width;
              for (const level of FIB_LEVELS) {
                const price = p1.price + (p2.price - p1.price) * level;
                const y = this.y(price);
                if (y == null) continue;
                const emphasised = level === 0.5;
                ctx.strokeStyle = emphasised ? this.d.style.color : withAlpha(this.d.style.color, 0.55);
                ctx.lineWidth = emphasised ? 1.5 : 1;
                ctx.setLineDash(emphasised ? [] : this.d.style.lineStyle === "dashed" ? [5, 4] : []);
                ctx.beginPath();
                ctx.moveTo(left, y + 0.5);
                ctx.lineTo(right, y + 0.5);
                ctx.stroke();
                const ratio = level.toFixed(3).replace(/0+$/, "").replace(/\.$/, "");
                label(
                  ctx,
                  `${ratio}  ${fmt(price)}`,
                  left + 6,
                  y,
                  emphasised ? this.d.style.color : withAlpha(this.d.style.color, 0.25),
                  emphasised ? INK : this.d.style.color,
                );
              }
              ctx.setLineDash([]);
            });
          },
        }),
      },
    ];
  }
}

// ---- Horizontal line ----
export class HLinePrimitive extends BasePrimitive {
  constructor(private d: Pick<HLineDrawing, "price" | "style">) {
    super();
  }
  update(d: Pick<HLineDrawing, "price" | "style">) {
    this.d = d;
    this.repaint();
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const y = this.y(this.d.price);
            if (y == null) return;
            target.useMediaCoordinateSpace(({ context: ctx, mediaSize }) => {
              applyLine(ctx, this.d.style);
              ctx.beginPath();
              ctx.moveTo(0, y + 0.5);
              ctx.lineTo(mediaSize.width, y + 0.5);
              ctx.stroke();
              ctx.setLineDash([]);
              label(ctx, fmt(this.d.price), 6, y, this.d.style.color, INK);
            });
          },
        }),
      },
    ];
  }
}

// ---- Vertical line ----
export class VLinePrimitive extends BasePrimitive {
  constructor(private d: Pick<VLineDrawing, "time" | "style">) {
    super();
  }
  update(d: Pick<VLineDrawing, "time" | "style">) {
    this.d = d;
    this.repaint();
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const x = this.x(this.d.time);
            if (x == null) return;
            target.useMediaCoordinateSpace(({ context: ctx, mediaSize }) => {
              applyLine(ctx, this.d.style);
              ctx.beginPath();
              ctx.moveTo(x + 0.5, 0);
              ctx.lineTo(x + 0.5, mediaSize.height);
              ctx.stroke();
              ctx.setLineDash([]);
            });
          },
        }),
      },
    ];
  }
}

// ---- Text / note ----
export class TextPrimitive extends BasePrimitive {
  constructor(private d: Pick<TextDrawing, "p" | "text" | "style">) {
    super();
  }
  update(d: Pick<TextDrawing, "p" | "text" | "style">) {
    this.d = d;
    this.repaint();
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const x = this.x(this.d.p.time);
            const y = this.y(this.d.p.price);
            if (x == null || y == null) return;
            target.useMediaCoordinateSpace(({ context: ctx }) => {
              ctx.font = "500 12px Poppins, sans-serif";
              ctx.fillStyle = this.d.style.color;
              ctx.textBaseline = "middle";
              ctx.fillText(this.d.text || "Text", x, y);
            });
          },
        }),
      },
    ];
  }
}

// ---- Measure (price + time range) ----
export class MeasurePrimitive extends BasePrimitive {
  constructor(private d: Pick<MeasureDrawing, "p1" | "p2" | "style">) {
    super();
  }
  update(d: Pick<MeasureDrawing, "p1" | "p2" | "style">) {
    this.d = d;
    this.repaint();
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const { p1, p2 } = this.d;
            const x1 = this.x(p1.time);
            const x2 = this.x(p2.time);
            const y1 = this.y(p1.price);
            const y2 = this.y(p2.price);
            if (x1 == null || x2 == null || y1 == null || y2 == null) return;
            const up = p2.price >= p1.price;
            const color = up ? ACCENT : DOWN;
            target.useMediaCoordinateSpace(({ context: ctx }) => {
              const left = Math.min(x1, x2);
              const top = Math.min(y1, y2);
              const w = Math.abs(x2 - x1);
              const h = Math.abs(y2 - y1);
              ctx.fillStyle = withAlpha(color, 0.12);
              ctx.fillRect(left, top, w, h);
              ctx.strokeStyle = color;
              ctx.lineWidth = 1;
              ctx.setLineDash([4, 3]);
              ctx.strokeRect(left + 0.5, top + 0.5, w, h);
              ctx.setLineDash([]);

              const delta = p2.price - p1.price;
              const pct = (delta / p1.price) * 100;
              const bars = barsBetween(p1.time, p2.time, fixtureCandles);
              const seconds = Math.abs(timeSeconds(p2.time) - timeSeconds(p1.time));
              const elapsed = formatElapsed(seconds);
              const text = `${delta >= 0 ? "+" : ""}${fmt(delta)}  (${pct >= 0 ? "+" : ""}${pct.toFixed(2)}%)  ${bars} bars  ${elapsed}`;
              label(ctx, text, left + 6, top - 10, color, INK);
            });
          },
        }),
      },
    ];
  }
}

// ---- Position tool (long / short): entry/stop/target lines + reward/risk boxes ----
export class PositionPrimitive extends BasePrimitive {
  constructor(private d: Pick<PositionDrawing, "kind" | "time" | "rightTime" | "entry" | "stop" | "targets" | "style">) {
    super();
  }
  update(d: Pick<PositionDrawing, "kind" | "time" | "rightTime" | "entry" | "stop" | "targets" | "style">) {
    this.d = d;
    this.repaint();
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const { time, rightTime, entry, stop, targets } = this.d;
            const xe = this.x(time);
            const yEntry = this.y(entry);
            const yStop = this.y(stop);
            if (xe == null || yEntry == null || yStop == null) return;
            const tps = targets.map((t) => ({ t, y: this.y(t) })).filter((o): o is { t: number; y: number } => o.y != null);
            const far = tps.length ? tps[tps.length - 1] : null;

            target.useMediaCoordinateSpace(({ context: ctx, mediaSize }) => {
              // Bounded box: entry time to the right time-anchor, a finite
              // forward width -- never flooding to the pane's right edge.
              // Falls back to the pane's own edge only if rightTime cannot
              // currently be resolved to a coordinate (e.g. scrolled out of
              // the chart's logical range). forwardSpan keeps the left
              // clamp: the left edge can never end up past the right one,
              // which is what let the box fill backwards before.
              const xRightRaw = this.x(rightTime);
              const { x0, width: w } = forwardSpan(xe, xRightRaw ?? mediaSize.width);
              const x1 = x0 + w;

              // Reward above risk for a long (targets sit above entry, stop
              // below); the mirror holds automatically for a short, since
              // yStop/yEntry/target pixel order flips with price order.
              if (far) {
                ctx.fillStyle = "rgba(215,254,98,0.10)";
                ctx.fillRect(x0, Math.min(yEntry, far.y), w, Math.abs(far.y - yEntry));
              }
              ctx.fillStyle = "rgba(255,91,91,0.12)";
              ctx.fillRect(x0, Math.min(yEntry, yStop), w, Math.abs(yStop - yEntry));

              const line = (y: number, color: string, dashed: boolean) => {
                ctx.strokeStyle = color;
                ctx.lineWidth = 1;
                ctx.setLineDash(dashed ? [4, 3] : []);
                ctx.beginPath();
                ctx.moveTo(x0, y + 0.5);
                ctx.lineTo(x1, y + 0.5);
                ctx.stroke();
                ctx.setLineDash([]);
              };

              line(yEntry, ACCENT, false);
              line(yStop, DOWN, true);
              tps.forEach((o) => line(o.y, ACCENT, true));

              // The right edge itself, so the bounded width (and its
              // resize handle) is visible, not just implied by where the
              // horizontal lines happen to stop.
              const top = Math.min(yEntry, yStop, ...tps.map((o) => o.y));
              const bottom = Math.max(yEntry, yStop, ...tps.map((o) => o.y));
              ctx.strokeStyle = "rgba(255,255,255,0.35)";
              ctx.lineWidth = 1;
              ctx.setLineDash([3, 3]);
              ctx.beginPath();
              ctx.moveTo(x1 + 0.5, top);
              ctx.lineTo(x1 + 0.5, bottom);
              ctx.stroke();
              ctx.setLineDash([]);

              const rrOf = (t: number) => Math.abs((t - entry) / (entry - stop));
              label(ctx, `Entry  ${fmt(entry)}`, x0 + 6, yEntry, ACCENT, INK);
              const stopPct = Math.abs((stop - entry) / entry) * 100;
              label(ctx, `Stop  ${fmt(stop)}  -${stopPct.toFixed(2)}%`, x0 + 6, yStop, DOWN, "#fff");
              tps.forEach((o, i) => {
                const pct = Math.abs((o.t - entry) / entry) * 100;
                label(ctx, `TP${i + 1}  ${fmt(o.t)}  +${pct.toFixed(2)}%  ${rrOf(o.t).toFixed(2)}R`, x0 + 6, o.y, "rgba(215,254,98,0.18)", ACCENT);
              });
            });
          },
        }),
      },
    ];
  }
}

// ---- System label: a small background-chip label at a point, for ARGUS's
// own structure markers (swings, BOS/CHOCH, sweeps). Deliberately a chip
// (reuses the same label() helper hline/fib/position use for their own
// levels), not bare text, so a system marker never looks like a plain user
// "text" drawing (TextPrimitive), which draws unstyled fillText. Never
// constructed from a Drawing, never attached to the user store. ----
export class SystemLabelPrimitive extends BasePrimitive {
  constructor(
    private p: { time: Time; price: number },
    private text: string,
    private color: string,
  ) {
    super();
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const x = this.x(this.p.time);
            const y = this.y(this.p.price);
            if (x == null || y == null) return;
            target.useMediaCoordinateSpace(({ context: ctx }) => {
              label(ctx, this.text, x, y, this.color, INK);
            });
          },
        }),
      },
    ];
  }
}

// ---- Selection handles overlay: draws the handle markers for the currently
// selected drawing. Attached/detached by the controller as selection
// changes; never part of the persisted drawing data. ----
export class SelectionOverlayPrimitive extends BasePrimitive {
  constructor(private getHandlesFn: () => Handle[]) {
    super();
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const acc = this.accessors();
            if (!acc) return;
            const handles = this.getHandlesFn();
            target.useMediaCoordinateSpace(({ context: ctx }) => {
              for (const h of handles) {
                ctx.beginPath();
                ctx.arc(h.px.x, h.px.y, 4.5, 0, Math.PI * 2);
                ctx.fillStyle = "#0a0a0a";
                ctx.fill();
                ctx.lineWidth = 1.5;
                ctx.strokeStyle = ACCENT;
                ctx.stroke();
              }
            });
          },
        }),
      },
    ];
  }
}

// Build a primitive from a stored Drawing (used on initial reconciliation;
// drags call .update() on the existing instance instead of rebuilding).
export function primitiveFor(d: Drawing): ISeriesPrimitive<Time> {
  switch (d.kind) {
    case "trendline":
    case "ray":
    case "extended":
      return new LinePrimitive(d);
    case "rectangle":
      return new RectanglePrimitive(d);
    case "fib":
      return new FibPrimitive(d);
    case "hline":
      return new HLinePrimitive(d);
    case "vline":
      return new VLinePrimitive(d);
    case "text":
      return new TextPrimitive(d);
    case "measure":
      return new MeasurePrimitive(d);
    case "position-long":
    case "position-short":
      return new PositionPrimitive(d);
  }
}

// Pushes a drawing's current data into an already-attached primitive, for
// smooth in-place updates during drag (no detach/reattach, no primitive
// identity churn). Returns false if the primitive's kind mismatches, so the
// caller can fall back to rebuilding it (should not happen in practice --
// a drawing's kind never changes after creation).
export function updatePrimitive(p: ISeriesPrimitive<Time>, d: Drawing): boolean {
  if ((d.kind === "trendline" || d.kind === "ray" || d.kind === "extended") && p instanceof LinePrimitive) {
    p.update(d);
    return true;
  }
  if (d.kind === "rectangle" && p instanceof RectanglePrimitive) {
    p.update(d);
    return true;
  }
  if (d.kind === "fib" && p instanceof FibPrimitive) {
    p.update(d);
    return true;
  }
  if (d.kind === "hline" && p instanceof HLinePrimitive) {
    p.update(d);
    return true;
  }
  if (d.kind === "vline" && p instanceof VLinePrimitive) {
    p.update(d);
    return true;
  }
  if (d.kind === "text" && p instanceof TextPrimitive) {
    p.update(d);
    return true;
  }
  if (d.kind === "measure" && p instanceof MeasurePrimitive) {
    p.update(d);
    return true;
  }
  if ((d.kind === "position-long" || d.kind === "position-short") && p instanceof PositionPrimitive) {
    p.update(d);
    return true;
  }
  return false;
}

export { getHandles };
