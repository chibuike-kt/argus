import { cn } from "@/lib/cn";

export function Stat({
  label,
  value,
  delta,
  tone = "default",
  size = "md",
  className,
}: {
  label: string;
  value: string;
  delta?: string;
  tone?: "default" | "accent" | "up" | "down";
  size?: "sm" | "md" | "lg";
  className?: string;
}) {
  const valueTone = {
    default: "text-fg",
    accent: "text-accent",
    up: "text-up",
    down: "text-down",
  }[tone];

  const valueSize = {
    sm: "text-base",
    md: "text-xl",
    lg: "text-3xl",
  }[size];

  return (
    <div className={cn("flex flex-col gap-0.5", className)}>
      <span className="text-[11px] font-medium uppercase tracking-wide text-fg-subtle">
        {label}
      </span>
      <span className={cn("tnum font-semibold leading-tight", valueSize, valueTone)}>
        {value}
      </span>
      {delta && (
        <span
          className={cn(
            "tnum text-xs font-medium",
            delta.trim().startsWith("-") ? "text-down" : "text-up",
          )}
        >
          {delta}
        </span>
      )}
    </div>
  );
}
