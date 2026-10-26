# Unresolved decisions register

- Source: ARGUS Master ADR, section 16
- Version: 0.1.0-draft
- Last updated: 26 October 2026

---

These items must remain visible in planning until they are resolved or explicitly accepted as trade-offs.

| Priority | Open item | Why it matters | Required resolution |
| --- | --- | --- | --- |
| Critical | Crypto and forex providers | Determines data availability, licensing and operating cost | Evidence-backed provider scorecard |
| Critical | Commercial data rights | Public display and redistribution may require separate agreements | Written, applicable rights |
| Critical | $50 all-in feasibility | Data, hosting, backups, monitoring and email all consume budget | Costed operating plan |
| Critical | Charting library and drawing APIs | Determines whether required tools can be implemented within budget and license terms | Technical and licensing comparison |
| Critical | Initial strategy conditions | Confirmation cannot be implemented reliably without precise rules | Versioned strategy specification |
| Critical | Event precedence | Confirmation, expiry and invalidation may coincide | Deterministic transition rules |
| High | Swing tie policy | Affects structure, POIs and reproducibility | Exact deterministic algorithm |
| High | Base and impulse thresholds | Affects supply/demand detection | Numerical parameters and fixtures |
| High | ATR and FVG thresholds | Affects displacement and confluence | Numerical parameters and edge cases |
| High | Trend and timeframe mapping | Determines how higher-timeframe context qualifies setups | Explicit versioned timeframe profile |
| High | Material plan changes | Prevents excessive revisions or silent updates | Versioned change policy |
| High | Re-entry and candidate identity | Prevents duplicates without suppressing valid setups | Stable identity and re-entry rules |
| High | Instrument-aware sizing | Incorrect contract assumptions distort risk calculations | Verified instrument metadata and formulas |
| High | Freshness and latency thresholds | Needed for trustworthy live analysis | Per-market SLOs and analysis-lag limits |
| High | Load-test acceptance | 1,000 active users and one host are unvalidated | Defined workload and measured pass criteria |
| High | Backup strategy | Daily backup alone does not establish a 15-minute RPO | Recovery design and tested restore |
| Medium | Queue library | Affects worker reliability and operations | Compare options against workload needs |
| Medium | Database migration tool | Needed for safe schema evolution | Compare tools before selection |
| Medium | Session and cookie parameters | Security and user experience | Explicit policy and security tests |
| Medium | Retention periods | Storage, privacy and reproducibility trade-offs | Data-class retention matrix |
| Medium | Replay cursor and snapshot protocol | Needed for reliable WebSocket recovery | Versioned protocol and recovery tests |
| Medium | Paper-trading launch timing | Adds cost and simulation complexity | Release-scope decision |
| Medium | Shadow promotion criteria | Prevents unsafe or resource-intensive strategy rollout | Explicit comparison thresholds |
| Medium | Export and deletion details | Privacy and operational requirements | Format, expiry, exceptions and backup policy |
