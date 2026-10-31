import type {
  ISeriesPrimitive,
  IChartApi,
  ISeriesApi,
  IPriceLine,
  SeriesType,
  Time,
  SeriesAttachedParameter,
} from "lightweight-charts";
import { candles as fixtureCandles } from "@/mocks/fixtures";
import {
  barsBetween,
  forwardSpan,
  timeSeconds,
  timeSecondsToSmoothCoordinate,
  type ChartAccessors,
} from "./geometry";
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

// ACCENT is ARGUS's own voice -- plan/POI/fib lines, labels, the default
// drawing colour -- kept a different hue from UP/DOWN deliberately, so
// ARGUS's marks never compete with or get mistaken for the market itself.
const ACCENT = "#6d7bff";
const UP = "#2ebd85";
const DOWN = "#f0616d";
const INK = "#0a0b0d";

function roundRect(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  w: number,
  h: number,
  r: number,
) {
  ctx.beginPath();
  ctx.roundRect(x, y, w, h, r);
}

function label(
  ctx: CanvasRenderingContext2D,
  text: string,
  x: number,
  y: number,
  bg: string,
  fg: string,
  align: "left" | "center" = "left",
) {
  // Price/pct chips -- every number in the app renders in the mono face.
  ctx.font = "600 11px IBM Plex Mono, ui-monospace, monospace";
  const padX = 5;
  const w = ctx.measureText(text).width + padX * 2;
  const h = 16;
  const left = align === "center" ? x - w / 2 : x;
  ctx.fillStyle = bg;
  roundRect(ctx, left, y - h / 2, w, h, 3);
  ctx.fill();
  ctx.fillStyle = fg;
  ctx.textBaseline = "middle";
  ctx.textAlign = "left";
  ctx.fillText(text, left + padX, y);
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

  // Every tool's price level(s) show on the price axis itself -- "beside
  // the chart where prices are" -- via native price lines, not as chips
  // drawn on the chart on top of the drawing. lineVisible is always false:
  // each primitive's own draw() still draws its bounded line/box, these
  // price lines exist only for their axis label, in white text on the
  // tool's own colour. Subclasses override priceLineSpecs() with their own
  // levels (the default is none) and call syncAxisPriceLines() from their
  // own update(), after assigning the new data.
  private axisPriceLines: IPriceLine[] = [];

  attached(param: SeriesAttachedParameter<Time, SeriesType>) {
    this.chart = param.chart;
    this.series = param.series;
    this.requestUpdate = param.requestUpdate;
    this.syncAxisPriceLines();
  }
  detached() {
    this.clearAxisPriceLines();
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
  // Default: no price levels of its own (vline, text, system/selection
  // overlays). Override in any primitive that has real price levels.
  protected priceLineSpecs(): { price: number; color: string }[] {
    return [];
  }
  protected syncAxisPriceLines() {
    if (!this.series) return;
    this.clearAxisPriceLines();
    this.axisPriceLines = this.priceLineSpecs().map((s) =>
      this.series!.createPriceLine({
        price: s.price,
        lineVisible: false,
        axisLabelVisible: true,
        color: s.color,
        axisLabelColor: s.color,
        axisLabelTextColor: "#fff",
      }),
    );
  }
  private clearAxisPriceLines() {
    if (!this.series) return;
    this.axisPriceLines.forEach((l) => this.series!.removePriceLine(l));
    this.axisPriceLines = [];
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
    this.syncAxisPriceLines();
    this.repaint();
  }
  protected priceLineSpecs() {
    const color = this.d.style.color;
    return [
      { price: this.d.p1.price, color },
      { price: this.d.p2.price, color },
    ];
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
    this.syncAxisPriceLines();
    this.repaint();
  }
  protected priceLineSpecs() {
    const color = this.d.style.color;
    return [
      { price: this.d.p1.price, color },
      { price: this.d.p2.price, color },
    ];
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
              // Borderless -- a flat tinted zone, not an outlined box.
              // 0.12 matches the position tool's own reward/risk fills.
              ctx.fillStyle = withAlpha(this.d.style.color, 0.12);
              ctx.fillRect(left, top, w, h);
              if (this.d.label) {
                ctx.font = "600 10px Montserrat, sans-serif";
                ctx.fillStyle = "#fff";
                ctx.textAlign = "center";
                ctx.textBaseline = "middle";
                ctx.fillText(this.d.label.toUpperCase(), left + w / 2, top + h / 2);
                ctx.textAlign = "left";
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
    this.syncAxisPriceLines();
    this.repaint();
  }
  protected priceLineSpecs() {
    const { p1, p2, style } = this.d;
    return FIB_LEVELS.map((level) => ({
      price: p1.price + (p2.price - p1.price) * level,
      color: style.color,
    }));
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
            target.useMediaCoordinateSpace(({ context: ctx }) => {
              // Bounded strictly between the fib's own two anchors -- never
              // to the pane's edge. p1/p2 are already each a handle, so the
              // right one (by time) already acts as the draggable right
              // edge; no third anchor needed, unlike the position tool.
              const left = Math.min(x1, x2);
              const right = Math.max(x1, x2);
              const mid = (left + right) / 2;
              for (const level of FIB_LEVELS) {
                const price = p1.price + (p2.price - p1.price) * level;
                const y = this.y(price);
                if (y == null) continue;
                const emphasised = level === 0.5;
                ctx.strokeStyle = emphasised
                  ? this.d.style.color
                  : withAlpha(this.d.style.color, 0.55);
                ctx.lineWidth = emphasised ? 1.5 : 1;
                ctx.setLineDash(
                  emphasised ? [] : this.d.style.lineStyle === "dashed" ? [5, 4] : [],
                );
                ctx.beginPath();
                ctx.moveTo(left, y + 0.5);
                ctx.lineTo(right, y + 0.5);
                ctx.stroke();
                // Ratio only -- the price itself reads on the axis label
                // (see priceLineSpecs), not repeated on the chart too.
                const ratio = level.toFixed(3).replace(/0+$/, "").replace(/\.$/, "");
                label(
                  ctx,
                  ratio,
                  mid,
                  y,
                  emphasised ? this.d.style.color : withAlpha(this.d.style.color, 0.55),
                  "#fff",
                  "center",
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
    this.syncAxisPriceLines();
    this.repaint();
  }
  protected priceLineSpecs() {
    return [{ price: this.d.price, color: this.d.style.color }];
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
              // No on-chart chip -- the price already reads on the axis
              // (see priceLineSpecs), which is the whole point of an hline.
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
              ctx.font = "500 12px Montserrat, sans-serif";
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
    this.syncAxisPriceLines();
    this.repaint();
  }
  protected priceLineSpecs() {
    const { p1, p2 } = this.d;
    const color = p2.price >= p1.price ? UP : DOWN;
    return [
      { price: p1.price, color },
      { price: p2.price, color },
    ];
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
            // The measure tool reports a price move, not an ARGUS judgment,
            // so it speaks the market's own up/down colours, not the
            // intelligence accent.
            const color = up ? UP : DOWN;
            target.useMediaCoordinateSpace(({ context: ctx }) => {
              const left = Math.min(x1, x2);
              const top = Math.min(y1, y2);
              const w = Math.abs(x2 - x1);
              const h = Math.abs(y2 - y1);
              // Borderless -- a flat tinted zone, not an outlined box.
              ctx.fillStyle = withAlpha(color, 0.12);
              ctx.fillRect(left, top, w, h);

              const delta = p2.price - p1.price;
              const pct = (delta / p1.price) * 100;
              const bars = barsBetween(p1.time, p2.time, fixtureCandles);
              const seconds = Math.abs(timeSeconds(p2.time) - timeSeconds(p1.time));
              const elapsed = formatElapsed(seconds);
              const text = `${delta >= 0 ? "+" : ""}${fmt(delta)}  (${pct >= 0 ? "+" : ""}${pct.toFixed(2)}%)  ${bars} bars  ${elapsed}`;
              label(ctx, text, left + w / 2, top - 10, color, "#fff", "center");
            });
          },
        }),
      },
    ];
  }
}

// ---- Position tool (long / short): entry/stop/target lines + reward/risk boxes ----
export class PositionPrimitive extends BasePrimitive {
  constructor(
    private d: Pick<
      PositionDrawing,
      "kind" | "time" | "rightTime" | "entry" | "stop" | "targets" | "style"
    >,
  ) {
    super();
  }
  update(
    d: Pick<
      PositionDrawing,
      "kind" | "time" | "rightTime" | "entry" | "stop" | "targets" | "style"
    >,
  ) {
    this.d = d;
    this.syncAxisPriceLines();
    this.repaint();
  }
  protected priceLineSpecs() {
    const { entry, stop, targets } = this.d;
    return [
      { price: entry, color: ACCENT },
      { price: stop, color: DOWN },
      ...targets.map((t) => ({ price: t, color: UP })),
    ];
  }
  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: RenderTarget) => {
            const { time, rightTime, entry, stop, targets } = this.d;
            const yEntry = this.y(entry);
            const yStop = this.y(stop);
            // The position must always render -- timeToCoordinate returning
            // null (the right anchor, a fixed number of bars forward, lands
            // past the last candle on a short series -- the normal case, not
            // an edge case) is not a reason to skip it. Only a genuinely
            // unresolved price (the series not sized yet) bails.
            if (yEntry == null || yStop == null) return;
            const tps = targets
              .map((t) => ({ t, y: this.y(t) }))
              .filter((o): o is { t: number; y: number } => o.y != null);
            const far = tps.length ? tps[tps.length - 1] : null;
            const acc = this.accessors();
            if (!acc) return;

            target.useMediaCoordinateSpace(({ context: ctx, mediaSize }) => {
              // In range, timeToCoordinate resolves directly. Out of range,
              // fall through to the logical scale, which keeps extrapolating
              // a coordinate beyond the data (unlike timeToCoordinate, which
              // just returns null there) -- the right anchor still lands at
              // a real, finite x proportional to how far forward it is, not
              // snapped to the pane edge. The edge itself is only the
              // absolute last resort, if even that fails to resolve.
              const resolveX = (t: Time, fallback: number) =>
                this.x(t) ??
                timeSecondsToSmoothCoordinate(acc, timeSeconds(t), fixtureCandles) ??
                fallback;
              const xe = resolveX(time, 0);
              const xRight = resolveX(rightTime, mediaSize.width);

              // forwardSpan keeps the left clamp: the left edge can never
              // end up past the right one, which is what let the box fill
              // backwards before.
              const { x0, width: w } = forwardSpan(xe, xRight);

              // No on-chart lines or chip labels for entry/stop/target --
              // only the fill zones themselves, plus their price-axis
              // labels (see priceLineSpecs). The right edge is still fully
              // interactive (its resize handle draws separately, only
              // while selected), bounding these fills to the box's own
              // width without needing a drawn line to mark it.
              //
              // Reward above risk for a long (targets sit above entry, stop
              // below); the mirror holds automatically for a short, since
              // yStop/yEntry/target pixel order flips with price order.
              if (far) {
                ctx.fillStyle = "rgba(46,189,133,0.12)";
                ctx.fillRect(x0, Math.min(yEntry, far.y), w, Math.abs(far.y - yEntry));
              }
              ctx.fillStyle = "rgba(240,97,109,0.12)";
              ctx.fillRect(x0, Math.min(yEntry, yStop), w, Math.abs(yStop - yEntry));
            });
          },
        }),
      },
    ];
  }
}

// ---- System label: a short dotted trendline-style tick at a point, for
// ARGUS's own structure markers (swing highs/lows, CHOCH, sweeps,
// equilibrium) -- just a dotted line and plain text centered on it, no
// chip/background behind the text, the same way a trader marks a swing by
// hand. Never constructed from a Drawing, never attached to the user
// store. ----
const SYSTEM_TICK_HALF_WIDTH = 28;

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
              const x0 = x - SYSTEM_TICK_HALF_WIDTH;
              const x1 = x + SYSTEM_TICK_HALF_WIDTH;
              ctx.strokeStyle = this.color;
              ctx.lineWidth = 1.5;
              ctx.lineCap = "round";
              ctx.setLineDash([1, 4]);
              ctx.beginPath();
              ctx.moveTo(x0, y + 0.5);
              ctx.lineTo(x1, y + 0.5);
              ctx.stroke();
              ctx.setLineDash([]);
              ctx.lineCap = "butt";

              // Plain text, no chip -- sits just above the line rather
              // than on top of it, so neither the dots nor the label
              // compete with each other.
              ctx.font = "600 11px IBM Plex Mono, ui-monospace, monospace";
              ctx.fillStyle = this.color;
              ctx.textAlign = "center";
              ctx.textBaseline = "bottom";
              ctx.fillText(this.text, x, y - 4);
              ctx.textAlign = "left";
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
                ctx.fillStyle = INK;
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
  if (
    (d.kind === "trendline" || d.kind === "ray" || d.kind === "extended") &&
    p instanceof LinePrimitive
  ) {
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
  if (
    (d.kind === "position-long" || d.kind === "position-short") &&
    p instanceof PositionPrimitive
  ) {
    p.update(d);
    return true;
  }
  return false;
}

export { getHandles };
