import { Clock } from "lucide-react";
import { asOf } from "@/mocks/fixtures";
import { formatUTC } from "@/lib/format";

// Point-in-time indicator. ARGUS answers "what was known as of this moment",
// so the clock is a first-class control. The scrubber lives in the analysis
// view; here it reads the active as-of boundary.
export function AsOfControl() {
  return (
    <button className="glass focus-ring inline-flex h-10 items-center gap-2 rounded-full pl-3 pr-4 text-left hover:border-line-strong">
      <Clock className="size-4 text-accent" />
      <span className="flex flex-col leading-none">
        <span className="text-[9px] font-medium uppercase tracking-wider text-fg-subtle">
          As of
        </span>
        <span className="tnum mt-0.5 text-xs font-medium">{formatUTC(asOf)}</span>
      </span>
    </button>
  );
}
