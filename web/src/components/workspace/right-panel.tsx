import { useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { Columns, PencilSimple, DotsThree, ArrowSquareOut } from "@phosphor-icons/react";
import { CoinIcon } from "@/components/ui/coin-icon";
import { TabsRoot, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { SampleDataBadge } from "@/components/ui/sample-data-badge";
import { useAnalysisWorkspace } from "./analysis-workspace";
import { Watchlist } from "./watchlist";
import { AnalysisPanel } from "./analysis-panel";

// Volume has no endpoint today (ADR-006's own market-data contracts
// cover candles/quotes, not a traded-volume rollup) -- kept as a mock,
// marked, matching TradingView's own symbol-detail layout this panel is
// based on.
const volume = 43.94;
const avgVolume = 50.08;

const WIDTH_STORAGE_KEY = "argus:right-panel-width";
const MIN_WIDTH = 280;
const MAX_WIDTH = 560;
const DEFAULT_WIDTH = 340;

function clampWidth(w: number): number {
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, w));
}

// A per-viewer convenience (not data), so read/write is wrapped -- a
// private window, cleared site data or a blocked store should degrade to
// the default width rather than throw.
function readWidth(): number {
  try {
    const raw = window.localStorage.getItem(WIDTH_STORAGE_KEY);
    const w = raw ? Number(raw) : NaN;
    return Number.isFinite(w) ? clampWidth(w) : DEFAULT_WIDTH;
  } catch {
    return DEFAULT_WIDTH;
  }
}

function writeWidth(w: number) {
  try {
    window.localStorage.setItem(WIDTH_STORAGE_KEY, String(w));
  } catch {
    // Not persisted this session; the panel still resizes in memory.
  }
}

function IconBtn({ children }: { children: ReactNode }) {
  return (
    <button className="focus-ring grid size-7 place-items-center rounded-md transition-colors hover:bg-white/5 hover:text-fg">
      {children}
    </button>
  );
}

const n = (v: number) => v.toLocaleString("en-US", { maximumFractionDigits: 1 });

// Symbol detail: the selected instrument's own real id/symbol/provider
// (GET /instruments, via the workspace), "Last" derived from the most
// recent candle already fetched for the chart (trivial -- no extra
// request). changePct, "market open" and News have no endpoint at all
// (ADR-006 covers candles/quotes, not a day-change rollup or a market-
// status/news feed), so those are now fixed, marked samples instead of
// numbers computed to look real.
function SymbolDetail() {
  const { instrument, candles } = useAnalysisWorkspace();
  if (!instrument) return null;
  const lastCandle = candles[candles.length - 1];
  const last = lastCandle ? Number(lastCandle.close) : undefined;

  return (
    <div className="max-h-[50%] shrink-0 overflow-y-auto border-t-2 border-line-strong">
      <div className="p-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <CoinIcon symbol={instrument.native_symbol} size={26} />
            <span className="text-sm font-semibold">{instrument.native_symbol}</span>
          </div>
          <div className="flex items-center gap-0.5 text-fg-subtle">
            <IconBtn>
              <Columns className="size-4" />
            </IconBtn>
            <IconBtn>
              <PencilSimple className="size-4" />
            </IconBtn>
            <IconBtn>
              <DotsThree className="size-4" />
            </IconBtn>
          </div>
        </div>

        <div className="mt-2 flex items-center gap-1 text-[12px] text-fg-muted">
          {instrument.asset_class}
          <ArrowSquareOut className="size-3 text-fg-subtle" />
          <span className="text-fg-subtle">· {instrument.provider}</span>
        </div>

        <div className="mt-3 flex items-end gap-2">
          <span className="tnum text-3xl font-semibold leading-none">
            {last !== undefined ? n(last) : "—"}
          </span>
          {instrument.quote_asset && <span className="text-xs text-fg-subtle">{instrument.quote_asset}</span>}
          <span className="ml-auto">
            <SampleDataBadge label="Sample % change" />
          </span>
        </div>

        <div className="mt-4">
          <div className="mb-1 flex items-center justify-between">
            <span className="text-[11px] text-fg-subtle">Volume · Avg. volume (30D)</span>
            <SampleDataBadge />
          </div>
          <div className="tnum flex items-center justify-between text-[13px] font-medium">
            <span>{volume} K</span>
            <span>{avgVolume} K</span>
          </div>
          <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-white/10">
            <div
              className="h-full rounded-full bg-up"
              style={{ width: `${Math.min(100, (volume / avgVolume) * 100)}%` }}
            />
          </div>
        </div>
      </div>

      <div className="border-t border-line p-4">
        <div className="flex items-center justify-between">
          <div className="text-[11px] font-medium uppercase tracking-wide text-fg-subtle">News</div>
          <SampleDataBadge />
        </div>
        <button className="focus-ring mt-1.5 block text-left text-[13px] font-medium leading-snug transition-colors hover:underline">
          Crypto Selloff Has Further to Go After Binance Hack — Market Talk
        </button>
        <button className="focus-ring mt-2 text-[12px] text-fg-muted transition-colors hover:text-fg">
          More events ›
        </button>
      </div>
    </div>
  );
}

// ARGUS's read on the current setup is the panel's primary surface; the
// watchlist (and the symbol detail/news that goes with browsing it) stays
// one tab away, not gone. Defaulting to "analysis" is the reframe: this
// panel used to open on the watchlist.
export function RightPanel() {
  const [width, setWidth] = useState(readWidth);
  const dragRef = useRef<{ startX: number; startWidth: number } | null>(null);

  const onHandlePointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    e.currentTarget.setPointerCapture(e.pointerId);
    dragRef.current = { startX: e.clientX, startWidth: width };
  };
  const onHandlePointerMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!dragRef.current) return;
    // The handle sits on the panel's left edge: dragging left (negative
    // delta) widens the panel, dragging right narrows it.
    const delta = e.clientX - dragRef.current.startX;
    setWidth(clampWidth(dragRef.current.startWidth - delta));
  };
  const onHandlePointerUp = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!dragRef.current) return;
    e.currentTarget.releasePointerCapture(e.pointerId);
    dragRef.current = null;
    writeWidth(width);
  };

  return (
    <div
      className="argus-in glass relative flex shrink-0 flex-col overflow-hidden rounded-2xl"
      style={{ "--argus-in-delay": "120ms", width } as React.CSSProperties}
    >
      <div
        onPointerDown={onHandlePointerDown}
        onPointerMove={onHandlePointerMove}
        onPointerUp={onHandlePointerUp}
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize panel"
        className="absolute inset-y-0 left-0 z-10 w-1.5 -translate-x-1/2 cursor-col-resize touch-none"
      />
      <TabsRoot defaultValue="analysis" className="flex min-h-0 flex-1 flex-col">
        <div className="flex shrink-0 justify-center border-b border-line px-3 py-2">
          <TabsList>
            <TabsTrigger value="analysis">Analysis</TabsTrigger>
            <TabsTrigger value="watchlist">Watchlist</TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="analysis" className="min-h-0 flex-1 overflow-y-auto p-4 focus-ring">
          <AnalysisPanel />
        </TabsContent>

        <TabsContent
          value="watchlist"
          className="flex min-h-0 flex-1 flex-col overflow-hidden focus-ring"
        >
          <div className="min-h-0 flex-1 overflow-hidden">
            <Watchlist />
          </div>

          <SymbolDetail />
        </TabsContent>
      </TabsRoot>
    </div>
  );
}
