import { useState } from "react";
import {
  Eye,
  EyeSlash,
  Bell,
  BellSlash,
  PaperPlaneTilt,
  Broadcast,
  Warning,
  Prohibit,
} from "@phosphor-icons/react";
import { ProvenanceChip } from "@/components/ui/provenance-chip";
import { Button } from "@/components/ui/button";
import { setup, annotations, dataQuality, instrument } from "@/mocks/fixtures";
import { formatUTC } from "@/lib/format";
import { cn } from "@/lib/cn";
import { SetupPanel } from "./setup-panel";
import { BiasGauge } from "./bias-gauge";

const choch = annotations.events.find((e) => e.label === "CHOCH");

function fmt(p: number) {
  return p.toLocaleString("en-US", { maximumFractionDigits: 1 });
}

// %-over-timeframe tiles, tinted by direction -- the reference dashboard's
// performance-at-a-glance pattern. Market data, so it speaks the market's
// own up/down colours, not the ARGUS accent.
function PerformanceTiles() {
  const perf = instrument.performance;
  if (!perf) return null;
  return (
    <div className="grid grid-cols-3 gap-2">
      {Object.entries(perf).map(([tf, pct]) => {
        const up = pct >= 0;
        return (
          <div key={tf} className={cn("rounded-lg px-2.5 py-2", up ? "bg-up/10" : "bg-down/10")}>
            <div className="text-[10px] font-medium uppercase tracking-wide text-fg-subtle">
              {tf}
            </div>
            <div
              className={cn("tnum mt-0.5 text-[13px] font-semibold", up ? "text-up" : "text-down")}
            >
              {up ? "+" : ""}
              {pct.toFixed(2)}%
            </div>
          </div>
        );
      })}
    </div>
  );
}

// Bias and structure state: ARGUS's own read on current direction, as a
// semicircle gauge (the reference dashboard's "Strong Sell -> Strong Buy"
// pattern, recast bearish -> bullish) rather than a plain line -- the
// gauge speaks the intelligence accent, since this is ARGUS's structural
// judgment, not a raw price tick. Source-confirmed [S] because it reports
// the engine's own BOS/CHOCH classification, not a guess.
function BiasLine() {
  return (
    <div className="flex items-start gap-2.5">
      <ProvenanceChip p="S" className="mt-0.5" />
      <div className="flex-1">
        <BiasGauge direction={setup.direction} rr={setup.rr} />
        {choch && (
          <p className="-mt-1 text-center text-[12px] text-fg-muted">
            {choch.label} confirmed at {fmt(choch.price)}
          </p>
        )}
      </div>
    </div>
  );
}

const QUALITY_META = {
  live: { icon: Broadcast, cls: "text-up", label: "Live" },
  degraded: { icon: Warning, cls: "text-prov-r", label: "Degraded" },
  stale: { icon: Prohibit, cls: "text-down", label: "Stale" },
} as const;

// Data quality is first class, not a footnote: ARGUS only acts on trusted
// data (ADR-006), so whether this analysis is still current is right next
// to the plan that depends on it.
function DataQualityRow() {
  const meta = QUALITY_META[dataQuality.status];
  const Icon = meta.icon;
  return (
    <div className="flex items-center gap-2 rounded-lg bg-white/[0.03] px-3 py-2">
      <Icon className={cn("size-4 shrink-0", meta.cls)} />
      <p className="text-[12px] leading-snug text-fg-muted">
        <span className={cn("font-medium", meta.cls)}>{meta.label}</span>, finalized through{" "}
        <span className="tnum text-fg">{formatUTC(dataQuality.finalizedThrough)}</span>
        {dataQuality.note && <span className="text-fg-subtle"> — {dataQuality.note}</span>}
      </p>
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
// controls. This replaces the watchlist as the right panel's primary
// surface (ADR-008-adjacent reframe: the system's findings are the home
// surface, drawing is secondary).
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
