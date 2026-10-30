import { Clock } from "lucide-react";
import { asOf } from "@/mocks/fixtures";

function fmtUTC(iso: string) {
  const d = new Date(iso);
  const date = d.toLocaleString("en-GB", {
    month: "short",
    day: "2-digit",
    timeZone: "UTC",
  });
  const time = d.toLocaleString("en-GB", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    timeZone: "UTC",
  });
  return `${date}, ${time} UTC`;
}

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
        <span className="tnum mt-0.5 text-xs font-medium">{fmtUTC(asOf)}</span>
      </span>
    </button>
  );
}
