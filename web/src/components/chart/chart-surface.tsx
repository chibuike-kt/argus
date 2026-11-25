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
  type WhitespaceData,
  type Time,
} from "lightweight-charts";
import { GearSix, CircleNotch } from "@phosphor-icons/react";
import { usePreferences } from "@/lib/preferences";
import type { ApiSetup } from "@/api/types";
import { CoinIcon } from "@/components/ui/coin-icon";
import { compareDecimal, formatDecimal, formatPercent, toneClass } from "@/lib/decimal-display";
import { cn } from "@/lib/cn";
import { LiveFigure } from "@/components/ui/live-figure";
import { LivePrice } from "@/components/ui/live-price";
import { liveReason } from "@/lib/figure-reasons";
import type { LiveStatsSnapshot } from "@/lib/live-stats";
import { useAnalysisWorkspace } from "@/components/workspace/analysis-workspace";
import { formatMoney } from "@/api/mappers";
import { useDrawing } from "./drawing-context";
import type { HistoryBackOutcome, HistoryEdge, LiveTick, OlderPageOutcome } from "@/lib/use-live-candles";
import { historyCapNote, historyStartNote, prependShift, shouldLoadOlder } from "./history-paging";
import type { ConnectionState, WireStatusPayload } from "@/lib/live-socket";
import { isNewerCandleKey, decideApplyAction, type CandleKey } from "@/lib/live-candles-reducer";
import { fromSavedView, loadChartView, saveChartView, toSavedView } from "./chart-view-persistence";
import { defaultVisibleRange } from "./default-view";
import { RANGES, describeShown, rangeStartSec, type RangeKey } from "./range-buttons";
import { INTERVAL_SECONDS, withGapWhitespace } from "./gap-whitespace";
import { LastPriceLabelPrimitive, SystemAxisLabelsPrimitive } from "./system-axis-labels";
import { buildLayers, filtersFromPrefs, type LayerElement } from "./setup-layers";
import { SetupLayersPrimitive } from "./setup-layers-primitive";
import { SetupElementPopover } from "./setup-popover";
import { focusNotice, focusRange, focusSpan } from "./setup-focus";
import { cloneDrawing, nextId, type Drawing } from "./drawings/types";
import { setSeriesTimeAxis } from "./drawings/time-axis";
import {
  primitiveFor,
  updatePrimitive,
} from "./drawings/primitives";
import { getHandles } from "./drawings/hit-test";
import { DrawingController } from "./drawings/controller";
import { ContextToolbar } from "./drawings/context-toolbar";
import { DrawingContextMenu } from "./drawings/context-menu";


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

// Wide enough for the longest label ARGUS puts on the price axis
// ("+12 levels" at 11px) whatever the instrument's price length. Display
// value.
const PRICE_AXIS_MIN_WIDTH_PX = 76;

// The price scale's default margins (fractions of the pane); the top one
// grows to clear the legend (polish pass, item 2). LEGEND_GAP_PX is the
// space kept between the legend and the highest candle. Display values.
const DEFAULT_TOP_MARGIN = 0.12;
const DEFAULT_BOTTOM_MARGIN = 0.12;
const LEGEND_GAP_PX = 10;
// legendTopMargin is the price scale's top margin (a fraction of the pane)
// that keeps every candle below the legend: the legend's bottom edge plus
// LEGEND_GAP_PX, never less than the default and never more than 45% of
// the pane (a tiny pane keeps room for its candles).
export function legendTopMargin(legendBottomPx: number, paneHeightPx: number): number {
  if (paneHeightPx <= 0) return DEFAULT_TOP_MARGIN;
  return Math.min(0.45, Math.max(DEFAULT_TOP_MARGIN, (legendBottomPx + LEGEND_GAP_PX) / paneHeightPx));
}

// When framing a setup whose evidence predates the loaded candles, page
// back this much further. Display value.
const FOCUS_HISTORY_PAD_SEC = 3600;

// A visible-range change counts as the trader's (and is saved) only within
// this long after a wheel, drag or touch on the chart. Display value.
const USER_GESTURE_WINDOW_MS = 1000;

const unix = (iso: string) => Math.floor(Date.parse(iso) / 1000) as Time;

type OHLC = { open: number; high: number; low: number; close: number };

// LegendBar is a bar as the server's decimal strings (the legend shows
// these, never a number the browser rounded); null fields show a dash.
type LegendField = "open" | "high" | "low" | "close";
type LegendBar = Record<LegendField, string | null>;
const LEGEND_FIELDS: readonly LegendField[] = ["open", "high", "low", "close"];
const EMPTY_LEGEND: LegendBar = { open: null, high: null, low: null, close: null };

export interface ChartCandle {
  openTime: string;
  // Numbers for the chart canvas only (lightweight-charts plots numbers).
  open: number;
  high: number;
  low: number;
  close: number;
  // The server's exact decimal strings, for the legend. Optional so a
  // caller with numbers only (a test) still renders; the legend then
  // shows the number's own string.
  text?: LegendBar;
}

function legendOf(c: ChartCandle): LegendBar {
  return c.text ?? { open: String(c.open), high: String(c.high), low: String(c.low), close: String(c.close) };
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
// LegendChange is the chart legend's 24h change for the selected
// instrument, from the page's ONE live stats source -- the very entry the
// side panel and the watchlist read (Phase 3: "from one shared source,
// never two calculations"). D3: against the closed candle exactly 24h
// before the current minute began, computed on the server. A dash
// carries its reason on hover, never a sample badge, never a number
// computed in the browser.
const EMPTY_SNAPSHOT: LiveStatsSnapshot = {
  entries: new Map(),
  loading: true,
  error: undefined,
  connection: "closed",
  everOpen: false,
  fatal: null,
};

function LegendChange() {
  const { statsEntry, statsSnapshot } = useAnalysisWorkspace();
  const snap = statsSnapshot ?? EMPTY_SNAPSHOT;
  const f = statsEntry?.figures ?? null;
  const tone = toneClass(f?.change_24h);
  return (
    <span className="tnum flex items-center" data-testid="legend-change-24h">
      <LiveFigure
        tone="sign"
        signTone={tone}
        text={f?.change_24h != null ? formatDecimal(f.change_24h, { signed: true }) : null}
        reason={liveReason("change_24h", statsEntry, snap)}
        motion={statsEntry?.motion.change_24h}
      />
      <span className={tone}>{" "}(</span>
      <LiveFigure
        tone="sign"
        signTone={tone}
        className="px-0"
        text={f?.change_24h_pct != null ? formatPercent(f.change_24h_pct) : null}
        reason={liveReason("change_24h_pct", statsEntry, snap)}
        motion={statsEntry?.motion.change_24h_pct}
      />
      <span className={tone}>)</span>
    </span>
  );
}

export interface AnalysisLayer {
  label: string;
  visible: boolean;
}

const POI_WORDS: Record<string, string> = { order_block: "order block", qmr: "QMR zone" };

// analysisLabel says what the system layer is, in one line, from the
// setup's own recorded fields (no generated prose).
export function analysisLabel(setup: ApiSetup | undefined, drawn: number): string {
  const count = `${drawn} setup${drawn === 1 ? "" : "s"} drawn`;
  if (!setup) return `ARGUS analysis · ${count}`;
  const poi = POI_WORDS[setup.poi_type] ?? setup.poi_type.replace(/_/g, " ");
  return `ARGUS analysis · ${count} · selected #${setup.id} · ${setup.direction} ${poi} · ${setup.timeframe} · ${setup.state}`;
}

// AnalysisLayerChip labels ARGUS's own layer and toggles it. It is the
// only control for that layer: drawing tools (select, delete, the
// eraser) never touch it.
function AnalysisLayerChip({ layer }: { layer: AnalysisLayer }) {
  if (!layer.visible) return null;
  return (
    <span
      className="pointer-events-auto flex min-w-0 max-w-full items-center gap-1 rounded-md border border-[#6d7bff]/40 bg-[#6d7bff]/10 px-1.5 py-0.5 text-[11px] text-fg-muted"
      data-testid="analysis-layer"
      title={`${layer.label}. ARGUS's own analysis: every setup it found on this instrument, and the selected setup's evidence. Not a drawing: filter or hide it with Setups in the chart controls; it cannot be moved or deleted. Click any element for details.`}
    >
      <span className="size-1.5 shrink-0 rounded-full bg-[#6d7bff]" aria-hidden />
      <span className="min-w-0 truncate">{layer.label}</span>
    </span>
  );
}

export function ChartSurface({ className }: { className?: string }) {
  const {
    setup,
    instrument,
    instrumentNotFound,
    candles,
    candlesLoading,
    candlesError,
    instrumentsLoading,
    forming,
    connectionState,
    status,
    fatalReason,
  } = useAnalysisWorkspace();
  const hasLoadedOnceRef = useRef(false);
  if (candles.length > 0) hasLoadedOnceRef.current = true;

  // Memoized on their real inputs, before any early return: this
  // component re-renders on every live `stats` message (the workspace
  // carries them), and a fresh candles array or targets array on each
  // render re-ran the chart's data path -- a full setData that dropped
  // the live forming bar and reset the legend to the last closed bar
  // several times a second (seen on real data at 1h, Phase 3).
  const chartCandles: ChartCandle[] = useMemo(
    () =>
      candles.map((c) => ({
        openTime: c.open_time,
        open: formatMoney(c.open),
        high: formatMoney(c.high),
        low: formatMoney(c.low),
        close: formatMoney(c.close),
        text: { open: c.open, high: c.high, low: c.low, close: c.close },
      })),
    [candles],
  );
  // ARGUS's own layers (Phase 6.3): every setup it found for this
  // instrument, drawn by setup-layers.ts, and the selected one's evidence.
  // System output, never user drawings: labelled, filterable, hideable (a
  // saved preference), never movable or deletable.
  const prefs = usePreferences();
  const analysisVisible = prefs.analysis_layers_visible !== false;
  const {
    timeframe: chartTimeframe,
    instrumentSetups,
    focusedElement,
    setFocusedElement,
    storyStep,
    selectSetup,
    focusRequest,
    setFocusNotice,
  } = useAnalysisWorkspace();
  const filters = useMemo(() => filtersFromPrefs(prefs.setup_layers), [prefs.setup_layers]);
  const lastCandleSec = candles.length ? Math.floor(Date.parse(candles[candles.length - 1].open_time) / 1000) : 0;
  const formingSec = forming ? Math.floor(Date.parse(forming.candle.open_time) / 1000) : 0;
  const newestSec = Math.max(lastCandleSec, formingSec);
  const layers = useMemo(() => {
    if (!analysisVisible || newestSec === 0) return [] as LayerElement[];
    const list = instrumentSetups ?? [];
    // The selected setup is drawn even when it is not among the listed ones.
    const all = setup && !list.some((x) => x.id === setup.id) ? [...list, setup] : list;
    return buildLayers(all, { chartTimeframe: chartTimeframe ?? "1m", lastCandleSec: newestSec, selectedId: setup?.id, filters });
  }, [analysisVisible, instrumentSetups, setup, chartTimeframe, newestSec, filters]);
  const drawnSetups = useMemo(() => new Set(layers.map((l) => l.setupId)).size, [layers]);
  const analysisLayer = useMemo<AnalysisLayer | undefined>(
    () => (setup || drawnSetups > 0 ? { label: analysisLabel(setup, drawnSetups), visible: analysisVisible } : undefined),
    [setup, drawnSetups, analysisVisible],
  );

  if (!hasLoadedOnceRef.current) {
    // instrumentsLoading, not setupLoading: an instrument with zero
    // setups must still reach the chart below. setupLoading no longer
    // gates anything here -- SetupPanel/BiasLine/etc. show their own
    // "no setups yet" state independently once setup settles, but the
    // chart itself is never blocked on that (see the brief's own real
    // bug report: the chart used to never even mount for such an
    // instrument).
    if (instrumentsLoading || candlesLoading) {
      return (
        <div className={cn("flex h-full w-full items-center justify-center gap-2 text-fg-subtle", className)}>
          <CircleNotch className="size-5 animate-spin" />
          Loading chart…
        </div>
      );
    }
    if (instrumentNotFound) {
      return (
        <div className={cn("flex h-full w-full items-center justify-center text-[13px] text-down", className)}>
          Instrument not found or not available.
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
    if (!instrument || candles.length === 0) {
      return (
        <div className={cn("flex h-full w-full items-center justify-center text-[13px] text-fg-subtle", className)}>
          No candles in range for this instrument.
        </div>
      );
    }
  }
  // Past the first successful load: instrument is only ever cleared
  // together with candles (AnalysisWorkspaceProvider's own candle-
  // fetch effect), so if candles is non-empty here, instrument already
  // is too -- but TypeScript cannot see that across a hook boundary,
  // so this still needs the same guard, now reached only on the
  // (rare, transient) in-between render where candles has not cleared
  // yet but instrument briefly has. setup is deliberately NOT part of
  // this guard any more: an instrument with no setups must still
  // render past this point.
  if (!instrument) {
    return hasLoadedOnceRef.current ? null : (
      <div className={cn("flex h-full w-full items-center justify-center text-[13px] text-fg-subtle", className)}>
        No candles in range for this instrument.
      </div>
    );
  }

  // chartCandles always builds from candles alone now -- never
  // conditioned on setup. overlay/formationOutsideWindow are undefined
  // whenever there is no setup (an instrument with none, or one still
  // loading): ChartSurfaceInner below skips drawing the POI zone and
  // plan box entirely in that case, rather than this component
  // blocking the whole chart on setup the way it used to.
  // The chart window is now "the most recent MAX_CANDLES," not anchored
  // at this setup's own evaluation_time (web-wiring-followup brief, Part
  // 5) -- an older setup's formation candle can fall before that window
  // starts. Rather than silently clipping the plan box/POI zone to
  // whatever candle happens to be first on screen, this is surfaced as a
  // small, honest label instead of hidden.
  const formationOutsideWindow =
    !!setup &&
    chartCandles.length > 0 &&
    new Date(setup.plan_snapshot.poi.order_block_open_time) < new Date(chartCandles[0].openTime);

  return (
    <ChartSurfaceInner
      className={className}
      symbol={instrument.native_symbol}
      instrumentId={instrument.id}
      venue={instrument.provider}
      candles={chartCandles}
      loading={candlesLoading}
      loadError={candlesError?.message}
      layers={layers}
      storyStep={storyStep ?? null}
      focusedElement={focusedElement ?? null}
      onFocusElement={setFocusedElement}
      onOpenSetup={selectSetup}
      selectedSetupId={setup?.id}
      focusRequest={focusRequest ?? 0}
      onFocusNotice={setFocusNotice}
      formationOutsideWindow={formationOutsideWindow}
      analysisLayer={analysisLayer}
      forming={forming}
      connectionState={connectionState}
      status={status}
      fatalReason={fatalReason}
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
  // The selected instrument's id: the saved chart view is keyed by
  // "<instrumentId>:<timeframe>" (a preference, D1). Undefined only before
  // an instrument resolves; nothing is saved then.
  instrumentId?: number;
  venue: string;
  candles: ChartCandle[];
  loading: boolean;
  loadError: string | undefined;
  // Both undefined when there is no setup (an instrument with none, or
  // one still loading) -- the chart still renders candles/live
  // updates/the badge in that case, just with no system annotations
  // drawn (applyCandleData below detaches whatever was attached and
  // draws nothing new).
  // ARGUS's setup layers (setup-layers.ts): every drawn element, in paint
  // order; empty or undefined draws nothing of ARGUS's.
  layers?: LayerElement[];
  // "Play the story": the selected setup's elements up to this step.
  storyStep?: number | null;
  // The element highlighted together with its "Why this setup" row.
  focusedElement?: string | null;
  onFocusElement?: (id: string | null) => void;
  onOpenSetup?: (setupId: number) => void;
  selectedSetupId?: number;
  // Framing the selected setup (polish pass, item 4): each new value asks
  // for one frame; the chart reports a sentence when not all of it fits.
  focusRequest?: number;
  onFocusNotice?: (s: string | null) => void;
  formationOutsideWindow: boolean;
  // The selected setup's layer chip (label and shown/hidden); undefined
  // when no setup is selected (nothing of ARGUS's is drawn).
  analysisLayer?: AnalysisLayer;
  // Part 2D: the latest candle/candle_closed tick, applied via
  // series.update() by its own effect below -- never through the
  // candles prop above (rule 3). null when there is nothing live yet
  // (no subscription, or a fresh one that has not sent its first
  // message).
  forming: LiveTick | null;
  // Part 2F: the status badge's own three inputs, read straight off
  // live-socket.ts/use-live-candles.ts -- never derived or guessed
  // here.
  connectionState: ConnectionState;
  status: WireStatusPayload | null;
  fatalReason: string | null;
}

// useNow re-renders every intervalMs so a duration computed against it
// (this badge's own "last candle Xs ago") actually counts up on
// screen, instead of staying frozen at whatever it read when the one
// status message that set it arrived.
function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

// formatAge renders a millisecond duration as the coarsest unit that
// keeps it readable ("12s", "3m", "4h", "2d") -- a badge, not a
// precise timer.
function formatAge(ms: number): string {
  const seconds = Math.max(0, Math.round(ms / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

// LiveStatusBadge (Part 2F): one badge, three layers of truth, cheapest
// first. A fatal close (Part 2E: the session itself is invalid) always
// wins, since no amount of waiting fixes it. Otherwise the socket's own
// connecting/closed/open lifecycle is shown until the server's own
// per-subscription `status` message arrives -- that message is the one
// source of truth for degraded vs. not; this badge never computes
// that itself from staleness/latency on its own.
//
// The wire's own "live" state is shown as "Connected", with the age of
// the last candle (status.last_event_at) next to it -- never the word
// "Live": nothing in this project has a decided freshness/latency
// threshold (appendix-c-unresolved-decisions.md; ADR-006's own
// "Freshness targets... must be defined numerically before release"),
// so claiming data IS live/fresh would be a claim this badge cannot
// back up yet. "Connected, last candle 12s ago" is exactly what is
// known: the subscription is live, and here is how old its own last
// candle actually is -- the viewer judges staleness themselves until
// that threshold exists. "Degraded" is unchanged: a real stream-loss
// event, not a guess.
function LiveStatusBadge({
  connectionState,
  status,
  fatalReason,
}: {
  connectionState: ConnectionState;
  status: WireStatusPayload | null;
  fatalReason: string | null;
}) {
  const now = useNow(1000);
  if (fatalReason) {
    return (
      <span className="flex shrink-0 items-center gap-1 whitespace-nowrap rounded-md border border-down/30 bg-down/10 px-1.5 py-0.5 text-[11px] text-down">
        <span className="size-1.5 rounded-full bg-down" />
        Disconnected -- sign in again
      </span>
    );
  }
  if (connectionState === "connecting" || connectionState === "closed") {
    return (
      <span className="flex shrink-0 items-center gap-1 whitespace-nowrap rounded-md border border-line bg-white/5 px-1.5 py-0.5 text-[11px] text-fg-subtle">
        <span className="size-1.5 animate-pulse rounded-full bg-fg-subtle" />
        {connectionState === "connecting" ? "Connecting…" : "Reconnecting…"}
      </span>
    );
  }
  if (!status) return null; // open, but the server has not sent a status message yet
  if (status.state === "degraded") {
    return (
      <span className="flex shrink-0 items-center gap-1 whitespace-nowrap rounded-md border border-line bg-white/5 px-1.5 py-0.5 text-[11px] text-down">
        <span className="size-1.5 rounded-full bg-down" />
        Degraded
        {status.provider && <span className="text-fg-subtle">· {status.provider}</span>}
      </span>
    );
  }
  const label = status.state === "live" ? "Connected" : status.state[0].toUpperCase() + status.state.slice(1);
  const age = status.last_event_at ? formatAge(now - new Date(status.last_event_at).getTime()) : null;
  return (
    <span className="flex shrink-0 items-center gap-1 whitespace-nowrap rounded-md border border-line bg-white/5 px-1.5 py-0.5 text-[11px] text-fg-subtle">
      <span className="size-1.5 rounded-full bg-up" />
      {label}
      {age && <span>· last candle {age} ago</span>}
      {status.provider && <span>· {status.provider}</span>}
    </span>
  );
}

export function ChartSurfaceInner({
  className,
  symbol,
  instrumentId,
  venue,
  candles,
  loading,
  loadError,
  layers,
  storyStep = null,
  focusedElement = null,
  onFocusElement,
  onOpenSetup,
  selectedSetupId,
  focusRequest = 0,
  onFocusNotice,
  formationOutsideWindow,
  analysisLayer,
  forming,
  connectionState,
  status,
  fatalReason,
}: ChartSurfaceInnerProps) {
  const hasCandles = candles.length > 0;
  // Missing candles inside the loaded range become whitespace points
  // (time only, no price) so a hole shows as a hole instead of the time
  // axis silently jumping -- display only (withGapWhitespace). timeframe
  // is read at call time (it is declared further down this function).
  const toData = () =>
    withGapWhitespace(
      candles.map((c) => ({
        time: unix(c.openTime),
        open: c.open,
        high: c.high,
        low: c.low,
        close: c.close,
      })),
      INTERVAL_SECONDS[timeframe] ?? 60,
    );
  const hostRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const controllerRef = useRef<DrawingController | null>(null);
  const attachedRef = useRef<Map<string, ISeriesPrimitive<Time>>>(new Map());
  // ARGUS's setup layers: one primitive for every element, created with
  // the chart and fed new elements in place (no reload of the candles).
  const layersPrimitiveRef = useRef<SetupLayersPrimitive | null>(null);
  const layersRef = useRef<LayerElement[]>(layers ?? []);
  layersRef.current = layers ?? [];
  // The system annotations' price-axis labels, capped so they never pile
  // up (SystemAxisLabelsPrimitive). Created once with the chart.
  const systemAxisLabelsRef = useRef<SystemAxisLabelsPrimitive | null>(null);
  // How many data points (candles plus gap whitespace) the series holds
  // now -- the logical-index length the default view is computed from.
  const dataLengthRef = useRef(0);
  // The open time (unix seconds) of every data point on the series, by
  // logical index (whitespace included) -- what a saved view's times are
  // converted against.
  const dataTimesRef = useRef<number[]>([]);
  // The legend: the live bar (the newest candle, moving with every tick),
  // or the bar under the crosshair while hovering. Strings, not numbers.
  const [legend, setLegend] = useState<{ bar: LegendBar; hovering: boolean }>({
    bar: hasCandles ? legendOf(candles[candles.length - 1]) : EMPTY_LEGEND,
    hovering: false,
  });
  // The newest bar's strings (what the legend shows when not hovering),
  // and every loaded bar's strings by open time (unix seconds), for the
  // crosshair -- the series itself only holds numbers.
  const liveLegendRef = useRef<LegendBar>(hasCandles ? legendOf(candles[candles.length - 1]) : EMPTY_LEGEND);
  // The live bar as state too: the last-price label and the symbol dot
  // must re-render on every tick even while the legend shows a hovered bar.
  const [liveBar, setLiveBar] = useState<LegendBar>(liveLegendRef.current);
  const legendByTimeRef = useRef<Map<number, LegendBar>>(new Map());
  const hoveredTimeRef = useRef<number | null>(null);
  // The range buttons' own notice ("Showing 3.5 days, the most history
  // stored"), and the range still waiting for older history.
  const [rangeNotice, setRangeNotice] = useState<string | null>(null);
  // The legend overlay, kept inside the plot (never over the price axis),
  // and the price axis width it is bounded by (polish pass, items 1 and 2).
  const legendRef = useRef<HTMLDivElement>(null);
  const [axisWidth, setAxisWidth] = useState(PRICE_AXIS_MIN_WIDTH_PX);
  // A frame of the selected setup waiting for the chart to be ready (the
  // saved view restored, the setup's elements built, older history in).
  const pendingFocusRef = useRef<{ token: number; historyAsked: boolean; oldestSec: number | null } | null>(null);
  const focusTokenRef = useRef(0);
  // The popover of a clicked ARGUS element, in host pixels.
  const [elementPopover, setElementPopover] = useState<{ id: string; x: number; y: number } | null>(null);
  const [activeRange, setActiveRange] = useState<RangeKey | null>(null);
  const rangeRequestRef = useRef(0);
  // A range waiting for paged-in history to reach the chart: applied by
  // the data path once the series starts at or before oldestSec.
  const pendingRangeRef = useRef<{ key: RangeKey; outcome: HistoryBackOutcome; oldestSec: number; token: number } | null>(null);
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
  const viewScope = `${instrumentId ?? symbol}:${timeframe}`;
  const chartViewScopeRef = useRef(viewScope);
  // pendingRestoreScopeRef: the scope a restore-or-default is still owed
  // for, set the moment chartViewScopeRef changes and cleared only once
  // that restore actually runs against real data. A plain one-shot
  // boolean (checked once, on the same render the scope changed) is not
  // enough: a slow instrument switch clears candles to empty before the
  // new history has loaded, so the render where the scope first changes
  // can easily be the one render where hasCandles is false -- without
  // tracking this across renders, that one-shot moment is spent on an
  // empty chart and the restore never gets a real turn once data
  // actually arrives.
  const pendingRestoreScopeRef = useRef<string | null>(null);
  // staleDataRef: the data last shown under the PREVIOUS scope. On a
  // timeframe (or instrument) switch the scope can change one render before
  // the old candles are cleared -- found on real data: a 4h switch spent its
  // owed restore on the old 1m candles, so the 4h data then kept the
  // library's leftover offset. The owed restore therefore waits while the
  // candles on hand are still exactly the old scope's (same content
  // signature); anything else -- new content, or an empty render and then
  // data -- is the new scope's own data.
  const staleDataRef = useRef<string | null>(null);
  // The signature of the data the last data-effect run applied.
  const appliedSignatureRef = useRef("");
  const dataSignature = hasCandles
    ? `${candles.length}|${candles[0].openTime}|${candles[candles.length - 1].openTime}|${candles[0].open}|${candles[candles.length - 1].close}`
    : "";
  // isProgrammaticRangeChangeRef brackets a call that moves the chart's
  // own visible range but is not a user gesture (applyCandleData's own
  // setData, the saved-view restore, the no-saved-view default fit).
  // subscribeVisibleLogicalRangeChange's own handler (below) reads this
  // to skip saving exactly those changes -- only a range change the
  // trader actually made (wheel, drag, scroll, a toolbar zoom control)
  // may overwrite what is persisted. Before this, EVERY range change
  // was saved, including the ones setData triggers while switching
  // instruments with little or no data yet -- a real, reproduced bug:
  // the saved view for an instrument could be silently overwritten by
  // whatever near-empty range the library picked for a different
  // instrument's data, while that one was still loading.
  const isProgrammaticRangeChangeRef = useRef(false);
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
  // lastAppliedOpenTimeRef/appliedLiveKeyRef back the live-tick effect
  // below (Part 2D): the former is decideApplyAction's own
  // currentLastOpenTime (replace same bar / append a new one / drop an
  // older one, never attempted through update()), the latter is
  // isNewerCandleKey's own "already applied" side of the (open_time,
  // revision, seq) comparison (rule 5) -- rejecting a duplicate or
  // out-of-order message before even reaching the apply-action
  // decision. Both reset in applyCandleData, since any full setData
  // (reload, gap-fill, instrument/interval switch) makes whatever was
  // "last applied" for live-tick purposes stale.
  const lastAppliedOpenTimeRef = useRef<string | null>(hasCandles ? candles[candles.length - 1].openTime : null);
  const appliedLiveKeyRef = useRef<CandleKey | null>(null);
  const appliedEpochRef = useRef<number | null>(null);
  // formingRef: the latest live tick, so a full setData can put the live
  // bar straight back (applyCandleData) instead of leaving it off the
  // chart until the next tick arrives.
  const formingRef = useRef<LiveTick | null>(forming);
  formingRef.current = forming;
  // Scrolling back (L1.2): the range handler is registered once, so it
  // reads the workspace's loader and history edge through refs.
  const loadOlderRef = useRef<(() => Promise<OlderPageOutcome>) | undefined>(undefined);
  const historyEdgeRef = useRef<HistoryEdge | undefined>(undefined);
  // atStart: the oldest held bar is in view (for the history start note).
  const [atStart, setAtStart] = useState(false);
  const atStartRef = useRef(false);

  // withProgrammaticRangeChange brackets fn so any visible-range change
  // it causes is not mistaken for a user gesture by
  // subscribeVisibleLogicalRangeChange's own handler, below. Safe to
  // call synchronously like this: lightweight-charts fires that event
  // synchronously, inline, from within the very call that changes the
  // range (TimeScale's own _setVisibleRange, read directly from the
  // library's source) -- never deferred to a microtask or animation
  // frame, so a plain try/finally around fn correctly brackets it.
  function withProgrammaticRangeChange(fn: () => void): void {
    isProgrammaticRangeChangeRef.current = true;
    try {
      fn();
    } finally {
      isProgrammaticRangeChangeRef.current = false;
    }
  }

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
  // 2A's own rule). A live single-bar update (Part 2D) calls
  // series.update() directly, from its own effect below, and never
  // routes through here, which always does a full setData.
  function applyCandleData(preserveView = false) {
    const series = seriesRef.current;
    const chart = chartRef.current;
    if (!series || !chart) return;
    // preserveView (same instrument and timeframe): if this data only adds
    // older bars in front (scrolling back, L1.2), the same bars stay in
    // view: the logical range is shifted by the number of bars added.
    const prevTimes = dataTimesRef.current;
    const prevRange = preserveView ? chart.timeScale().getVisibleLogicalRange() : null;
    // Any full setData makes whatever the live-tick effect last applied
    // stale -- this is new data (a reload, a gap-fill, an instrument or
    // interval switch), so the next live tick must be compared against
    // THIS data's own last bar, not a previous one.
    appliedLiveKeyRef.current = null;
    if (!hasCandles) {
      series.setData([]);
      dataLengthRef.current = 0;
      dataTimesRef.current = [];
      setSeriesTimeAxis(series, { times: [], intervalSec: INTERVAL_SECONDS[timeframe] ?? 60 });
      lastBarRef.current = EMPTY_BAR;
      lastAppliedOpenTimeRef.current = null;
      legendByTimeRef.current = new Map();
      liveLegendRef.current = EMPTY_LEGEND;
      setLiveBar(EMPTY_LEGEND);
      setLegend({ bar: EMPTY_LEGEND, hovering: false });
      return;
    }

    const data = toData();
    series.setData(data);
    dataLengthRef.current = data.length;
    dataTimesRef.current = data.map((d) => d.time as number);
    // The drawings' time <-> x mapping past either end of the data
    // (drawings/time-axis.ts): the series' own data times, whitespace
    // included, and this timeframe's bar length.
    setSeriesTimeAxis(series, { times: dataTimesRef.current, intervalSec: INTERVAL_SECONDS[timeframe] ?? 60 });
    lastAppliedOpenTimeRef.current = candles[candles.length - 1].openTime;

    // ARGUS's setup layers are not part of the data path: they are their
    // own primitive, anchored by time and price, updated by their own
    // effect below whenever the setups or filters change.

    controllerRef.current?.setCandles(candles);
    lastBarRef.current = candles[candles.length - 1];

    const byTime = new Map<number, LegendBar>();
    for (const c of candles) byTime.set(unix(c.openTime) as number, legendOf(c));
    legendByTimeRef.current = byTime;
    // (Colouring is LivePrice's: a switch resets it via resetKey.)
    const nextLive = legendOf(candles[candles.length - 1]);
    liveLegendRef.current = nextLive;
    setLiveBar(nextLive);
    setLegend((l) => (l.hovering ? l : { bar: nextLive, hovering: false }));

    // The live bar back on at once, not at the next tick: setData above
    // holds closed candles only. A tick older than the newest closed bar is
    // dropped as usual (applyLiveTick).
    if (formingRef.current) applyLiveTick(formingRef.current);

    if (prevRange) {
      const added = prependShift(prevTimes, dataTimesRef.current);
      if (added > 0) chart.timeScale().setVisibleLogicalRange({ from: prevRange.from + added, to: prevRange.to + added });
    }
  }

  // restoreOrDefaultView applies the scope's saved view when it still fits
  // the data on the chart (fromSavedView), else the default range
  // (defaultVisibleRange: about 120 bars ending at the newest, normal bar
  // width, never stretched). Call inside withProgrammaticRangeChange.
  function restoreOrDefaultView(chart: IChartApi, scope: string) {
    const n = dataLengthRef.current;
    const saved = loadChartView(scope);
    const restored = saved ? fromSavedView(saved, dataTimesRef.current) : null;
    if (restored) {
      chart.timeScale().setVisibleLogicalRange(restored);
      return;
    }
    const range = defaultVisibleRange(n);
    if (range) chart.timeScale().setVisibleLogicalRange(range);
  }
  // lastUserGestureAtRef: when the trader last scrolled, dragged or zoomed
  // the chart (wheel, pointer drag, touch on the chart host). A view is
  // saved only when a range change follows a gesture within
  // USER_GESTURE_WINDOW_MS. Seen on real data: the library reports range
  // changes it makes itself -- re-fitting after an autoSize resize, or
  // reporting the default range a frame after it was applied, outside
  // withProgrammaticRangeChange's bracket -- and those were being saved as
  // if the trader had chosen them, so later visits restored stale bar
  // indices instead of re-anchoring on the newest bar.
  const lastUserGestureAtRef = useRef(Number.NEGATIVE_INFINITY);

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
      // minimumWidth: the library sizes the axis from the series' own
      // price labels only, so on an instrument with short prices (SOL
      // "72.71") ARGUS's "+N levels" label was cut off ("els").
      rightPriceScale: { borderColor: C.axis, scaleMargins: { top: 0.12, bottom: 0.12 }, minimumWidth: PRICE_AXIS_MIN_WIDTH_PX },
      timeScale: { borderColor: C.axis, timeVisible: true, secondsVisible: false, rightOffset: 6 },
    });
    chartRef.current = chart;

    const host = hostRef.current;
    const markGesture = () => {
      lastUserGestureAtRef.current = performance.now();
    };
    const markDrag = (e: PointerEvent) => {
      if (e.buttons !== 0) markGesture();
    };
    // Capture phase: the library handles these on its own canvas and
    // reports the resulting range change synchronously, BEFORE the event
    // would bubble up to the host -- a bubbling listener marks the gesture
    // too late (measured in a real browser: range change, then the
    // bubbled wheel event).
    host.addEventListener("wheel", markGesture, { passive: true, capture: true });
    host.addEventListener("pointerdown", markGesture, { capture: true });
    host.addEventListener("pointermove", markDrag, { capture: true });
    host.addEventListener("touchmove", markGesture, { passive: true, capture: true });

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
      // The last price label is LastPriceLabelPrimitive (shared contrast rule).
      lastValueVisible: false,
    });
    seriesRef.current = series;
    const lastPriceLabel = new LastPriceLabelPrimitive(() => {
      if (!lastAppliedOpenTimeRef.current) return null;
      const b = lastBarRef.current;
      return { price: b.close, color: b.close >= b.open ? C.up : C.down };
    });
    series.attachPrimitive(lastPriceLabel);

    // Restore wherever the trader last left the view (per instrument +
    // timeframe); only fall back to the default fit (and the explicit
    // thinner spacing fitContent's own wide auto-fit would otherwise
    // leave in place) if nothing was saved yet. Programmatic: must not
    // be mistaken for a user gesture by the save handler just below.
    const axisLabels = new SystemAxisLabelsPrimitive(
      () => (lastAppliedOpenTimeRef.current ? lastBarRef.current.close : null),
      "#3a4150",
    );
    series.attachPrimitive(axisLabels);
    systemAxisLabelsRef.current = axisLabels;
    const layersPrimitive = new SetupLayersPrimitive();
    series.attachPrimitive(layersPrimitive);
    layersPrimitive.setElements(layersRef.current);
    layersPrimitiveRef.current = layersPrimitive;
    // Dev only (stripped from production builds): lets a browser check find
    // where each ARGUS element was drawn, to click it.
    if (import.meta.env.DEV) {
      const w = window as unknown as { __argusSetupLayers?: SetupLayersPrimitive; __argusChart?: { chart: IChartApi; times: () => number[] } };
      w.__argusSetupLayers = layersPrimitive;
      w.__argusChart = { chart, times: () => dataTimesRef.current };
    }

    chart.subscribeCrosshairMove((param) => {
      const d = param.seriesData.get(series) as CandlestickData<Time> | WhitespaceData<Time> | undefined;
      const hovered = d && "open" in d ? legendByTimeRef.current.get(d.time as number) : undefined;
      // A gap's whitespace point has no prices, and leaving the chart
      // clears the crosshair: both show the live bar again.
      if (hovered) {
        hoveredTimeRef.current = d!.time as number;
        setLegend({ bar: hovered, hovering: true });
      } else {
        hoveredTimeRef.current = null;
        setLegend({ bar: liveLegendRef.current, hovering: false });
      }
    });
    chart.timeScale().subscribeVisibleLogicalRangeChange((range) => {
      setToolbarTick((t) => t + 1);
      // Persist only a range change the trader actually made -- never
      // one caused by this component's own setData/restore/default-fit
      // calls (see withProgrammaticRangeChange's own doc comment).
      const byUser = performance.now() - lastUserGestureAtRef.current < USER_GESTURE_WINDOW_MS;
      if (range && byUser && !isProgrammaticRangeChangeRef.current) {
        const view = toSavedView(range, dataTimesRef.current, chart.timeScale().options().barSpacing);
        if (view) saveChartView(chartViewScopeRef.current, view);
      }
      noteRange(range?.from);
      if (range && shouldLoadOlder(range.from, historyEdgeRef.current)) void loadOlderRef.current?.();
    });

    const controller = new DrawingController(chart, series, hostRef.current, store, {
      onCommitted: () => setActiveTool("cross"),
      onContextMenu: (id, x, y) => setContextMenu({ id, x, y }),
      onSelectionChange: () => setToolbarTick((t) => t + 1),
    });
    controller.attach();
    controllerRef.current = controller;
    withProgrammaticRangeChange(() => applyCandleData());
    appliedSignatureRef.current = dataSignature;
    // Restore the saved view (per instrument + timeframe) if it still fits
    // the data, else the default -- after setData, since both are measured
    // against the data actually on the chart. No data yet: owe it.
    if (hasCandles) {
      withProgrammaticRangeChange(() => restoreOrDefaultView(chart, chartViewScopeRef.current));
    } else {
      pendingRestoreScopeRef.current = chartViewScopeRef.current;
    }
    setChartGeneration((g) => g + 1);

    return () => {
      host.removeEventListener("wheel", markGesture, { capture: true });
      host.removeEventListener("pointerdown", markGesture, { capture: true });
      host.removeEventListener("pointermove", markDrag, { capture: true });
      host.removeEventListener("touchmove", markGesture, { capture: true });
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
    const chart = chartRef.current;

    // A genuine instrument switch resets the view (fitContent, or the
    // new scope's own saved one): the old zoom/scroll was over a
    // different price range entirely and generally means nothing for
    // the new one. chartViewScopeRef must track the new scope too --
    // it was captured once at mount (see its own declaration, above),
    // which was correct while symbol/timeframe genuinely never changed
    // after that; now they can.
    const newScope = viewScope;
    const sameScope = chartViewScopeRef.current === newScope;
    if (chartViewScopeRef.current !== newScope) {
      chartViewScopeRef.current = newScope;
      // Owe this scope a restore-or-default -- not necessarily on THIS
      // render. See pendingRestoreScopeRef's own doc comment, above: a
      // slow switch can mean candles is still empty right here, below.
      pendingRestoreScopeRef.current = newScope;
      staleDataRef.current = appliedSignatureRef.current;
    }

    withProgrammaticRangeChange(() => applyCandleData(sameScope));
    flushPendingRange();

    if (!hasCandles) staleDataRef.current = null; // emptied: whatever comes next is new
    const dataIsNewScopes = staleDataRef.current === null || staleDataRef.current !== dataSignature;
    appliedSignatureRef.current = dataSignature;
    if (pendingRestoreScopeRef.current === newScope && hasCandles && dataIsNewScopes) {
      staleDataRef.current = null;
      pendingRestoreScopeRef.current = null;
      withProgrammaticRangeChange(() => restoreOrDefaultView(chart, newScope));
    }
    tryApplyFocus();
    // Still at the left edge after an older page landed (a fast scroll, or
    // a short page): read the next one, until the stored start or the cap.
    const r = chart.timeScale().getVisibleLogicalRange();
    noteRange(r?.from);
    if (sameScope && r && shouldLoadOlder(r.from, historyEdgeRef.current)) void loadOlderRef.current?.();
    // applyCandleData/symbol/timeframe/hasCandles are read fresh each
    // run via closure, same convention the create effect above already
    // uses (this project's own lint config does not enforce
    // react-hooks/exhaustive-deps); candles/poiZone/plan are the only
    // real triggers this effect needs. poiZone/plan are now optional
    // (no setup): the `?.` reads below produce undefined deps in that
    // case, still a valid, stable dependency value React can compare.
  }, [candles]);

  // ARGUS's setup layers: new elements (setups re-read, filters changed,
  // another setup selected, a new candle extending an active zone) are
  // handed to the one primitive; the price axis labels the selected
  // setup's zone and plan.
  useEffect(() => {
    const els = layers ?? [];
    layersPrimitiveRef.current?.setElements(els);
    const levels: { key: string; price: number; color: string }[] = [];
    for (const el of els) {
      if (!el.selected) continue;
      const g = el.geometry;
      if (el.kind === "zone" && g.type === "rect") {
        levels.push({ key: "ob-high", price: g.high, color: SYSTEM_COLOR }, { key: "ob-low", price: g.low, color: SYSTEM_COLOR });
      }
      if (g.type === "plan") {
        levels.push({ key: "entry", price: g.entry, color: SYSTEM_COLOR }, { key: "stop", price: g.stop, color: C.down });
        g.targets.forEach((t, i) => levels.push({ key: `target-${i}`, price: t.price, color: C.up }));
      }
    }
    systemAxisLabelsRef.current?.setLevels(levels);
  }, [layers, chartGeneration]);
  useEffect(() => layersPrimitiveRef.current?.setStoryLimit(storyStep), [storyStep, chartGeneration]);

  // Items 1 and 2: the legend stays inside the plot, and the price scale
  // keeps a top margin as tall as the legend, so no candle high sits under
  // it. Re-measured when the legend or the chart changes size.
  useEffect(() => {
    const chart = chartRef.current;
    const host = hostRef.current;
    const legend = legendRef.current;
    if (!chart || !host || !legend) return;
    const fit = () => {
      const axis = chart.priceScale("right").width();
      if (axis > 0) setAxisWidth((w) => (Math.abs(w - axis) > 0.5 ? axis : w));
      const paneH = host.clientHeight - chart.timeScale().height();
      if (paneH <= 0) return;
      const legendBottom = legend.offsetTop + legend.offsetHeight;
      const top = legendTopMargin(legendBottom, paneH);
      chart.priceScale("right").applyOptions({ scaleMargins: { top, bottom: DEFAULT_BOTTOM_MARGIN } });
      layersPrimitiveRef.current?.setReservedTop(legendBottom + 2);
    };
    fit();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(fit);
    ro.observe(legend);
    ro.observe(host);
    return () => ro.disconnect();
  }, [chartGeneration, toolbarTick]);

  // Item 4: frame the selected setup once per request -- never on a tick
  // or a refresh -- after the saved view has been restored (so the frame
  // is not overwritten by it), paging older history in first when the
  // evidence starts before the loaded candles.
  useEffect(() => {
    if (!focusRequest) return;
    pendingFocusRef.current = { token: ++focusTokenRef.current, historyAsked: false, oldestSec: null };
    tryApplyFocus();
  }, [focusRequest, chartGeneration]);
  useEffect(() => tryApplyFocus(), [layers, selectedSetupId]);

  function tryApplyFocus() {
    const p = pendingFocusRef.current;
    const chart = chartRef.current;
    const times = dataTimesRef.current;
    if (!p || !chart || selectedSetupId === undefined || times.length === 0) return;
    if (pendingRestoreScopeRef.current !== null) return; // the restore runs first
    const span = focusSpan(layersRef.current, selectedSetupId);
    if (!span) return;
    if (span.start < times[0] && loadHistoryBack) {
      if (!p.historyAsked) {
        p.historyAsked = true;
        const token = p.token;
        void loadHistoryBack(new Date((span.start - FOCUS_HISTORY_PAD_SEC) * 1000).toISOString()).then((res) => {
          const cur = pendingFocusRef.current;
          if (!cur || cur.token !== token) return;
          cur.oldestSec = res.oldest ? (unix(res.oldest) as number) : Number.NEGATIVE_INFINITY;
          tryApplyFocus();
        });
        return;
      }
      // Waiting for the paged-in history to reach the chart.
      if (p.oldestSec === null || times[0] > p.oldestSec) return;
    }
    const plan = focusRange(span, { times, intervalSec: INTERVAL_SECONDS[timeframe] ?? 60 }, chart.timeScale().width());
    if (!plan) return;
    pendingFocusRef.current = null;
    withProgrammaticRangeChange(() => chart.timeScale().setVisibleLogicalRange({ from: plan.from, to: plan.to }));
    onFocusNotice?.(focusNotice(plan, timeframe));
  }
  useEffect(() => layersPrimitiveRef.current?.setFocused(focusedElement), [focusedElement, chartGeneration]);

  // live-tick path (Part 2D): a candle/candle_closed message, applied
  // with series.update() -- its own path, never through the candles
  // prop/applyCandleData above (rule 3). Two guards before it is ever
  // allowed to touch the chart:
  //  1. isNewerCandleKey (rule 5): reject anything not strictly newer
  //     than the last tick actually applied, by (open_time, revision,
  //     seq) -- a duplicate or out-of-order delivery after a resubscribe
  //     must not re-apply or regress the bar on screen.
  //  2. decideApplyAction: same open_time replaces (price moving within
  //     the still-forming bar), a later open_time appends (the bar just
  //     closed and/or a new one started), an EARLIER open_time is
  //     dropped outright -- update() throws on an older time without
  //     historicalUpdate=true (lightweight-charts-update-contract.test.ts),
  //     and a live tick is never the right place to rewrite an older
  //     bar anyway (that is candle_corrected's own job, which never
  //     reaches `forming` at all -- see use-live-candles.ts's own doc
  //     comment).
  function applyLiveTick(forming: LiveTick) {
    const series = seriesRef.current;
    if (!series) return;

    const nextKey: CandleKey = {
      openTime: forming.candle.open_time,
      revision: forming.candle.revision,
      seq: forming.seq,
    };
    // seq restarts with every snapshot (LiveTick.epoch): a tick from a
    // new epoch is compared by open_time and revision only, never against
    // a seq from before the reconnect. decideApplyAction below still
    // drops an older bar.
    if (forming.epoch !== appliedEpochRef.current) {
      appliedEpochRef.current = forming.epoch;
      if (appliedLiveKeyRef.current) appliedLiveKeyRef.current = { ...appliedLiveKeyRef.current, seq: 0 };
    }
    if (!isNewerCandleKey(nextKey, appliedLiveKeyRef.current)) return;

    const action = decideApplyAction(forming.candle, lastAppliedOpenTimeRef.current);
    if (action === "drop-stale") return;

    const bar: OHLC = {
      open: formatMoney(forming.candle.open),
      high: formatMoney(forming.candle.high),
      low: formatMoney(forming.candle.low),
      close: formatMoney(forming.candle.close),
    };
    series.update({ time: unix(forming.candle.open_time), ...bar });
    if (action === "append") {
      // A new bar: it extends the logical index, so the drawings' time axis
      // must too (a drawing placed in the future stays at its time).
      const t = unix(forming.candle.open_time) as number;
      const times = dataTimesRef.current;
      if (times.length === 0 || t > times[times.length - 1]) {
        dataTimesRef.current = [...times, t];
        dataLengthRef.current = dataTimesRef.current.length;
        setSeriesTimeAxis(series, { times: dataTimesRef.current, intervalSec: INTERVAL_SECONDS[timeframe] ?? 60 });
      }
    }

    appliedLiveKeyRef.current = nextKey;
    lastAppliedOpenTimeRef.current = forming.candle.open_time;
    lastBarRef.current = bar;

    const text: LegendBar = {
      open: forming.candle.open,
      high: forming.candle.high,
      low: forming.candle.low,
      close: forming.candle.close,
    };
    const t = unix(forming.candle.open_time) as number;
    legendByTimeRef.current.set(t, text);
    liveLegendRef.current = text;
    setLiveBar(text);
    // Hovering the live bar itself keeps it moving too.
    setLegend((l) => (!l.hovering || hoveredTimeRef.current === t ? { bar: text, hovering: l.hovering } : l));
  }
  useEffect(() => {
    if (forming) applyLiveTick(forming);
  }, [forming]);

  // A click on an ARGUS element (not on a user drawing, not while a
  // drawing is selected): on another setup it selects that setup; on the
  // selected setup it opens the element's popover and highlights its row in
  // "Why this setup". A click elsewhere closes the popover. Hovering brings
  // a setup up, and names an element whose label had no room (tooltip).
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const at = (e: MouseEvent) => {
      const r = host.getBoundingClientRect();
      return { x: e.clientX - r.left, y: e.clientY - r.top, r };
    };
    const onClick = (e: MouseEvent) => {
      const { x, y, r } = at(e);
      const id = store.selectedId ? null : (layersPrimitiveRef.current?.hitElement(x, y) ?? null);
      if (!id) {
        setElementPopover(null);
        return;
      }
      const setupId = Number(id.split(":")[0]);
      if (selectedSetupId !== undefined && setupId !== selectedSetupId && onOpenSetup) {
        setElementPopover(null);
        onOpenSetup(setupId);
        return;
      }
      setElementPopover({ id, x: Math.max(0, Math.min(x, r.width - 330)), y: Math.min(y + 12, Math.max(0, r.height - 320)) });
      onFocusElement?.(id);
    };
    let frame = 0;
    let last: MouseEvent | null = null;
    const onMove = (e: MouseEvent) => {
      last = e;
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        const prim = layersPrimitiveRef.current;
        if (!prim || !last) return;
        const { x, y } = at(last);
        const id = prim.hitElement(x, y);
        prim.setHoveredSetup(id ? Number(id.split(":")[0]) : null);
        const hidden = id ? prim.hiddenLabels.find((h) => h.elementId === id) : undefined;
        host.title = id ? `${hidden?.text ?? prim.elementLabel(id)} (ARGUS · click for details)` : "";
      });
    };
    const onLeave = () => layersPrimitiveRef.current?.setHoveredSetup(null);
    host.addEventListener("click", onClick);
    host.addEventListener("mousemove", onMove);
    host.addEventListener("mouseleave", onLeave);
    return () => {
      host.removeEventListener("click", onClick);
      host.removeEventListener("mousemove", onMove);
      host.removeEventListener("mouseleave", onLeave);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [store, onFocusElement, onOpenSetup, selectedSetupId]);
  // A popover whose element is no longer drawn closes.
  const popoverElement = elementPopover ? (layers ?? []).find((l) => l.id === elementPopover.id) : undefined;

  // Range buttons (Phase 3): set the visible range to the last 1 day, 5
  // days, ... ending at the newest bar. When the held history is shorter,
  // older candles are paged in first (useLiveCandles.loadHistoryBack); if
  // the store runs out (or the chart's bar cap is reached) the range shows
  // everything held and says so, never doing nothing silently.
  const workspace = useAnalysisWorkspace();
  const loadHistoryBack = workspace.loadHistoryBack;
  loadOlderRef.current = workspace.loadOlder;
  historyEdgeRef.current = workspace.historyEdge;
  // noteRange tracks whether the oldest held bar is in view (logical index
  // 0), changing state only when that flips.
  function noteRange(from: number | undefined) {
    const next = from !== undefined && from < 1;
    if (next !== atStartRef.current) {
      atStartRef.current = next;
      setAtStart(next);
    }
  }
  const edge = workspace.historyEdge;
  const startNote =
    atStart && hasCandles && edge ? (edge.exhausted ? historyStartNote(candles[0].openTime) : edge.capped ? historyCapNote() : null) : null;
  function applyRange(key: RangeKey, outcome: HistoryBackOutcome) {
    const chart = chartRef.current;
    const times = dataTimesRef.current;
    if (!chart || times.length === 0) return;
    const last = times[times.length - 1];
    const start = rangeStartSec(key, last, times[0]);
    const from = Math.max(start, times[0]);
    withProgrammaticRangeChange(() => {
      chart.timeScale().setVisibleRange({ from: from as Time, to: last as Time });
    });
    setRangeNotice(describeShown(key, outcome, from, start, last, timeframe));
  }
  // flushPendingRange applies a waiting range once the chart holds the
  // history it waited for (called from the data path after setData).
  function flushPendingRange() {
    const p = pendingRangeRef.current;
    const times = dataTimesRef.current;
    if (!p || p.token !== rangeRequestRef.current || times.length === 0) return;
    if (times[0] > p.oldestSec) return; // not there yet
    pendingRangeRef.current = null;
    applyRange(p.key, p.outcome);
  }
  async function selectRange(key: RangeKey) {
    const token = ++rangeRequestRef.current;
    pendingRangeRef.current = null;
    setActiveRange(key);
    setRangeNotice(null);
    const times = dataTimesRef.current;
    if (times.length === 0) {
      setRangeNotice("No candles loaded yet");
      return;
    }
    const last = times[times.length - 1];
    const start = rangeStartSec(key, last, times[0]);
    if (start >= times[0] || !loadHistoryBack) {
      applyRange(key, "covered");
      return;
    }
    setRangeNotice("Loading older candles…");
    const fromISO = Number.isFinite(start) ? new Date(start * 1000).toISOString() : "1970-01-01T00:00:00.000Z";
    const res = await loadHistoryBack(fromISO);
    if (token !== rangeRequestRef.current) return; // a newer click won
    const oldestSec = res.oldest ? (unix(res.oldest) as number) : Number.POSITIVE_INFINITY;
    pendingRangeRef.current = { key, outcome: res.outcome, oldestSec, token };
    flushPendingRange();
  }

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

  // The dot next to the symbol: the live bar's own colour (close vs open,
  // an exact string comparison). The 24h change next to it is
  // LegendChange (the page's one stats source).
  const liveCmp = liveBar.close !== null && liveBar.open !== null ? compareDecimal(liveBar.close, liveBar.open) : null;
  const up = liveCmp === null || liveCmp >= 0;
  // While hovering a past bar, its values are shown plainly (its close
  // coloured against its own open); otherwise each value is the shared
  // <LivePrice> (direction colour, then only the changed digits).
  const hoverCloseCmp =
    legend.hovering && legend.bar.close !== null && legend.bar.open !== null ? compareDecimal(legend.bar.close, legend.bar.open) : null;
  const legendReason = loading ? "Loading candles" : loadError ? `Could not load candles: ${loadError}` : "No candle loaded for this instrument";

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

        {elementPopover && popoverElement && (
          <SetupElementPopover
            element={popoverElement}
            x={elementPopover.x}
            y={elementPopover.y}
            onClose={() => setElementPopover(null)}
            onCopy={() => {
              const now = Date.now();
              add({ ...popoverElement.copy, id: nextId(), createdAt: now, updatedAt: now } as Drawing);
              setElementPopover(null);
            }}
            onOpenSetup={
              onOpenSetup && popoverElement.setupId !== selectedSetupId
                ? () => {
                    onOpenSetup(popoverElement.setupId);
                    setElementPopover(null);
                  }
                : undefined
            }
          />
        )}

        <div
          ref={legendRef}
          data-testid="chart-legend"
          className="pointer-events-none absolute left-3 top-2.5 z-10 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[12px]"
          style={{ right: axisWidth + 8 }}
        >
          <div className="flex shrink-0 items-center gap-1.5 whitespace-nowrap">
            <CoinIcon symbol={symbol} size={18} />
            <span className="font-semibold text-fg">{symbol}</span>
            <span className="text-fg-subtle">· {venue}</span>
            <span className={cn("ml-1 size-1.5 rounded-full", up ? "bg-up" : "bg-down")} />
          </div>
          <LegendChange />
          <LiveStatusBadge connectionState={connectionState} status={status} fatalReason={fatalReason} />
          {analysisLayer && <AnalysisLayerChip layer={analysisLayer} />}
          {formationOutsideWindow && (
            <span className="rounded-md border border-line bg-white/5 px-1.5 py-0.5 text-[11px] text-fg-subtle">
              Setup formed before this window
            </span>
          )}
          <div className="tnum flex shrink-0 items-center gap-2.5 whitespace-nowrap text-fg-subtle" data-testid="legend-ohlc" data-hovering={legend.hovering ? "true" : "false"}>
            {LEGEND_FIELDS.map((f) => {
              const label = f === "open" ? "O" : f === "high" ? "H" : f === "low" ? "L" : "C";
              const text = legend.bar[f] !== null ? formatDecimal(legend.bar[f]) : null;
              return (
                <span key={f}>
                  {label}{" "}
                  {legend.hovering ? (
                    <LiveFigure
                      testId={`legend-${f}`}
                      tone="sign"
                      signTone={
                        f === "close" && hoverCloseCmp !== null ? (hoverCloseCmp >= 0 ? "text-up" : "text-down") : "text-fg-muted"
                      }
                      text={text}
                      reason={legendReason}
                    />
                  ) : (
                    <LivePrice
                      testId={`legend-${f}`}
                      className="text-fg-muted"
                      value={legend.bar[f]}
                      text={text}
                      reason={legendReason}
                      resetKey={`${symbol}:${timeframe}`}
                    />
                  )}
                </span>
              );
            })}
          </div>
        </div>
        {startNote && (
          <div className="pointer-events-none absolute bottom-9 left-2 z-10 text-[11px] text-fg-subtle" data-testid="history-start">
            {startNote}
          </div>
        )}
        {rangeNotice && (
          <div className="pointer-events-none absolute inset-x-0 bottom-2 z-10 flex justify-center">
            <div className="rounded-md border border-line bg-surface px-2 py-1 text-[11px] text-fg-subtle shadow-sm" data-testid="range-notice">
              {rangeNotice}
            </div>
          </div>
        )}
      </div>

      <div className="flex h-8 shrink-0 items-center justify-between border-t border-line px-2">
        <div className="flex items-center gap-0.5" role="group" aria-label="Visible range">
          {RANGES.map((r) => (
            <button
              key={r}
              type="button"
              data-testid={`range-${r}`}
              aria-pressed={activeRange === r}
              onClick={() => void selectRange(r)}
              className={cn(
                "focus-ring rounded px-1.5 py-0.5 text-[11px] font-medium transition-colors hover:bg-white/5 hover:text-fg",
                activeRange === r ? "bg-white/10 text-fg" : "text-fg-subtle",
              )}
            >
              {r}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-1 text-fg-subtle">
          <span className="mr-1 flex items-center text-[11px]" data-testid="last-price-label">
            <LivePrice
              value={liveBar.close}
              text={liveBar.close !== null ? formatDecimal(liveBar.close) : null}
              reason={legendReason}
              resetKey={`${symbol}:${timeframe}`}
            />
            <span className="ml-1">· UTC</span>
          </span>
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
