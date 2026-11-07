// A small, hand-written User-Agent reader for the Account page's own
// sessions list ("Chrome on Windows") -- no library, just the handful
// of substrings that actually distinguish the browsers/OSes this
// product's users realistically show up with. Not a general-purpose
// UA parser: an unrecognized string degrades to "Unknown browser"/
// "Unknown OS" rather than guessing.
export function parseUserAgent(ua: string | null): string {
  if (!ua) return "Unknown device";

  let browser = "Unknown browser";
  if (/Edg\//.test(ua)) browser = "Edge";
  else if (/OPR\//.test(ua)) browser = "Opera";
  else if (/Chrome\//.test(ua)) browser = "Chrome";
  else if (/Firefox\//.test(ua)) browser = "Firefox";
  else if (/Safari\//.test(ua)) browser = "Safari";

  let os = "Unknown OS";
  if (/Windows/.test(ua)) os = "Windows";
  else if (/Mac OS X/.test(ua)) os = "macOS";
  else if (/Android/.test(ua)) os = "Android";
  else if (/iPhone|iPad|iPod/.test(ua)) os = "iOS";
  else if (/Linux/.test(ua)) os = "Linux";

  return `${browser} on ${os}`;
}
