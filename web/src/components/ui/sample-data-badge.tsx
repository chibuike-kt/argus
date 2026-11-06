import { FlaskIcon } from "@phosphor-icons/react";
import { cn } from "@/lib/cn";

// A small, muted marker for any panel (or part of a panel) still backed
// by @/mocks/fixtures because no real endpoint exists for it yet (the
// web-api-wiring brief's own rule 1: real and fake data must never look
// the same). Deliberately NOT one of Pill's semantic variants (up/down/
// accent/active) -- those all mean something about market or ARGUS state;
// this means "this number is not real," a different kind of fact, so it
// gets its own muted, dashed-border token instead of borrowing one of
// theirs.
export function SampleDataBadge({ className, label = "Sample data" }: { className?: string; label?: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border border-dashed border-line px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-fg-subtle",
        className,
      )}
      title="Not from the live API -- shown for layout only"
    >
      <FlaskIcon className="size-3" />
      {label}
    </span>
  );
}
