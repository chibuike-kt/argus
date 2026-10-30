import type { Provenance } from "@/types/domain";
import { cn } from "@/lib/cn";
import { Tooltip } from "./tooltip";

const META: Record<Provenance, { cls: string; title: string }> = {
  S: {
    cls: "text-prov-s border-prov-s/30 bg-prov-s/10",
    title: "Source-confirmed from the trader's material",
  },
  A: {
    cls: "text-prov-a border-prov-a/30 bg-prov-a/10",
    title: "Architecturally necessary",
  },
  R: {
    cls: "text-prov-r border-prov-r/30 bg-prov-r/10",
    title: "Recommendation, revisit after backtesting",
  },
  U: {
    cls: "text-prov-u border-prov-u/30 bg-prov-u/10",
    title: "Unresolved, open item",
  },
};

export function ProvenanceChip({ p, className }: { p: Provenance; className?: string }) {
  const meta = META[p];
  return (
    <Tooltip content={meta.title}>
      <span
        className={cn(
          "tnum inline-flex h-5 min-w-5 cursor-default items-center justify-center rounded-md border px-1 text-[11px] font-semibold leading-none",
          meta.cls,
          className,
        )}
      >
        {p}
      </span>
    </Tooltip>
  );
}
