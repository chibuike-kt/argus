// safeNextPath validates a `?next=` redirect target: only a same-origin
// relative path is ever followed. "//evil.com" is rejected -- a browser
// treats a leading "//" as scheme-relative, i.e. "go to this other
// host", the classic open-redirect vector a `next` query param invites
// (web auth UI brief, Part A2). Anything else that isn't a clean
// in-app path ("http://...", "javascript:...", an empty string) is
// rejected too, falling back to fallback.
export function safeNextPath(next: string | null, fallback = "/setups"): string {
  if (!next) return fallback;
  if (!next.startsWith("/") || next.startsWith("//")) return fallback;
  return next;
}
