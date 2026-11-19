import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { EM_DASH } from "@/lib/decimal-display";
import type { Motion } from "@/lib/live-stats";

const FLASH_MS = 450;

// useMotionFlash returns the direction of a change for FLASH_MS after
// motion.seq increments (a figure just changed), else null. The first
// value seen never flashes (nothing changed yet, it was only loaded).
export function useMotionFlash(motion: Motion | undefined): "up" | "down" | null {
  const [flash, setFlash] = useState<"up" | "down" | null>(null);
  const seen = useRef<number | null>(null);
  const seq = motion?.seq ?? 0;
  const dir = motion?.dir ?? null;
  useEffect(() => {
    if (seen.current === null) {
      seen.current = seq;
      return;
    }
    if (seq === seen.current) return;
    seen.current = seq;
    if (!dir) return;
    setFlash(dir);
    const t = setTimeout(() => setFlash(null), FLASH_MS);
    return () => clearTimeout(t);
  }, [seq, dir]);
  return flash;
}

// LiveFigure renders one live figure: the formatted value, or an em dash
// whose hover (title) and accessible name give the reason it is absent.
//   - tone "motion": coloured by the direction of the value's last change
//     (prices: green after a rise, red after a fall), neutral before any.
//   - tone "sign": coloured by the value's own sign (24h change figures).
//   - tone "none": not coloured.
// Either way the cell flashes on every change. Tabular figures and a
// fixed font keep the layout still while digits change.
export function LiveFigure({
  text,
  reason,
  motion,
  tone = "motion",
  signTone,
  className,
  testId,
  suffix,
}: {
  // The formatted value; null shows the em dash with reason.
  text: string | null;
  reason: string | null;
  motion?: Motion;
  tone?: "motion" | "sign" | "none";
  // For tone "sign": the tone class to use (from toneClass).
  signTone?: string;
  className?: string;
  testId?: string;
  suffix?: ReactNode;
}) {
  const flash = useMotionFlash(motion);
  if (text === null) {
    const why = reason ?? "Not available";
    return (
      <span className={cn("tnum cursor-help text-fg-subtle", className)} data-testid={testId} title={why} aria-label={`Not available: ${why}`}>
        {EM_DASH}
      </span>
    );
  }
  const toneCls =
    tone === "motion"
      ? motion?.dir === "up"
        ? "text-up"
        : motion?.dir === "down"
          ? "text-down"
          : undefined
      : tone === "sign"
        ? signTone
        : undefined;
  return (
    <span
      className={cn("tnum transition-base rounded px-0.5", toneCls, flash === "up" && "flash-up", flash === "down" && "flash-down", className)}
      data-testid={testId}
      data-dir={motion?.dir ?? undefined}
    >
      {text}
      {suffix}
    </span>
  );
}
