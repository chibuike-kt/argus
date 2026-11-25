// @vitest-environment jsdom
//
// Owner decision D17: drawings belong to the instrument. A timeframe switch
// keeps the drawings on screen and their undo history; an instrument switch
// changes both.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render } from "@testing-library/react";
import type { CandleInterval } from "@/lib/live-socket";

const ws = vi.hoisted(() => ({ instrument: { id: 4, native_symbol: "BTC" }, timeframe: "1m" as CandleInterval }));

vi.mock("@/components/workspace/analysis-workspace", () => ({
  useAnalysisWorkspace: () => ({ instrument: ws.instrument, timeframe: ws.timeframe, setTimeframe: () => {} }),
}));

const { DrawingProvider, useDrawing } = await import("../drawing-context");

let seen: ReturnType<typeof useDrawing> | null = null;
function Probe() {
  seen = useDrawing();
  return null;
}

function renderAt() {
  return render(
    <DrawingProvider>
      <Probe />
    </DrawingProvider>,
  );
}

describe("drawings scope (D17)", () => {
  afterEach(() => {
    cleanup();
    localStorage.clear();
  });

  it("a timeframe switch keeps the drawings and the undo history; an instrument switch does not", () => {
    ws.instrument = { id: 4, native_symbol: "BTC" };
    ws.timeframe = "1d";
    const view = renderAt();
    act(() => {
      seen!.drawings.add({ id: "t1", kind: "hline", price: 100, style: { color: "#fff", lineWidth: 1, lineStyle: "solid" }, createdAt: 1, updatedAt: 1 });
    });
    expect(seen!.drawings.drawings.map((d) => d.id)).toContain("t1");

    for (const tf of ["4h", "1h", "15m", "1m", "1d"] as CandleInterval[]) {
      ws.timeframe = tf;
      view.rerender(
        <DrawingProvider>
          <Probe />
        </DrawingProvider>,
      );
      expect(seen!.drawings.drawings.map((d) => d.id)).toContain("t1");
      expect(seen!.drawings.canUndo).toBe(true);
    }
    // Undo still works after the switches: the add is undone.
    act(() => seen!.drawings.undo());
    expect(seen!.drawings.drawings.map((d) => d.id)).not.toContain("t1");
    act(() => seen!.drawings.redo());

    // Another instrument: its own drawings, its own (empty) history.
    ws.instrument = { id: 5, native_symbol: "ETH" };
    view.rerender(
      <DrawingProvider>
        <Probe />
      </DrawingProvider>,
    );
    expect(seen!.drawings.drawings.map((d) => d.id)).not.toContain("t1");
    expect(seen!.drawings.canUndo).toBe(false);
    // Stored once, under the instrument id, not per timeframe.
    expect(JSON.parse(localStorage.getItem("argus:drawings:v1:4")!).map((d: { id: string }) => d.id)).toEqual(["t1"]);
    expect(localStorage.getItem("argus:drawings:v1:4:1d")).toBeNull();
  });
});
