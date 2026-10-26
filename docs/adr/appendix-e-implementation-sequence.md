# Proposed implementation sequence

- Source: ARGUS Master ADR, section 18
- Version: 0.1.0-draft
- Last updated: 26 October 2026

---

Status: Proposed. Requires approval.

The following sequence is a recommendation, not an approved delivery commitment.

## Phase 1: Resolve hard gates

- Validate provider rights and feasibility.

- Evaluate charting solutions and drawing APIs.

- Define the first instrument universe.

- Establish the initial strategy specification.

- Define measurable data-quality and freshness requirements.

- Reconcile the budget with recovery and scale requirements.

## Phase 2: Architecture foundations

- Establish the Go module structure.

- Define PostgreSQL schemas and migration workflow.

- Define the market-data contract and canonical instrument registry.

- Implement normalized ingestion and gap recovery.

- Establish queue processing, idempotency and observability.

- Implement authentication and authorization foundations.

## Phase 3: Deterministic analysis

- Implement swing and structure detection.

- Implement POI identification and validity.

- Implement confluence evaluation.

- Implement candidate creation and lifecycle transitions.

- Implement immutable setup events and plan versions.

- Build point-in-time replay and corrected-candle tests.

## Phase 4: Chart and realtime experience

- Implement the selected charting solution.

- Add the required drawing tools.

- Implement automatic and user-owned drawing persistence.

- Integrate live market updates and setup transitions.

- Implement replay, reconciliation and meaningful notifications.

## Phase 5: Safety and operational validation

- Implement optional paper-trading simulation if approved for the release.

- Run sustained and spike load tests.

- Test provider failure, stale data, duplicate events and recovery.

- Test database restoration and retention jobs.

- Validate security controls and critical user journeys.

- Confirm the all-in cost and measured capacity before public launch.
