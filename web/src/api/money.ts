// Exact scaled-integer <-> decimal-string conversion, matching
// internal/quant/amount.go's own formatScaled byte for byte (string
// digit-shifting, never a division). Used only for plan_snapshot's
// entry/stop/targets[].level, the one money representation this API
// sends as a bare scaled integer instead of an already-formatted decimal
// string (see api/types.ts's own note on this). Candle/instrument fields
// never need this -- they arrive pre-formatted and are rendered as-is.

// formatScaledInt renders a non-negative scaled integer as an exact
// decimal string at scale, with no rounding -- the same algorithm as
// quant.Price.String(scale)/formatScaled in Go. raw must be a safe
// integer (Number.isSafeInteger); every real price/qty value here is one,
// since it came from a Go int64 small enough that JSON.parse kept it
// exact.
export function formatScaledInt(raw: number, scale: number): string {
  if (!Number.isFinite(raw) || !Number.isSafeInteger(raw)) {
    throw new Error(`formatScaledInt: raw value ${raw} is not a safe integer`);
  }
  if (raw < 0) {
    return `-${formatScaledInt(-raw, scale)}`;
  }
  let digits = String(raw);
  if (scale <= 0) return digits;
  while (digits.length <= scale) digits = `0${digits}`;
  const intPart = digits.slice(0, digits.length - scale);
  const fracPart = digits.slice(digits.length - scale);
  return `${intPart}.${fracPart}`;
}

// scaledIntToNumber converts a scaled integer to a JS number for display
// formatting or chart rendering ONLY (rule 2 of the wiring brief) -- it
// goes through the exact string form above and back, never a raw
// division by 10**scale, so it never depends on whether that power of
// ten happens to be exactly representable as a double.
export function scaledIntToNumber(raw: number, scale: number): number {
  return Number(formatScaledInt(raw, scale));
}

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
