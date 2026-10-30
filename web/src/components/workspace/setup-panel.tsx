import { Pill } from "@/components/ui/pill";
import { Stat } from "@/components/ui/stat";
import { ProvenanceChip } from "@/components/ui/provenance-chip";
import { setup } from "@/mocks/fixtures";

const n = (v: number) => v.toLocaleString("en-US", { maximumFractionDigits: 1 });

export function SetupPanel() {
  const bull = setup.direction === "bullish";
  const risk = Math.abs(setup.entry - setup.stop);
  const reward = Math.abs(setup.targets[setup.targets.length - 1] - setup.entry);
  const rewardPct = (reward / (reward + risk)) * 100;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <span className="text-sm font-semibold">Proposed setup</span>
        <Pill variant={bull ? "up" : "down"} dot>
          {setup.direction}
        </Pill>
      </div>

      <div className="grid grid-cols-3 gap-2">
        <Stat label="Entry" value={n(setup.entry)} />
        <Stat label="Stop" value={n(setup.stop)} tone="down" />
        <Stat label="R:R" value={setup.rr.toFixed(1)} tone="accent" />
      </div>

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

      <div>
        <div className="text-[11px] font-medium uppercase tracking-wide text-fg-subtle">Targets</div>
        <div className="mt-1.5 flex flex-wrap gap-1.5">
          {setup.targets.map((t, i) => (
            <Pill key={t} variant="glass" size="md" className="gap-1.5">
              <span className="text-fg-subtle">TP{i + 1}</span>
              {n(t)}
            </Pill>
          ))}
        </div>
      </div>

      <Pill variant="outline">POI: order block</Pill>

      <div className="border-t border-line pt-3">
        <div className="mb-2 flex items-center justify-between">
          <span className="text-sm font-semibold">Evidence</span>
          <span className="text-xs text-fg-subtle">{setup.evidence.length} items</span>
        </div>
        <ul className="space-y-3">
          {setup.evidence.map((e) => (
            <li key={e.label} className="flex gap-2.5">
              <ProvenanceChip p={e.provenance} className="mt-0.5" />
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{e.label}</div>
                <div className="text-[12px] leading-snug text-fg-muted">{e.detail}</div>
              </div>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
