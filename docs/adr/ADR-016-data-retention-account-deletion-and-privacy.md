# ADR-016: Data retention, account deletion and privacy

- Status: Confirmed direction; retention periods partially open
- Category: Identity, security and privacy
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-015](ADR-015-authentication-and-authorization.md), [ADR-006](ADR-006-market-data-contracts-and-canonical-representation.md)

---

## Decision

ARGUS will use separate retention policies for distinct data classes.

The intended categories include:

- Canonical candles and revisions.

- Latest quotes.

- Setup and analytical events.

- Security and audit records.

- Notification records.

- Backups.

- Exports.

- User-created chart drawings.

- Paper-trading records.

The exact retention period for each class remains open, except for the intended two-year candle-history policy.

## Account deletion

Account deletion follows a staged process.

Personal data is deleted where possible. Necessary historical evidence may be retained in an anonymized form when there is a justified retention need.

Shared market data is separated from user-owned records so that deleting a user does not unnecessarily delete shared canonical market history.

Deletion exceptions must be documented and auditable.

## Exports

Users can export account-associated personal data and relevant analysis and simulation records.

Export format, delivery mechanism, expiry and access-control requirements remain open.

## Privacy controls

- Watchlists are private.

- Product analytics are minimal and privacy-conscious.

- Security logs are minimized.

- Audit records are structured and avoid unnecessary sensitive data.

- Email notification preferences are granular.

- Retention enforcement is automated and auditable.

Retention jobs must be idempotent, dependency-aware and observable. Destructive operations should have a suitable validation or dry-run process.

Backup expiry must be consistent with deletion policy and recovery requirements.
