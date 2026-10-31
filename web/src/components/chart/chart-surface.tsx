import { useEffect, useMemo, useRef, useState } from "react";
import {
  createChart,
  CandlestickSeries,
  CrosshairMode,
  ColorType,
  createTextWatermark,
  type IChartApi,
  type ISeriesApi,
  type ISeriesPrimitive,
  type CandlestickData,
  type Time,
} from "lightweight-charts";
import { Settings2 } from "lucide-react";
import { candles, annotations, instrument } from "@/mocks/fixtures";
import { CoinIcon } from "@/components/ui/coin-icon";
import { cn } from "@/lib/cn";
import { useDrawing } from "./drawing-context";
import { cloneDrawing } from "./drawings/types";
import {
  RectanglePrimitive,
  PositionPrimitive,
  HLinePrimitive,
  SystemLabelPrimitive,
  primitiveFor,
  updatePrimitive,
} from "./drawings/primitives";
import { getHandles } from "./drawings/hit-test";
import { DrawingController } from "./drawings/controller";
import { ContextToolbar } from "./drawings/context-toolbar";
import { DrawingContextMenu } from "./drawings/context-menu";

const { orderBlock, plan } = annotations;
const RANGES = ["1D", "5D", "1M", "3M", "6M", "YTD", "1Y", "5Y", "All"];

const C = {
  up: "#d7fe62",
  down: "#ff5b5b",
  grid: "rgba(255,255,255,0.04)",
  axis: "rgba(255,255,255,0.08)",
  text: "#6e6e6e",
  label: "#282828",
};

// System annotations (order block, trade plan) are read-only ARGUS output,
// not user drawings -- rendered through the same primitive classes for
// visual consistency, but in a provenance-blue tone so they read as
// distinct, and never part of the user drawing store or its hit testing.
const SYSTEM_COLOR = "#8ab4ff";

const unix = (iso: string) => Math.floor(Date.parse(iso) / 1000) as Time;
const lastTime = unix(candles[candles.length - 1].openTime);

const toData = (): CandlestickData<Time>[] =>
  candles.map((c) => ({
    time: unix(c.openTime),
    open: c.open,
    high: c.high,
    low: c.low,
    close: c.close,
  }));

const fmt1 = (p: number) => p.toLocaleString("en-US", { maximumFractionDigits: 1 });
const fmtLast = (p: number) => Math.round(p).toLocaleString("en-US");

type OHLC = { open: number; high: number; low: number; close: number };

export function ChartSurface({ className }: { className?: string }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const controllerRef = useRef<DrawingController | null>(null);
  const attachedRef = useRef<Map<string, ISeriesPrimitive<Time>>>(new Map());
  const [bar, setBar] = useState<OHLC>(candles[candles.length - 1]);
  const [toolbarTick, setToolbarTick] = useState(0);
  const [contextMenu, setContextMenu] = useState<{ id: string; x: number; y: number } | null>(null);
  // Bumped once the chart/series/controller are (re)created. React 18
  // StrictMode double-invokes a mount-once effect in dev: init runs, its
  // cleanup tears the chart down, then init runs again. The drawing
  // reconciliation effect below is keyed on the store's own data, which
  // does not change just because the chart was recreated -- without this,
  // any drawing already in the store before that remount (e.g. loaded from
  // localStorage on page load) gets attached to the first, now-destroyed
  // series and never reattached to the one actually on screen. Including
  // chartGeneration in that effect's deps forces it to re-run exactly when
  // the series identity changes, regardless of whether the data did.
  const [chartGeneration, setChartGeneration] = useState(0);

  const { activeTool, setActiveTool, magnetOn, stayInDrawingMode, locked, hidden, drawings } = useDrawing();
  const { drawings: list, selectedId, selected, add, update, remove, select, bringToFront, store } = drawings;

  // chart init (once)
  useEffect(() => {
    if (!hostRef.current) return;

    const chart = createChart(hostRef.current, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: "transparent" },
        textColor: C.text,
        fontFamily: "Poppins, system-ui, sans-serif",
        attributionLogo: false,
      },
      grid: { vertLines: { color: C.grid }, horzLines: { color: C.grid } },
      crosshair: {
        mode: CrosshairMode.Normal,
        vertLine: { color: "rgba(255,255,255,0.25)", width: 1, labelBackgroundColor: C.label },
        horzLine: { color: "rgba(255,255,255,0.25)", width: 1, labelBackgroundColor: C.label },
      },
      rightPriceScale: { borderColor: C.axis, scaleMargins: { top: 0.12, bottom: 0.12 } },
      timeScale: { borderColor: C.axis, timeVisible: true, secondsVisible: false, rightOffset: 6 },
    });
    chartRef.current = chart;

    createTextWatermark(chart.panes()[0], {
      horzAlign: "center",
      vertAlign: "center",
      lines: [{ text: instrument.symbol, color: "rgba(255,255,255,0.035)", fontSize: 64, fontStyle: "bold" }],
    });

    const series = chart.addSeries(CandlestickSeries, {
      upColor: C.up,
      downColor: C.down,
      borderUpColor: C.up,
      borderDownColor: C.down,
      wickUpColor: C.up,
      wickDownColor: C.down,
      priceLineVisible: true,
      priceLineColor: "rgba(255,255,255,0.3)",
    });
    series.setData(toData());
    seriesRef.current = series;

    // SMC annotations: ARGUS's own read-only output, in its own layer.
    series.attachPrimitive(
      new RectanglePrimitive({
        p1: { time: unix(candles[orderBlock.fromIndex].openTime), price: orderBlock.high },
        p2: { time: lastTime, price: orderBlock.low },
        style: { color: SYSTEM_COLOR, lineWidth: 1, lineStyle: "solid" },
        label: "Order block",
      }),
    );
    series.attachPrimitive(
      new PositionPrimitive({
        kind: "position-long",
        time: unix(candles[plan.fromIndex].openTime),
        rightTime: lastTime,
        entry: plan.entry,
        stop: plan.stop,
        targets: plan.targets,
        style: { color: SYSTEM_COLOR, lineWidth: 1, lineStyle: "solid" },
      }),
    );

    // Structure (confirmed swings) and events (BOS/CHOCH/sweep): the
    // "structure, sweeps" part of ARGUS's read, so the chart opens with
    // more than just the POI and the plan already drawn.
    for (const s of annotations.swings) {
      const price = Math.round(s.price).toLocaleString("en-US");
      series.attachPrimitive(
        new SystemLabelPrimitive(
          { time: unix(candles[s.index].openTime), price: s.price },
          `${s.kind === "high" ? "H" : "L"} ${price}`,
          SYSTEM_COLOR,
        ),
      );
    }
    for (const e of annotations.events) {
      series.attachPrimitive(
        new SystemLabelPrimitive({ time: unix(candles[e.index].openTime), price: e.price }, e.label, SYSTEM_COLOR),
      );
    }

    // Premium/discount split (spec section 8): the active range's
    // equilibrium, the SMC buy-zone/sell-zone divider.
    series.attachPrimitive(
      new HLinePrimitive({
        price: annotations.range.equilibrium,
        style: { color: SYSTEM_COLOR, lineWidth: 1, lineStyle: "dashed" },
      }),
    );
    series.attachPrimitive(
      new SystemLabelPrimitive({ time: lastTime, price: annotations.range.equilibrium }, "Equilibrium", SYSTEM_COLOR),
    );

    chart.timeScale().fitContent();

    chart.subscribeCrosshairMove((param) => {
      const d = param.seriesData.get(series) as CandlestickData<Time> | undefined;
      if (d) setBar({ open: d.open, high: d.high, low: d.low, close: d.close });
      else setBar(candles[candles.length - 1]);
    });
    chart.timeScale().subscribeVisibleLogicalRangeChange(() => setToolbarTick((t) => t + 1));

    const controller = new DrawingController(chart, series, hostRef.current, store, {
      onCommitted: () => setActiveTool("cross"),
      onContextMenu: (id, x, y) => setContextMenu({ id, x, y }),
      onSelectionChange: () => setToolbarTick((t) => t + 1),
    });
    controller.setCandles(candles);
    controller.attach();
    controllerRef.current = controller;
    setChartGeneration((g) => g + 1);

    return () => {
      controller.detach();
      controllerRef.current = null;
      chart.remove();
      chartRef.current = null;
      seriesRef.current = null;
      attachedRef.current.clear();
    };
    // store, setActiveTool are stable for the component's lifetime (the
    // store singleton and a state setter), so this intentionally only
    // re-runs on mount/unmount -- re-initialising the whole chart on every
    // drawing change would defeat the point of the primitive layer.
  }, []);

  // keep the controller's tool/option state in sync with context, without
  // re-running chart init
  useEffect(() => controllerRef.current?.setTool(activeTool), [activeTool]);
  useEffect(() => controllerRef.current?.setMagnet(magnetOn), [magnetOn]);
  useEffect(() => controllerRef.current?.setStayInDrawingMode(stayInDrawingMode), [stayInDrawingMode]);
  useEffect(() => controllerRef.current?.setLocked(locked), [locked]);
  useEffect(() => controllerRef.current?.setHidden(hidden), [hidden]);

  // reconcile user drawings -> primitives. Updates in place (no detach/
  // reattach) when a drawing's kind is unchanged, which is the common case
  // during a drag -- smooth even with a dozen-plus drawings on the chart.
  useEffect(() => {
    const series = seriesRef.current;
    if (!series) return;
    const attached = attachedRef.current;

    if (hidden) {
      for (const [, p] of attached) series.detachPrimitive(p);
      attached.clear();
      return;
    }

    for (const d of list) {
      const existing = attached.get(d.id);
      if (existing) {
        if (!updatePrimitive(existing, d)) {
          series.detachPrimitive(existing);
          const p = primitiveFor(d);
          series.attachPrimitive(p);
          attached.set(d.id, p);
        }
      } else {
        const p = primitiveFor(d);
        series.attachPrimitive(p);
        attached.set(d.id, p);
      }
    }
    for (const [id, p] of attached) {
      if (!list.some((d) => d.id === id)) {
        series.detachPrimitive(p);
        attached.delete(id);
      }
    }
    controllerRef.current?.syncSelectionOverlay();
    setToolbarTick((t) => t + 1);
  }, [list, hidden, chartGeneration]);

  useEffect(() => {
    controllerRef.current?.syncSelectionOverlay();
    setToolbarTick((t) => t + 1);
  }, [selectedId, chartGeneration]);

  // Context toolbar position: topmost handle of the selected drawing, in
  // container-relative pixels. toolbarTick forces a recompute on pan/zoom/
  // drag without the toolbar having its own chart subscriptions.
  const toolbarPos = useMemo(() => {
    void toolbarTick;
    const chart = chartRef.current;
    const series = seriesRef.current;
    if (!chart || !series || !selected || hidden || locked) return null;
    const handles = getHandles(selected, { chart, series });
    if (handles.length === 0) return null;
    const top = handles.reduce((a, b) => (b.px.y < a.px.y ? b : a));
    const avgX = handles.reduce((s, h) => s + h.px.x, 0) / handles.length;
    return { x: avgX, y: top.px.y };
  }, [selected, hidden, locked, toolbarTick]);

  const contextMenuDrawing = contextMenu ? store.get(contextMenu.id) : null;

  const up = instrument.changePct >= 0;
  const chg = (instrument.last * instrument.changePct) / 100;

  return (
    <div className={cn("flex h-full w-full flex-col overflow-hidden rounded-xl", className)}>
      <div className="relative min-h-0 flex-1">
        <div ref={hostRef} className="absolute inset-0" />

        {selected && toolbarPos && (
          <ContextToolbar
            drawing={selected}
            x={toolbarPos.x}
            y={toolbarPos.y}
            onStyleChange={(patch) => update(selected.id, { style: { ...selected.style, ...patch } })}
            onLabelChange={
              selected.kind === "text"
                ? (text) => update(selected.id, { text })
                : selected.kind === "rectangle"
                  ? (label) => update(selected.id, { label })
                  : undefined
            }
            onClone={() => add(cloneDrawing(selected))}
            onDelete={() => remove(selected.id)}
          />
        )}

        {contextMenu && contextMenuDrawing && (
          <DrawingContextMenu
            x={contextMenu.x}
            y={contextMenu.y}
            onClone={() => add(cloneDrawing(contextMenuDrawing))}
            onDelete={() => remove(contextMenu.id)}
            onBringToFront={() => bringToFront(contextMenu.id)}
            onSettings={() => {
              select(contextMenu.id);
            }}
            onClose={() => setContextMenu(null)}
          />
        )}

        <div className="pointer-events-none absolute left-3 top-2.5 z-10 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px]">
          <div className="flex items-center gap-1.5">
            <CoinIcon symbol={instrument.symbol} size={18} />
            <span className="font-semibold text-fg">{instrument.symbol}</span>
            <span className="text-fg-subtle">· 5m · {instrument.venue}</span>
            <span className={cn("ml-1 size-1.5 rounded-full", up ? "bg-up" : "bg-down")} />
          </div>
          <span className={cn("tnum font-medium", up ? "text-up" : "text-down")}>
            {up ? "+" : ""}
            {fmt1(chg)} ({up ? "+" : ""}
            {instrument.changePct.toFixed(2)}%)
          </span>
          <div className="tnum flex items-center gap-2.5 text-fg-subtle">
            <span>
              O <span className="text-fg-muted">{fmt1(bar.open)}</span>
            </span>
            <span>
              H <span className="text-fg-muted">{fmt1(bar.high)}</span>
            </span>
            <span>
              L <span className="text-fg-muted">{fmt1(bar.low)}</span>
            </span>
            <span>
              C{" "}
              <span className={bar.close >= bar.open ? "text-up" : "text-down"}>{fmt1(bar.close)}</span>
            </span>
          </div>
        </div>
      </div>

      <div className="flex h-8 shrink-0 items-center justify-between border-t border-line px-2">
        <div className="flex items-center gap-0.5">
          {RANGES.map((r) => (
            <button
              key={r}
              onClick={() => chartRef.current?.timeScale().fitContent()}
              className={cn(
                "focus-ring rounded px-1.5 py-0.5 text-[11px] font-medium transition-colors",
                r === "1D" ? "text-accent" : "text-fg-subtle hover:text-fg",
              )}
            >
              {r}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-1 text-fg-subtle">
          <span className="tnum mr-1 text-[11px]">{fmtLast(instrument.last)} · UTC+1</span>
          <button className="focus-ring rounded px-1.5 py-0.5 text-[11px] font-medium hover:text-fg">A</button>
          <button className="focus-ring rounded px-1.5 py-0.5 text-[11px] font-medium text-accent">L</button>
          <button className="focus-ring grid size-6 place-items-center rounded hover:text-fg">
            <Settings2 className="size-3.5" />
          </button>
        </div>
      </div>
    </div>
  );
}
