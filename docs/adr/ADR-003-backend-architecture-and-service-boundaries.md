# ADR-003: Backend architecture and service boundaries

- Status: Confirmed direction; detailed boundaries partially open
- Category: Core architecture
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-004](ADR-004-api-and-frontend-architecture.md), [ADR-005](ADR-005-postgresql-redis-and-background-processing.md)

---

## Decision

ARGUS will use a modular Go backend rather than beginning with a distributed microservice architecture.

The initial application will be organized around feature modules with explicit interfaces and responsibilities.

Expected logical modules include:

- Identity and access.

- User profiles and preferences.

- Watchlists and subscriptions.

- Instrument registry.

- Market-data ingestion and normalization.

- Historical data and backfill.

- Technical-analysis primitives.

- Strategy evaluation.

- POI and setup lifecycle management.

- Risk and trading-plan calculations.

- Chart annotations and drawing persistence.

- Notifications and delivery.

- Optional paper-trading simulation.

- Administration, observability and operational controls.

These are logical boundaries. They do not automatically imply separate deployable services.

## Process boundaries

The deployment should distinguish at least these workloads:

- API and user-facing application processes.

- Market-data ingestion workers.

- Analysis and reevaluation workers.

- Historical backfill workers.

These workloads may run on one host while retaining separate resource limits and operational controls.

## Consequences

- Module interfaces must prevent unnecessary coupling.

- Shared domain logic should not be duplicated across API and worker processes.

- Separate processes can be scaled or isolated later if measured demand justifies it.

- Live ingestion and timely analysis take priority over historical backfill and shadow evaluation.
