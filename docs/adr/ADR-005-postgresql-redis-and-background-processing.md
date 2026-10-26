# ADR-005: PostgreSQL, Redis and background processing

- Status: Confirmed direction
- Category: Core architecture
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-013](ADR-013-notification-delivery-and-websocket-recovery.md), [ADR-017](ADR-017-availability-backups-and-recovery-objectives.md)

---

## Decision

PostgreSQL will be the primary persistent data store.

Redis will be introduced from the beginning to support appropriate realtime and background-processing use cases.

A Redis-backed queue library will manage asynchronous work. The exact library remains to be selected.

The processing contract is:

- At-least-once delivery.

- Idempotent processing.

- Per-instrument ordering where required.

- Shared ingestion workers.

- Background historical backfill.

- Explicit recovery and reconciliation.

- Priority for live ingestion over backfill and lower-priority work.

## Consistency and recovery

PostgreSQL is the durable source of truth for persisted business state.

Redis queue state must not be treated as the sole durable record of market-data history, setup history or analytical decisions.

Workers must tolerate duplicate delivery, process restarts and interrupted jobs.

Per-instrument ordering does not imply global ordering across all instruments.

## Workload isolation

Live ingestion, analysis and historical backfill must be distinguishable operationally.

If capacity becomes constrained, ARGUS should throttle or pause lower-priority work before allowing live ingestion to fall materially behind.

Analysis timeliness must also be monitored so that protecting ingestion does not conceal an expanding analysis backlog.

## Open decisions

- Queue-library selection.

- Redis persistence and recovery configuration.

- Queue retention and dead-letter handling.

- Concurrency and per-instrument partitioning strategy.

- Resource limits and scheduling policy.
