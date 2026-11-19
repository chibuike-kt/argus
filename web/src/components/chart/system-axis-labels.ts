import type {
  ISeriesApi,
  ISeriesPrimitive,
  ISeriesPrimitiveAxisView,
  SeriesAttachedParameter,
  SeriesType,
  Time,
} from "lightweight-charts";
import { PositionPrimitive, RectanglePrimitive } from "./drawings/primitives";
import { labelColors } from "@/lib/label-contrast";

// ARGUS's own annotations (order block high/low, entry, stop, targets) all
// label the price axis. Drawn as separate native price lines they pile on
// top of each other whenever the levels are close in pixels -- unreadable
// at 4h/1d, where the whole plan spans a few pixels. This module draws them
// through one primitive whose axis labels are re-decided on every render:
// the labels nearest the current price are shown, as long as they do not
// overlap; everything else collapses into one "+N" label.

// [U] display values.
// Axis label height in CSS pixels (the library's own label at its default
// 11-12px font is about 18px tall); labels closer than this overlap.
export const AXIS_LABEL_HEIGHT_PX = 18;
// At most this many annotation labels are shown at once.
export const MAX_SYSTEM_AXIS_LABELS = 4;

export interface AxisLabel {
  key: string;
  price: number;
  y: number;
  color: string;
}

export interface CappedLabels<L extends AxisLabel> {
  shown: L[];
  hidden: L[];
  // Where the single "+N" label for the hidden ones goes, if any.
  groupY: number | null;
}

// capAxisLabels decides which labels fit. anchorY is the series' own last-
// price label (always drawn by the library), so nothing is placed on top of
// it; null when unknown. Labels are taken nearest-to-anchor first; one is
// shown only if it overlaps no label already placed and fewer than
// maxVisible are shown. The rest are hidden, with one group label placed at
// the nearest free slot to the first hidden label. Pure: no chart access.
export function capAxisLabels<L extends AxisLabel>(
  labels: L[],
  anchorY: number | null,
  opts: { labelHeight?: number; maxVisible?: number } = {},
): CappedLabels<L> {
  const h = opts.labelHeight ?? AXIS_LABEL_HEIGHT_PX;
  const max = opts.maxVisible ?? MAX_SYSTEM_AXIS_LABELS;
  const valid = labels.filter((l) => Number.isFinite(l.y));
  const ref = anchorY ?? 0;
  const ordered = [...valid].sort((a, b) => Math.abs(a.y - ref) - Math.abs(b.y - ref) || a.y - b.y);

  const occupied: number[] = anchorY !== null ? [anchorY] : [];
  const free = (y: number) => occupied.every((o) => Math.abs(y - o) >= h);
  const shown: L[] = [];
  const hidden: L[] = [];
  for (const l of ordered) {
    if (shown.length < max && free(l.y)) {
      shown.push(l);
      occupied.push(l.y);
    } else {
      hidden.push(l);
    }
  }

  let groupY: number | null = null;
  if (hidden.length > 0) {
    const start = hidden[0].y;
    for (let k = 0; k < 50 && groupY === null; k++) {
      for (const y of k === 0 ? [start] : [start + k * h, start - k * h]) {
        if (free(y)) {
          groupY = y;
          break;
        }
      }
    }
  }
  return { shown: shown.sort((a, b) => a.y - b.y), hidden, groupY };
}

export interface SystemLevel {
  key: string;
  price: number;
  color: string;
}

// SystemAxisLabelsPrimitive draws the capped labels. setLevels replaces the
// set (on every data/plan change); the cap itself runs inside
// priceAxisViews, which the library calls on every render -- so zooming,
// scrolling or resizing re-decides what fits.
export class SystemAxisLabelsPrimitive implements ISeriesPrimitive<Time> {
  private series?: ISeriesApi<SeriesType, Time>;
  private requestUpdate?: () => void;
  private levels: SystemLevel[] = [];

  constructor(
    private anchorPrice: () => number | null,
    private groupColor: string,
  ) {}

  attached(param: SeriesAttachedParameter<Time, SeriesType>) {
    this.series = param.series;
    this.requestUpdate = param.requestUpdate;
  }
  detached() {
    this.series = undefined;
    this.requestUpdate = undefined;
  }
  setLevels(levels: SystemLevel[]) {
    this.levels = levels;
    this.requestUpdate?.();
  }

  priceAxisViews(): readonly ISeriesPrimitiveAxisView[] {
    const series = this.series;
    if (!series || this.levels.length === 0) return [];
    const labels: (AxisLabel & SystemLevel)[] = [];
    for (const l of this.levels) {
      const y = series.priceToCoordinate(l.price);
      if (y !== null) labels.push({ ...l, y });
    }
    const anchor = this.anchorPrice();
    const anchorY = anchor === null ? null : series.priceToCoordinate(anchor);
    const capped = capAxisLabels(labels, anchorY);
    const fmt = series.priceFormatter();
    const views: ISeriesPrimitiveAxisView[] = capped.shown.map((l) => {
      const c = labelColors(l.color);
      return {
        coordinate: () => l.y,
        text: () => fmt.format(l.price),
        textColor: () => c.text,
        backColor: () => c.back,
      };
    });
    if (capped.groupY !== null) {
      const y = capped.groupY;
      const c = labelColors(this.groupColor);
      views.push({
        coordinate: () => y,
        text: () => `+${capped.hidden.length} levels`,
        textColor: () => c.text,
        backColor: () => c.back,
      });
    }
    return views;
  }
}

// The system annotations' own primitives with their native price-axis
// labels switched off (SystemAxisLabelsPrimitive draws those instead). Same
// drawing otherwise.
export class SystemRectanglePrimitive extends RectanglePrimitive {
  protected priceLineSpecs() {
    return [];
  }
}

export class SystemPositionPrimitive extends PositionPrimitive {
  protected priceLineSpecs() {
    return [];
  }
}

// [U] display values: the smallest a system zone is drawn, so a zone a
// few cents tall (a 1m order block on an hourly chart) never disappears.
// Its real high and low are in the zone's popover; nothing stored changes.
export const MIN_ZONE_HEIGHT_PX = 4;
export const MIN_ZONE_WIDTH_PX = 6;

export interface ZoneRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

// SystemZonePrimitive draws ARGUS's setup zone from its start to its end
// time (system-zone.ts), at least MIN_ZONE_* in size, with its label. It
// keeps the rectangle it last drew so a click on it can open the zone's
// popover. Not a user drawing: no handles, never in the drawing store.
export class SystemZonePrimitive extends RectanglePrimitive {
  lastRect: ZoneRect | null = null;

  constructor(private z: { startSec: number; endSec: number; low: number; high: number; label: string; color: string }) {
    super({
      p1: { time: z.startSec as Time, price: z.high },
      p2: { time: z.endSec as Time, price: z.low },
      style: { color: z.color, lineWidth: 1, lineStyle: "solid" },
      label: z.label,
    });
  }

  protected priceLineSpecs() {
    return [];
  }

  paneViews() {
    return [
      {
        renderer: () => ({
          draw: (target: Parameters<ReturnType<ReturnType<RectanglePrimitive["paneViews"]>[number]["renderer"]>["draw"]>[0]) => {
            const x1 = this.x(this.z.startSec as Time);
            const x2 = this.x(this.z.endSec as Time);
            const y1 = this.y(this.z.high);
            const y2 = this.y(this.z.low);
            if (x1 == null || x2 == null || y1 == null || y2 == null) {
              this.lastRect = null;
              return;
            }
            target.useMediaCoordinateSpace(({ context: ctx }) => {
              let left = Math.min(x1, x2);
              let w = Math.abs(x2 - x1);
              if (w < MIN_ZONE_WIDTH_PX) {
                left -= (MIN_ZONE_WIDTH_PX - w) / 2;
                w = MIN_ZONE_WIDTH_PX;
              }
              let top = Math.min(y1, y2);
              let h = Math.abs(y2 - y1);
              if (h < MIN_ZONE_HEIGHT_PX) {
                top -= (MIN_ZONE_HEIGHT_PX - h) / 2;
                h = MIN_ZONE_HEIGHT_PX;
              }
              this.lastRect = { left, top, width: w, height: h };
              ctx.fillStyle = `${this.z.color}33`;
              ctx.fillRect(left, top, w, h);
              ctx.strokeStyle = `${this.z.color}aa`;
              ctx.lineWidth = 1;
              ctx.strokeRect(left + 0.5, top + 0.5, w - 1, h - 1);
              ctx.font = "600 10px Onest, sans-serif";
              ctx.fillStyle = "#c9cdff";
              ctx.textBaseline = "bottom";
              ctx.fillText(this.z.label.toUpperCase(), left + 2, top - 2);
            });
          },
        }),
      },
    ];
  }
}

// LastPriceLabelPrimitive is the last price label on the price axis, in
// the last bar's own up or down colour, with its text colour from the
// shared contrast rule (lib/label-contrast.ts) -- the library's own
// last-value label picks its text by a different rule (a light green
// label got white text at about 2.4:1), so the series' own label is off
// and this one is drawn instead.
export class LastPriceLabelPrimitive implements ISeriesPrimitive<Time> {
  private series?: ISeriesApi<SeriesType, Time>;
  private requestUpdate?: () => void;

  constructor(private last: () => { price: number; color: string } | null) {}

  attached(param: SeriesAttachedParameter<Time, SeriesType>) {
    this.series = param.series;
    this.requestUpdate = param.requestUpdate;
  }
  detached() {
    this.series = undefined;
    this.requestUpdate = undefined;
  }
  repaint() {
    this.requestUpdate?.();
  }

  priceAxisViews(): readonly ISeriesPrimitiveAxisView[] {
    const series = this.series;
    const last = this.last();
    if (!series || !last) return [];
    const y = series.priceToCoordinate(last.price);
    if (y === null) return [];
    const c = labelColors(last.color);
    const text = series.priceFormatter().format(last.price);
    return [{ coordinate: () => y, text: () => text, textColor: () => c.text, backColor: () => c.back }];
  }
}

