# ADR-018: Observability and load testing

- Status: Confirmed direction; numerical thresholds open
- Category: Quality, reliability and operations
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-002](ADR-002-deployment-model-audience-and-scale.md), [ADR-017](ADR-017-availability-backups-and-recovery-objectives.md)

---

## Decision

ARGUS will begin with lean, actionable observability:

- Structured logs.

- Error tracking.

- Health checks.

- Core metrics.

- Alerts for stale market feeds.

- Alerts for failed jobs and dead-lettered work.

- Ingestion-lag and analysis-lag metrics.

- Queue-depth and resource-pressure monitoring.

The system must distinguish data-provider failures from application failures.

Stale-feed and ingestion-lag alerts are evaluated against the per-market trading calendar (see ADR-006), so scheduled forex closures do not trigger false alerts.

## Load-testing gate

Load testing is required before public launch.

Testing must include sustained load and traffic spikes using a representative mixed workload that reflects:

- WebSocket connections and subscriptions.

- Watchlist sizes.

- Shared versus unique instrument subscriptions.

- Live update fan-out.

- REST requests.

- Analysis evaluation load.

- Historical and background work.

- Notification traffic.

The target of 1,000 active users must be defined using explicit workload assumptions, not simply 1,000 logged-in accounts.

## Release criteria

Numerical latency and load thresholds will be defined after baseline benchmarking and before release-candidate testing.

Release gates must include correctness, authorization, migration safety, data-quality handling, load behavior and recovery acceptance criteria.
