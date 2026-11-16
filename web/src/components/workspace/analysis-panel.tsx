import { useEffect, useState } from "react";
import {
  Eye,
  EyeSlash,
  Bell,
  BellSlash,
  PaperPlaneTilt,
  Broadcast,
} from "@phosphor-icons/react";
import { ProvenanceChip } from "@/components/ui/provenance-chip";
import { Button } from "@/components/ui/button";
import { formatUTC } from "@/lib/format";
import { EM_DASH, addOneMinuteISO, direction, formatAge, formatPercent, toneClass } from "@/lib/decimal-display";
import { PERFORMANCE_PERIODS } from "@/api/types";
import { parseRatString } from "@/api/money";
import { cn } from "@/lib/cn";
import { useAnalysisWorkspace } from "./analysis-workspace";
import { SetupPanel } from "./setup-panel";
import { BiasGauge } from "./bias-gauge";

// %-over-timeframe tiles (real-panel-data brief rule 4), every value
// from GET /instruments/stats: last close against the closed 1-minute
// candle exactly that far back (1D = 24h, 1W = 7d, 1M = 30d, 3M = 90d,
// 6M = 180d, 1Y = 365d). A period whose exact reference candle does not
// exist shows an em dash on a neutral tile -- with a few days of
// history only 1D can show, which is correct, not a bug.
function PerformanceTiles() {
  const { instrument, stats } = useAnalysisWorkspace();
  if (!instrument) return null;
  return (
    <div data-testid="performance-tiles">
      <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-fg-subtle">Performance</div>
      <div className="grid grid-cols-3 gap-2">
        {PERFORMANCE_PERIODS.map((tf) => {
          const pct = stats?.performance[tf] ?? null;
          const d = direction(pct);
          return (
            <div
              key={tf}
              data-testid={`perf-${tf}`}
              className={cn(
                "rounded-lg px-2.5 py-2",
                d === "up" ? "bg-up/10" : d === "down" ? "bg-down/10" : "bg-white/[0.03]",
              )}
            >
              <div className="text-[10px] font-medium uppercase tracking-wide text-fg-subtle">{tf}</div>
              <div className={cn("tnum mt-0.5 text-[13px] font-semibold", toneClass(pct))}>{formatPercent(pct)}</div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// Bias and structure state: ARGUS's own read on current direction, as a
// semicircle gauge. direction and the R:R magnitude both come from the
// real selected setup now; the CHOCH caption the fixture version showed
// underneath is dropped -- that came from the structure-events
// annotations, which have no per-setup API source (see chart-surface.tsx
// and the brief's own report). This is not a market-wide sentiment
// read (no such endpoint exists): the caption under the gauge says so
// explicitly, naming the selected setup's own direction and R:R as what
// it is derived from (web-wiring-followup brief, Part 1's own
// instruction not to leave an unlabelled gauge implying otherwise).
function BiasLine() {
  const { setup } = useAnalysisWorkspace();
  if (!setup) return null;
  const direction = setup.direction === "bearish" ? "bearish" : "bullish";
  const targets = setup.plan_snapshot.targets;
  const rr = targets.length > 0 ? parseRatString(targets[targets.length - 1].rr) : 1;
  return (
    <div className="flex items-start gap-2.5">
      <ProvenanceChip p="S" className="mt-0.5" />
      <div className="flex-1">
        <BiasGauge direction={direction} rr={rr} />
        <p className="-mt-1 text-center text-[11px] text-fg-subtle">
          Derived from the selected setup's direction and R:R, not market-wide sentiment
        </p>
      </div>
    </div>
  );
}

// useNow re-renders once a second so an age readout keeps counting.
function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  return now;
}

// Data quality (real-panel-data brief rule 7): "finalized through" is
// the end of the latest closed 1-minute candle (its open time from GET
// /instruments/stats plus one minute) in UTC, with that candle's age as
// a plain number; provider name and connection state come from the live
// socket. Deliberately no freshness verdict (live/stale): those
// thresholds are open in ADR-006 and appendix C, so nothing here judges
// the age.
//
// The open time comes from the stats endpoint, not the candles
// endpoint's own last_closed_open_time: that field is the chart's
// per-interval join point (a 1h chart's is the start of its latest
// complete hour), so "+ one minute" would be wrong at any interval
// above 1m. The stats value is always the latest closed 1-minute
// candle's.
function DataQualityRow() {
  const { stats, status, connectionState } = useAnalysisWorkspace();
  const now = useNow();
  const openTime = stats?.last_closed_open_time ?? null;
  const through = openTime ? addOneMinuteISO(openTime) : null;
  return (
    <div className="flex items-start gap-2 rounded-lg bg-white/[0.03] px-3 py-2" data-testid="data-quality">
      <Broadcast className="mt-0.5 size-4 shrink-0 text-fg-subtle" />
      <div className="flex-1 text-[12px] leading-snug text-fg-muted">
        <p>
          Finalized through{" "}
          <span className="tnum text-fg" data-testid="finalized-through">
            {through ? formatUTC(through) : EM_DASH}
          </span>
          {through && (
            <span className="tnum text-fg-subtle" data-testid="finalized-age">
              {" "}
              · {formatAge(now - new Date(through).getTime())}
            </span>
          )}
        </p>
        <p className="text-fg-subtle">
          <span data-testid="feed-provider">{status?.provider ?? EM_DASH}</span> · socket{" "}
          <span data-testid="feed-connection">{connectionState}</span>
        </p>
      </div>
    </div>
  );
}

// Review controls: acting on ARGUS's judgment, not drawing. Stubs for now
// (ADR-001: no real execution) -- Watch/Dismiss hold a little local state so
// they feel real; Alert/Send to paper are presentational only.
function ReviewControls() {
  const [watching, setWatching] = useState(false);
  const [dismissed, setDismissed] = useState(false);

  if (dismissed) {
    return (
      <div className="flex items-center justify-between rounded-lg border border-line px-3 py-2 text-[12px] text-fg-subtle">
        Dismissed
        <button
          onClick={() => setDismissed(false)}
          className="focus-ring font-medium text-fg hover:underline"
        >
          Undo
        </button>
      </div>
    );
  }

  return (
    <div className="grid grid-cols-2 gap-2">
      <Button
        variant={watching ? "primary" : "glass"}
        size="sm"
        className="gap-1.5"
        onClick={() => setWatching((w) => !w)}
      >
        {watching ? <Eye className="size-4" weight="fill" /> : <EyeSlash className="size-4" />}
        {watching ? "Watching" : "Watch"}
      </Button>
      <Button variant="glass" size="sm" className="gap-1.5" onClick={() => setDismissed(true)}>
        <BellSlash className="size-4" />
        Dismiss
      </Button>
      <Button variant="outline" size="sm" className="gap-1.5">
        <Bell className="size-4" />
        Alert me
      </Button>
      <Button variant="outline" size="sm" className="gap-1.5">
        <PaperPlaneTilt className="size-4" />
        Send to paper
      </Button>
    </div>
  );
}

// The analysis panel: ARGUS's read on the current instrument/setup, top to
// bottom bias -> plan + evidence (SetupPanel) -> data quality -> review
// controls.
export function AnalysisPanel() {
  return (
    <div className="space-y-4">
      <BiasLine />
      <PerformanceTiles />
      <div className="border-t border-line pt-4">
        <SetupPanel />
      </div>
      <div className="border-t border-line pt-4">
        <DataQualityRow />
      </div>
      <ReviewControls />
    </div>
  );
}
