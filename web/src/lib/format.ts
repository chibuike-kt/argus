// Shared display formatting. ARGUS is point-in-time by design (ADR-009), so
// "what time is this as of" shows up in several places -- the as-of clock,
// data-quality, evidence timestamps -- and should read identically everywhere.
export function formatUTC(iso: string): string {
  const d = new Date(iso);
  const date = d.toLocaleString("en-GB", { month: "short", day: "2-digit", timeZone: "UTC" });
  const time = d.toLocaleString("en-GB", { hour: "2-digit", minute: "2-digit", hour12: false, timeZone: "UTC" });
  return `${date}, ${time} UTC`;
}
