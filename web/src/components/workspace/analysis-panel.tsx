import { useState } from "react";
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
import { SampleDataBadge } from "@/components/ui/sample-data-badge";
import { instrument as fixtureInstrument, dataQuality } from "@/mocks/fixtures";
import { formatUTC } from "@/lib/format";
import { parseRatString } from "@/api/money";
import { cn } from "@/lib/cn";
import { useAnalysisWorkspace } from "./analysis-workspace";
import { SetupPanel } from "./setup-panel";
import { BiasGauge } from "./bias-gauge";

// %-over-timeframe tiles, tinted by direction. No endpoint carries this
// (no per-instrument performance series exists in the API today), so
// this stays the fixture's own numbers with a visible marker -- a
// self-contained tile cluster, not drawn over real chart data, so a
// badge is enough to keep it honest (contrast the chart's own stricter
// choice: dropping fixture structure labels entirely rather than
// overlaying them on real candles, see chart-surface.tsx).
function PerformanceTiles() {
  const perf = fixtureInstrument.performance;
  if (!perf) return null;
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between">
        <span className="text-[11px] font-medium uppercase tracking-wide text-fg-subtle">
          Performance
        </span>
        <SampleDataBadge />
      </div>
      <div className="grid grid-cols-3 gap-2">
        {Object.entries(perf).map(([tf, pct]) => {
          const up = pct >= 0;
          return (
            <div key={tf} className={cn("rounded-lg px-2.5 py-2", up ? "bg-up/10" : "bg-down/10")}>
              <div className="text-[10px] font-medium uppercase tracking-wide text-fg-subtle">{tf}</div>
              <div className={cn("tnum mt-0.5 text-[13px] font-semibold", up ? "text-up" : "text-down")}>
                {up ? "+" : ""}
                {pct.toFixed(2)}%
              </div>
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

// Data quality (ADR-006: freshness/finality) has no live signal yet --
// no endpoint reports feed staleness today -- so this now always reads
// as a marked sample rather than silently claiming "Live".
function DataQualityRow() {
  return (
    <div className="flex items-center gap-2 rounded-lg bg-white/[0.03] px-3 py-2">
      <Broadcast className="size-4 shrink-0 text-fg-subtle" />
      <p className="flex-1 text-[12px] leading-snug text-fg-muted">
        Finalized through{" "}
        <span className="tnum text-fg">{formatUTC(dataQuality.finalizedThrough)}</span>
      </p>
      <SampleDataBadge />
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
