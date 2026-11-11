import { deleteRequest, getJSON, postJSON, putJSON, type RequestOptions } from "./client";
import type { ApiAuthSession, ApiAuthUser, ApiWatchlistItem } from "./types";

// One function per real /auth/* (and /me/*, /watchlist) endpoint
// (api/openapi.yaml), mirroring the existing instruments.ts/setups.ts
// convention. No fixturesEnabled() branch here: fixtures mode bypasses
// auth entirely at the provider level (web/src/auth/auth-context.tsx),
// so these never run while it's on.

// Signup is a sequence (ADR-015 "Signup and onboarding"): start sends
// an emailed code, verify exchanges a correct code for a single-use
// ticket, complete consumes the ticket and the chosen password to
// create the account, already signed in. The email/code/password are
// never cached by this module -- each call takes exactly what it
// needs and nothing more is kept around after it resolves.
export function signupStart(
  email: string,
  acceptedTerms: boolean,
  options?: RequestOptions,
): Promise<void> {
  return postJSON("/auth/signup/start", { email, accepted_terms: acceptedTerms }, options);
}

export function signupVerify(
  email: string,
  code: string,
  options?: RequestOptions,
): Promise<{ ticket: string }> {
  return postJSON<{ ticket: string }>("/auth/signup/verify", { email, code }, options);
}

// signupComplete's own 400 means "that ticket/password did not work",
// not "your session died" -- it must not trigger the global
// unauthorized handler, the same reason login suppresses it.
export function signupComplete(
  ticket: string,
  password: string,
  options?: RequestOptions,
): Promise<ApiAuthUser> {
  return postJSON<ApiAuthUser>(
    "/auth/signup/complete",
    { ticket, password },
    { ...options, suppressUnauthorizedHandler: true },
  );
}

// login's own 401 means "wrong credentials", not "your session died" --
// it must not trigger the global unauthorized handler (web auth UI
// brief A1), so this is the one call site that suppresses it.
export function login(
  email: string,
  password: string,
  options?: RequestOptions,
): Promise<ApiAuthUser> {
  return postJSON<ApiAuthUser>(
    "/auth/login",
    { email, password },
    { ...options, suppressUnauthorizedHandler: true },
  );
}

export function logout(options?: RequestOptions): Promise<void> {
  return postJSON("/auth/logout", undefined, options);
}

export function logoutAll(options?: RequestOptions): Promise<void> {
  return postJSON("/auth/logout-all", undefined, options);
}

export function me(options?: RequestOptions): Promise<ApiAuthUser> {
  return getJSON<ApiAuthUser>("/auth/me", undefined, options);
}

export function listSessions(options?: RequestOptions): Promise<ApiAuthSession[]> {
  return getJSON<ApiAuthSession[]>("/auth/sessions", undefined, options);
}

export function revokeSession(id: number, options?: RequestOptions): Promise<void> {
  return deleteRequest(`/auth/sessions/${id}`, options);
}

export function updateProfile(
  displayName: string,
  options?: RequestOptions,
): Promise<ApiAuthUser> {
  return putJSON<ApiAuthUser>("/me/profile", { display_name: displayName }, options);
}

export function completeOnboarding(options?: RequestOptions): Promise<ApiAuthUser> {
  return postJSON<ApiAuthUser>("/me/onboarding/complete", undefined, options);
}

export function getWatchlist(options?: RequestOptions): Promise<ApiWatchlistItem[]> {
  return getJSON<ApiWatchlistItem[]>("/watchlist", undefined, options);
}

export function updateWatchlist(
  instrumentIds: number[],
  options?: RequestOptions,
): Promise<ApiWatchlistItem[]> {
  return putJSON<ApiWatchlistItem[]>("/watchlist", { instrument_ids: instrumentIds }, options);
}
