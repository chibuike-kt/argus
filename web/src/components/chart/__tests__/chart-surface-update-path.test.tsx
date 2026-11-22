// @vitest-environment jsdom
//
// Component test for Part 2A: switching instrument or interval (a new
// `candles` prop) must not rebuild the chart -- same chart instance,
// drawings kept. chart-surface.tsx's own doc comment on ChartSurface
// explains the bug this guards: before this phase, a candles change
// forced React to unmount and remount ChartSurfaceInner (tearing down
// the chart and every attached primitive) because the loading/error/
// chart states were three different element trees.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { ChartSurface, ChartSurfaceInner, type ChartSurfaceInnerProps } from "../chart-surface";
import { SetupElementPopover } from "../setup-popover";
import { buildLayers, DEFAULT_FILTERS } from "../setup-layers";
import { loadChartView, saveChartView, type SavedChartView } from "../chart-view-persistence";
import { preferences } from "@/lib/preferences";
import { DrawingProvider, useDrawing } from "../drawing-context";
import type { DrawingsStore } from "../drawings/store";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { ApiInstrument } from "@/api/types";
import type { LiveTick } from "@/lib/use-live-candles";

// legendState.stats is what the mocked workspace reports as the selected
// instrument's GET /instruments/stats row (the legend's 24h change).
const legendState = vi.hoisted(() => ({
  stats: undefined as import("@/api/types").ApiInstrumentStats | undefined,
  // For tests that render the ChartSurface wrapper itself.
  apiCandles: [] as import("@/api/types").ApiCandle[],
  setup: undefined as import("@/api/types").ApiSetup | undefined,
  events: [] as import("@/api/types").ApiSetupEvent[],
  loadHistoryBack: undefined as
    | ((fromISO: string) => Promise<import("@/lib/use-live-candles").HistoryBackResult>)
    | undefined,
}));

vi.mock("@/components/workspace/analysis-workspace", () => ({
  useAnalysisWorkspace: () => ({
    stats: legendState.stats,
    // The page's one live stats source, as the workspace exposes it: the
    // entry the legend shares with the panel and the watchlist.
    // Before the figures arrive the entry exists with nothing in it, as
    // live-stats.ts creates it on retain.
    statsEntry: {
          figures: legendState.stats ?? null,
          closed: legendState.stats ?? null,
          rejected: false,
          motion: {
            last_price: { dir: null, seq: 0 },
            change_24h: { dir: null, seq: 0 },
            change_24h_pct: { dir: null, seq: 0 },
            volume_24h: { dir: null, seq: 0 },
          },
        },
    statsSnapshot: {
      entries: new Map(),
      loading: legendState.stats === undefined,
      error: undefined,
      connection: "open",
      everOpen: true,
      fatal: null,
    },
    loadHistoryBack: legendState.loadHistoryBack,
    candles: legendState.apiCandles,
    candlesLoading: false,
    candlesError: undefined,
    instrumentsLoading: false,
    instrumentNotFound: false,
    setup: legendState.setup,
    events: legendState.events,
    forming: null,
    connectionState: "open",
    status: null,
    fatalReason: null,
    instrument: { id: 1, native_symbol: "BTC-PERP", provider: "test" } as ApiInstrument,
    // DrawingProvider now reads timeframe/setTimeframe from this hook
    // (Part 4, item 1) instead of owning its own state -- this test
    // exercises ChartSurfaceInner directly, not AnalysisWorkspaceProvider,
    // so the mock has to stand in for both.
    timeframe: "1m",
    setTimeframe: () => {},
  }),
}));

// ES module named exports are non-configurable live bindings --
// vi.spyOn cannot redefine lightweight-charts' own `createChart` export
// directly (vitest's own error points at this exact mocking guide).
// vi.mock replaces the whole module instead, keeping every real export
// except createChart, which becomes a vi.fn wrapping the real
// implementation -- still a real chart, just with a call count this
// test can read.
//
// The chart-view-persistence tests below need two things jsdom's own
// zero-layout canvas stub cannot provide for real (confirmed by
// instrumenting a throwaway probe: across a full mount + switch + empty
// + switch-back sequence, the real library's own
// subscribeVisibleLogicalRangeChange handler fired zero times -- with
// no real pixel width, the library's own old-range/new-range comparison
// never sees a change worth firing):
//  - capturedRangeHandler: the real handler chart-surface.tsx registers
//    with subscribeVisibleLogicalRangeChange, captured so a test can
//    invoke it directly with a synthetic range -- simulating what a
//    genuine user gesture (wheel/drag/scroll) would make the library
//    fire, without needing real layout.
//  - series.setData wrapped to ALSO invoke that same captured handler
//    synchronously, exactly as a real browser's layout engine would
//    when setData changes the visible range -- reproducing, for real,
//    the one call withProgrammaticRangeChange exists to bracket
//    (confirmed from the library's own source: _setVisibleRange fires
//    this event synchronously, inline, from within setData/
//    setVisibleLogicalRange, never deferred -- see chart-surface.tsx's
//    own doc comment on withProgrammaticRangeChange for the citation).
const createChartSpy = vi.fn();
let lastCreatedChart: ReturnType<typeof import("lightweight-charts")["createChart"]> | null = null;
let capturedRangeHandler: ((range: { from: number; to: number } | null) => void) | null = null;
// Mirrors the bug report's own corrupted value (from:-70, to:3) --
// what the library picks when setData leaves little or no data to show.
const SYNTHETIC_AUTO_RANGE = { from: -70, to: 3 };
let lastVisibleRange: { from: number; to: number } | null = null;
let setDataCount = 0;
// The crosshair handler chart-surface.tsx registers, so a test can hover.
let capturedCrosshair: ((param: { seriesData: Map<unknown, unknown> }) => void) | null = null;
let lastSeries: unknown = null;

vi.mock("lightweight-charts", async (importOriginal) => {
  const actual = await importOriginal<typeof import("lightweight-charts")>();
  return {
    ...actual,
    createChart: (...args: Parameters<typeof actual.createChart>) => {
      createChartSpy(...args);
      const chart = actual.createChart(...args);
      lastCreatedChart = chart;

      const realCrosshair = chart.subscribeCrosshairMove.bind(chart);
      chart.subscribeCrosshairMove = ((h: (param: { seriesData: Map<unknown, unknown> }) => void) => {
        capturedCrosshair = h;
        realCrosshair(h as never);
      }) as typeof chart.subscribeCrosshairMove;
      const realAdd = chart.addSeries.bind(chart);
      chart.addSeries = ((...a: Parameters<typeof chart.addSeries>) => {
        const sr = realAdd(...a);
        lastSeries = sr;
        return sr;
      }) as typeof chart.addSeries;
      const ts = chart.timeScale();
      // jsdom has no layout, so the library cannot apply a time range
      // (its deferred resize throws): record the call instead, which is
      // also what the range-button tests assert.
      ts.setVisibleRange = ((r: { from: number; to: number }) => {
        lastVisibleRange = r;
      }) as unknown as typeof ts.setVisibleRange;
      const realSubscribe = ts.subscribeVisibleLogicalRangeChange.bind(ts);
      ts.subscribeVisibleLogicalRangeChange = ((handler: (range: { from: number; to: number } | null) => void) => {
        capturedRangeHandler = handler;
        return realSubscribe(handler);
      }) as typeof ts.subscribeVisibleLogicalRangeChange;

      const realAddSeries = chart.addSeries.bind(chart);
      chart.addSeries = ((...seriesArgs: Parameters<typeof chart.addSeries>) => {
        const series = realAddSeries(...seriesArgs);
        const realSetData = series.setData.bind(series);
        series.setData = ((data: Parameters<typeof series.setData>[0]) => {
          setDataCount++;
          realSetData(data);
          capturedRangeHandler?.(SYNTHETIC_AUTO_RANGE);
        }) as typeof series.setData;
        return series;
      }) as typeof chart.addSeries;

      return chart;
    },
  };
});

if (typeof window.matchMedia !== "function") {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
}

function candleSet(n: number, basePrice: number) {
  const out = [];
  for (let i = 0; i < n; i++) {
    const p = basePrice + i;
    out.push({
      openTime: new Date(Date.UTC(2026, 0, 1, 0, i)).toISOString(),
      open: p,
      high: p + 1,
      low: p - 1,
      close: p + 0.5,
    });
  }
  return out;
}


// A tiny host that adds one drawing to the real store on mount, so the
// test can prove it survives a later props change -- DrawingProvider's
// own store is the real useDrawingsStore, not a mock, since the whole
// point is to exercise the real reconciliation effect in chart-surface.tsx.
function AddOneDrawing({ onReady }: { onReady: (store: DrawingsStore) => void }) {
  const { drawings } = useDrawing();
  if (drawings.drawings.length === 0) {
    drawings.add({
      id: "d1",
      kind: "hline",
      price: 103,
      style: { color: "#fff", lineWidth: 1, lineStyle: "solid" },
      createdAt: 0,
      updatedAt: 0,
    });
  }
  onReady(drawings.store);
  return null;
}

function Providers({ children }: { children: React.ReactNode }) {
  return (
    <TooltipProvider>
      <DrawingProvider>{children}</DrawingProvider>
    </TooltipProvider>
  );
}

type HarnessProps = Omit<
  ChartSurfaceInnerProps,
  "loading" | "loadError" | "forming" | "connectionState" | "status" | "fatalReason"
> & {
  forming?: ChartSurfaceInnerProps["forming"];
  connectionState?: ChartSurfaceInnerProps["connectionState"];
  status?: ChartSurfaceInnerProps["status"];
  fatalReason?: ChartSurfaceInnerProps["fatalReason"];
};

const HARNESS_IDS: Record<string, number> = { "BTC-PERP": 1, "ETH-PERP": 2, BTC: 1 };

// The open time (unix seconds) of candleSet's i-th candle.
const tSec = (i: number) => Date.UTC(2026, 0, 1, 0, i) / 1000;

// A saved (time-based) view of candleSet bars [from, to], not pinned.
function viewOver(from: number, to: number): SavedChartView {
  return { from_time: tSec(from), to_time: tSec(to), bars_visible: to - from, bar_spacing: 6, right_offset: 0, pinned_to_latest: false };
}

// Saved views live in the preference store (a module singleton): start
// every test from an empty one.
beforeEach(() => {
  preferences.detach();
});

function Harness({
  forming = null,
  connectionState = "open",
  status = null,
  fatalReason = null,
  ...props
}: HarnessProps) {
  const instrumentId = props.instrumentId ?? HARNESS_IDS[props.symbol];
  return (
    <Providers>
      <AddOneDrawing onReady={() => {}} />
      <ChartSurfaceInner
        {...props}
        instrumentId={instrumentId}
        forming={forming}
        connectionState={connectionState}
        status={status}
        fatalReason={fatalReason}
        loading={false}
        loadError={undefined}
      />
    </Providers>
  );
}

describe("switching instrument or interval does not rebuild the chart", () => {
  beforeEach(() => {
    createChartSpy.mockClear();
  });

  afterEach(() => {
    cleanup();
  });

  it("createChart is called exactly once across a candles prop change (same chart instance)", () => {
    const { rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        formationOutsideWindow={false}
      />,
    );
    expect(createChartSpy).toHaveBeenCalledTimes(1);

    // A different candle set, as a setup/interval switch would produce
    // -- same symbol, new data.
    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(8, 200)}
        formationOutsideWindow={false}
      />,
    );

    expect(createChartSpy).toHaveBeenCalledTimes(1);
  });

  it("the same <canvas> element persists across the candles change (lightweight-charts never tore down and recreated its own DOM)", () => {
    const { container, rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        formationOutsideWindow={false}
      />,
    );
    const canvasBefore = container.querySelector("canvas");
    expect(canvasBefore).not.toBeNull();

    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(8, 200)}
        formationOutsideWindow={false}
      />,
    );
    const canvasAfter = container.querySelector("canvas");
    expect(canvasAfter).toBe(canvasBefore);
  });

  // Real bug report: switching to an instrument with no setups used to
  // mean the chart (ChartSurface, one level up) never even mounted
  // ChartSurfaceInner at all -- plan/poiZone being undefined is what
  // that now looks like at THIS level (ChartSurface passes undefined
  // for both rather than refusing to render). This proves
  // ChartSurfaceInner itself tolerates that cleanly: same chart
  // instance, same canvas, candles still render, no crash -- and that
  // switching BACK to an instrument with a setup re-attaches the
  // system primitives rather than leaving them permanently gone.
  it("switching to an instrument with no setup (undefined plan/poiZone) and back does not rebuild the chart", () => {
    const candlesA = candleSet(5, 100);
    const candlesB = candleSet(6, 300);
    const { container, rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candlesA}
        formationOutsideWindow={false}
      />,
    );
    expect(createChartSpy).toHaveBeenCalledTimes(1);
    const canvasBefore = container.querySelector("canvas");
    expect(canvasBefore).not.toBeNull();

    // Switched to an instrument with no setup: ChartSurface passes
    // candles alone, poiZone/plan undefined.
    rerender(
      <Harness
        className=""
        symbol="ETH-PERP"
        venue="test"
        candles={candlesB}
        formationOutsideWindow={false}
      />,
    );
    expect(createChartSpy).toHaveBeenCalledTimes(1); // still the same chart
    expect(container.querySelector("canvas")).toBe(canvasBefore); // still the same canvas
    expect(container.textContent).toContain("ETH-PERP"); // candles/symbol still rendered

    // Switched back to an instrument with a setup.
    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candlesA}
        formationOutsideWindow={false}
      />,
    );
    expect(createChartSpy).toHaveBeenCalledTimes(1); // still never rebuilt
    expect(container.querySelector("canvas")).toBe(canvasBefore);
  });

  it("a drawing added before the switch is still in the store after it (the reconciliation effect never clears it on a mere data update)", () => {
    const captured: { store: DrawingsStore | null } = { store: null };
    const { rerender } = render(
      <Providers>
        <AddOneDrawing onReady={(s) => { captured.store = s; }} />
        <ChartSurfaceInner
          className=""
          symbol="BTC-PERP"
          venue="test"
          candles={candleSet(5, 100)}
          loading={false}
          loadError={undefined}
          formationOutsideWindow={false}
          forming={null}
          connectionState="open"
          status={null}
          fatalReason={null}
        />
      </Providers>,
    );
    expect(captured.store?.get("d1")).toBeDefined();

    rerender(
      <Providers>
        <AddOneDrawing onReady={(s) => { captured.store = s; }} />
        <ChartSurfaceInner
          className=""
          symbol="BTC-PERP"
          venue="test"
          candles={candleSet(8, 200)}
          loading={false}
          loadError={undefined}
          formationOutsideWindow={false}
          forming={null}
          connectionState="open"
          status={null}
          fatalReason={null}
        />
      </Providers>,
    );

    expect(captured.store?.get("d1")).toBeDefined();
  });

  it("createChart is still called exactly once even when the symbol itself changes (an instrument switch, not just new data for the same one)", () => {
    const { rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        formationOutsideWindow={false}
      />,
    );
    rerender(
      <Harness
        className=""
        symbol="ETH-PERP"
        venue="test"
        candles={candleSet(5, 300)}
        formationOutsideWindow={false}
      />,
    );
    expect(createChartSpy).toHaveBeenCalledTimes(1);
  });

  it("the NEW candles are actually shown after the switch, not just left over from the first render -- the data path itself, not only that createChart wasn't called again", () => {
    // Before Part 2A, ChartSurfaceInner had no update effect at all: a
    // candles prop change was silently ignored (the create effect's
    // own deps: [] already meant createChart was never called twice,
    // so that assertion alone -- the earlier tests in this file --
    // would have passed against the old, broken code too). This is the
    // test that would actually have failed: the legend's own "last"
    // price, which chart-surface.tsx renders straight from the last
    // candle in its own `candles` prop, must reflect the SECOND
    // candle set after the switch, not the first.
    const { container, rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        formationOutsideWindow={false}
      />,
    );
    expect(container.textContent).toMatch(/10[0-9]/); // candleSet(5, 100): closes ~100.5-104.5

    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(8, 200)}
        formationOutsideWindow={false}
      />,
    );
    expect(container.textContent).toMatch(/20[0-9]/); // candleSet(8, 200): closes ~200.5-207.5
    expect(container.textContent).not.toMatch(/10[0-4]\D/);
  });
});

// liveTick builds a WireCandlePayload-shaped forming tick -- decimal
// strings, like the real wire does (rule 2); formatMoney converts them
// at the chart edge, inside chart-surface.tsx's own live-tick effect.
function liveTick(openTime: string, close: number, opts: { revision?: number; seq: number; epoch?: number }): LiveTick {
  return {
    epoch: opts.epoch ?? 0,
    candle: {
      open_time: openTime,
      close_time: openTime,
      open: String(close - 1),
      high: String(close + 1),
      low: String(close - 2),
      close: String(close),
      volume: "1",
      revision: opts.revision ?? 1,
    },
    seq: opts.seq,
  };
}

describe("Part 2D: a live tick is applied through series.update(), never through the candles prop", () => {
  beforeEach(() => {
    createChartSpy.mockClear();
  });

  afterEach(() => {
    cleanup();
  });

  it("same open_time as the last candle: replaces that bar's own OHLC on screen, candles prop unchanged", () => {
    const candles = candleSet(5, 100);
    const lastOpenTime = candles[candles.length - 1].openTime;
    const { container, rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candles}
        formationOutsideWindow={false}
      />,
    );
    expect(container.textContent).not.toMatch(/999/);

    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candles}
        formationOutsideWindow={false}
        forming={liveTick(lastOpenTime, 999, { seq: 1 })}
      />,
    );
    // The legend's own C value now reads the live tick's close, even
    // though `candles` itself (and so createChart's own call count --
    // this update went through series.update(), not a rebuild) never
    // changed.
    expect(container.textContent).toMatch(/999/);
    expect(createChartSpy).toHaveBeenCalledTimes(1);
  });

  it("a newer open_time: appends a new bar via series.update(), rather than being dropped or throwing", () => {
    const candles = candleSet(5, 100);
    const lastOpenTime = candles[candles.length - 1].openTime;
    const nextOpenTime = new Date(new Date(lastOpenTime).getTime() + 60_000).toISOString();
    const { container, rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candles}
        formationOutsideWindow={false}
      />,
    );

    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candles}
        formationOutsideWindow={false}
        forming={liveTick(nextOpenTime, 888, { seq: 1 })}
      />,
    );
    expect(container.textContent).toMatch(/888/);
  });

  it("an out-of-order/duplicate tick (not newer by (open_time, revision, seq)) is rejected -- the on-screen bar does not regress", () => {
    const candles = candleSet(5, 100);
    const lastOpenTime = candles[candles.length - 1].openTime;
    const { container, rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candles}
        formationOutsideWindow={false}
        forming={liveTick(lastOpenTime, 999, { revision: 2, seq: 10 })}
      />,
    );
    expect(container.textContent).toMatch(/999/);

    // A lower (revision, seq) at the same open_time -- e.g. a buffered
    // duplicate delivered late after a resubscribe -- must not replace
    // what a strictly newer tick already applied.
    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candles}
        formationOutsideWindow={false}
        forming={liveTick(lastOpenTime, 111, { revision: 1, seq: 5 })}
      />,
    );
    expect(container.textContent).toMatch(/999/);
    expect(container.textContent).not.toMatch(/111/);
  });
});

describe("Part 2F: the status badge never shows the word \"Live\" (no decided freshness threshold), shows an age instead", () => {
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("a wire state of \"live\" renders as \"Connected\" with the last candle's own age, never the word \"Live\"", () => {
    vi.useFakeTimers();
    const now = new Date("2026-11-13T00:00:12Z");
    vi.setSystemTime(now);
    const lastEventAt = new Date("2026-11-13T00:00:00Z").toISOString(); // exactly 12s before "now"

    const { container } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        formationOutsideWindow={false}
        connectionState="open"
        status={{ state: "live", provider: "fixture", last_event_at: lastEventAt }}
      />,
    );

    expect(container.textContent).toMatch(/Connected/);
    expect(container.textContent).not.toMatch(/\bLive\b/);
    expect(container.textContent).toMatch(/last candle 12s ago/);
  });

  it("\"degraded\" is unchanged: no age shown, label stays \"Degraded\"", () => {
    const { container } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        formationOutsideWindow={false}
        connectionState="open"
        status={{ state: "degraded" }}
      />,
    );
    expect(container.textContent).toMatch(/Degraded/);
    expect(container.textContent).not.toMatch(/ago/);
    expect(container.textContent).not.toMatch(/\bLive\b/);
  });
});

// Bug repro: an independent run of chart-survives-instrument-switch.spec.ts
// ("switching instrument and back keeps the saved chart view") passed only
// 1 run in 3. Root cause, confirmed by reading chart-surface.tsx: every
// visible-range change was saved, including the ones caused by this
// component's own setData (applyCandleData) and setVisibleLogicalRange
// (the restore/default-fit calls) -- not just the ones a trader actually
// makes. During a slow instrument switch, candles clears to [] before the
// new instrument's own history has loaded; applyCandleData's own
// setData([]) makes the library pick a near-empty logical range for
// that moment, and the handler saved THAT, overwriting whatever the
// trader had actually zoomed to. A second, compounding issue: the old
// restore logic gated itself on a one-shot "symbolChanged" boolean,
// consumed on whichever render first saw the new scope -- if that
// render is the empty one (the common case under a slow fetch), the
// restore never gets a second chance once real data later arrives on a
// subsequent render. Both are fixed below: only a range change made
// outside this component's own guarded calls is ever saved
// (isProgrammaticRangeChangeRef/withProgrammaticRangeChange), and the
// scope's own restore is now owed across renders
// (pendingRestoreScopeRef) until one with real data actually runs it.
describe("chart view persistence: only a user-made range change is saved", () => {
  beforeEach(() => {
    createChartSpy.mockClear();
    lastCreatedChart = null;
    capturedRangeHandler = null;
    localStorage.clear();
  });
  afterEach(() => {
    cleanup();
    localStorage.clear();
  });

  it("a programmatic setData during a slow instrument switch (candles briefly empty) does not overwrite the previously saved view", () => {
    const goodView = viewOver(1, 4);
    saveChartView("1:1m", goodView);

    const { rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        formationOutsideWindow={false}
      />,
    );
    // The create effect's own restore read and re-applied goodView --
    // confirm the fix did not also stop that from happening.
    expect(loadChartView("1:1m")).toEqual(goodView);

    // Switch away, with real data (an ordinary, clean switch).
    rerender(
      <Harness
        className=""
        symbol="ETH-PERP"
        venue="test"
        candles={candleSet(5, 300)}
        formationOutsideWindow={false}
      />,
    );

    // Switch back to BTC-PERP, but candles is still empty -- exactly
    // the slow-fetch intermediate render the bug report describes.
    // applyCandleData's own setData([]) runs under the scope ref
    // already pointed at "1:1m" (it updates eagerly, before data
    // arrives) -- this is the moment that used to corrupt the saved
    // view.
    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={[]}
        formationOutsideWindow={false}
      />,
    );

    expect(loadChartView("1:1m")).toEqual(goodView);
  });

  it("the saved view is still restored once real data arrives, even though the scope flipped earlier on an empty render", () => {
    const goodView = viewOver(1, 4);
    saveChartView("1:1m", goodView);

    const { rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        formationOutsideWindow={false}
      />,
    );
    rerender(
      <Harness
        className=""
        symbol="ETH-PERP"
        venue="test"
        candles={candleSet(5, 300)}
        formationOutsideWindow={false}
      />,
    );
    // The empty intermediate render -- this is the one that, under the
    // old one-shot "symbolChanged" check, permanently consumed this
    // switch's only chance to restore.
    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={[]}
        formationOutsideWindow={false}
      />,
    );
    // jsdom's own zero-layout chart does not actually move when told to
    // (see the mock's own doc comment) -- getVisibleLogicalRange() is
    // not a reliable way to observe the restore here. Spying on the
    // real setVisibleLogicalRange call itself is: it proves the restore
    // code path actually ran, with the right value, independent of
    // whether the library's own internal state visibly reflects it in
    // this environment.
    const setRangeSpy = vi.spyOn(lastCreatedChart!.timeScale(), "setVisibleLogicalRange");

    // Real data for BTC-PERP finally arrives, on a LATER render.
    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        formationOutsideWindow={false}
      />,
    );

    // The saved times map back onto the same bars: indexes 1..4.
    expect(setRangeSpy).toHaveBeenCalledWith({ from: 1, to: 4 });
  });

  it("a range change made outside this component's own calls (a real user gesture, at the library level) is saved", () => {
    render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        formationOutsideWindow={false}
      />,
    );
    expect(loadChartView("1:1m")).toBeNull(); // nothing saved yet

    // The real subscribeVisibleLogicalRangeChange handler chart-
    // surface.tsx registered, invoked directly with a synthetic range
    // -- exactly what the library itself would pass it on a genuine
    // wheel/drag/scroll gesture (jsdom's own zero-layout chart cannot
    // produce that for real; see the mock's own doc comment). Called
    // from here, completely outside any of this component's own
    // withProgrammaticRangeChange-wrapped calls. A real gesture also
    // fires a DOM event on the chart host first (a wheel here) -- the save
    // is gated on that, so the library's own re-fits are never saved.
    document.querySelector("canvas")!.dispatchEvent(new WheelEvent("wheel", { bubbles: true, deltaY: -100 }));
    capturedRangeHandler!({ from: 0, to: 3 });

    // Saved as times plus width, not raw indexes.
    expect(loadChartView("1:1m")).toMatchObject({ from_time: tSec(0), to_time: tSec(3), bars_visible: 3, pinned_to_latest: false });
  });
});

// Rule 9: the legend's change is the server's 24h change for the
// selected instrument, an em dash when absent, and never a sample badge.
describe("chart legend 24h change", () => {
  afterEach(() => {
    cleanup();
    legendState.stats = undefined;
  });

  const legendProps = {
    className: "",
    symbol: "BTC",
    venue: "hyperliquid",
    candles: candleSet(5, 100),
    systemZone: undefined,
    plan: undefined,
    formationOutsideWindow: false,
  } as unknown as HarnessProps;

  it("shows the server's change and percent, with no Sample marker", () => {
    legendState.stats = {
      instrument_id: 1,
      last_close: "95123.4",
      last_closed_open_time: "2026-11-16T18:16:00Z",
      last_price: "95124.0",
      last_price_closed: false,
      as_of: "2026-11-16T18:17:00Z",
      last_tick_at: "2026-11-16T18:17:03Z",
      change_24h: "1440.0",
      change_24h_pct: "1.54",
      change_24h_reference_open_time: "2026-11-15T18:16:00Z",
      volume_24h: null,
      volume_24h_minutes: 1439,
      performance: { "1D": "1.54", "1W": null, "1M": null, "3M": null, "6M": null, "1Y": null },
    };
    const { container } = render(<Harness {...legendProps} />);
    expect(screen.getByTestId("legend-change-24h")).toHaveTextContent("+1,440.0 (+1.54%)");
    expect(container.textContent).not.toMatch(/Sample/i);
  });

  it("absent change is an em dash with its reason on hover", () => {
    legendState.stats = {
      instrument_id: 1,
      last_close: "95123.4",
      last_closed_open_time: "2026-11-16T18:16:00Z",
      last_price: "95124.0",
      last_price_closed: false,
      as_of: "2026-11-16T18:17:00Z",
      last_tick_at: "2026-11-16T18:17:03Z",
      change_24h: null,
      change_24h_pct: null,
      change_24h_reference_open_time: "2026-11-15T18:16:00Z",
      volume_24h: null,
      volume_24h_minutes: 1439,
      performance: { "1D": null, "1W": null, "1M": null, "3M": null, "6M": null, "1Y": null },
    };
    const { container } = render(<Harness {...legendProps} />);
    const legend = screen.getByTestId("legend-change-24h");
    expect(legend).toHaveTextContent("— (—)");
    // Never a bare dash: the hover names the missing minute.
    for (const dash of legend.querySelectorAll("[title]")) {
      expect(dash.getAttribute("title")).toMatch(/24h reference minute missing \(15 Nov, 18:16 UTC\)/);
    }
    expect(legend.querySelectorAll("[title]")).toHaveLength(2);
    expect(container.textContent).not.toMatch(/Sample/i);
  });

  it("before the figures arrive, the dash says they are loading", () => {
    render(<Harness {...legendProps} />);
    const dashes = screen.getByTestId("legend-change-24h").querySelectorAll("[title]");
    expect(dashes).toHaveLength(2);
    for (const d of dashes) expect(d.getAttribute("title")).toBe("Loading live figures");
  });
});

// Phase 3: the legend OHLC and the last price label come from the live
// forming candle, as the server's decimal strings, each coloured by the
// direction of its last change.
describe("chart legend live OHLC", () => {
  afterEach(() => cleanup());

  const textCandles = candleSet(3, 100).map((c) => ({
    ...c,
    text: { open: c.open.toFixed(2), high: c.high.toFixed(2), low: c.low.toFixed(2), close: c.close.toFixed(2) },
  }));
  const lastOpen = textCandles[2].openTime;
  const tick = (close: string, high: string, seq: number, epoch = 0): LiveTick => ({
    seq,
    epoch,
    candle: {
      open_time: lastOpen,
      close_time: new Date(Date.parse(lastOpen) + 60_000).toISOString(),
      open: "102.00",
      high,
      low: "101.00",
      close,
      volume: "1.0000",
      revision: 1,
    },
  });
  const props = {
    className: "",
    symbol: "BTC",
    venue: "hyperliquid",
    candles: textCandles,
    systemZone: undefined,
    plan: undefined,
    formationOutsideWindow: false,
  } as unknown as HarnessProps;

  it("shows the server's strings exactly, through the shared LivePrice", () => {
    const view = render(<Harness {...props} />);
    // The newest loaded bar: 102.00 / 103.00 / 101.00 / 102.50, at rest.
    expect(screen.getByTestId("legend-close")).toHaveTextContent("102.50");
    expect(screen.getByTestId("legend-close")).not.toHaveAttribute("data-dir");

    // A live tick: close 102.50 -> 102.75 (up), high 103.00 -> 103.10 (up).
    view.rerender(<Harness {...props} forming={tick("102.75", "103.10", 2)} />);
    const close = screen.getByTestId("legend-close");
    expect(close).toHaveTextContent("102.75");
    expect(close).toHaveAttribute("data-dir", "up");
    // Changed digits from the left: "102." unchanged, "75" changed.
    expect(close.querySelector('[data-part="changed"]')).toHaveTextContent(/^75$/);
    expect(close.querySelector('[data-part="changed"]')).toHaveClass("text-up");
    expect(screen.getByTestId("legend-high")).toHaveAttribute("data-dir", "up");
    // Open did not move: still at rest.
    expect(screen.getByTestId("legend-open")).not.toHaveAttribute("data-dir");
    expect(screen.getByTestId("last-price-label")).toHaveTextContent("102.75");

    // Then down: 102.75 -> 102.40. Exact strings, never rounded.
    view.rerender(<Harness {...props} forming={tick("102.40", "103.10", 3)} />);
    expect(screen.getByTestId("legend-close")).toHaveTextContent("102.40");
    expect(screen.getByTestId("legend-close")).toHaveAttribute("data-dir", "down");
    // High unchanged this time: equal price changes nothing.
    expect(screen.getByTestId("legend-high")).toHaveAttribute("data-dir", "up");
    expect(screen.getByTestId("last-price-label")).toHaveTextContent("102.40");
  });

  it("while hovering an old bar, the legend shows that bar and the last price label keeps moving", () => {
    const view = render(<Harness {...props} />);
    // Hover the first bar (100.00 / 101.00 / 99.00 / 100.50).
    act(() => capturedCrosshair!({ seriesData: new Map([[lastSeries, { time: tSec(0), open: 100, high: 101, low: 99, close: 100.5 }]]) }));
    expect(screen.getByTestId("legend-close")).toHaveTextContent("100.50");
    view.rerender(<Harness {...props} forming={tick("102.75", "103.10", 2)} />);
    expect(screen.getByTestId("legend-close")).toHaveTextContent("100.50"); // still the hovered bar
    expect(screen.getByTestId("last-price-label")).toHaveTextContent("102.75"); // live
  });

  it("after a reconnect (a new snapshot epoch, seq back at 1) the same bar keeps moving", () => {
    const view = render(<Harness {...props} />);
    view.rerender(<Harness {...props} forming={tick("102.75", "103.10", 500)} />);
    expect(screen.getByTestId("legend-close")).toHaveTextContent("102.75");
    // The resubscribe's snapshot restarts seq: seq 1 of a new epoch.
    view.rerender(<Harness {...props} forming={tick("102.90", "103.10", 1, 1)} />);
    expect(screen.getByTestId("legend-close")).toHaveTextContent("102.90");
    view.rerender(<Harness {...props} forming={tick("102.95", "103.10", 2, 1)} />);
    expect(screen.getByTestId("legend-close")).toHaveTextContent("102.95");
    // Within an epoch an out-of-order (lower seq) tick is still dropped.
    view.rerender(<Harness {...props} forming={tick("101.00", "103.10", 1, 1)} />);
    expect(screen.getByTestId("legend-close")).toHaveTextContent("102.95");
  });

  it("an instrument with no candles shows dashes with a reason, never zeros", () => {
    render(<Harness {...props} candles={[]} />);
    for (const f of ["open", "high", "low", "close"]) {
      const el = screen.getByTestId(`legend-${f}`);
      expect(el).toHaveTextContent("—");
      expect(el).toHaveAttribute("title", "No candle loaded for this instrument");
    }
  });
});

// Phase 3: the range buttons set the visible range, and say so when the
// stored history is shorter than the range.
describe("range buttons", () => {
  // These tests wait asynchronously (paging is async), long enough for the
  // library's own redraw frame to fire -- which cannot size a price axis
  // in jsdom (no canvas layout) and throws. Faking only the animation
  // frame keeps that redraw from running; timers and promises stay real.
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["requestAnimationFrame", "cancelAnimationFrame"] });
    lastVisibleRange = null;
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    legendState.loadHistoryBack = undefined;
  });

  const props = {
    className: "",
    symbol: "BTC",
    venue: "hyperliquid",
    systemZone: undefined,
    plan: undefined,
    formationOutsideWindow: false,
  } as unknown as HarnessProps;

  it("are enabled, and All shows the whole stored history", async () => {
    render(<Harness {...props} candles={candleSet(180, 100)} />);
    const all = screen.getByTestId("range-All");
    expect(all).toBeEnabled();
    legendState.loadHistoryBack = async () => ({ outcome: "exhausted", oldest: candleSet(180, 100)[0].openTime });
    all.click();
    await waitFor(() => expect(screen.getByTestId("range-notice")).toHaveTextContent("Showing all 3 hours of stored history"));
    expect(all).toHaveAttribute("aria-pressed", "true");
  });

  it("says how much is shown when stored history is shorter than the range", async () => {
    const held = candleSet(180, 100); // 179 minutes
    legendState.loadHistoryBack = async () => ({ outcome: "exhausted", oldest: held[0].openTime });
    render(<Harness {...props} candles={held} />);
    screen.getByTestId("range-5D").click();
    await waitFor(() => expect(screen.getByTestId("range-notice")).toHaveTextContent("Showing 3 hours, the most history stored"));
    // Everything held, ending at the newest bar.
    expect(lastVisibleRange).toEqual({ from: tSec(0), to: tSec(179) });
  });

  it("a range the held history covers is applied with no notice and no paging", async () => {
    const held = candleSet(180, 100);
    let paged = false;
    legendState.loadHistoryBack = async () => {
      paged = true;
      return { outcome: "covered", oldest: held[0].openTime };
    };
    render(<Harness {...props} candles={held} />);
    // A 1D range over 3 hours cannot be covered, so hand-build a "1D"
    // check over a full day instead: 1441 one-minute bars.
    cleanup();
    const day = candleSet(1441, 100);
    render(<Harness {...props} candles={day} />);
    screen.getByTestId("range-1D").click();
    await waitFor(() => expect(lastVisibleRange).toEqual({ from: tSec(0), to: tSec(1440) }));
    expect(paged).toBe(false);
    expect(screen.queryByTestId("range-notice")).toBeNull();
  });

  it("says so when the chart's bar cap stops the paging", async () => {
    const held = candleSet(180, 100);
    legendState.loadHistoryBack = async () => ({ outcome: "capped", oldest: held[0].openTime });
    render(<Harness {...props} candles={held} />);
    screen.getByTestId("range-1M").click();
    await waitFor(() => expect(screen.getByTestId("range-notice")).toHaveTextContent(/the most this chart loads at 1m \(10,000 bars\)/));
  });
});

// Item 2 (chart timeframe default view): what the chart shows when there is
// no saved view, or the saved one no longer fits, and how a hole in the
// data reaches the series.
describe("default view and gap whitespace", () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    cleanup();
    localStorage.clear();
  });

  const props = (symbol: string, candles: ReturnType<typeof candleSet>) =>
    ({
      className: "",
      symbol,
      venue: "test",
      candles,
      systemZone: undefined,
      plan: undefined,
      formationOutsideWindow: false,
    }) as unknown as HarnessProps;

  it("switching to a short series with no saved view shows a normal-width window from the left, not fitContent", () => {
    const { rerender } = render(<Harness {...props("BTC-PERP", candleSet(200, 100))} />);
    const ts = lastCreatedChart!.timeScale();
    const setRange = vi.spyOn(ts, "setVisibleLogicalRange");
    const fit = vi.spyOn(ts, "fitContent");

    rerender(<Harness {...props("ETH-PERP", candleSet(3, 300))} />);

    expect(setRange).toHaveBeenCalledWith({ from: -2, to: 122 });
    expect(fit).not.toHaveBeenCalled();
  });

  it("a long series with no saved view ends at the newest bar with about 120 in view", () => {
    const { rerender } = render(<Harness {...props("BTC-PERP", candleSet(5, 100))} />);
    const setRange = vi.spyOn(lastCreatedChart!.timeScale(), "setVisibleLogicalRange");
    rerender(<Harness {...props("ETH-PERP", candleSet(1000, 300))} />);
    expect(setRange).toHaveBeenCalledWith({ from: 880, to: 1004 });
  });

  it("a saved view that no longer fits the data falls back to the default", () => {
    // Saved over times this data does not contain at all (a long-gone window).
    saveChartView("2:1m", { from_time: tSec(5000), to_time: tSec(5150), bars_visible: 150, bar_spacing: 6, right_offset: 0, pinned_to_latest: false });
    const { rerender } = render(<Harness {...props("BTC-PERP", candleSet(5, 100))} />);
    const setRange = vi.spyOn(lastCreatedChart!.timeScale(), "setVisibleLogicalRange");
    rerender(<Harness {...props("ETH-PERP", candleSet(40, 300))} />);
    expect(setRange).not.toHaveBeenCalledWith({ from: 800, to: 950 });
    expect(setRange).toHaveBeenCalledWith({ from: -2, to: 122 });
  });

  it("a switch whose first render still carries the OLD candles waits for the new data before positioning (real 4h-switch bug)", () => {
    const old = candleSet(200, 100);
    const { rerender } = render(<Harness {...props("BTC-PERP", old)} />);
    const setRange = vi.spyOn(lastCreatedChart!.timeScale(), "setVisibleLogicalRange");
    // Scope flips while the previous candles are still the ones on hand
    // (a fresh array with the same content, as ChartSurface builds one on
    // every render)...
    rerender(<Harness {...props("ETH-PERP", old.map((c) => ({ ...c })))} />);
    // ...then the live-candles hook clears them...
    rerender(<Harness {...props("ETH-PERP", [])} />);
    // ...then the new scope's own 3 candles arrive.
    rerender(<Harness {...props("ETH-PERP", candleSet(3, 300))} />);

    // Never positioned against the stale 200 bars (that default would be
    // {80, 204}); positioned once the new data is on hand.
    expect(setRange).not.toHaveBeenCalledWith({ from: 80, to: 204 });
    expect(setRange).toHaveBeenCalledWith({ from: -2, to: 122 });
  });

  it("range changes the library makes on its own (late report of the default, re-fit after resize) are not saved; one after a real gesture is", () => {
    const { rerender } = render(<Harness {...props("BTC-PERP", candleSet(5, 100))} />);
    rerender(<Harness {...props("ETH-PERP", candleSet(1000, 300))} />);
    // A real browser fires the range-change event a frame later, with the
    // range the component itself just applied.
    capturedRangeHandler?.({ from: 880, to: 1004 });
    // ...and a re-fit to a slightly different range after a resize.
    capturedRangeHandler?.({ from: 862.33, to: 1006 });
    expect(loadChartView("2:1m")).toBeNull();
    // A range change that follows a real gesture (wheel on the chart) saves.
    document.querySelector("canvas")!.dispatchEvent(new WheelEvent("wheel", { bubbles: true, deltaY: -100 }));
    capturedRangeHandler?.({ from: 900, to: 1004 });
    // The newest bar (index 999) was in view: saved as following the latest
    // bar, 5 bars past it, 104 bars wide -- not as indexes 900..1004.
    expect(loadChartView("2:1m")).toMatchObject({ pinned_to_latest: true, right_offset: 5, bars_visible: 104, from_time: tSec(900), to_time: tSec(999) });
  });

  it("a hole in the candles reaches the series as whitespace (time only), never as a jump", () => {
    const withHole = [...candleSet(3, 100)];
    // Next candle 5 minutes after the last: 4 missing minutes.
    withHole.push({ ...withHole[2], openTime: new Date(Date.UTC(2026, 0, 1, 0, 7)).toISOString() });
    render(<Harness {...props("BTC-PERP", withHole)} />);
    const series = lastCreatedChart!.panes()[0].getSeries()[0];
    // series.data() omits whitespace items, so read by logical index: the
    // 00:07 candle must sit at index 7 (after four whitespace slots), not
    // at index 3 right after 00:02 -- which is what a time-axis jump is.
    expect(series.dataByIndex(7)).toMatchObject({ time: Date.UTC(2026, 0, 1, 0, 7) / 1000 });
    for (const i of [3, 4, 5, 6]) expect(series.dataByIndex(i)).toBeNull();
    expect(series.dataByIndex(2)).toMatchObject({ time: Date.UTC(2026, 0, 1, 0, 2) / 1000 });
  });
});

// Phase 3 regression (seen on real data at 1h): the workspace re-renders
// on every live `stats` message. That must not re-run the chart's data
// path -- a full setData dropped the live forming bar and reset the
// legend several times a second.
describe("a live stats update does not reload the chart", () => {
  afterEach(() => {
    cleanup();
    legendState.apiCandles = [];
    legendState.stats = undefined;
  });

  it("re-rendering with the same candles calls setData no more", () => {
    legendState.apiCandles = Array.from({ length: 5 }, (_, i) => ({
      open_time: new Date(Date.UTC(2026, 0, 1, 0, i)).toISOString(),
      close_time: new Date(Date.UTC(2026, 0, 1, 0, i + 1)).toISOString(),
      open: "100.00",
      high: "101.00",
      low: "99.00",
      close: "100.50",
      volume: "1.0000",
      is_closed: true,
      revision: 1,
    }));
    const view = render(
      <Providers>
        <ChartSurface />
      </Providers>,
    );
    const before = setDataCount;
    for (let i = 0; i < 5; i++) {
      // A stats message: new figures, same candles.
      legendState.stats = {
        instrument_id: 1,
        last_close: "100.50",
        last_closed_open_time: "2026-01-01T00:04:00Z",
        last_price: `100.5${i}`,
        last_price_closed: false,
        as_of: "2026-01-01T00:05:00Z",
        last_tick_at: `2026-01-01T00:05:0${i}Z`,
        change_24h: null,
        change_24h_pct: null,
        change_24h_reference_open_time: "2025-12-31T00:04:00Z",
        volume_24h: null,
        volume_24h_minutes: 5,
        performance: { "1D": null, "1W": null, "1M": null, "3M": null, "6M": null, "1Y": null },
      };
      view.rerender(
        <Providers>
          <ChartSurface />
        </Providers>,
      );
    }
    expect(setDataCount).toBe(before);
  });
});

// fix/chart-interaction, item 1: ARGUS's own layer is labelled, can be
// hidden with Setups (a preference), and explains itself on click.
describe("ARGUS analysis layer", () => {
  afterEach(() => {
    cleanup();
    legendState.apiCandles = [];
    legendState.setup = undefined;
    legendState.events = [];
    preferences.detach();
  });

  const setup = {
    id: 172, instrument_id: 1, timeframe: "1m", poi_type: "order_block", poi_source_candle_id: 1, direction: "bullish",
    strategy_version: "v1", state: "invalidated", poi_source_candle_revision: 1, last_event_id: 2,
    evaluation_time: "2026-01-01T00:04:00Z", algorithm_version: "v1", created_at: "2026-01-01T00:01:00Z", updated_at: "2026-01-01T00:04:00Z",
    plan_snapshot: {
      status: "invalidated", direction: "bullish", entry: "100.40", stop: "99.00", targets: [{ level: "103.00", rr: "2/1" }],
      poi: {
        type: "order_block", direction: "bullish", low: "100.10", high: "100.40", order_block_candle_id: 1, order_block_candle_revision: 1,
        order_block_open_time: "2026-01-01T00:01:00Z", qmr_fail_candle_id: 0, qmr_fail_candle_revision: 0, structural_swing_id: 1, in_discount: true,
      },
      protected_swing_id: 1,
    },
  } as unknown as import("@/api/types").ApiSetup;
  const events = [
    { id: 1, setup_id: 172, event_type: "detected", evaluation_time: "2026-01-01T00:02:00Z" },
    { id: 2, setup_id: 172, event_type: "invalidated", evaluation_time: "2026-01-01T00:04:00Z" },
  ] as unknown as import("@/api/types").ApiSetupEvent[];

  function renderWithSetup() {
    legendState.apiCandles = Array.from({ length: 6 }, (_, i) => ({
      open_time: new Date(Date.UTC(2026, 0, 1, 0, i)).toISOString(),
      close_time: new Date(Date.UTC(2026, 0, 1, 0, i + 1)).toISOString(),
      open: "100.00", high: "101.00", low: "99.00", close: "100.50", volume: "1.0000", is_closed: true, revision: 1,
    }));
    legendState.setup = setup;
    legendState.events = events;
    return render(
      <Providers>
        <ChartSurface />
      </Providers>,
    );
  }

  it("is labelled as ARGUS analysis with how many setups are drawn and the selected one", () => {
    renderWithSetup();
    expect(screen.getByTestId("analysis-layer")).toHaveTextContent(
      "ARGUS analysis · 1 setup drawn · selected #172 · bullish order block · 1m · invalidated",
    );
  });

  it("Setups hidden (the saved preference) removes the label and the layer", () => {
    preferences.set("analysis_layers_visible", false);
    renderWithSetup();
    expect(screen.queryByTestId("analysis-layer")).toBeNull();
  });

  it("the popover gives the stored high and low, where it came from, that it cannot be moved or deleted, and copies", () => {
    const lifecycle = { ...setup, invalidated_at: "2026-01-01T00:04:00Z", found_mode: "replay" } as typeof setup;
    const [zone] = buildLayers([lifecycle], {
      chartTimeframe: "1h",
      lastCandleSec: Date.UTC(2026, 0, 1, 0, 5) / 1000,
      selectedId: undefined,
      filters: { ...DEFAULT_FILTERS, states: { ...DEFAULT_FILTERS.states, invalidated: true } },
    });
    let copied = 0;
    render(<SetupElementPopover element={zone} x={0} y={0} onClose={() => {}} onCopy={() => copied++} />);
    expect(screen.getByTestId("popover-title")).toHaveTextContent("Bullish order block · 1m");
    expect(screen.getByTestId("popover-row-zone-high")).toHaveTextContent("100.40");
    expect(screen.getByTestId("popover-row-zone-low")).toHaveTextContent("100.10");
    expect(screen.getByTestId("popover-row-timeframe")).toHaveTextContent("1m");
    expect(screen.getByTestId("popover-row-state")).toHaveTextContent("invalidated");
    expect(screen.getByTestId("popover-row-found")).toHaveTextContent("replay");
    expect(screen.getByTestId("popover-candles")).toHaveTextContent("formation: 2026-01-01 00:01 UTC");
    expect(screen.getByTestId("popover-reason")).toHaveTextContent("ends 2026-01-01 00:04 UTC, when the setup was invalidated");
    expect(screen.getByTestId("setup-element-popover")).toHaveTextContent("cannot be moved or deleted");
    expect(screen.queryByTestId("open-setup")).toBeNull();
    screen.getByTestId("copy-to-drawings").click();
    expect(copied).toBe(1);
  });
});
