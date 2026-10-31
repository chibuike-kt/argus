import { useState } from "react";
import { Link } from "react-router-dom";
import { CaretRight } from "@phosphor-icons/react";
import { Panel } from "@/components/ui/panel";
import { Pill } from "@/components/ui/pill";
import { SetupStateBadge } from "@/components/ui/setup-state-badge";
import { CoinIcon, baseTicker } from "@/components/ui/coin-icon";
import { setups, asOf } from "@/mocks/fixtures";
import type { SetupState, POIType } from "@/types/domain";
import { cn } from "@/lib/cn";

const COLS =
  "grid grid-cols-[1.5fr_0.9fr_0.9fr_0.5fr_0.9fr_0.9fr_0.5fr_1fr_0.5fr_0.3fr] items-center gap-3";

const n = (v: number) => v.toLocaleString("en-US", { maximumFractionDigits: 4 });

const POI_LABEL: Record<POIType, string> = { order_block: "Order block", qmr: "QMR" };

const FILTERS: { label: string; value: SetupState | "all" }[] = [
  { label: "All", value: "all" },
  { label: "Actionable", value: "valid" },
  { label: "Forming", value: "forming" },
  { label: "Triggered", value: "triggered" },
];

function age(createdAt: string) {
  const ms = new Date(asOf).getTime() - new Date(createdAt).getTime();
  const m = Math.max(1, Math.round(ms / 60000));
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  return h < 24 ? `${h}h` : `${Math.round(h / 24)}d`;
}

export function Setups() {
  const [filter, setFilter] = useState<SetupState | "all">("all");
  const rows = setups.filter((s) => filter === "all" || s.state === filter);

  return (
    <div className="mx-auto h-full max-w-[1200px] space-y-5 overflow-y-auto p-4 sm:p-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Setups</h1>
          <p className="mt-1 text-sm text-fg-muted">
            What ARGUS found across your watchlist. {setups.length} live findings.
          </p>
        </div>
        <div className="glass inline-flex items-center gap-1 rounded-full p-1">
          {FILTERS.map((f) => (
            <button
              key={f.value}
              onClick={() => setFilter(f.value)}
              className={cn(
                "focus-ring h-8 rounded-full px-4 text-[13px] font-medium transition-colors",
                f.value === filter
                  ? "bg-accent text-accent-foreground"
                  : "text-fg-muted hover:text-fg",
              )}
            >
              {f.label}
            </button>
          ))}
        </div>
      </div>

      <Panel variant="glass" pad="none" className="argus-in overflow-hidden">
        <div
          className={cn(
            COLS,
            "border-b border-line px-4 py-2.5 text-[11px] font-semibold uppercase tracking-wide text-fg-subtle",
          )}
        >
          <span>Instrument</span>
          <span>Direction</span>
          <span>POI</span>
          <span>TF</span>
          <span className="text-right">Entry</span>
          <span className="text-right">Stop</span>
          <span className="text-right">R:R</span>
          <span>State</span>
          <span className="text-right">Age</span>
          <span />
        </div>

        {rows.map((s) => {
          const bull = s.direction === "bullish";
          return (
            <Link
              key={s.id}
              to="/analysis"
              className={cn(
                COLS,
                "focus-ring border-b border-line/60 px-4 py-3 text-[13px] transition-colors last:border-0 hover:bg-white/[0.03]",
              )}
            >
              <span className="flex items-center gap-2.5">
                <CoinIcon symbol={s.symbol} size={24} />
                <span className="font-medium">{baseTicker(s.symbol).toUpperCase()}</span>
                <span className="text-fg-subtle">{s.symbol.endsWith(".P") ? "Perp" : ""}</span>
              </span>
              <span>
                <Pill variant={bull ? "up" : "down"} dot>
                  {bull ? "Long" : "Short"}
                </Pill>
              </span>
              <span className="text-fg-muted">{POI_LABEL[s.poiType]}</span>
              <span className="text-fg-muted">{s.timeframe}</span>
              <span className="tnum text-right">{n(s.entry)}</span>
              <span className="tnum text-right text-down">{n(s.stop)}</span>
              <span className="tnum text-right font-medium text-accent">{s.rr.toFixed(1)}</span>
              <span>
                <SetupStateBadge state={s.state} />
              </span>
              <span className="tnum text-right text-fg-subtle">{age(s.createdAt)} ago</span>
              <CaretRight className="size-4 justify-self-end text-fg-subtle" />
            </Link>
          );
        })}
      </Panel>
    </div>
  );
}
