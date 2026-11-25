import { createContext, useContext, useState, type ReactNode } from "react";
import { useAnalysisWorkspace } from "@/components/workspace/analysis-workspace";
import type { CandleInterval } from "@/lib/live-socket";
import { preferences, usePreferences } from "@/lib/preferences";
import { getDrawingsPersistence, useDrawingsStore } from "./drawings/store";

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

  timeframe: CandleInterval;
  setTimeframe: (tf: CandleInterval) => void;

  drawings: ReturnType<typeof useDrawingsStore>;
}

const Ctx = createContext<DrawingCtx | null>(null);

export function DrawingProvider({ children }: { children: ReactNode }) {
  const [activeTool, setActiveTool] = useState("cross");
  // The drawing toolbar's toggles are preferences (D1): they survive a
  // refresh, a cleared browser and another device.
  const tools = usePreferences().drawing_tools ?? {};
  const magnetOn = tools.magnet ?? false;
  const stayInDrawingMode = tools.stay_in_drawing_mode ?? false;
  const locked = tools.locked ?? false;
  const hidden = tools.hidden ?? false;
  const setMagnetOn = (v: boolean) => preferences.setEntry("drawing_tools", "magnet", v);
  const setStayInDrawingMode = (v: boolean) => preferences.setEntry("drawing_tools", "stay_in_drawing_mode", v);
  const setLocked = (v: boolean) => preferences.setEntry("drawing_tools", "locked", v);
  const setHidden = (v: boolean) => preferences.setEntry("drawing_tools", "hidden", v);

  // Scoped by instrument (D17): switching instrument swaps the visible
  // set; switching timeframe does not. instrument.native_symbol is the real selected
  // instrument now (GET /instruments, via the workspace this provider
  // is always mounted inside -- analysis-preview.tsx), not a fixture
  // import; a page with no instrument resolved yet scopes to a fixed
  // placeholder key rather than crashing or sharing drawings across
  // every instrument.
  //
  // timeframe itself is NOT owned here (it used to be, fixed at "1m")
  // -- it now lives in AnalysisWorkspaceProvider (Part 4, item 1),
  // which is this provider's own ancestor and already needs it to
  // drive useLiveCandles' own subscription; a descendant provider has
  // no way to feed a value back up to an ancestor, so the single
  // source of truth has to live there, read back here through the same
  // useAnalysisWorkspace() call this provider already makes.
  const { instrument, timeframe, setTimeframe } = useAnalysisWorkspace();
  // Scoped by instrument id alone (owner decision D17): a drawing belongs
  // to the instrument and shows on every timeframe, so a timeframe switch
  // keeps the same drawings and the same undo history. The keys drawings
  // were cached under before (per timeframe by id, and older, per
  // timeframe by symbol) are merged into this one once.
  const scope = instrument ? `${instrument.id}` : "unresolved";
  if (instrument) getDrawingsPersistence().aliasLegacy(scope, legacyDrawingScopes(instrument.id, instrument.native_symbol));
  const drawings = useDrawingsStore(scope);

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

// legacyDrawingScopes are the cache keys an instrument's drawings had before
// D17: one per timeframe by instrument id, and before those, by symbol.
// Shortest timeframe first, ids before symbols (the newer of the two).
export function legacyDrawingScopes(instrumentId: number, symbol: string): string[] {
  const tfs = ["1m", "5m", "15m", "1h", "4h", "1d"];
  return [...tfs.map((tf) => `${symbol}:${tf}`), ...tfs.map((tf) => `${instrumentId}:${tf}`)];
}

export function useDrawing() {
  const c = useContext(Ctx);
  if (!c) throw new Error("useDrawing must be used within DrawingProvider");
  return c;
}
