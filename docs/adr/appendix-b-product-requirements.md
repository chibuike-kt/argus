# Consolidated product requirements

- Source: ARGUS Master ADR, section 15
- Version: 0.1.0-draft
- Last updated: 26 October 2026

---

This section records cross-cutting requirements that apply to the architecture and must not be lost when individual ADRs are split into separate files.

# 15.1 Market coverage

- Both crypto perpetual futures and major forex pairs are included at public launch.

- The initial universe is small and curated.

- Instrument coverage expands in phases.

- A launch instrument requires verified historical coverage and data rights.

- Shared upstream ingestion serves per-user watchlist subscriptions.

- Tiered, configurable watchlist and usage limits are enforced server-side.

# 15.2 Data correctness

- Canonical market data is normalized.

- Missing candles are marked as gaps.

- Candle corrections are versioned.

- Data freshness is market-specific.

- Stale or insufficient evidence blocks confirmation.

- Corrected data triggers targeted reevaluation.

- Historical decisions remain reproducible using the original available evidence.

# 15.3 Analytical correctness

- Deterministic rules control eligibility and confirmation.

- AI is interpretive and advisory.

- Multi-timeframe relationships are explicit.

- Required confluences are evaluated deterministically.

- Conflicts remain visible.

- Candidate and setup lifecycle events are idempotent.

- Strategy versions are immutable.

- A new strategy version is tested in shadow mode before explicit promotion.

# 15.4 Chart experience

- Live charts support LONG, SHORT, INFO LINE, TREND LINE, RECTANGLE, FIB and TEXT.

- ARGUS automatically creates evidence-linked drawings.

- Users can independently create and edit drawings.

- Automatic and user-created drawings have separate ownership.

- Drawing changes do not silently alter strategy decisions.

- Historical annotations and amendments remain traceable.

- The charting library is still unselected.

# 15.5 Realtime experience

- WebSockets deliver live updates.

- Cursor replay recovers missed events where retained history allows.

- Reconciliation restores authoritative state when replay alone is insufficient.

- Notifications are generated from committed events.

- Delivery is at-least-once with deduplication.

- Failed deliveries are retried and eventually dead-lettered for operational handling.

# 15.6 Privacy and access

- Public registration with verified email.

- Optional MFA.

- User-visible session management.

- Immediate session revocation, including realtime access.

- Private watchlists.

- Deny-by-default authorization.

- Future organization readiness without organization workspaces at launch.

- No public API keys or real-execution credentials in the initial release.

- Export and deletion workflows.

- Automated, auditable retention enforcement.
