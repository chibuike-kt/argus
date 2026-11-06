import type { ApiError } from "./types";

// Base URL for the real API. Defaults to a relative /api/v1 so the Vite
// dev proxy (vite.config.ts) and any same-origin production deployment
// both work with no env var set; override with VITE_API_BASE_URL for a
// backend on a different origin.
export function apiBaseUrl(): string {
  return import.meta.env.VITE_API_BASE_URL ?? "/api/v1";
}

const DEFAULT_TIMEOUT_MS = 10_000;

// ApiClientError is what every call site actually catches -- it never
// leaks a raw fetch/DOM exception or an un-parsed response body. status
// is 0 for a network failure or timeout (nothing to report), otherwise
// the real HTTP status. code/requestId come from the shared {error:
// {code, message, request_id?}} shape every real 4xx/5xx uses
// (internal/httpapi/setups.go's own apiError); a response that doesn't
// parse as that shape (a network failure, or something not running this
// API at all) still gets a usable message instead of throwing a second,
// unrelated error.
export class ApiClientError extends Error {
  readonly status: number;
  readonly code?: string;
  readonly requestId?: string;

  constructor(message: string, status: number, code?: string, requestId?: string) {
    super(message);
    this.name = "ApiClientError";
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }
}

function isApiError(v: unknown): v is ApiError {
  if (typeof v !== "object" || v === null) return false;
  const err = (v as { error?: unknown }).error;
  return (
    typeof err === "object" &&
    err !== null &&
    typeof (err as { code?: unknown }).code === "string" &&
    typeof (err as { message?: unknown }).message === "string"
  );
}

export interface GetJSONOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
}

// getJSON is the one place every API call in this app goes through: it
// builds the URL from apiBaseUrl(), applies a timeout (via its own
// AbortController, merged with a caller-supplied signal so either one can
// cancel the request), and turns a non-2xx response or a malformed body
// into an ApiClientError rather than letting a hook's own try/catch guess
// at what went wrong.
export async function getJSON<T>(
  path: string,
  params?: Record<string, string | number | undefined>,
  options?: GetJSONOptions,
): Promise<T> {
  // A plain relative URL string, not `new URL(path, window.location.origin)`:
  // fetch resolves a relative string against the page's own origin in a
  // browser with no window reference needed, and this same code then
  // works unchanged under vitest's node environment (no DOM, no window).
  const query = new URLSearchParams();
  if (params) {
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined) query.set(key, String(value));
    }
  }
  const queryString = query.toString();
  const url = `${apiBaseUrl()}${path}${queryString ? `?${queryString}` : ""}`;

  const timeoutMs = options?.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const timeoutController = new AbortController();
  const timeoutId = setTimeout(() => timeoutController.abort(), timeoutMs);
  const externalSignal = options?.signal;
  const onExternalAbort = () => timeoutController.abort();
  externalSignal?.addEventListener("abort", onExternalAbort);

  try {
    const resp = await fetch(url, { signal: timeoutController.signal });
    let body: unknown = undefined;
    const text = await resp.text();
    if (text.length > 0) {
      try {
        body = JSON.parse(text);
      } catch {
        // Not JSON at all (e.g. a proxy's own HTML error page) -- body
        // stays undefined, handled below.
      }
    }

    if (!resp.ok) {
      if (isApiError(body)) {
        throw new ApiClientError(body.error.message, resp.status, body.error.code, body.error.request_id);
      }
      throw new ApiClientError(`request failed with status ${resp.status}`, resp.status);
    }

    return body as T;
  } catch (err) {
    if (err instanceof ApiClientError) throw err;
    if (externalSignal?.aborted) {
      throw new ApiClientError("request was cancelled", 0);
    }
    if (timeoutController.signal.aborted) {
      throw new ApiClientError(`request timed out after ${timeoutMs}ms`, 0);
    }
    const message = err instanceof Error ? err.message : "network request failed";
    throw new ApiClientError(message, 0);
  } finally {
    clearTimeout(timeoutId);
    externalSignal?.removeEventListener("abort", onExternalAbort);
  }
}
