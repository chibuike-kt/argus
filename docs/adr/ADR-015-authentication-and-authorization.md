# ADR-015: Authentication and authorization

- Status: Confirmed direction; parameters approved [R], thresholds to be validated by security review; signup by emailed code and onboarding approved [R]
- Category: Identity, security and privacy
- Version: 0.3.0
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
  public authentication routes (`/auth/signup/start`, `/auth/signup/verify`, `/auth/signup/complete`
  -- see "Signup and onboarding" -- login, forgot and reset password). Market data and setups are
  not public while ADR-007 is open.

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
timing for unknown accounts. Reset tokens last 1 hour; they are 32 random bytes stored as a
SHA-256 hash, single use. Email verification at signup uses an emailed code instead of a link
token (see "Signup and onboarding"). A successful password reset revokes every session of that
user. An append only `auth_events` table records authentication events without secrets. Email is
delivered through a `Mailer` interface; the local logging mailer must not run in production, and no
mail provider has been chosen.

### Signup and onboarding

Parameters `[R]`, approved by the product owner on 7 November 2026.

- Signup is a sequence: email, emailed code, password, display name, watchlist. No user row exists
  until the password step, at which point the user is created already verified. Nobody can
  pre register someone else's email, and no unverified users exist. This satisfies the requirement
  that email verification precedes first login.
- The code is 6 digits, valid for 10 minutes, with at most 5 wrong guesses, after which that code
  is dead and a new one must be requested. A new code can be requested after 60 seconds and
  replaces the previous one. At most 5 codes per email per hour and 10 signup starts per IP per
  hour. Only an HMAC-SHA256 of the code (server key) is stored, never the code, and it is compared
  in constant time.
- A correct code returns a signup ticket: 32 random bytes stored as a SHA-256 hash, valid 30
  minutes, single use, bound to that email, required to set the password.
- Signup start returns the same 202 for new and existing emails with equal timing. For an existing
  account it sends a notice instead of a code, subject to the same per account hourly limit, which
  is skipped silently on exceed (never a 429 that depends on the account existing). This per-email
  send limit is skipped silently the same way for a new email as for an existing one, never a 429
  on either, since a 429 that happened on only one of the two would itself reveal, after enough
  attempts, whether the account exists. Code verification returns one generic `invalid_code` for a
  wrong, expired, unknown or exhausted code.
- Signup start requires `accepted_terms: true`, confirming the person is 18 or over and accepts the
  Terms and Privacy Policy. The user records `terms_accepted_at` and a `terms_version`. No age or
  birthdate is stored.
- The display name is one field, 1 to 60 characters after trimming, with no control characters.
- Each user has a watchlist of enabled instruments with an order (maximum 50). Onboarding is
  complete only with a display name and at least one watchlist item, recorded as
  `onboarding_completed_at`. A signed in user who has not completed onboarding is sent back to the
  step they left.
- The earlier link based registration endpoints (`/auth/register`, `/auth/verify-email`,
  `/auth/resend-verification`) are removed so there is a single signup path. The email token table
  remains for password reset.

## WebSocket revocation

Revoking a session must invalidate its active WebSocket access promptly.

This requires reliable cross-process revocation and tests for disconnection and shared-state failure scenarios.

`[R]` Until the WebSocket gateway exists, every session revocation publishes the session id on the
Redis channel `auth:session_revoked`. The gateway consumes it when built (ADR-013).

## Authorization

Authorization is deny-by-default.

Every user-owned resource must be checked for ownership at the server boundary. Future organization support must not weaken individual account isolation.

No public API keys are planned for the first release.
