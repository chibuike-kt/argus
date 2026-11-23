# ADR-002: Deployment model, audience and scale

- Status: Confirmed direction; feasibility validation required
- Category: Product definition and boundaries
- Version: 0.1.0-draft
- Last updated: 22 November 2026
- Amended: 22 November 2026 (D15)
- Related: [ADR-017](ADR-017-availability-backups-and-recovery-objectives.md), [ADR-018](ADR-018-observability-and-load-testing.md)

---

## Context

ARGUS is intended to become a publicly accessible tool, not a private single-user application.

The initial operating budget is constrained, while the desired product must support live analysis, multiple market instruments and concurrent users.

## Decision

The target deployment model is:

- Local development using Docker.

- A hosted environment for the public product.

- Public user registration from the first release.

- A single-host deployment initially.

- A phased instrument rollout.

- A small, curated initial market universe.

- A longer-term target of approximately 1,000 supported instruments.

- A target of approximately 1,000 active concurrent users.

- A core application availability target of 99.5% measured monthly.

- A hard all-in infrastructure and operations budget of approximately US$50 per month.

Amended 22 November 2026 (decision D15): for private research use, the owner may add Hyperliquid
perpetuals to a watchlist from the provider's catalog, which enables them outside production through
one audited operation, and the system tracks at most 8 distinct symbols at once. The small curated
universe remains the rule for any public or multi user launch, which stays subject to ADR-007.

## Required qualification

These are product and operational targets, not demonstrated capacity.

The combination of 1,000 active users, live chart subscriptions, approximately 1,000 eventual instruments, continuous analysis, versioned history and a single host has not yet been validated.

The public launch must not claim these capacity or availability targets until load testing demonstrates acceptable behavior under a defined workload.

## Consequences

The system must support configurable limits, workload prioritization, degradation behavior and incremental capacity increases.

If the requirements cannot fit the budget, the preferred response is to narrow the launch universe or defer expensive capabilities rather than silently weaken licensing, data-quality or safety requirements.
