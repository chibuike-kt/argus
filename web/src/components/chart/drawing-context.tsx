import { createContext, useContext, useState, type ReactNode } from "react";
import { useAnalysisWorkspace } from "@/components/workspace/analysis-workspace";
import { useDrawingsStore } from "./drawings/store";

interface DrawingCtx {
  // activeTool is the rail variant id (e.g. "rect", "long", "cross"). The
  // single source of truth for which tool is active -- the rail's highlight
  // and the chart's cursor/crosshair both read this, so they can never
  // desync the way two independently-held copies could.
  activeTool: string;
  setActiveTool: (t: string) => void;

  magnetOn: boolean;
  setMagnetOn: (v: boolean) => void;
  stayInDrawingMode: boolean;
  setStayInDrawingMode: (v: boolean) => void;
  locked: boolean;
  setLocked: (v: boolean) => void;
  hidden: boolean;
  setHidden: (v: boolean) => void;

  timeframe: string;
  setTimeframe: (tf: string) => void;

  drawings: ReturnType<typeof useDrawingsStore>;
}

const Ctx = createContext<DrawingCtx | null>(null);

export function DrawingProvider({ children }: { children: ReactNode }) {
  const [activeTool, setActiveTool] = useState("cross");
  const [magnetOn, setMagnetOn] = useState(false);
  const [stayInDrawingMode, setStayInDrawingMode] = useState(false);
  const [locked, setLocked] = useState(false);
  const [hidden, setHidden] = useState(false);
  // "1m" is the only timeframe the backend has (ChartToolbar's own
  // symbol/interval controls lock the UI to it, web-wiring-followup
  // brief Part 2) -- this default now matches that, not the fixture
  // era's arbitrary "5m".
  const [timeframe, setTimeframe] = useState("1m");

  // Scoped by instrument + timeframe (ADR-008): switching either swaps
  // the visible set. instrument.native_symbol is the real selected
  // instrument now (GET /instruments, via the workspace this provider
  // is always mounted inside -- analysis-preview.tsx), not a fixture
  // import; a page with no instrument resolved yet scopes to a fixed
  // placeholder key rather than crashing or sharing drawings across
  // every instrument.
  const { instrument } = useAnalysisWorkspace();
  const scopeSymbol = instrument?.native_symbol ?? "unresolved";
  const drawings = useDrawingsStore(`${scopeSymbol}:${timeframe}`);

  return (
    <Ctx.Provider
      value={{
        activeTool,
        setActiveTool,
        magnetOn,
        setMagnetOn,
        stayInDrawingMode,
        setStayInDrawingMode,
        locked,
        setLocked,
        hidden,
        setHidden,
        timeframe,
        setTimeframe,
        drawings,
      }}
    >
      {children}
    </Ctx.Provider>
  );
}

export function useDrawing() {
  const c = useContext(Ctx);
  if (!c) throw new Error("useDrawing must be used within DrawingProvider");
  return c;
}
