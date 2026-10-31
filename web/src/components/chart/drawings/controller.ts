import type { IChartApi, ISeriesApi, ISeriesPrimitive, SeriesType, Time } from "lightweight-charts";
import type { Candle } from "@/types/domain";
import type { DrawingsStore } from "./store";
import {
  DEFAULT_STYLE,
  TOOL_TO_KIND,
  TWO_POINT_KINDS,
  nextId,
  type DPoint,
  type Drawing,
  type DrawKind,
} from "./types";
import { coordinateToSmoothTimeSeconds, distance, snapToCandle, type ChartAccessors, type Px } from "./geometry";
import { getHandles, hitTest, type Handle, type HitResult } from "./hit-test";
import { primitiveFor, updatePrimitive, SelectionOverlayPrimitive } from "./primitives";
import { rectAnchorsOf, reshapeDrawing, translateDrawing, type RectAnchors } from "./transform";

const DRAG_THRESHOLD_PX = 4;

export interface ControllerCallbacks {
  // Called after a drawing tool commits a new drawing, so the host can
  // reset the rail to the cursor tool unless "stay in drawing mode" is on.
  onCommitted: () => void;
  onContextMenu: (id: string, clientX: number, clientY: number) => void;
  // Lets the host (React) know selection changed, for UI outside the chart
  // (the floating context toolbar's position, the rail's delete button...).
  // The store itself is already the source of truth; this is a convenience
  // nudge so React does not have to poll it.
  onSelectionChange: (id: string | null) => void;
}

interface Draft {
  kind: DrawKind;
  p1: DPoint;
  p2: DPoint;
  awaitingSecondClick: boolean;
  preview: ISeriesPrimitive<Time>;
}

interface DragSession {
  id: string;
  mode: "move" | "handle";
  handle?: Handle;
  rectAnchors?: RectAnchors;
  origin: DPoint; // chart-space point under the pointer at drag start
  original: Drawing; // snapshot to diff against, and to restore on Esc
  historyPushed: boolean;
}

/**
 * Owns tool state, the in-progress draft, hit testing, selection, and drag --
 * everything pointer-driven that is not rendering (primitives.ts) or state
 * (store.ts). One instance per chart mount; React effects call setTool/
 * setMagnet/setStayInDrawingMode/setCandles as those values change, and
 * attach/detach bind and unbind the actual DOM listeners.
 */
export class DrawingController {
  private tool = "cursor";
  private magnetOn = false;
  private stayInDrawingMode = false;
  private locked = false;
  private hidden = false;
  private candles: Candle[] = [];

  private draft: Draft | null = null;
  private drag: DragSession | null = null;
  private pointerDownPx: Px | null = null;
  private selectionOverlay: ISeriesPrimitive<Time> | null = null;

  constructor(
    private chart: IChartApi,
    private series: ISeriesApi<SeriesType, Time>,
    private host: HTMLElement,
    private store: DrawingsStore,
    private cb: ControllerCallbacks,
  ) {}

  private get acc(): ChartAccessors {
    return { chart: this.chart, series: this.series };
  }

  setTool(tool: string) {
    if (this.tool === tool) return;
    this.cancelDraft();
    this.tool = tool;
    this.applyCursorAndPan();
  }
  setMagnet(on: boolean) {
    this.magnetOn = on;
  }
  setStayInDrawingMode(on: boolean) {
    this.stayInDrawingMode = on;
  }
  setCandles(c: Candle[]) {
    this.candles = c;
  }
  // The position tool's default forward width, in bars -- used only to
  // seed rightTime on creation; dragging the right handle afterwards is
  // unaffected by this constant.
  private static readonly DEFAULT_FORWARD_BARS = 20;
  private static readonly FALLBACK_BAR_SECONDS = 300; // 5m, matching the fixtures
  private barIntervalSeconds(): number {
    if (this.candles.length < 2) return DrawingController.FALLBACK_BAR_SECONDS;
    const a = Date.parse(this.candles[0].openTime) / 1000;
    const b = Date.parse(this.candles[1].openTime) / 1000;
    const diff = b - a;
    return diff > 0 ? diff : DrawingController.FALLBACK_BAR_SECONDS;
  }
  setLocked(on: boolean) {
    this.locked = on;
  }
  setHidden(on: boolean) {
    this.hidden = on;
    if (on) {
      this.store.select(null);
      this.cb.onSelectionChange(null);
    }
  }

  private applyCursorAndPan() {
    const kind = TOOL_TO_KIND[this.tool];
    const drawMode = Boolean(kind) || this.tool === "eraser";
    this.chart.applyOptions({ handleScroll: !drawMode, handleScale: !drawMode });
    this.host.style.cursor = drawMode ? "crosshair" : "";
  }

  attach() {
    this.host.addEventListener("pointerdown", this.onPointerDown);
    this.host.addEventListener("pointermove", this.onPointerMove);
    this.host.addEventListener("pointerup", this.onPointerUp);
    this.host.addEventListener("contextmenu", this.onContextMenu);
    window.addEventListener("keydown", this.onKeyDown);
    this.applyCursorAndPan();
  }

  detach() {
    this.host.removeEventListener("pointerdown", this.onPointerDown);
    this.host.removeEventListener("pointermove", this.onPointerMove);
    this.host.removeEventListener("pointerup", this.onPointerUp);
    this.host.removeEventListener("contextmenu", this.onContextMenu);
    window.removeEventListener("keydown", this.onKeyDown);
    this.cancelDraft();
    this.detachSelectionOverlay();
  }

  // Called by the host whenever the selected drawing changes (including
  // from outside the controller, e.g. a context-toolbar edit) so the
  // handle overlay tracks it.
  syncSelectionOverlay() {
    const id = this.store.selectedId;
    if (!id) {
      this.detachSelectionOverlay();
      return;
    }
    if (!this.selectionOverlay) {
      this.selectionOverlay = new SelectionOverlayPrimitive(() => {
        const d = this.store.get(this.store.selectedId ?? "");
        return d ? getHandlesSafe(d, this.acc) : [];
      });
      this.series.attachPrimitive(this.selectionOverlay);
    } else {
      (this.selectionOverlay as SelectionOverlayPrimitive).repaint();
    }
  }

  private detachSelectionOverlay() {
    if (this.selectionOverlay) {
      this.series.detachPrimitive(this.selectionOverlay);
      this.selectionOverlay = null;
    }
  }

  // --- pointer helpers ---

  private clientToPx(e: PointerEvent): Px {
    const r = this.host.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  }

  private resolvePoint(px: Px): DPoint | null {
    const price = this.series.coordinateToPrice(px.y);
    if (price == null) return null;

    // Smooth (sub-bar) time by default -- coordinateToTime snaps to the
    // nearest bar, which is what made dragging step bar by bar. Falls back
    // to the library's own (bar-snapped) resolution only when there are
    // too few candles to interpolate at all.
    const smoothSeconds = coordinateToSmoothTimeSeconds(this.acc, px.x, this.candles);
    let time: Time;
    if (smoothSeconds != null) {
      time = smoothSeconds as unknown as Time;
    } else {
      const fallback = this.chart.timeScale().coordinateToTime(this.paneWidth()) ?? (0 as Time);
      time = (this.chart.timeScale().coordinateToTime(px.x) ?? fallback) as Time;
    }

    const point: DPoint = { time, price };
    // Magnet (candle O/H/L/C snap) is the only thing that should quantize
    // the point -- off by default, so placement and dragging stay smooth.
    return this.magnetOn ? snapToCandle(point, this.candles) : point;
  }

  private paneWidth(): number {
    return this.host.getBoundingClientRect().width;
  }

  // --- draft (creating a new drawing) ---

  private startDraft(kind: DrawKind, p: DPoint) {
    const draft: Draft = {
      kind,
      p1: p,
      p2: p,
      awaitingSecondClick: false,
      preview: primitiveFor(draftDrawing(kind, p, p)),
    };
    this.series.attachPrimitive(draft.preview);
    this.draft = draft;
  }

  private updateDraft(p2: DPoint) {
    if (!this.draft) return;
    this.draft.p2 = p2;
    updatePrimitive(this.draft.preview, draftDrawing(this.draft.kind, this.draft.p1, p2));
  }

  private commitDraft(p2: DPoint) {
    if (!this.draft) return;
    const { kind, p1 } = this.draft;
    this.series.detachPrimitive(this.draft.preview);
    this.draft = null;
    this.store.add(draftDrawing(kind, p1, p2, nextId()));
    this.finishCreation();
  }

  cancelDraft() {
    if (!this.draft) return;
    this.series.detachPrimitive(this.draft.preview);
    this.draft = null;
  }

  private finishCreation() {
    if (!this.stayInDrawingMode) this.cb.onCommitted();
  }

  // --- event handlers ---

  private onPointerDown = (e: PointerEvent) => {
    if (e.button !== 0) return;
    const px = this.clientToPx(e);
    const point = this.resolvePoint(px);
    if (!point) return;

    const kind = TOOL_TO_KIND[this.tool];

    if (kind) {
      if (TWO_POINT_KINDS.includes(kind)) {
        if (this.draft && this.draft.awaitingSecondClick) {
          this.commitDraft(point);
          return;
        }
        this.pointerDownPx = px;
        this.startDraft(kind, point);
        this.host.setPointerCapture(e.pointerId);
        return;
      }
      // One-click tools commit immediately.
      this.store.add(oneClickDrawing(kind, point, this.barIntervalSeconds() * DrawingController.DEFAULT_FORWARD_BARS));
      this.finishCreation();
      return;
    }

    if (this.hidden) return;

    if (this.tool === "eraser") {
      if (this.locked) return;
      const hit = this.hitTestTop(px);
      if (hit) this.store.remove(hit.id);
      return;
    }

    if (this.locked) return;

    // Cursor mode: handle hit (on the already-selected drawing) beats a
    // fresh body hit, so re-grabbing a handle never gets stolen by an
    // overlapping drawing underneath.
    const selected = this.store.selectedId ? this.store.get(this.store.selectedId) : undefined;
    if (selected) {
      const r = hitTest(selected, this.acc, px, this.paneWidth());
      if (r?.kind === "handle") {
        this.beginDrag(selected, point, { mode: "handle", handle: r.handle });
        this.host.setPointerCapture(e.pointerId);
        return;
      }
    }

    const hit = this.hitTestTop(px);
    if (hit) {
      this.store.select(hit.id);
      this.cb.onSelectionChange(hit.id);
      const d = this.store.get(hit.id)!;
      this.beginDrag(d, point, { mode: "move" });
      this.host.setPointerCapture(e.pointerId);
    } else {
      this.store.select(null);
      this.cb.onSelectionChange(null);
    }
  };

  private beginDrag(
    d: Drawing,
    origin: DPoint,
    opts: { mode: "move" | "handle"; handle?: Handle },
  ) {
    this.drag = {
      id: d.id,
      mode: opts.mode,
      handle: opts.handle,
      rectAnchors: d.kind === "rectangle" ? rectAnchorsOf(d) : undefined,
      origin,
      original: d,
      historyPushed: false,
    };
    // Suspend the chart's own pan/zoom for the duration of the drag. In
    // cursor mode (no drawing tool active) applyCursorAndPan() leaves them
    // enabled, since the chart's own canvas receives the same pointer
    // events ours does (they bubble from the same element): without this,
    // dragging a drawing's body also pans the chart underneath it, and the
    // two coordinate shifts cancel out horizontally -- the drawing visibly
    // moves with price but appears stuck in time.
    this.chart.applyOptions({ handleScroll: false, handleScale: false });
  }

  private onPointerMove = (e: PointerEvent) => {
    const px = this.clientToPx(e);

    if (this.draft) {
      const point = this.resolvePoint(px);
      if (point) this.updateDraft(point);
      return;
    }

    if (this.drag) {
      const point = this.resolvePoint(px);
      if (!point) return;
      this.applyDrag(point);
      return;
    }

    // Hover feedback in cursor mode only.
    if (!this.hidden && !this.locked && !TOOL_TO_KIND[this.tool] && this.tool !== "eraser") {
      this.updateHoverCursor(px);
    }
  };

  private applyDrag(point: DPoint) {
    const drag = this.drag;
    if (!drag) return;
    const deltaPrice = point.price - drag.origin.price;
    const deltaTimeSec = timeSecondsOf(point.time) - timeSecondsOf(drag.origin.time);

    const patch =
      drag.mode === "move"
        ? translateDrawing(drag.original, deltaPrice, deltaTimeSec)
        : reshapeDrawing(drag.original, drag.handle!.id, point, drag.rectAnchors);

    this.store.update(drag.id, patch, { history: !drag.historyPushed });
    drag.historyPushed = true;
    this.syncSelectionOverlay();
  }

  private onPointerUp = (e: PointerEvent) => {
    const px = this.clientToPx(e);

    if (this.draft) {
      const point = this.resolvePoint(px) ?? this.draft.p2;
      const moved = this.pointerDownPx ? distance(this.pointerDownPx, px) : 0;
      if (this.host.hasPointerCapture(e.pointerId)) this.host.releasePointerCapture(e.pointerId);
      if (moved > DRAG_THRESHOLD_PX) {
        this.commitDraft(point);
      } else {
        this.draft.awaitingSecondClick = true;
      }
      return;
    }

    if (this.drag) {
      if (this.host.hasPointerCapture(e.pointerId)) this.host.releasePointerCapture(e.pointerId);
      this.drag = null;
      this.applyCursorAndPan(); // restores pan/zoom now the drag is over
    }
  };

  private onContextMenu = (e: MouseEvent) => {
    if (this.hidden || this.locked) return;
    const px = this.clientToPx(e as unknown as PointerEvent);
    const hit = this.hitTestTop(px);
    if (!hit) return;
    e.preventDefault();
    this.store.select(hit.id);
    this.cb.onSelectionChange(hit.id);
    this.cb.onContextMenu(hit.id, e.clientX, e.clientY);
  };

  private onKeyDown = (e: KeyboardEvent) => {
    const target = e.target as HTMLElement | null;
    const editing =
      target &&
      (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable);
    if (editing) return;

    const mod = e.ctrlKey || e.metaKey;
    if (mod && e.key.toLowerCase() === "z") {
      e.preventDefault();
      if (e.shiftKey) this.store.redo();
      else this.store.undo();
      return;
    }
    if (mod && e.key.toLowerCase() === "y") {
      e.preventDefault();
      this.store.redo();
      return;
    }
    if (e.key === "Escape") {
      if (this.draft) {
        this.cancelDraft();
      } else {
        this.store.select(null);
        this.cb.onSelectionChange(null);
      }
      return;
    }
    if (!this.locked && (e.key === "Delete" || e.key === "Backspace") && this.store.selectedId) {
      e.preventDefault();
      this.store.remove(this.store.selectedId);
      this.cb.onSelectionChange(null);
    }
  };

  // --- hit testing / hover ---

  private hitTestTop(px: Px): { id: string; result: HitResult } | null {
    const list = this.store.list();
    for (let i = list.length - 1; i >= 0; i--) {
      const d = list[i];
      const r = hitTest(d, this.acc, px, this.paneWidth());
      if (r) return { id: d.id, result: r };
    }
    return null;
  }

  private updateHoverCursor(px: Px) {
    const selected = this.store.selectedId ? this.store.get(this.store.selectedId) : undefined;
    if (selected) {
      const r = hitTest(selected, this.acc, px, this.paneWidth());
      if (r?.kind === "handle") {
        this.host.style.cursor = r.handle.cursor === "resize" ? "nwse-resize" : "move";
        return;
      }
    }
    const hit = this.hitTestTop(px);
    this.host.style.cursor = hit ? "move" : "";
  }
}

function timeSecondsOf(t: Time): number {
  return typeof t === "number" ? t : Math.floor(Date.parse(t as unknown as string) / 1000);
}

// Re-reads handles defensively: a drag can momentarily make a rectangle
// degenerate (zero width/height) where a coordinate conversion returns
// null; returning [] for that one frame is preferable to throwing.
function getHandlesSafe(d: Drawing, acc: ChartAccessors): Handle[] {
  try {
    return getHandles(d, acc);
  } catch {
    return [];
  }
}

function draftDrawing(kind: DrawKind, p1: DPoint, p2: DPoint, id = "__draft__"): Drawing {
  const now = Date.now();
  switch (kind) {
    case "trendline":
    case "ray":
    case "extended":
      return { id, kind, p1, p2, style: DEFAULT_STYLE, createdAt: now, updatedAt: now };
    case "rectangle":
      return { id, kind, p1, p2, style: DEFAULT_STYLE, createdAt: now, updatedAt: now };
    case "fib":
      return { id, kind, p1, p2, style: DEFAULT_STYLE, createdAt: now, updatedAt: now };
    case "measure":
      return { id, kind, p1, p2, style: DEFAULT_STYLE, createdAt: now, updatedAt: now };
    default:
      throw new Error(`draftDrawing: ${kind} is not a two-point kind`);
  }
}

function oneClickDrawing(kind: DrawKind, p: DPoint, forwardSeconds: number): Drawing {
  const now = Date.now();
  const id = nextId();
  switch (kind) {
    case "hline":
      return { id, kind, price: p.price, style: DEFAULT_STYLE, createdAt: now, updatedAt: now };
    case "vline":
      return { id, kind, time: p.time, style: DEFAULT_STYLE, createdAt: now, updatedAt: now };
    case "text":
      return { id, kind, p, text: "Text", style: DEFAULT_STYLE, createdAt: now, updatedAt: now };
    case "position-long":
    case "position-short": {
      const span = p.price * 0.004;
      const long = kind === "position-long";
      return {
        id,
        kind,
        time: p.time,
        rightTime: (timeSecondsOf(p.time) + forwardSeconds) as Time,
        entry: p.price,
        stop: long ? p.price - span : p.price + span,
        targets: [long ? p.price + span * 2 : p.price - span * 2],
        style: DEFAULT_STYLE,
        createdAt: now,
        updatedAt: now,
      };
    }
    default:
      throw new Error(`oneClickDrawing: ${kind} is not a one-click kind`);
  }
}
