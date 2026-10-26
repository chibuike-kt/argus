# ADR-004: API and frontend architecture

- Status: Confirmed direction; implementation details partially open
- Category: Core architecture
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-013](ADR-013-notification-delivery-and-websocket-recovery.md), [ADR-003](ADR-003-backend-architecture-and-service-boundaries.md)

---

## Decision

The selected direction is:

- Backend: Go.

- Frontend: React with TypeScript and Vite.

- API: REST for commands, account operations, historical queries and resource management.

- Realtime: WebSockets for live updates, lifecycle transitions and chart-related events.

- REST API versioning: URL-based versioning, beginning with /api/v1/.

- API contract: OpenAPI-first.

- Database access: PostgreSQL with sqlc-generated Go code.

- Realtime message format: a versioned WebSocket envelope.

- Frontend authority: server-authoritative state.

## WebSocket envelope

The message contract should include, as applicable:

- Message or event type.

- Schema version.

- Event identifier.

- Event timestamp.

- Subscription or resource reference.

- Payload.

- Cursor or sequence information where required.

- Error or acknowledgement information.

The exact schema and cursor format remain open.

## Frontend behavior

The frontend may use optimistic interaction for appropriate user actions, but server responses and reconciled state remain authoritative.

Client-side chart interactions must not independently redefine the analytical state of a setup.

## Open decisions

- Frontend server-state and query library.

- Client-side state-management approach.

- Exact WebSocket event schema.

- Charting library and drawing API.

- Production deployment and rollback implementation.
