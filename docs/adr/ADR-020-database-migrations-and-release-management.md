# ADR-020: Database migrations and release management

- Status: Confirmed direction; migration tooling open
- Category: Testing, reproducibility and deployment
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-017](ADR-017-availability-backups-and-recovery-objectives.md), [ADR-018](ADR-018-observability-and-load-testing.md)

---

## Decision

The release process will use GitHub Actions for automated checks and builds.

Production release requires manual approval after the required automated checks pass.

Container images must use immutable, traceable tags.

Database migrations follow expand-contract by default and run as a dedicated, serialized release step.

## CI gates

Required checks include:

- Formatting and linting.

- Unit and integration tests.

- API contract validation.

- Build validation.

- Security and dependency checks.

- Migration validation.

- Relevant release-specific tests.

## Migration and rollback behavior

Application rollback is permitted only when the database state remains compatible.

For irreversible or data-changing migrations, forward fixes may be safer than attempting to reverse the migration.

A failed migration must have explicit failure handling and must not leave the release process assuming the schema is healthy.

## Post-deployment gates

Post-deployment validation checks:

- Application availability.

- Database connectivity.

- Worker and queue health.

- Market-data freshness.

- Analysis lag.

- Critical user journeys.

Secrets must remain outside source code and container images. Secret rotation and revocation procedures must be documented.

## Open decisions

- Migration tool.

- Production secret-management approach.

- Exact deployment and rollback mechanism.

- Emergency-release process.

- Measurable post-deployment thresholds.
