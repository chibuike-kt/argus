# ARGUS Architecture Decision Records

Product: ARGUS (Autonomous Market Intelligence)
Tagline: Always watching. Always adapting.
Document type: Architecture Decision Records, split from the consolidated master ADR
Version: 0.1.0-draft
Status: Draft for review. Not approved for implementation
Last updated: 26 October 2026
Primary implementation language: Go
Initial deployment model: Hosted public application with local Docker development

## How this is organized

This folder is the consolidated master ADR split into one file per decision. Each ADR file stands alone with its own status header and cross-links. The appendix files hold the cross-cutting material that applies across ADRs and must not be lost when the decisions are read in isolation.

No decision is upgraded in status by this split. A recommendation does not become a confirmed decision merely because it appears here.

## Decision status legend

- Confirmed: explicitly selected.
- Partially confirmed: direction selected, implementation details open.
- Proposed: recommended, requires approval.
- Open: no final decision made.
- Validation required: target agreed, feasibility or correctness not yet demonstrated.
- Deferred: excluded from initial scope or reserved for a later phase.

## ADR lifecycle

Draft, then review trade-offs and evidence, then approve, revise or reject, then implement against the approved decision, then revisit when new evidence materially changes its assumptions.

## Index

### Product definition and boundaries

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-001](ADR-001-argus-product-scope-and-execution-boundary.md) | ARGUS product scope and execution boundary | Confirmed |
| [ADR-002](ADR-002-deployment-model-audience-and-scale.md) | Deployment model, audience and scale | Confirmed direction; feasibility validation required |

### Core architecture

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-003](ADR-003-backend-architecture-and-service-boundaries.md) | Backend architecture and service boundaries | Confirmed direction; detailed boundaries partially open |
| [ADR-004](ADR-004-api-and-frontend-architecture.md) | API and frontend architecture | Confirmed direction; implementation details partially open |
| [ADR-005](ADR-005-postgresql-redis-and-background-processing.md) | PostgreSQL, Redis and background processing | Confirmed direction |

### Market-data architecture

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-006](ADR-006-market-data-contracts-and-canonical-representation.md) | Market-data contracts and canonical representation | Partially confirmed |
| [ADR-007](ADR-007-provider-selection-and-commercial-data-rights.md) | Provider selection and commercial data rights | Open; research and validation required |

### Charting and visual analysis

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-008](ADR-008-interactive-live-charts-and-analytical-drawing-tools.md) | Interactive live charts and analytical drawing tools | Charting technology decided (Option A); phase 2 (interaction layer) implemented |

### Technical-analysis engine

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-009](ADR-009-deterministic-versioned-multi-timeframe-analysis.md) | Deterministic, versioned, multi-timeframe analysis | Confirmed direction; exact rules partially open |

### Setup lifecycle and event history

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-010](ADR-010-setup-state-invalidation-and-re-entry.md) | Setup state, invalidation and re-entry | Partially confirmed |
| [ADR-011](ADR-011-immutable-decisions-corrections-and-plan-revisions.md) | Immutable decisions, corrections and plan revisions | Confirmed direction |

### Entry, risk and trading plans

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-012](ADR-012-proposed-plans-sizing-and-targets.md) | Proposed plans, sizing and targets | Partially confirmed |

### Notifications and realtime recovery

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-013](ADR-013-notification-delivery-and-websocket-recovery.md) | Notification delivery and WebSocket recovery | Confirmed direction; implementation details partially open |

### Optional paper trading

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-014](ADR-014-paper-trading-simulation.md) | Paper-trading simulation | Confirmed direction; launch timing open |

### Identity, security and privacy

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-015](ADR-015-authentication-and-authorization.md) | Authentication and authorization | Confirmed direction; security parameters partially open |
| [ADR-016](ADR-016-data-retention-account-deletion-and-privacy.md) | Data retention, account deletion and privacy | Confirmed direction; retention periods partially open |

### Quality, reliability and operations

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-017](ADR-017-availability-backups-and-recovery-objectives.md) | Availability, backups and recovery objectives | Targets confirmed; feasibility validation required |
| [ADR-018](ADR-018-observability-and-load-testing.md) | Observability and load testing | Confirmed direction; numerical thresholds open |

### Testing, reproducibility and deployment

| ADR | Title | Status |
| --- | --- | --- |
| [ADR-019](ADR-019-testing-and-point-in-time-reproducibility.md) | Testing and point-in-time reproducibility | Confirmed direction; some acceptance criteria open |
| [ADR-020](ADR-020-database-migrations-and-release-management.md) | Database migrations and release management | Confirmed direction; migration tool selected (golang-migrate) |

## Appendices

These carry the cross-cutting content from the master ADR. They are reference material, not separate decisions.

| File | Contents |
| --- | --- |
| [appendix-a-strategy-defaults.md](appendix-a-strategy-defaults.md) | Consolidated analytical conventions and their unresolved details |
| [appendix-b-product-requirements.md](appendix-b-product-requirements.md) | Cross-cutting product requirements |
| [appendix-c-unresolved-decisions.md](appendix-c-unresolved-decisions.md) | Open decisions register, by priority |
| [appendix-d-feasibility-tradeoffs.md](appendix-d-feasibility-tradeoffs.md) | Feasibility risks and explicit trade-offs |
| [appendix-e-implementation-sequence.md](appendix-e-implementation-sequence.md) | Proposed phased implementation sequence |
| [appendix-f-approval-checklist.md](appendix-f-approval-checklist.md) | Approval checklist for the master ADR |

## Changes from the master document

The content is preserved from the master ADR. The changes made during the split are:

- Each ADR is a standalone file with a status header (status, category, version, date, related ADRs).
- Sections 14 to 19 of the master became the appendix files above. Section 20 (final status) is reproduced below.
- ADR-006 gained a Market calendar subsection so that scheduled forex closures are not treated as data gaps or stale feeds. ADR-018 cross-references it so stale-feed alerts respect the same calendar.

## Final status

ARGUS has a substantially defined architectural direction:

- A modular Go backend with PostgreSQL and Redis.

- A shared, normalized market-data pipeline.

- Deterministic, versioned multi-timeframe technical analysis.

- Explainable setup lifecycle management.

- Immutable decisions and plan revisions.

- Interactive live charts with automatic and user-created drawing tools.

- REST and WebSocket APIs.

- Public accounts with explicit security and privacy controls.

- Optional downstream paper trading.

- A testing, recovery and deployment discipline designed for a constrained initial budget.

However, the architecture is not yet fully validated or approved.

The most important remaining decisions are provider feasibility and licensing, charting technology, the exact strategy specification, and whether the budget and deployment can satisfy the operational targets.

Until those are resolved, this document remains the master draft against which future ADRs, implementation plans and technical decisions should be reviewed.
