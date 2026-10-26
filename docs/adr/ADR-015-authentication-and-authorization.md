# ADR-015: Authentication and authorization

- Status: Confirmed direction; security parameters partially open
- Category: Identity, security and privacy
- Version: 0.1.0-draft
- Last updated: 26 October 2026
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

Exact length limits and hashing parameters remain open.

## Sessions

Use opaque server-side sessions with secure, HttpOnly browser cookies.

The session policy includes idle and absolute expiry, user-visible session management, recovery and revocation.

The following still require explicit values or policies:

- Idle and absolute expiration durations.

- SameSite cookie settings.

- CSRF protection.

- Sensitive-action reauthentication.

- Session revocation behavior during Redis or shared-state failure.

## MFA

MFA is optional.

The selected direction is TOTP with recovery codes. Recovery codes must be single-use and stored securely, and sensitive MFA changes should require appropriate reauthentication.

## Recovery and abuse prevention

Account recovery uses email tokens.

Abuse protection uses layered rate limits, including relevant IP and account dimensions, progressive delays or throttling, generic authentication errors and abuse monitoring.

Exact thresholds remain open.

## WebSocket revocation

Revoking a session must invalidate its active WebSocket access promptly.

This requires reliable cross-process revocation and tests for disconnection and shared-state failure scenarios.

## Authorization

Authorization is deny-by-default.

Every user-owned resource must be checked for ownership at the server boundary. Future organization support must not weaken individual account isolation.

No public API keys are planned for the first release.
