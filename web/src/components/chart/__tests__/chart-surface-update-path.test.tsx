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
import { DrawingProvider, useDrawing } from "../drawing-context";
import type { DrawingsStore } from "../drawings/store";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { ApiInstrument } from "@/api/types";

vi.mock("@/components/workspace/analysis-workspace", () => ({
  useAnalysisWorkspace: () => ({
    instrument: { id: 1, native_symbol: "BTC-PERP", provider: "test" } as ApiInstrument,
  }),
}));

// ES module named exports are non-configurable live bindings --
// vi.spyOn cannot redefine lightweight-charts' own `createChart` export
// directly (vitest's own error points at this exact mocking guide).
// vi.mock replaces the whole module instead, keeping every real export
// except createChart, which becomes a vi.fn wrapping the real
// implementation -- still a real chart, just with a call count this
// test can read.
const createChartSpy = vi.fn();
vi.mock("lightweight-charts", async (importOriginal) => {
  const actual = await importOriginal<typeof import("lightweight-charts")>();
  return {
    ...actual,
    createChart: (...args: Parameters<typeof actual.createChart>) => {
      createChartSpy(...args);
      return actual.createChart(...args);
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

function Harness(props: Omit<ChartSurfaceInnerProps, "loading" | "loadError">) {
  return (
    <Providers>
      <AddOneDrawing onReady={() => {}} />
      <ChartSurfaceInner {...props} loading={false} loadError={undefined} />
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
