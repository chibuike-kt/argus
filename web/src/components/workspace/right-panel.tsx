import { useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { Columns, PencilSimple, DotsThree, ArrowSquareOut } from "@phosphor-icons/react";
import { CoinIcon } from "@/components/ui/coin-icon";
import { TabsRoot, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { cn } from "@/lib/cn";
import { formatDecimal, formatPercent, toneClass } from "@/lib/decimal-display";
import { LiveFigure } from "@/components/ui/live-figure";
import { LivePrice } from "@/components/ui/live-price";
import { feedLine, liveReason } from "@/lib/figure-reasons";
import { useNow } from "@/lib/use-now";
import { useAnalysisWorkspace } from "./analysis-workspace";
import { preferences, usePreferences } from "@/lib/preferences";
import { Watchlist } from "./watchlist";
import { AnalysisPanel } from "./analysis-panel";

const WIDTH_STORAGE_KEY = "argus:right-panel-width";
const MIN_WIDTH = 280;
const MAX_WIDTH = 560;
const DEFAULT_WIDTH = 340;

function clampWidth(w: number): number {
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, w));
}

// readLegacyWidth reads the width saved before preferences moved to the
// server (D1), so an existing choice is kept once; wrapped -- blocked or
// cleared storage degrades to the default rather than throwing.
function readLegacyWidth(): number {
  try {
    const raw = window.localStorage.getItem(WIDTH_STORAGE_KEY);
    const w = raw ? Number(raw) : NaN;
    return Number.isFinite(w) ? clampWidth(w) : DEFAULT_WIDTH;
  } catch {
    return DEFAULT_WIDTH;
  }
}

function IconBtn({ children }: { children: ReactNode }) {
  return (
    <button className="focus-ring grid size-7 place-items-center rounded-md transition-colors hover:bg-white/5 hover:text-fg">
      {children}
    </button>
  );
}

// Symbol detail: the selected instrument's own id/symbol/provider (GET
// /instruments, via the workspace) and its live figures from the page's
// one stats source (Phase 3, D3: the forming candle's close, the 24h
// change against the price exactly 24h before the current minute, the
// 24h volume over the 1440 minutes ending now). Every figure is the
// server's decimal string as sent; the last price is the shared
// <LivePrice> (direction colour, then only the changed digits stay
// coloured); the change figures are coloured by sign.
// A dash always carries its reason on hover.
function SymbolDetail() {
  const { instrument, statsEntry, statsSnapshot } = useAnalysisWorkspace();
  const now = useNow(1000);
  if (!instrument) return null;
  const f = statsEntry?.figures ?? null;
  const motion = statsEntry?.motion;
  const changeTone = toneClass(f?.change_24h);
  const volumeUnit = instrument.base_asset ?? "";
  const feed = feedLine(statsEntry, statsSnapshot, now);

  return (
    <div className="max-h-[50%] shrink-0 overflow-y-auto border-t-2 border-line-strong" data-testid="symbol-detail">
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
          <LivePrice
            testId="detail-last"
            className={cn("text-3xl font-semibold leading-none", feed.quiet && "opacity-70")}
            value={f?.last_price ?? null}
            text={f?.last_price != null ? formatDecimal(f.last_price) : null}
            reason={liveReason("last_price", statsEntry, statsSnapshot)}
            resetKey={instrument.id}
          />
          {instrument.quote_asset && <span className="text-xs text-fg-subtle">{instrument.quote_asset}</span>}
        </div>
        <div className="tnum mt-1.5 flex items-center gap-1.5 text-[13px] font-medium">
          <LiveFigure
            testId="detail-change-24h"
            tone="sign"
            signTone={changeTone}
            text={f?.change_24h != null ? formatDecimal(f.change_24h, { signed: true }) : null}
            reason={liveReason("change_24h", statsEntry, statsSnapshot)}
            motion={motion?.change_24h}
          />
          <span className={changeTone}>(</span>
          <LiveFigure
            testId="detail-change-24h-pct"
            tone="sign"
            signTone={changeTone}
            className="px-0"
            text={f?.change_24h_pct != null ? formatPercent(f.change_24h_pct) : null}
            reason={liveReason("change_24h_pct", statsEntry, statsSnapshot)}
            motion={motion?.change_24h_pct}
          />
          <span className={changeTone}>)</span>
          <span className="text-[11px] font-normal text-fg-subtle">24h</span>
        </div>
        <p className="mt-1 text-[11px] text-fg-subtle" data-testid="detail-feed">
          {feed.text}
        </p>

        <div className="mt-4">
          <div className="mb-1 text-[11px] text-fg-subtle">Volume 24h (base asset volume)</div>
          <div className="text-[13px] font-medium">
            <LiveFigure
              testId="detail-volume-24h"
              tone="none"
              text={f?.volume_24h != null ? formatDecimal(f.volume_24h) : null}
              reason={liveReason("volume_24h", statsEntry, statsSnapshot)}
              motion={motion?.volume_24h}
              suffix={f?.volume_24h != null && volumeUnit ? <span className="ml-1 text-fg-subtle">{volumeUnit}</span> : null}
            />
          </div>
        </div>
      </div>
    </div>
  );
}

// ARGUS's read on the current setup is the panel's primary surface; the
// watchlist (and the symbol detail/news that goes with browsing it) stays
// one tab away, not gone. Defaulting to "analysis" is the reframe: this
// panel used to open on the watchlist.
export function RightPanel() {
  // Width and tab are preferences (D1): saved per user on the server, so
  // they survive a refresh, a cleared browser and another device.
  const prefs = usePreferences();
  const savedWidth = prefs.right_panel_width !== undefined ? clampWidth(prefs.right_panel_width) : readLegacyWidth();
  // While dragging, the live width; otherwise the saved one.
  const [dragWidth, setWidth] = useState<number | null>(null);
  const width = dragWidth ?? savedWidth;
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
    preferences.set("right_panel_width", Math.round(width));
    setWidth(null);
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
      <TabsRoot
        value={prefs.right_panel_tab ?? "analysis"}
        onValueChange={(v) => preferences.set("right_panel_tab", v === "watchlist" ? "watchlist" : "analysis")}
        className="flex min-h-0 flex-1 flex-col"
      >
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
