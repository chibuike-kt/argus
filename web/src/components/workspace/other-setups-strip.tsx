import { useState } from "react";
import { Link } from "react-router-dom";
import { CaretDown, CaretRight } from "@phosphor-icons/react";
import { Panel } from "@/components/ui/panel";
import { Pill } from "@/components/ui/pill";
import { SetupStateBadge } from "@/components/ui/setup-state-badge";
import { CoinIcon, baseTicker } from "@/components/ui/coin-icon";
import { setups, asOf, instrument } from "@/mocks/fixtures";
import { cn } from "@/lib/cn";

const COLS = "grid grid-cols-[1.6fr_0.8fr_1fr_0.6fr_0.9fr_0.6fr_0.3fr] items-center gap-3";
const MAX_ROWS = 6;
const OPEN_STORAGE_KEY = "argus:other-setups-open";

const n = (v: number) => v.toLocaleString("en-US", { maximumFractionDigits: 4 });

function age(createdAt: string) {
  const ms = new Date(asOf).getTime() - new Date(createdAt).getTime();
  const m = Math.max(1, Math.round(ms / 60000));
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  return h < 24 ? `${h}h` : `${Math.round(h / 24)}d`;
}

// A per-viewer convenience (not data), so read/write is wrapped -- a
// private window, cleared site data or a blocked store should degrade to
// "open" rather than throw.
function readOpen(): boolean {
  try {
    const raw = window.localStorage.getItem(OPEN_STORAGE_KEY);
    return raw === null ? true : raw === "1";
  } catch {
    return true;
  }
}

function writeOpen(v: boolean) {
  try {
    window.localStorage.setItem(OPEN_STORAGE_KEY, v ? "1" : "0");
  } catch {
    // Not persisted this session; the strip still toggles in memory.
  }
}

// A condensed Setups table for the other instruments ARGUS is tracking, so
// the trader can see what else is actionable without leaving the analysis
// screen -- the reference's below-chart screener, adapted for a
// single-instrument view rather than copied: ARGUS has one chart open at a
// time, not a scrollable universe, so this stays a short list of what's
// elsewhere, not a full screener. Excludes the instrument already on
// screen. Row navigation reuses the Setups page's own pattern (today that
// always opens the one analysis route; a per-instrument route is a
// follow-up, not something this strip can address on its own).
export function OtherSetupsStrip() {
  const [open, setOpen] = useState(readOpen);
  const rows = setups.filter((s) => s.symbol !== instrument.symbol).slice(0, MAX_ROWS);
  if (rows.length === 0) return null;

  const toggle = () => {
    setOpen((o) => {
      const next = !o;
      writeOpen(next);
      return next;
    });
  };

  return (
    <Panel variant="glass" pad="none" className="argus-in shrink-0 overflow-hidden">
      <button
        onClick={toggle}
        aria-expanded={open}
        className="focus-ring flex w-full items-center justify-between px-4 py-2 transition-colors hover:bg-white/[0.03]"
      >
        <span className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-fg-subtle">
          <CaretDown className={cn("size-3 transition-transform", !open && "-rotate-90")} />
          Other setups
        </span>
        <Link
          to="/setups"
          onClick={(e) => e.stopPropagation()}
          className="focus-ring text-[11px] font-medium text-fg-muted transition-colors hover:text-fg"
        >
          View all
        </Link>
      </button>

      {open && (
        <>
          <div
            className={cn(
              COLS,
              "border-t border-line px-4 py-1.5 text-[10px] font-semibold uppercase tracking-wide text-fg-subtle",
            )}
          >
            <span>Instrument</span>
            <span>Direction</span>
            <span className="text-right">Entry</span>
            <span className="text-right">R:R</span>
            <span>State</span>
            <span className="text-right">Age</span>
            <span />
          </div>

          <div className="max-h-[168px] overflow-y-auto">
            {rows.map((s) => {
              const bull = s.direction === "bullish";
              return (
                <Link
                  key={s.id}
                  to="/analysis"
                  className={cn(
                    COLS,
                    "focus-ring border-b border-line/60 px-4 py-2 text-[12px] transition-colors last:border-0 hover:bg-white/[0.03]",
                  )}
                >
                  <span className="flex items-center gap-2 overflow-hidden">
                    <CoinIcon symbol={s.symbol} size={18} />
                    <span className="truncate font-medium">
                      {baseTicker(s.symbol).toUpperCase()}
                    </span>
                  </span>
                  <span>
                    <Pill variant={bull ? "up" : "down"} dot>
                      {bull ? "Long" : "Short"}
                    </Pill>
                  </span>
                  <span className="tnum text-right">{n(s.entry)}</span>
                  <span className="tnum text-right font-medium text-fg">{s.rr.toFixed(1)}</span>
                  <span>
                    <SetupStateBadge state={s.state} />
                  </span>
                  <span className="tnum text-right text-fg-subtle">{age(s.createdAt)}</span>
                  <CaretRight className="size-3.5 justify-self-end text-fg-subtle" />
                </Link>
              );
            })}
          </div>
        </>
      )}
    </Panel>
  );
}
