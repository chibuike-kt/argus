import type { ReactNode } from "react";
import { Columns3, SquarePen, MoreHorizontal, ExternalLink } from "lucide-react";
import { CoinIcon } from "@/components/ui/coin-icon";
import { instrument } from "@/mocks/fixtures";
import { cn } from "@/lib/cn";
import { Watchlist } from "./watchlist";

const n = (v: number) => v.toLocaleString("en-US", { maximumFractionDigits: 1 });

// Volume mock, matching TradingView's symbol detail.
const volume = 43.94;
const avgVolume = 50.08;

function IconBtn({ children }: { children: ReactNode }) {
  return (
    <button className="focus-ring grid size-7 place-items-center rounded-md transition-colors hover:bg-white/5 hover:text-fg">
      {children}
    </button>
  );
}

export function RightPanel() {
  const up = instrument.changePct >= 0;
  const chg = (instrument.last * instrument.changePct) / 100;
  const volPct = Math.min(100, (volume / avgVolume) * 100);

  return (
    <div className="glass flex w-[340px] shrink-0 flex-col overflow-hidden rounded-2xl">
      {/* watchlist: fixed region; only its own row list scrolls (header pinned) */}
      <div className="min-h-0 flex-1 overflow-hidden">
        <Watchlist />
      </div>

      {/* symbol detail + news: pinned, always visible, with its own scroll */}
      <div className="max-h-[50%] shrink-0 overflow-y-auto border-t-2 border-line-strong">
        <div className="p-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <CoinIcon symbol={instrument.symbol} size={26} />
              <span className="text-sm font-semibold">{instrument.symbol}</span>
            </div>
            <div className="flex items-center gap-0.5 text-fg-subtle">
              <IconBtn>
                <Columns3 className="size-4" />
              </IconBtn>
              <IconBtn>
                <SquarePen className="size-4" />
              </IconBtn>
              <IconBtn>
                <MoreHorizontal className="size-4" />
              </IconBtn>
            </div>
          </div>

          <div className="mt-2 flex items-center gap-1 text-[12px] text-fg-muted">
            {instrument.name} Contract
            <ExternalLink className="size-3 text-fg-subtle" />
            <span className="text-fg-subtle">· {instrument.venue}</span>
          </div>
          <div className="text-[11px] text-fg-subtle">Swap · Crypto</div>

          <div className="mt-3 flex items-end gap-2">
            <span className="tnum text-3xl font-semibold leading-none">{n(instrument.last)}</span>
            <span className="text-xs text-fg-subtle">USDT</span>
            <span className={cn("tnum ml-auto text-sm font-medium", up ? "text-up" : "text-down")}>
              {up ? "+" : ""}
              {n(chg)} {up ? "+" : ""}
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
                className="h-full rounded-full bg-gradient-to-r from-accent to-up"
                style={{ width: `${volPct}%` }}
              />
            </div>
          </div>
        </div>

        <div className="border-t border-line p-4">
          <div className="text-[11px] font-medium uppercase tracking-wide text-fg-subtle">
            News · 16 minutes ago
          </div>
          <button className="focus-ring mt-1.5 block text-left text-[13px] font-medium leading-snug transition-colors hover:text-accent">
            Crypto Selloff Has Further to Go After Binance Hack — Market Talk
          </button>
          <button className="focus-ring mt-2 text-[12px] text-fg-muted transition-colors hover:text-fg">
            More events ›
          </button>
        </div>
      </div>
    </div>
  );
}
