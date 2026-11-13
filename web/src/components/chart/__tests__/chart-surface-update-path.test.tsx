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
import { cleanup, render } from "@testing-library/react";
import { ChartSurfaceInner, type ChartSurfaceInnerProps } from "../chart-surface";
import { loadChartView, saveChartView } from "../chart-view-persistence";
import { DrawingProvider, useDrawing } from "../drawing-context";
import type { DrawingsStore } from "../drawings/store";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { ApiInstrument } from "@/api/types";
import type { LiveTick } from "@/lib/use-live-candles";

vi.mock("@/components/workspace/analysis-workspace", () => ({
  useAnalysisWorkspace: () => ({
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
vi.mock("lightweight-charts", async (importOriginal) => {
  const actual = await importOriginal<typeof import("lightweight-charts")>();
  return {
    ...actual,
    createChart: (...args: Parameters<typeof actual.createChart>) => {
      createChartSpy(...args);
      const chart = actual.createChart(...args);
      lastCreatedChart = chart;

      const ts = chart.timeScale();
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

const basePlan = { entry: 100, stop: 95, targets: [110], fromTime: candleSet(5, 100)[0].openTime };

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

function Harness({
  forming = null,
  connectionState = "open",
  status = null,
  fatalReason = null,
  ...props
}: HarnessProps) {
  return (
    <Providers>
      <AddOneDrawing onReady={() => {}} />
      <ChartSurfaceInner
        {...props}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
        poiZone={{ low: 195, high: 205 }}
        plan={{ ...basePlan, fromTime: candleSet(8, 200)[0].openTime }}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
        poiZone={{ low: 195, high: 205 }}
        plan={{ ...basePlan, fromTime: candleSet(8, 200)[0].openTime }}
        formationOutsideWindow={false}
      />,
    );
    const canvasAfter = container.querySelector("canvas");
    expect(canvasAfter).toBe(canvasBefore);
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
          poiZone={{ low: 95, high: 105 }}
          plan={basePlan}
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
          poiZone={{ low: 195, high: 205 }}
          plan={{ ...basePlan, fromTime: candleSet(8, 200)[0].openTime }}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
        formationOutsideWindow={false}
      />,
    );
    rerender(
      <Harness
        className=""
        symbol="ETH-PERP"
        venue="test"
        candles={candleSet(5, 300)}
        poiZone={{ low: 295, high: 305 }}
        plan={{ ...basePlan, fromTime: candleSet(5, 300)[0].openTime }}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
        poiZone={{ low: 195, high: 205 }}
        plan={{ ...basePlan, fromTime: candleSet(8, 200)[0].openTime }}
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
function liveTick(openTime: string, close: number, opts: { revision?: number; seq: number }): LiveTick {
  return {
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
        formationOutsideWindow={false}
      />,
    );

    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candles}
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
    const goodView = { from: 1, to: 4 };
    saveChartView("BTC-PERP:1m", goodView);

    const { rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
        formationOutsideWindow={false}
      />,
    );
    // The create effect's own restore read and re-applied goodView --
    // confirm the fix did not also stop that from happening.
    expect(loadChartView("BTC-PERP:1m")).toEqual(goodView);

    // Switch away, with real data (an ordinary, clean switch).
    rerender(
      <Harness
        className=""
        symbol="ETH-PERP"
        venue="test"
        candles={candleSet(5, 300)}
        poiZone={{ low: 295, high: 305 }}
        plan={{ ...basePlan, fromTime: candleSet(5, 300)[0].openTime }}
        formationOutsideWindow={false}
      />,
    );

    // Switch back to BTC-PERP, but candles is still empty -- exactly
    // the slow-fetch intermediate render the bug report describes.
    // applyCandleData's own setData([]) runs under the scope ref
    // already pointed at "BTC-PERP:1m" (it updates eagerly, before data
    // arrives) -- this is the moment that used to corrupt the saved
    // view.
    rerender(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={[]}
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
        formationOutsideWindow={false}
      />,
    );

    expect(loadChartView("BTC-PERP:1m")).toEqual(goodView);
  });

  it("the saved view is still restored once real data arrives, even though the scope flipped earlier on an empty render", () => {
    const goodView = { from: 1, to: 4 };
    saveChartView("BTC-PERP:1m", goodView);

    const { rerender } = render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
        formationOutsideWindow={false}
      />,
    );
    rerender(
      <Harness
        className=""
        symbol="ETH-PERP"
        venue="test"
        candles={candleSet(5, 300)}
        poiZone={{ low: 295, high: 305 }}
        plan={{ ...basePlan, fromTime: candleSet(5, 300)[0].openTime }}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
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
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
        formationOutsideWindow={false}
      />,
    );

    expect(setRangeSpy).toHaveBeenCalledWith(goodView);
  });

  it("a range change made outside this component's own calls (a real user gesture, at the library level) is saved", () => {
    render(
      <Harness
        className=""
        symbol="BTC-PERP"
        venue="test"
        candles={candleSet(5, 100)}
        poiZone={{ low: 95, high: 105 }}
        plan={basePlan}
        formationOutsideWindow={false}
      />,
    );
    expect(loadChartView("BTC-PERP:1m")).toBeNull(); // nothing saved yet

    // The real subscribeVisibleLogicalRangeChange handler chart-
    // surface.tsx registered, invoked directly with a synthetic range
    // -- exactly what the library itself would pass it on a genuine
    // wheel/drag/scroll gesture (jsdom's own zero-layout chart cannot
    // produce that for real; see the mock's own doc comment). Called
    // from here, completely outside any of this component's own
    // withProgrammaticRangeChange-wrapped calls, so the guard must see
    // this exactly as it would see a real user gesture.
    capturedRangeHandler!({ from: 0, to: 3 });

    expect(loadChartView("BTC-PERP:1m")).toEqual({ from: 0, to: 3 });
  });
});
