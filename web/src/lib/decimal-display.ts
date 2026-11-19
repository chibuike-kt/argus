// Display formatting for the exact decimal strings the API sends
// (prices, volumes, percentages -- real-panel-data brief rule 10). Pure
// string manipulation: nothing here converts a price or volume to a JS
// number, so formatting can never round or drift what the server
// computed. Every function maps null/undefined to EM_DASH, the one
// "absent" marker (never a made-up "0").

export const EM_DASH = "—";

const DECIMAL = /^(-?)(\d+)(?:\.(\d+))?$/;

// groupThousands inserts "," every three digits of an integer string.
function groupThousands(digits: string): string {
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

function isZero(intPart: string, frac: string | undefined): boolean {
  return /^0+$/.test(intPart) && (frac === undefined || /^0+$/.test(frac));
}

// formatDecimal renders an exact decimal string with thousands
// separators, keeping every fractional digit the server sent. With
// signed, a positive value gets a leading "+" (a negative one already
// has "-"; zero gets neither). A string that is not a plain decimal is
// shown as received rather than guessed at.
export function formatDecimal(value: string | null | undefined, opts?: { signed?: boolean }): string {
  if (value === null || value === undefined) return EM_DASH;
  const m = DECIMAL.exec(value);
  if (!m) return value;
  const [, minus, intPart, frac] = m;
  const zero = isZero(intPart, frac);
  const sign = zero ? "" : minus ? "-" : opts?.signed ? "+" : "";
  return sign + groupThousands(intPart) + (frac !== undefined ? `.${frac}` : "");
}

// formatPercent is formatDecimal plus a "%" suffix, always signed.
export function formatPercent(value: string | null | undefined): string {
  if (value === null || value === undefined) return EM_DASH;
  return `${formatDecimal(value, { signed: true })}%`;
}

// direction reads the sign of a decimal string without converting it
// to a number: "up" for a positive value, "down" for a negative one,
// "flat" for zero, and "none" for absent. Used only to pick a colour.
export function direction(value: string | null | undefined): "up" | "down" | "flat" | "none" {
  if (value === null || value === undefined) return "none";
  const m = DECIMAL.exec(value);
  if (!m) return "none";
  const [, minus, intPart, frac] = m;
  if (isZero(intPart, frac)) return "flat";
  return minus ? "down" : "up";
}

// toneClass colours a change by the sign of its decimal string (no
// arithmetic): up/down tokens, neutral when flat or absent.
export function toneClass(value: string | null | undefined): string {
  const d = direction(value);
  return d === "up" ? "text-up" : d === "down" ? "text-down" : "text-fg-subtle";
}

// formatAge renders an elapsed duration as a plain number with a unit
// ("12s ago", "5m ago", "3h ago", "41d ago"). It states the age only;
// it never judges it (no live/stale verdict -- the freshness thresholds
// are open in ADR-006 and appendix C). A negative age (client clock
// behind the server) shows as "0s ago" rather than a time in the future.
export function formatAge(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

// addOneMinuteISO returns the end of the 1-minute candle that opens at
// openTimeISO -- "data finalized through" (rule 7). Time arithmetic, not
// price arithmetic.
export function addOneMinuteISO(openTimeISO: string): string {
  return new Date(new Date(openTimeISO).getTime() + 60_000).toISOString();
}

// compareDecimal compares two exact decimal strings without converting
// either to a number (so it can never round): -1 when a < b, 0 when
// equal, 1 when a > b. null when either is not a plain decimal string.
// Used only to pick the direction of a change (a colour and a flash),
// never to produce a displayed figure.
export function compareDecimal(a: string, b: string): -1 | 0 | 1 | null {
  const ma = DECIMAL.exec(a);
  const mb = DECIMAL.exec(b);
  if (!ma || !mb) return null;
  const negA = ma[1] === "-" && !isZero(ma[2], ma[3]);
  const negB = mb[1] === "-" && !isZero(mb[2], mb[3]);
  if (negA !== negB) return negA ? -1 : 1;
  const mag = compareMagnitude(ma[2], ma[3] ?? "", mb[2], mb[3] ?? "");
  return (negA ? -mag : mag) as -1 | 0 | 1;
}

function compareMagnitude(intA: string, fracA: string, intB: string, fracB: string): -1 | 0 | 1 {
  const ia = intA.replace(/^0+(?=\d)/, "");
  const ib = intB.replace(/^0+(?=\d)/, "");
  if (ia.length !== ib.length) return ia.length < ib.length ? -1 : 1;
  if (ia !== ib) return ia < ib ? -1 : 1;
  const len = Math.max(fracA.length, fracB.length);
  const fa = fracA.padEnd(len, "0");
  const fb = fracB.padEnd(len, "0");
  if (fa === fb) return 0;
  return fa < fb ? -1 : 1;
}

// changeDirection is the direction of a move from prev to next: "up",
// "down", or null when either is absent or they are equal.
export function changeDirection(prev: string | null | undefined, next: string | null | undefined): "up" | "down" | null {
  if (prev === null || prev === undefined || next === null || next === undefined) return null;
  const c = compareDecimal(next, prev);
  if (c === 1) return "up";
  if (c === -1) return "down";
  return null;
}
