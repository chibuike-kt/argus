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
import { GearSix, CircleNotch } from "@phosphor-icons/react";
import { CoinIcon } from "@/components/ui/coin-icon";
import { SampleDataBadge } from "@/components/ui/sample-data-badge";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import { useFlash } from "@/lib/use-flash";
import { useAnalysisWorkspace } from "@/components/workspace/analysis-workspace";
import { formatMoney, toPlanOverlay } from "@/api/mappers";
import { useDrawing } from "./drawing-context";
import { loadChartView, saveChartView } from "./chart-view-persistence";
import { cloneDrawing } from "./drawings/types";
import {
  RectanglePrimitive,
  PositionPrimitive,
  primitiveFor,
  updatePrimitive,
} from "./drawings/primitives";
import { getHandles } from "./drawings/hit-test";
import { DrawingController } from "./drawings/controller";
import { ContextToolbar } from "./drawings/context-toolbar";
import { DrawingContextMenu } from "./drawings/context-menu";

const RANGES = ["1D", "5D", "1M", "3M", "6M", "YTD", "1Y", "5Y", "All"];
// lightweight-charts computes candlestick body width as a fixed, non-
// configurable function of bar spacing alone (optimalCandlestickWidth) --
// there is no separate "thinness" option. fitContent() on this fixture's
// ~20 candles stretches that spacing wide (fat candles); applying this
// explicit, noticeably smaller spacing right after is the only lever,
// comfortably above the library's own 0.5px zoom-out floor.
const DEFAULT_BAR_SPACING = 10;

const C = {
  up: "#2ebd85",
  down: "#f0616d",
  grid: "rgba(255,255,255,0.035)",
  axis: "rgba(255,255,255,0.08)",
  text: "#626873",
  label: "#1c2027",
};

// System annotations (order block, trade plan) are read-only ARGUS output,
// not user drawings -- rendered through the same primitive classes for
// visual consistency, but in the indigo intelligence accent so they read as
// ARGUS's own voice, distinct from the market's green/red, and never part
// of the user drawing store or its hit testing.
const SYSTEM_COLOR = "#6d7bff";

const unix = (iso: string) => Math.floor(Date.parse(iso) / 1000) as Time;

const fmt1 = (p: number) => p.toLocaleString("en-US", { maximumFractionDigits: 1 });
const fmtLast = (p: number) => Math.round(p).toLocaleString("en-US");

type OHLC = { open: number; high: number; low: number; close: number };

export interface ChartCandle {
  openTime: string;
  open: number;
  high: number;
  low: number;
  close: number;
}

export interface ChartPoiZone {
  low: number;
  high: number;
}

export interface ChartPlanOverlay {
  entry: number;
  stop: number;
  targets: number[];
  fromTime: string;
}

// ChartSurface is a thin data-loading wrapper. lightweight-charts and the
// drawing controller are created exactly once per mount
// (ChartSurfaceInner's own create-effect, deps []); candles going in and
// out of a loading state must not force that -- live-chart brief, phase
// 4, Part 2A: "switching instrument or interval clears and reloads
// [through the data path] without destroying the chart."
//
// Before this: this component conditionally rendered either a loading/
// error/empty placeholder OR <ChartSurfaceInner>, swapping one React
// element tree for a structurally different one every time setup/
// candles changed. React has no way to know those two trees are "the
// same chart, new data" -- it tears down ChartSurfaceInner (destroying
// the chart, drawings layer, zoom, scroll) and mounts a fresh instance
// once candles arrive again. hasLoadedOnceRef is what fixes that:
// the full-page loading/error/empty states are shown only until the
// first successful load, for the first instrument this page ever
// shows. After that, ChartSurfaceInner stays mounted and renders its
// own small loading/error overlay (below) over whatever chart is
// already there, however many instruments or setups come and go.
export function ChartSurface({ className }: { className?: string }) {
  const { setup, instrument, candles, candlesLoading, candlesError, setupLoading } =
    useAnalysisWorkspace();
  const hasLoadedOnceRef = useRef(false);
  if (candles.length > 0) hasLoadedOnceRef.current = true;

  if (!hasLoadedOnceRef.current) {
    if (setupLoading || candlesLoading) {
      return (
        <div className={cn("flex h-full w-full items-center justify-center gap-2 text-fg-subtle", className)}>
          <CircleNotch className="size-5 animate-spin" />
          Loading chart…
        </div>
      );
    }
    if (candlesError) {
      return (
        <div className={cn("flex h-full w-full items-center justify-center text-[13px] text-down", className)}>
          Could not load candles: {candlesError.message}
        </div>
      );
    }
    if (!setup || !instrument || candles.length === 0) {
      return (
        <div className={cn("flex h-full w-full items-center justify-center text-[13px] text-fg-subtle", className)}>
          No candles in range for this instrument.
        </div>
      );
    }
  }
  // Past the first successful load: setup/instrument are only ever
  // cleared together with candles (AnalysisWorkspaceProvider's own
  // candle-fetch effect), so if candles is non-empty here, setup and
  // instrument already are too -- but TypeScript cannot see that
  // across a hook boundary, so this still needs the same guard, now
  // reached only on the (rare, transient) in-between render where
  // candles has not cleared yet but setup/instrument briefly have.
  if (!setup || !instrument) {
    return hasLoadedOnceRef.current ? null : (
      <div className={cn("flex h-full w-full items-center justify-center text-[13px] text-fg-subtle", className)}>
        No candles in range for this instrument.
      </div>
    );
  }

  // chartCandles/overlay/formationOutsideWindow read setup together
  // with candles; during a setup switch, AnalysisWorkspaceProvider's
  // own candle-fetch effect sets candlesLoading before clearing
  // candles, so for one render these are a real, previous instrument's
  // own candles paired with the new setup. ChartSurfaceInner's own
  // update effect (see below) only re-applies data when candles
  // actually changes, so this stale pairing is never drawn -- but
  // formationOutsideWindow computed from it here, this render, would
  // be meaningless. Skip it (false) whenever a fetch for the current
  // setup has not resolved even once.
  const chartCandles: ChartCandle[] =
    candles.length === 0
      ? []
      : candles.map((c) => ({
          openTime: c.open_time,
          open: formatMoney(c.open),
          high: formatMoney(c.high),
          low: formatMoney(c.low),
          close: formatMoney(c.close),
        }));
  const overlay = toPlanOverlay(setup);
  // The chart window is now "the most recent MAX_CANDLES," not anchored
  // at this setup's own evaluation_time (web-wiring-followup brief, Part
  // 5) -- an older setup's formation candle can fall before that window
  // starts. Rather than silently clipping the plan box/POI zone to
  // whatever candle happens to be first on screen, this is surfaced as a
  // small, honest label instead of hidden.
  const formationOutsideWindow =
    chartCandles.length > 0 && new Date(overlay.fromTime) < new Date(chartCandles[0].openTime);

  return (
    <ChartSurfaceInner
      className={className}
      symbol={instrument.native_symbol}
      venue={instrument.provider}
      candles={chartCandles}
      loading={candlesLoading}
      loadError={candlesError?.message}
      poiZone={{ low: overlay.poiLow, high: overlay.poiHigh }}
      plan={{ entry: overlay.entry, stop: overlay.stop, targets: overlay.targets, fromTime: overlay.fromTime }}
      formationOutsideWindow={formationOutsideWindow}
    />
  );
}

const EMPTY_BAR: OHLC = { open: 0, high: 0, low: 0, close: 0 };

// Exported for chart-surface-update-path.test.tsx (Part 2A's own
// component test: switching instrument/interval must not rebuild the
// chart) -- it renders this directly, under a real DrawingProvider, to
// exercise the actual create/update-effect split without needing the
// whole AnalysisWorkspaceProvider fetch chain ChartSurface itself sits
// behind.
export interface ChartSurfaceInnerProps {
  className?: string;
  symbol: string;
  venue: string;
  candles: ChartCandle[];
  loading: boolean;
  loadError: string | undefined;
  poiZone: ChartPoiZone;
  plan: ChartPlanOverlay;
  formationOutsideWindow: boolean;
}

export function ChartSurfaceInner({
  className,
  symbol,
  venue,
  candles,
  loading,
  loadError,
  poiZone,
  plan,
  formationOutsideWindow,
}: ChartSurfaceInnerProps) {
  const hasCandles = candles.length > 0;
  const lastTime = hasCandles ? unix(candles[candles.length - 1].openTime) : (0 as Time);
  const toData = (): CandlestickData<Time>[] =>
    candles.map((c) => ({
      time: unix(c.openTime),
      open: c.open,
      high: c.high,
      low: c.low,
      close: c.close,
    }));
  const hostRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const controllerRef = useRef<DrawingController | null>(null);
  const attachedRef = useRef<Map<string, ISeriesPrimitive<Time>>>(new Map());
  // System annotations (POI zone, plan overlay) are tracked here, not
  // user drawings -- attachedRef above is the drawing store's own
  // reconciliation target, scoped to the drawings the user actually
  // placed. These two are recreated (never mutated in place: neither
  // primitive type exposes an update() the way user-drawing primitives
  // do via updatePrimitive) whenever candles or the plan itself changes.
  const systemPrimitivesRef = useRef<{ poi: RectanglePrimitive | null; plan: PositionPrimitive | null }>({
    poi: null,
    plan: null,
  });
  const [bar, setBar] = useState<OHLC>(hasCandles ? candles[candles.length - 1] : EMPTY_BAR);
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

  const {
    activeTool,
    setActiveTool,
    magnetOn,
    stayInDrawingMode,
    locked,
    hidden,
    drawings,
    timeframe,
  } = useDrawing();
  // Captured once, at mount -- the chart does not currently react to a
  // timeframe change after the fact (it still renders the one fixture
  // candle set regardless), so this just needs to match whatever scope
  // the drawings store itself was given at that same moment.
  const chartViewScopeRef = useRef(`${symbol}:${timeframe}`);
  const {
    drawings: list,
    selectedId,
    selected,
    add,
    update,
    remove,
    select,
    bringToFront,
    store,
  } = drawings;
  // The crosshair handler's own "nothing under the cursor" fallback
  // needs the latest last bar, but it is registered once, in the
  // create effect below, and never re-registered -- a closure over
  // candles there would be stuck on whatever candles were at mount
  // forever, for exactly the reason this whole file is being split in
  // two. applyCandleData keeps this ref current instead.
  const lastBarRef = useRef<OHLC>(hasCandles ? candles[candles.length - 1] : EMPTY_BAR);

  // applyCandleData is this component's own "data path" (Part 2A):
  // setData plus repositioning the two system primitives (the POI zone
  // and the plan box, recreated rather than mutated -- neither
  // primitive type exposes an update() the way a user drawing's own
  // primitive does), told to the drawing controller, and the OHLC
  // legend reset to the new last bar. Never touches createChart,
  // addSeries, or the DrawingController instance itself -- called from
  // the create effect once at mount, and from the update effect below
  // on every later candles/plan change, so instrument and interval
  // switches "clear and reload" without rebuilding the chart (Part
  // 2A's own rule). A real live single-bar update (Part 2D, not built
  // in this phase) will call series.update() directly and must not
  // route through here, which always does a full setData.
  function applyCandleData() {
    const series = seriesRef.current;
    const chart = chartRef.current;
    if (!series || !chart) return;
    if (!hasCandles) {
      series.setData([]);
      const prev = systemPrimitivesRef.current;
      if (prev.poi) series.detachPrimitive(prev.poi);
      if (prev.plan) series.detachPrimitive(prev.plan);
      systemPrimitivesRef.current = { poi: null, plan: null };
      lastBarRef.current = EMPTY_BAR;
      setBar(EMPTY_BAR);
      return;
    }

    series.setData(toData());

    const prev = systemPrimitivesRef.current;
    if (prev.poi) series.detachPrimitive(prev.poi);
    if (prev.plan) series.detachPrimitive(prev.plan);

    // SMC annotations: ARGUS's own read-only output, in its own layer.
    // Anchored at the POI's own real formation candle (plan.fromTime),
    // not a fixture array index. If that candle fell outside the fetched
    // window (the chart's own candle request is bounded, the setup's
    // formation candle might predate it), fall back to the first visible
    // candle rather than throwing.
    const fromIndex = Math.max(0, candles.findIndex((c) => c.openTime === plan.fromTime));
    const poi = new RectanglePrimitive({
      p1: { time: unix(candles[fromIndex].openTime), price: poiZone.high },
      p2: { time: lastTime, price: poiZone.low },
      style: { color: SYSTEM_COLOR, lineWidth: 1, lineStyle: "solid" },
      label: "Order block",
    });
    const planPrimitive = new PositionPrimitive({
      kind: "position-long",
      time: unix(candles[fromIndex].openTime),
      rightTime: lastTime,
      entry: plan.entry,
      stop: plan.stop,
      targets: plan.targets,
      style: { color: SYSTEM_COLOR, lineWidth: 1, lineStyle: "solid" },
    });
    series.attachPrimitive(poi);
    series.attachPrimitive(planPrimitive);
    systemPrimitivesRef.current = { poi, plan: planPrimitive };

    // Structure labels (confirmed swings, BOS/CHOCH, the equilibrium
    // line) are deliberately NOT drawn here: they come from
    // structure_events/structure_state, which have no per-setup API
    // today (see the wiring brief's own report). Overlaying fixture
    // values for these on top of real candles would misrepresent them as
    // findings about this real data, which this wiring is specifically
    // meant not to do -- so this real-data path draws only the POI zone
    // and the plan box above, both read directly from the setup's own
    // plan_snapshot.

    // Premium/discount split (spec section 8, the active range's
    // equilibrium) is likewise not drawn here: no per-setup API field
    // carries it today. See the comment above this block.

    controllerRef.current?.setCandles(candles);
    lastBarRef.current = candles[candles.length - 1];
    setBar(candles[candles.length - 1]);
  }

  // chart init (once)
  useEffect(() => {
    if (!hostRef.current) return;

    const chart = createChart(hostRef.current, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: "transparent" },
        textColor: C.text,
        fontFamily: "IBM Plex Mono, ui-monospace, monospace",
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
      lines: [
        {
          text: symbol,
          color: "rgba(255,255,255,0.035)",
          fontSize: 64,
          fontStyle: "bold",
        },
      ],
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
    seriesRef.current = series;

    // Restore wherever the trader last left the view (per instrument +
    // timeframe); only fall back to the default fit (and the explicit
    // thinner spacing fitContent's own wide auto-fit would otherwise
    // leave in place) if nothing was saved yet.
    const savedView = loadChartView(chartViewScopeRef.current);
    if (savedView) {
      chart.timeScale().setVisibleLogicalRange(savedView);
    } else {
      chart.timeScale().fitContent();
      chart.timeScale().applyOptions({ barSpacing: DEFAULT_BAR_SPACING });
    }

    chart.subscribeCrosshairMove((param) => {
      const d = param.seriesData.get(series) as CandlestickData<Time> | undefined;
      if (d) setBar({ open: d.open, high: d.high, low: d.low, close: d.close });
      else setBar(lastBarRef.current);
    });
    chart.timeScale().subscribeVisibleLogicalRangeChange((range) => {
      setToolbarTick((t) => t + 1);
      if (range) saveChartView(chartViewScopeRef.current, range);
    });

    const controller = new DrawingController(chart, series, hostRef.current, store, {
      onCommitted: () => setActiveTool("cross"),
      onContextMenu: (id, x, y) => setContextMenu({ id, x, y }),
      onSelectionChange: () => setToolbarTick((t) => t + 1),
    });
    controller.attach();
    controllerRef.current = controller;
    applyCandleData();
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

  // data path (Part 2A): candles or the plan overlay changed after
  // mount -- apply the new data without touching createChart/addSeries/
  // DrawingController. Skipped on the render that created the chart
  // (chartGeneration still 0 then; the create effect above already
  // called applyCandleData for that first render's own data) so the
  // very first load is not applied twice.
  const isFirstDataEffectRef = useRef(true);
  useEffect(() => {
    if (isFirstDataEffectRef.current) {
      isFirstDataEffectRef.current = false;
      return;
    }
    if (!chartRef.current || !seriesRef.current) return;

    // A genuine instrument switch resets the view (fitContent, or the
    // new scope's own saved one): the old zoom/scroll was over a
    // different price range entirely and generally means nothing for
    // the new one. chartViewScopeRef must track the new scope too --
    // it was captured once at mount (see its own declaration, above),
    // which was correct while symbol/timeframe genuinely never changed
    // after that; now they can.
    const newScope = `${symbol}:${timeframe}`;
    const symbolChanged = chartViewScopeRef.current !== newScope;
    chartViewScopeRef.current = newScope;

    applyCandleData();

    if (symbolChanged && hasCandles) {
      const savedView = loadChartView(newScope);
      if (savedView) {
        chartRef.current.timeScale().setVisibleLogicalRange(savedView);
      } else {
        chartRef.current.timeScale().fitContent();
        chartRef.current.timeScale().applyOptions({ barSpacing: DEFAULT_BAR_SPACING });
      }
    }
    // applyCandleData/symbol/timeframe/hasCandles are read fresh each
    // run via closure, same convention the create effect above already
    // uses (this project's own lint config does not enforce
    // react-hooks/exhaustive-deps); candles/poiZone/plan are the only
    // real triggers this effect needs.
  }, [candles, poiZone.low, poiZone.high, plan.entry, plan.stop, plan.fromTime, plan.targets]);

  // keep the controller's tool/option state in sync with context, without
  // re-running chart init
  useEffect(() => controllerRef.current?.setTool(activeTool), [activeTool]);
  useEffect(() => controllerRef.current?.setMagnet(magnetOn), [magnetOn]);
  useEffect(
    () => controllerRef.current?.setStayInDrawingMode(stayInDrawingMode),
    [stayInDrawingMode],
  );
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

  // "up" colours the live dot/O-H-L-C legend by the latest candle's own
  // colour (close vs open) -- real, from the data on screen. There is no
  // endpoint for a day-level $ / % change (see the wiring brief's own
  // report), so that cluster is a SampleDataBadge below, not a computed
  // number dressed up to look real.
  const lastCandle = hasCandles ? candles[candles.length - 1] : EMPTY_BAR;
  const up = lastCandle.close >= lastCandle.open;
  const last = lastCandle.close;

  // A brief flash on whichever OHLC value just changed under the
  // crosshair, instead of the legend just snapping to new numbers.
  const openFlash = useFlash(bar.open);
  const highFlash = useFlash(bar.high);
  const lowFlash = useFlash(bar.low);
  const closeFlash = useFlash(bar.close);

  return (
    <div className={cn("flex h-full w-full flex-col overflow-hidden", className)}>
      <div className="relative min-h-0 flex-1">
        <div className="dot-grid pointer-events-none absolute inset-0" />
        <div ref={hostRef} className="absolute inset-0" />

        {/* A later instrument/setup switch's own loading or error state,
            layered over whatever chart is already on screen -- never a
            full replace (ChartSurface's own doc comment explains why:
            that is what used to destroy the chart on every switch). */}
        {(loading || loadError) && (
          <div className="pointer-events-none absolute inset-x-0 bottom-10 z-10 flex justify-center">
            <div
              className={cn(
                "flex items-center gap-1.5 rounded-md border px-2 py-1 text-[11px] font-medium shadow-sm",
                loadError
                  ? "border-down/30 bg-down/10 text-down"
                  : "border-line bg-surface text-fg-subtle",
              )}
            >
              {loadError ? `Could not load candles: ${loadError}` : (
                <>
                  <CircleNotch className="size-3 animate-spin" />
                  Loading…
                </>
              )}
            </div>
          </div>
        )}

        {selected && toolbarPos && (
          <ContextToolbar
            drawing={selected}
            x={toolbarPos.x}
            y={toolbarPos.y}
            onStyleChange={(patch) =>
              update(selected.id, { style: { ...selected.style, ...patch } })
            }
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
            <CoinIcon symbol={symbol} size={18} />
            <span className="font-semibold text-fg">{symbol}</span>
            <span className="text-fg-subtle">· {venue}</span>
            <span className={cn("ml-1 size-1.5 rounded-full", up ? "bg-up" : "bg-down")} />
          </div>
          <SampleDataBadge label="Sample % change" />
          {formationOutsideWindow && (
            <span className="rounded-md border border-line bg-white/5 px-1.5 py-0.5 text-[11px] text-fg-subtle">
              Setup formed before this window
            </span>
          )}
          <div className="tnum flex items-center gap-2.5 text-fg-subtle">
            <span>
              O{" "}
              <span
                className={cn(
                  "transition-base rounded px-0.5 text-fg-muted",
                  openFlash === "up" && "flash-up",
                  openFlash === "down" && "flash-down",
                )}
              >
                {fmt1(bar.open)}
              </span>
            </span>
            <span>
              H{" "}
              <span
                className={cn(
                  "transition-base rounded px-0.5 text-fg-muted",
                  highFlash === "up" && "flash-up",
                  highFlash === "down" && "flash-down",
                )}
              >
                {fmt1(bar.high)}
              </span>
            </span>
            <span>
              L{" "}
              <span
                className={cn(
                  "transition-base rounded px-0.5 text-fg-muted",
                  lowFlash === "up" && "flash-up",
                  lowFlash === "down" && "flash-down",
                )}
              >
                {fmt1(bar.low)}
              </span>
            </span>
            <span>
              C{" "}
              <span
                className={cn(
                  "transition-base rounded px-0.5",
                  bar.close >= bar.open ? "text-up" : "text-down",
                  closeFlash === "up" && "flash-up",
                  closeFlash === "down" && "flash-down",
                )}
              >
                {fmt1(bar.close)}
              </span>
            </span>
          </div>
        </div>
      </div>

      <div className="flex h-8 shrink-0 items-center justify-between border-t border-line px-2">
        {/* Every one of these buttons did the exact same thing
            regardless of its own label -- fitContent() plus a bar-
            spacing reset, not an actual "last 1 day"/"last 5 years"
            range (web-wiring-polish brief, item 3's own trace). That is
            not real range selection, so these are disabled with a
            tooltip rather than left looking functional. */}
        <div className="flex items-center gap-0.5">
          <Tooltip content="Range selection is not built yet">
            <div className="flex items-center gap-0.5">
              {RANGES.map((r) => (
                <button
                  key={r}
                  disabled
                  aria-disabled="true"
                  className="cursor-not-allowed rounded px-1.5 py-0.5 text-[11px] font-medium text-fg-subtle opacity-50"
                >
                  {r}
                </button>
              ))}
            </div>
          </Tooltip>
        </div>
        <div className="flex items-center gap-1 text-fg-subtle">
          <span className="tnum mr-1 text-[11px]">{fmtLast(last)} · UTC</span>
          <button className="focus-ring rounded px-1.5 py-0.5 text-[11px] font-medium hover:text-fg">
            A
          </button>
          <button className="focus-ring rounded px-1.5 py-0.5 text-[11px] font-medium text-fg">
            L
          </button>
          <button className="focus-ring grid size-6 place-items-center rounded hover:text-fg">
            <GearSix className="size-3.5" />
          </button>
        </div>
      </div>
    </div>
  );
}
