import { CoinIcon, baseTicker } from "@/components/ui/coin-icon";
import { markets, instrument } from "@/mocks/fixtures";
import { cn } from "@/lib/cn";

export function MarketsStrip() {
  return (
    <div className="flex gap-2 overflow-x-auto pb-1">
      {markets.map((m) => {
        const up = m.changePct >= 0;
        const active = m.symbol === instrument.symbol;
        return (
          <div
            key={m.symbol}
            className={cn(
              "glass flex shrink-0 items-center gap-2.5 rounded-full py-1.5 pl-1.5 pr-4",
              active && "border-accent/50",
            )}
          >
            <CoinIcon symbol={m.symbol} size={28} />
            <div className="flex flex-col leading-tight">
              <span className="text-[13px] font-semibold">
                {baseTicker(m.symbol).toUpperCase()}
              </span>
              <span className="tnum text-[11px] text-fg-subtle">
                {m.last.toLocaleString("en-US", { maximumFractionDigits: 4 })}
              </span>
            </div>
            <span className={cn("tnum text-[11px] font-medium", up ? "text-up" : "text-down")}>
              {up ? "+" : ""}
              {m.changePct.toFixed(2)}%
            </span>
          </div>
        );
      })}
    </div>
  );
}
