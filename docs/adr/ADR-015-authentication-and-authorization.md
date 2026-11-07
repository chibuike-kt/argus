# ADR-015: Authentication and authorization

- Status: Confirmed direction; parameters approved [R], thresholds to be validated by security review
- Category: Identity, security and privacy
- Version: 0.2.0
- Last updated: 7 November 2026
- Related: [ADR-016](ADR-016-data-retention-account-deletion-and-privacy.md)

---

## Decision

ARGUS will support public account registration using email and password.

Email verification is required.

The initial identity model is designed to support individual accounts, with future organization readiness but no organization-workspace functionality required at launch.

Authentication is custom at the application level, so its implementation requires explicit security review.

## Passwords

- Use Argon2id password hashing.

- Benchmark parameters and support rehashing.

- Apply a modern password-length policy.

- Avoid arbitrary periodic password rotation.

- Screen against common or compromised passwords where feasible.

Parameters `[R]`, approved by the product owner on 7 November 2026: Argon2id with memory 64 MiB,
time 3, parallelism 1, a 16 byte salt and a 32 byte key, stored as a PHC string, rehashed on login
when parameters change, with at most 4 concurrent hash operations per process. Passwords must be 12
to 128 characters with no composition rules and no rotation. Passwords in a bundled list of the
10,000 most common passwords, or containing the local part of the account email, are rejected.
Online breach screening is deferred. The hashing parameters must be benchmarked on the production
host before launch.

## Sessions

Use opaque server-side sessions with secure, HttpOnly browser cookies.

The session policy includes idle and absolute expiry, user-visible session management, recovery and revocation.

Session policy `[R]`, approved by the product owner on 7 November 2026:

- Postgres is the source of truth for sessions. Redis is not required to validate a session. If
  Postgres is unavailable, requests fail with 503 and are never treated as authenticated.

- Session tokens are 32 random bytes, base64url encoded, stored only as a SHA-256 hash.

- Idle expiry is 7 days (sliding) and absolute expiry is 30 days.

- The cookie is named `__Host-argus_session` in production and `argus_session` on local http. It is
  HttpOnly, Secure (except on local http), SameSite=Lax, Path=/, with no Domain attribute.

- CSRF protection uses no token. State changing requests must send `Content-Type:
  application/json` and an `Origin` header that exactly matches a configured allowed origin list;
  SameSite=Lax applies as well.

- Sensitive actions (change password, enable or disable MFA, regenerate recovery codes, change
  email) require the password again when the last password entry on that session is older than 10
  minutes.

- Revoking a session is a database update that takes effect on the next request.

- Every `/api/v1` route requires a session except `/healthz`, `/readyz`, `/api/v1/ping` and the
  public authentication routes (register, verify email, resend verification, login, forgot and
  reset password). Market data and setups are not public while ADR-007 is open.

## MFA

MFA is optional.

The selected direction is TOTP with recovery codes. Recovery codes must be single-use and stored securely, and sensitive MFA changes should require appropriate reauthentication.

Parameters `[R]`, approved on 7 November 2026: RFC 6238 TOTP with SHA1, 6 digits and a 30 second
step, accepting one step on either side and rejecting a code whose step was already used. The
secret is encrypted at rest with AES-256-GCM using a key from configuration. There are 10 recovery
codes of 10 characters from an unambiguous alphabet, stored as HMAC-SHA256 with a server key;
regenerating replaces all of them. When MFA is enabled, the password step of login returns an MFA
challenge with a single use ticket valid for 5 minutes and creates no session; the second step
sends the ticket with a TOTP or recovery code.

## Recovery and abuse prevention

Account recovery uses email tokens.

Abuse protection uses layered rate limits, including relevant IP and account dimensions, progressive delays or throttling, generic authentication errors and abuse monitoring.

Thresholds `[R]`, approved on 7 November 2026: login allows 5 failures per account per 15 minutes
before progressive delay (no hard lockout) and 20 attempts per IP per 15 minutes; registration
allows 10 per IP per hour; forgot password and resend verification allow 5 per account per hour and
10 per IP per hour. Counters live in Redis with an in process fallback when Redis is unavailable;
limiting is never disabled. Registration, forgot password and resend verification return the same
generic response whether or not the email exists, and login returns one generic error with equal
timing for unknown accounts. Email verification tokens last 24 hours and reset tokens 1 hour; both
are 32 random bytes stored as a SHA-256 hash, single use. A successful password reset revokes every
session of that user. An append only `auth_events` table records authentication events without
secrets. Email is delivered through a `Mailer` interface; the local logging mailer must not run in
production, and no mail provider has been chosen.

## WebSocket revocation

Revoking a session must invalidate its active WebSocket access promptly.

This requires reliable cross-process revocation and tests for disconnection and shared-state failure scenarios.

`[R]` Until the WebSocket gateway exists, every session revocation publishes the session id on the
Redis channel `auth:session_revoked`. The gateway consumes it when built (ADR-013).

## Authorization

Authorization is deny-by-default.

Every user-owned resource must be checked for ownership at the server boundary. Future organization support must not weaken individual account isolation.

No public API keys are planned for the first release.
