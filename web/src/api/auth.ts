import { deleteRequest, getJSON, postJSON, type RequestOptions } from "./client";
import type { ApiAuthSession, ApiAuthUser } from "./types";

// One function per real /auth/* endpoint (api/openapi.yaml), mirroring
// the existing instruments.ts/setups.ts convention. No fixturesEnabled()
// branch here: fixtures mode bypasses auth entirely at the provider
// level (web/src/auth/auth-context.tsx), so these never run while it's on.

export function register(email: string, password: string, options?: RequestOptions): Promise<void> {
  return postJSON("/auth/register", { email, password }, options);
}

export function verifyEmail(token: string, options?: RequestOptions): Promise<void> {
  return postJSON("/auth/verify-email", { token }, options);
}

export function resendVerification(email: string, options?: RequestOptions): Promise<void> {
  return postJSON("/auth/resend-verification", { email }, options);
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
