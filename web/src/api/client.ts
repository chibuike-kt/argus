import type { ApiError } from "./types";

// Base URL for the real API. Defaults to a relative /api/v1 so the Vite
// dev proxy (vite.config.ts) and any same-origin production deployment
// both work with no env var set; override with VITE_API_BASE_URL for a
// backend on a different origin.
export function apiBaseUrl(): string {
  return import.meta.env.VITE_API_BASE_URL ?? "/api/v1";
}

const DEFAULT_TIMEOUT_MS = 10_000;

// Stable error codes the UI can switch on, surfaced from the backend's
// own {error:{code,...}} body when present. When a non-2xx response
// doesn't parse as that shape at all (a network failure, or a proxy's
// own error page), STATUS_FALLBACK_CODE derives one from the HTTP
// status alone so a call site can still branch without string-matching
// a raw message. 403 maps to csrf_rejected because that is the only
// thing this API ever answers 403 for (internal/httpapi's own CSRF
// middleware) -- if that changes, this fallback needs revisiting.
const STATUS_FALLBACK_CODE: Record<number, string> = {
  401: "unauthorized",
  403: "csrf_rejected",
  413: "request_too_large",
  429: "rate_limited",
  503: "unavailable",
};

// ApiClientError is what every call site actually catches -- it never
// leaks a raw fetch/DOM exception or an un-parsed response body. status
// is 0 for a network failure or timeout (nothing to report), otherwise
// the real HTTP status. code/requestId come from the shared {error:
// {code, message, request_id?}} shape every real 4xx/5xx uses
// (internal/httpapi/setups.go's own apiError), falling back to
// STATUS_FALLBACK_CODE when the body didn't parse as that shape;
// retryAfterSeconds comes from the response's own Retry-After header
// (set alongside a 429 rate_limited body) when present.
export class ApiClientError extends Error {
  readonly status: number;
  readonly code?: string;
  readonly requestId?: string;
  readonly retryAfterSeconds?: number;

  constructor(
    message: string,
    status: number,
    code?: string,
    requestId?: string,
    retryAfterSeconds?: number,
  ) {
    super(message);
    this.name = "ApiClientError";
    this.status = status;
    this.code = code;
    this.requestId = requestId;
    this.retryAfterSeconds = retryAfterSeconds;
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

function parseRetryAfter(resp: Response): number | undefined {
  const raw = resp.headers.get("Retry-After");
  if (raw === null) return undefined;
  const n = Number(raw);
  return Number.isFinite(n) && n >= 0 ? n : undefined;
}

// unauthorizedHandler is the single global 401 hook (web auth UI brief
// A1): set once by AuthProvider, it fires for any call that gets back
// a 401 from a signed-in session, except a call that explicitly
// suppresses it (RequestOptions.suppressUnauthorizedHandler -- the
// login call itself, whose own 401 means "wrong credentials", not
// "your session died"). Implemented once here rather than in every
// hook, so a new call site gets the behaviour for free.
type UnauthorizedHandler = () => void;
let unauthorizedHandler: UnauthorizedHandler | null = null;

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null) {
  unauthorizedHandler = handler;
}

export interface RequestOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
  /** Skip the global 401 handler for this one call (the login call itself). */
  suppressUnauthorizedHandler?: boolean;
}

// GetJSONOptions is kept as its own exported name (existing call sites
// and tests reference it) -- identical shape to RequestOptions.
export type GetJSONOptions = RequestOptions;

interface PerformedResponse {
  resp: Response;
  body: unknown;
}

// performRequest is the one place every method below builds the URL,
// applies a timeout (via its own AbortController, merged with a
// caller-supplied signal so either one can cancel the request), sends
// the session cookie explicitly, and parses the body as JSON when
// there is one. It never throws for a non-2xx response -- the caller
// maps that to ApiClientError, since what counts as an error body
// differs slightly between callers (none here, as it happens, but
// kept symmetric with the original getJSON).
async function performRequest(
  url: string,
  init: RequestInit,
  options?: RequestOptions,
): Promise<PerformedResponse> {
  const timeoutMs = options?.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const timeoutController = new AbortController();
  const timeoutId = setTimeout(() => timeoutController.abort(), timeoutMs);
  const externalSignal = options?.signal;
  const onExternalAbort = () => timeoutController.abort();
  externalSignal?.addEventListener("abort", onExternalAbort);

  try {
    const resp = await fetch(url, {
      ...init,
      // Send the session cookie explicitly on every request, same-origin
      // only (never cross-origin) -- the browser then adds the Origin
      // header itself, which the backend's CSRF rule checks against its
      // own allow-list. No CSRF token: the backend's own defense is
      // Content-Type + Origin, not a token this client would need to read.
      credentials: "same-origin",
      signal: timeoutController.signal,
    });
    let body: unknown = undefined;
    const text = await resp.text();
    if (text.length > 0) {
      try {
        body = JSON.parse(text);
      } catch {
        // Not JSON at all (e.g. a proxy's own HTML error page) -- body
        // stays undefined, handled by the caller.
      }
    }
    return { resp, body };
  } catch (err) {
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

// toError builds the ApiClientError for a non-2xx response and fires
// the global 401 handler when applicable.
function toError(resp: Response, body: unknown, options?: RequestOptions): ApiClientError {
  const retryAfterSeconds = parseRetryAfter(resp);
  const err = isApiError(body)
    ? new ApiClientError(
        body.error.message,
        resp.status,
        body.error.code,
        body.error.request_id,
        retryAfterSeconds,
      )
    : new ApiClientError(
        `request failed with status ${resp.status}`,
        resp.status,
        STATUS_FALLBACK_CODE[resp.status],
        undefined,
        retryAfterSeconds,
      );

  if (resp.status === 401 && !options?.suppressUnauthorizedHandler) {
    unauthorizedHandler?.();
  }
  return err;
}

// getJSON is the one place every read-only API call in this app goes
// through: it builds the URL from apiBaseUrl() and turns a non-2xx
// response or a malformed body into an ApiClientError rather than
// letting a hook's own try/catch guess at what went wrong.
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

  const { resp, body } = await performRequest(url, {}, options);
  if (!resp.ok) throw toError(resp, body, options);
  return body as T;
}

// postJSON sends a JSON body and parses a JSON (or empty, e.g. 204)
// response the same way getJSON does.
export async function postJSON<T>(
  path: string,
  body?: unknown,
  options?: RequestOptions,
): Promise<T> {
  const url = `${apiBaseUrl()}${path}`;
  const { resp, body: respBody } = await performRequest(
    url,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    },
    options,
  );
  if (!resp.ok) throw toError(resp, respBody, options);
  return respBody as T;
}

// putJSON sends a JSON body via PUT (a full-replace write, e.g. the
// watchlist or the profile) and parses the response the same way
// postJSON does.
export async function putJSON<T>(
  path: string,
  body?: unknown,
  options?: RequestOptions,
): Promise<T> {
  const url = `${apiBaseUrl()}${path}`;
  const { resp, body: respBody } = await performRequest(
    url,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    },
    options,
  );
  if (!resp.ok) throw toError(resp, respBody, options);
  return respBody as T;
}

// deleteRequest sends a DELETE with no body (every DELETE route this
// app calls, e.g. a session revoke, takes none) and parses an empty
// (204) response. Content-Type is still sent on the empty body: the
// backend's CSRF rule checks it on every state-changing method, DELETE
// included, not only when there is a body to describe.
export async function deleteRequest<T = void>(path: string, options?: RequestOptions): Promise<T> {
  const url = `${apiBaseUrl()}${path}`;
  const { resp, body } = await performRequest(
    url,
    { method: "DELETE", headers: { "Content-Type": "application/json" } },
    options,
  );
  if (!resp.ok) throw toError(resp, body, options);
  return body as T;
}
