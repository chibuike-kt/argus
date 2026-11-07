import { CircleNotch } from "@phosphor-icons/react";
import { Pill } from "@/components/ui/pill";
import { Stat } from "@/components/ui/stat";
import { ProvenanceChip } from "@/components/ui/provenance-chip";
import { SetupStateBadge } from "@/components/ui/setup-state-badge";
import { mapApiPoiType, mapApiState, parseRatString, toEvidence, toPlanOverlay } from "@/api/mappers";
import { useAnalysisWorkspace } from "./analysis-workspace";
import type { POIType } from "@/types/domain";

const n = (v: number) => v.toLocaleString("en-US", { maximumFractionDigits: 1 });
const POI_LABEL: Record<POIType, string> = { order_block: "Order block", qmr: "QMR" };

export function SetupPanel() {
  const { setup, instrument, setupLoading, setupError, events, eventsLoading } = useAnalysisWorkspace();

  if (setupLoading) {
    return (
      <div className="flex items-center justify-center gap-2 py-8 text-[13px] text-fg-subtle">
        <CircleNotch className="size-4 animate-spin" />
        Loading plan…
      </div>
    );
  }
  if (setupError) {
    return <div className="py-8 text-center text-[13px] text-down">Could not load setup: {setupError.message}</div>;
  }
  if (!setup || !instrument) {
    return <div className="py-8 text-center text-[13px] text-fg-subtle">No setups yet.</div>;
  }

  const overlay = toPlanOverlay(setup);
  const bull = setup.direction === "bullish";
  const state = mapApiState(setup.state);
  const risk = Math.abs(overlay.entry - overlay.stop);
  const lastTargetIdx = overlay.targets.length - 1;
  const reward = lastTargetIdx >= 0 ? Math.abs(overlay.targets[lastTargetIdx] - overlay.entry) : 0;
  const rewardPct = risk + reward > 0 ? (reward / (reward + risk)) * 100 : 0;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <span className="text-sm font-semibold">The active plan</span>
        <div className="flex items-center gap-1.5">
          <Pill variant={bull ? "up" : "down"} dot>
            {setup.direction}
          </Pill>
          <SetupStateBadge state={state} />
        </div>
      </div>

      <div className="grid grid-cols-3 gap-2">
        <Stat label="Entry" value={n(overlay.entry)} />
        <Stat label="Stop" value={n(overlay.stop)} tone="down" />
        <Stat
          label="R:R"
          value={
            setup.plan_snapshot.targets.length > 0
              ? parseRatString(setup.plan_snapshot.targets[setup.plan_snapshot.targets.length - 1].rr).toFixed(1)
              : "—"
          }
        />
      </div>

      {risk + reward > 0 && (
        <div>
          <div className="flex h-2 overflow-hidden rounded-full">
            <div className="bg-down/70" style={{ width: `${100 - rewardPct}%` }} />
            <div className="bg-up/80" style={{ width: `${rewardPct}%` }} />
          </div>
          <div className="mt-1 flex justify-between text-[10px] text-fg-subtle">
            <span>Risk {n(risk)}</span>
            <span>Reward {n(reward)}</span>
          </div>
        </div>
      )}

      <div>
        <div className="text-[11px] font-medium uppercase tracking-wide text-fg-subtle">Targets</div>
        <div className="mt-1.5 flex flex-wrap gap-1.5">
          {setup.plan_snapshot.targets.map((t, i) => (
            <Pill key={`${t.level}-${i}`} variant="glass" size="md" className="gap-1.5">
              <span className="text-fg-subtle">TP{i + 1}</span>
              {n(overlay.targets[i])}
              <span className="text-fg">{parseRatString(t.rr).toFixed(1)}R</span>
            </Pill>
          ))}
          {setup.plan_snapshot.targets.length === 0 && (
            <span className="text-[12px] text-fg-subtle">No structural target beyond entry.</span>
          )}
        </div>
      </div>

      <Pill variant="outline">POI: {POI_LABEL[mapApiPoiType(setup.poi_type)]}</Pill>

      <div className="border-t border-line pt-3">
        <div className="mb-2 flex items-center justify-between">
          <span className="text-sm font-semibold">Evidence</span>
          <span className="text-xs text-fg-subtle">
            {eventsLoading ? "Loading…" : `${events.length} item${events.length === 1 ? "" : "s"}`}
          </span>
        </div>
        {!eventsLoading && events.length === 0 && (
          <p className="text-[12px] text-fg-subtle">No events recorded for this setup yet.</p>
        )}
        <ul className="space-y-3">
          {events.map((e) => {
            const item = toEvidence(e);
            return (
              <li key={e.id} className="flex gap-2.5">
                <ProvenanceChip p={item.provenance} className="mt-0.5" />
                <div className="min-w-0">
                  <div className="text-[13px] font-medium">{item.label}</div>
                  <div className="text-[12px] leading-snug text-fg-muted">{item.detail}</div>
                </div>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
