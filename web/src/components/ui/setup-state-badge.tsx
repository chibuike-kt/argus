import type { SetupState } from "@/types/domain";
import { cn } from "@/lib/cn";

// "Actionable" is the display label for the backend's PlanActionable status
// (section 14 of the strategy spec): a live plan, not invalidated, price has
// not yet reached entry. The underlying SetupState value stays "valid" --
// it also covers "forming," a setup-lifecycle concept (ADR-010) the backend
// plan-status enum does not have, so the two vocabularies are not a strict
// 1:1 rename, only this one shared state.
const META: Record<SetupState, { label: string; cls: string }> = {
  valid: { label: "Actionable", cls: "bg-up/15 text-up" },
  forming: { label: "Forming", cls: "bg-prov-r/15 text-prov-r" },
  triggered: { label: "Triggered", cls: "bg-accent/20 text-accent" },
  invalidated: { label: "Invalidated", cls: "bg-down/15 text-down" },
};

export function SetupStateBadge({ state, className }: { state: SetupState; className?: string }) {
  const meta = META[state];
  return (
    <span className={cn("inline-flex rounded-full px-2 py-0.5 text-[11px] font-medium", meta.cls, className)}>
      {meta.label}
    </span>
  );
}
