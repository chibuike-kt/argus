# ADR-013: Notification delivery and WebSocket recovery

- Status: Confirmed direction; implementation details partially open
- Category: Notifications and realtime recovery
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-004](ADR-004-api-and-frontend-architecture.md), [ADR-005](ADR-005-postgresql-redis-and-background-processing.md)

---

## Decision

Notifications are emitted for meaningful lifecycle transitions, not every repeated evaluation.

The architecture will use a transactional outbox so that the business event and the intent to notify are committed consistently.

## Delivery guarantees

Notification and event delivery are at-least-once.

Exactly-once delivery across external systems is not assumed. Consumers must deduplicate using stable idempotency keys.

The notification deduplication key is based on the lifecycle transition, recipient and notification category.

The exact key format and uniqueness scope remain open.

## Channels

The initial channels are:

- In-app notifications.

- Email notifications.

Email is meaningful-transition-only, with per-event notification preferences and granular unsubscribe behavior.

## Recovery

WebSocket clients use cursor-based event replay.

If replay cannot fully recover a client, the server must reconcile the client with authoritative state. A full resynchronization is required if a consistent recovery point cannot be established.

Duplicate events must be safe to process.

The exact cursor schema, replay retention, snapshot consistency protocol and gap-detection behavior remain open.

## Failure handling

Notification delivery requires retry and dead-letter handling, operational alerts and a defined replay process.

Outbox processing must be idempotent. Dead-lettered notifications must not disappear silently.

## User-facing history

Setup history is presented as a timeline.

Corrected analyses and material plan changes appear as amendment or revision events rather than overwriting earlier history.
