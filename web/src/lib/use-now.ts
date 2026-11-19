import { useEffect, useState } from "react";

// useNow re-renders every intervalMs so an age computed against it ("No
// new ticks for 12s", "finalized ... 40s ago") keeps counting on screen.
export function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}
