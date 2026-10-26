# ADR-007: Provider selection and commercial data rights

- Status: Open; research and validation required
- Category: Market-data architecture
- Version: 0.1.0-draft
- Last updated: 26 October 2026
- Related: [ADR-006](ADR-006-market-data-contracts-and-canonical-representation.md), [ADR-002](ADR-002-deployment-model-audience-and-scale.md)

---

## Context

ARGUS requires crypto perpetual-futures data and major forex data while supporting public users and staying within a hard all-in budget of approximately US$50 per month.

Technical API access does not necessarily grant the right to display, store, cache, redistribute or commercially exploit the resulting data.

## Confirmed requirements

- Both crypto and forex are included at public launch.

- Crypto coverage begins with a curated set of perpetual futures.

- Forex coverage begins with major pairs.

- The initial universe is small and curated.

- Instruments are enabled in phases.

- Full required historical candle coverage is a hard gate for each enabled launch instrument.

- Bid/ask access and market-specific freshness are hard gates.

- Written evidence of suitable commercial rights is required.

- Provider selection uses hard gates followed by a comparative scorecard.

- If requirements cannot be met within budget, ARGUS narrows the launch rather than silently relaxing requirements.

- Provider degradation comes before unvalidated automatic failover.

## Provider evaluation criteria

The evaluation must verify:

- Supported crypto perpetual venues and major forex pairs.

- One-minute candle history and the requested two-year coverage.

- Historical completeness for every launch instrument.

- Live bid/ask availability and rate limits.

- Data freshness and reliability.

- API request and subscription limits.

- Storage, caching and correction policies.

- Public display and commercial redistribution rights.

- Rights to derived analytics and chart annotations.

- Pricing and any separate licensing fees.

- Provider outages, recovery and historical backfill behavior.

- Compatibility with the all-in budget.

## Preliminary research status

Initial documentation review suggests that direct crypto-venue APIs may be viable for a curated launch, but commercial data rights still require verification.

Several forex options expose API-based quotes or historical candles, but their public redistribution rights and total commercial costs have not been established as compatible with ARGUS.

No provider combination has yet passed all hard gates.

## Decision rule

No provider will be selected solely on API availability, low price or technical convenience.

If no compliant combination meets the budget and product requirements, the project must document the trade-off and obtain approval for a narrower launch or a changed budget.

Automatic failover should be introduced only after a licensed secondary provider has been validated.
