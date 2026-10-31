import { useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { Columns, PencilSimple, DotsThree, ArrowSquareOut } from "@phosphor-icons/react";
import { CoinIcon } from "@/components/ui/coin-icon";
import { TabsRoot, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { instrument } from "@/mocks/fixtures";
import { cn } from "@/lib/cn";
import { Watchlist } from "./watchlist";
import { AnalysisPanel } from "./analysis-panel";

const n = (v: number) => v.toLocaleString("en-US", { maximumFractionDigits: 1 });

// Volume mock, matching TradingView's symbol detail.
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

          <div className="max-h-[50%] shrink-0 overflow-y-auto border-t-2 border-line-strong">
            <div className="p-4">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <CoinIcon symbol={instrument.symbol} size={26} />
                  <span className="text-sm font-semibold">{instrument.symbol}</span>
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
                {instrument.name} Contract
                <ArrowSquareOut className="size-3 text-fg-subtle" />
                <span className="text-fg-subtle">· {instrument.venue}</span>
              </div>
              <div className="text-[11px] text-fg-subtle">Swap · Crypto</div>

              <div className="mt-3 flex items-end gap-2">
                <span className="tnum text-3xl font-semibold leading-none">
                  {n(instrument.last)}
                </span>
                <span className="text-xs text-fg-subtle">USDT</span>
                <span
                  className={cn(
                    "tnum ml-auto text-sm font-medium",
                    instrument.changePct >= 0 ? "text-up" : "text-down",
                  )}
                >
                  {instrument.changePct >= 0 ? "+" : ""}
                  {n((instrument.last * instrument.changePct) / 100)}{" "}
                  {instrument.changePct >= 0 ? "+" : ""}
                  {instrument.changePct.toFixed(2)}%
                </span>
              </div>
              <div className="mt-2 flex items-center gap-1.5 text-[12px] text-up">
                <span className="size-1.5 rounded-full bg-up" /> Market open
              </div>

              <div className="mt-4">
                <div className="flex items-center justify-between text-[11px] text-fg-subtle">
                  <span>Volume</span>
                  <span>Avg. volume (30D)</span>
                </div>
                <div className="tnum mt-0.5 flex items-center justify-between text-[13px] font-medium">
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
              <div className="text-[11px] font-medium uppercase tracking-wide text-fg-subtle">
                News · 16 minutes ago
              </div>
              <button className="focus-ring mt-1.5 block text-left text-[13px] font-medium leading-snug transition-colors hover:underline">
                Crypto Selloff Has Further to Go After Binance Hack — Market Talk
              </button>
              <button className="focus-ring mt-2 text-[12px] text-fg-muted transition-colors hover:text-fg">
                More events ›
              </button>
            </div>
          </div>
        </TabsContent>
      </TabsRoot>
    </div>
  );
}
