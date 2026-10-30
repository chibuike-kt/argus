import { useState } from "react";
import { Eye, EyeOff, Bell, BellOff, Send, Radio, AlertTriangle, CircleOff } from "lucide-react";
import { ProvenanceChip } from "@/components/ui/provenance-chip";
import { Button } from "@/components/ui/button";
import { setup, annotations, dataQuality } from "@/mocks/fixtures";
import { formatUTC } from "@/lib/format";
import { cn } from "@/lib/cn";
import { SetupPanel } from "./setup-panel";

const choch = annotations.events.find((e) => e.label === "CHOCH");

function fmt(p: number) {
  return p.toLocaleString("en-US", { maximumFractionDigits: 1 });
}

// Bias and structure state, one line: what direction ARGUS currently reads
// the market as, and the event that established it. Source-confirmed [S]
// because it reports the engine's own BOS/CHOCH classification, not a guess.
function BiasLine() {
  const bull = setup.direction === "bullish";
  return (
    <div className="flex items-start gap-2.5">
      <ProvenanceChip p="S" className="mt-0.5" />
      <p className="text-[13px] leading-snug">
        <span className={cn("font-semibold", bull ? "text-up" : "text-down")}>
          {bull ? "Bullish" : "Bearish"}
        </span>
        {choch && (
          <span className="text-fg-muted">
            , {choch.label} confirmed at {fmt(choch.price)}
          </span>
        )}
      </p>
    </div>
  );
}

const QUALITY_META = {
  live: { icon: Radio, cls: "text-up", label: "Live" },
  degraded: { icon: AlertTriangle, cls: "text-prov-r", label: "Degraded" },
  stale: { icon: CircleOff, cls: "text-down", label: "Stale" },
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
        <button onClick={() => setDismissed(false)} className="focus-ring font-medium text-accent hover:underline">
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
        {watching ? <Eye className="size-4" /> : <EyeOff className="size-4" />}
        {watching ? "Watching" : "Watch"}
      </Button>
      <Button variant="glass" size="sm" className="gap-1.5" onClick={() => setDismissed(true)}>
        <BellOff className="size-4" />
        Dismiss
      </Button>
      <Button variant="outline" size="sm" className="gap-1.5">
        <Bell className="size-4" />
        Alert me
      </Button>
      <Button variant="outline" size="sm" className="gap-1.5">
        <Send className="size-4" />
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
