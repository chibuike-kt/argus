// parseRatString converts an exact rational ("3/2", from Go's
// big.Rat.RatString()) to a display number. This is the one place a
// division happens in this module, and it is display formatting (an R
// multiple shown to one decimal place), not price arithmetic -- the
// numerator/denominator themselves are untouched, exact integers from
// the API.
export function parseRatString(rr: string): number {
  const parts = rr.split("/");
  if (parts.length === 1) {
    const value = Number(parts[0]);
    if (!Number.isFinite(value)) throw new Error(`parseRatString: malformed ratio ${rr}`);
    return value;
  }
  const [num, den] = parts.map(Number);
  if (!Number.isFinite(num) || !Number.isFinite(den) || den === 0) {
    throw new Error(`parseRatString: malformed ratio ${rr}`);
  }
  return num / den;
}

// formatRR is display formatting for a parsed R:R value, not a change
// to the data itself (web-wiring-polish brief, item 3): a small ratio
// like 1/27 (~0.037) rounds to "0.0" at one decimal place, which reads
// as "no reward at all" rather than "small but real" -- two decimals
// below 0.1 keeps it legible ("0.04") without changing how larger,
// more common values like 2 or 1.5 are shown (one decimal, unchanged).
export function formatRR(rr: number): string {
  return (Math.abs(rr) < 0.1 ? rr.toFixed(2) : rr.toFixed(1)) + "R";
}
