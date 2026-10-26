# ADR-017: Availability, backups and recovery objectives

- Status: Targets confirmed; feasibility validation required
- Category: Quality, reliability and operations
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-002](ADR-002-deployment-model-audience-and-scale.md), [ADR-018](ADR-018-observability-and-load-testing.md), [ADR-020](ADR-020-database-migrations-and-release-management.md)

---

## Decision

The operational targets are:

| Metric | Target |
| --- | --- |
| Core application availability | 99.5% monthly |
| Recovery point objective (RPO) | 15 minutes |
| Recovery time objective (RTO) | 4 hours |
| Monthly all-in budget | Approximately US$50 maximum |
| Deployment | Single host initially |
| Backup policy | Daily backups, plus point-in-time recovery if affordable |

Market-data freshness and analysis lag must be measured separately from core application availability.

A healthy API does not mean the market data is fresh or the analysis engine is keeping up.

## Backup conflict

Daily backups alone cannot guarantee a 15-minute RPO for arbitrary database changes.

The 15-minute RPO therefore depends on a validated recovery mechanism, such as affordable point-in-time recovery or an equivalent durable recovery strategy.

If this cannot be achieved within the budget, the conflict must be surfaced for explicit resolution. The system must not claim the target without evidence.

## Recovery procedure

The recovery process must cover:

- Database restore.

- Application redeployment.

- Queue and worker recovery.

- Market-data gap reconciliation.

- Analysis reevaluation where required.

- WebSocket client resynchronization.

- Validation of freshness and operational health.

Backups are not considered sufficient until restores have been tested.
