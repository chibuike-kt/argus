import { cn } from "@/lib/cn";

// Equal rounded segments, filled light-primary / unfilled light-fill.
// Width ~29px and the 6px gap match the brief's own numbers exactly;
// height is 6px, not the brief's approximate 4px -- measured directly
// off docs/design-refs/signup-email.png (a vertical pixel scan through
// a filled segment), so the screenshot wins here per the brief's own
// rule. The segment COUNT is ARGUS's own 5-step flow (brief, Part 3),
// not the 6 segments the reference screenshots show -- those are the
// reference product's own (different, longer) flow; "filled count
// equals the current step" is what this builds to.
export function StepProgress({ current, total }: { current: number; total: number }) {
  return (
    <div
      className="flex items-center gap-1.5"
      role="progressbar"
      aria-valuenow={current}
      aria-valuemin={1}
      aria-valuemax={total}
      aria-label={`Step ${current} of ${total}`}
    >
      {Array.from({ length: total }, (_, i) => (
        <span
          key={i}
          className={cn(
            "h-1.5 w-[29px] rounded-full transition-colors",
            i < current ? "bg-light-primary" : "bg-light-fill",
          )}
        />
      ))}
    </div>
  );
}
