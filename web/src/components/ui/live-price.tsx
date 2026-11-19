import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { cn } from "@/lib/cn";
import { EM_DASH, compareDecimal } from "@/lib/decimal-display";

// [U] display value from the owner's spec: how long the whole price holds
// the direction colour before the unchanged digits fade back.
export const LIVE_PRICE_HOLD_MS = 300;

// changedFrom is the index of the first changed character between two
// formatted prices, compared from the left: that character and every
// one after it count as changed. A length change (999.9 -> 1,000.0)
// changes the whole number (index 0). Equal strings: the length (no
// changed digit).
export function changedFrom(prev: string, next: string): number {
  if (prev.length !== next.length) return 0;
  for (let i = 0; i < next.length; i++) {
    if (prev[i] !== next[i]) return i;
  }
  return next.length;
}

// priceDirection is the direction of an exact decimal move, with no
// threshold: any rise is "up", any fall "down", equal is null. Exact
// string comparison (compareDecimal), never floats.
export function priceDirection(prev: string, next: string): "up" | "down" | null {
  const c = compareDecimal(next, prev);
  return c === 1 ? "up" : c === -1 ? "down" : null;
}

function prefersReducedMotion(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function"
    ? window.matchMedia("(prefers-reduced-motion: reduce)").matches
    : false;
}

interface Paint {
  dir: "up" | "down" | null;
  // Characters from this index on are the changed digits.
  from: number;
  // "all": step one, the whole price in the direction colour; "split":
  // step two, only the changed digits.
  phase: "all" | "split";
}

const REST: Paint = { dir: null, from: 0, phase: "split" };

// LivePrice is the one shared live price display (owner spec, Phase 3):
//  1. on a change, the whole price turns green (higher) or red (lower);
//  2. after LIVE_PRICE_HOLD_MS the unchanged digits fade back to the
//     normal colour and the changed digits stay coloured;
//  3. they stay coloured until the next change, which starts again at 1
//     (also when it arrives during 1 or 2);
// an equal price changes nothing; the first value (first load, a switch,
// the first value after a dash) is at rest in the normal colour.
// Direction compares the exact decimal strings in `value`; the changed
// digits compare the formatted `text`. prefers-reduced-motion skips the
// fade and goes straight to step 2.
//
// resetKey: change it (an instrument or timeframe switch) and the next
// value is a first value again, never compared with the old instrument.
export function LivePrice({
  value,
  text,
  reason,
  resetKey,
  className,
  testId,
}: {
  // The exact decimal string as sent; null shows a dash with reason.
  value: string | null;
  // value formatted for display (thousands separators).
  text: string | null;
  reason: string | null;
  resetKey?: string | number;
  className?: string;
  testId?: string;
}) {
  const [paint, setPaint] = useState<Paint>(REST);
  const prev = useRef<{ value: string; text: string } | null>(null);
  const key = useRef(resetKey);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useLayoutEffect(() => {
    const clear = () => {
      if (timer.current) clearTimeout(timer.current);
      timer.current = null;
    };
    if (key.current !== resetKey) {
      key.current = resetKey;
      prev.current = null;
    }
    if (value === null || text === null) {
      clear();
      prev.current = null;
      setPaint(REST);
      return;
    }
    const before = prev.current;
    prev.current = { value, text };
    if (before === null) {
      clear();
      setPaint(REST);
      return;
    }
    const dir = priceDirection(before.value, value);
    if (dir === null) return; // equal price: nothing changes
    clear();
    const from = changedFrom(before.text, text);
    if (prefersReducedMotion()) {
      setPaint({ dir, from, phase: "split" });
      return;
    }
    setPaint({ dir, from, phase: "all" });
    timer.current = setTimeout(() => {
      timer.current = null;
      setPaint({ dir, from, phase: "split" });
    }, LIVE_PRICE_HOLD_MS);
  }, [value, text, resetKey]);

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );

  if (value === null || text === null) {
    const why = reason ?? "Not available";
    return (
      <span className={cn("tnum cursor-help text-fg-subtle", className)} data-testid={testId} title={why} aria-label={`Not available: ${why}`}>
        {EM_DASH}
      </span>
    );
  }
  const dirCls = paint.dir === "up" ? "text-up" : paint.dir === "down" ? "text-down" : undefined;
  const from = paint.dir === null ? text.length : Math.min(paint.from, text.length);
  return (
    <span
      className={cn("tnum", className)}
      data-testid={testId}
      data-dir={paint.dir ?? undefined}
      data-phase={paint.dir ? paint.phase : undefined}
      aria-label={text}
    >
      <span
        data-part="unchanged"
        className={cn(
          // Only the fade back (step two) is animated; step one is instant.
          paint.phase === "split" && "transition-colors duration-200 motion-reduce:transition-none",
          paint.phase === "all" && dirCls,
        )}
      >
        {text.slice(0, from)}
      </span>
      <span data-part="changed" className={dirCls}>
        {text.slice(from)}
      </span>
    </span>
  );
}
