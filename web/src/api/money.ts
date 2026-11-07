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
