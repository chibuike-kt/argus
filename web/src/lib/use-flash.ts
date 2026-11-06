import { useEffect, useRef, useState } from "react";

const FLASH_DURATION_MS = 450;

// A brief up/down tint on a number right as it changes, fading back to
// nothing over --dur/--ease-out (see the .flash-up/.flash-down utilities)
// -- the "tick" feel real price displays have, instead of a number just
// snapping to a new value with no transition at all. Returns null outside
// the flash window (the caller's own text colour, e.g. text-up/text-down,
// is what should stay applied at rest).
export function useFlash(value: number): "up" | "down" | null {
  const [flash, setFlash] = useState<"up" | "down" | null>(null);
  const prev = useRef(value);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (value === prev.current) return;
    const direction = value > prev.current ? "up" : "down";
    prev.current = value;
    setFlash(direction);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setFlash(null), FLASH_DURATION_MS);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [value]);

  return flash;
}
