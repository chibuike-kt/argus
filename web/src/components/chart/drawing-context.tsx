import { createContext, useContext, useState, type ReactNode } from "react";
import { instrument } from "@/mocks/fixtures";
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
  const [timeframe, setTimeframe] = useState("5m");

  // Scoped by instrument + timeframe (ADR-008): switching either swaps the
  // visible set. Only one instrument exists in the fixtures today, but the
  // key already carries both, so wiring a real symbol switcher later is a
  // data change here, not a structural one.
  const drawings = useDrawingsStore(`${instrument.symbol}:${timeframe}`);

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
