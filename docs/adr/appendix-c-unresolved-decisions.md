# Unresolved decisions register

- Source: ARGUS Master ADR, section 16
- Version: 0.1.0-draft
- Last updated: 6 November 2026

---

These items must remain visible in planning until they are resolved or explicitly accepted as trade-offs.

## Resolved

The rows below are kept in the table that follows (nothing is deleted from
this register once it is added), but each of these four has since been
settled elsewhere. This table is the pointer to where; the table below it
is still the full original list.

| Open item | Resolved by |
| --- | --- |
| Charting library and drawing APIs | ADR-008 |
| Event precedence | Spec sections 14 and 15.2 |
| Swing tie policy | Spec section 1 (strict inequality) |
| Re-entry and candidate identity | Spec section 15.3 |

Not included here: ADR-009's POI-category contradiction remains open --
that is an architect/product-owner decision, not a documentation gap, and
is unchanged by this update.

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
| High | Trend and timeframe mapping | Determines how higher-timeframe context qualifies setups | Explicit versioned timeframe profile. Partly decided by D13: setup timeframes 15m and 1h, context one step up; the rest stays open. |
| High | Material plan changes | Prevents excessive revisions or silent updates | Versioned change policy |
| High | Re-entry and candidate identity | Prevents duplicates without suppressing valid setups | Stable identity and re-entry rules |
| High | Instrument-aware sizing | Incorrect contract assumptions distort risk calculations | Verified instrument metadata and formulas |
| High | Freshness and latency thresholds | Needed for trustworthy live analysis | Per-market SLOs and analysis-lag limits |
| High | Load-test acceptance | 1,000 active users and one host are unvalidated | Defined workload and measured pass criteria |
| High | Backup strategy | Daily backup alone does not establish a 15-minute RPO | Recovery design and tested restore |
| Medium | Queue library | Affects worker reliability and operations | Compare options against workload needs |
| Medium | Session and cookie parameters | Security and user experience | Explicit policy and security tests |
| Medium | Retention periods | Storage, privacy and reproducibility trade-offs | Data-class retention matrix |
| Medium | Replay cursor and snapshot protocol | Needed for reliable WebSocket recovery | Versioned protocol and recovery tests |
| Medium | Paper-trading launch timing | Adds cost and simulation complexity | Release-scope decision |
| Medium | Shadow promotion criteria | Prevents unsafe or resource-intensive strategy rollout | Explicit comparison thresholds |
| Medium | Export and deletion details | Privacy and operational requirements | Format, expiry, exceptions and backup policy |

## Strategy spec amendments v0.9.0 (owner decisions of 22 November 2026)

- Decided (D16): one target, the nearest liquidity level at or above 2.0 R:R. Not open.
- [U] Whether a fixed 2R target should be offered later as an alternative to the liquidity target.
- [U] When a setup is finished after its target is reached (the end state, and whether the stop still
  matters after the target). Waiting for the owner's answer. Do not implement.
- [U] Re-evaluate a rejected identity when a candidate at or above `min_rr` appears, or when context
  alignment changes. Measure in L5.
- [U] Minimum stop distance and volatility stop buffer; trading cost estimation. A plan can pass the 2.0
  gate and still be smaller than the fees.
- [U] Whether the UI hides rejected setups by default (proposed: hidden by default, labelled "skipped").
- [U] Confirmation entry (all of 17.8).
- [U] A maximum target distance.
- Known limitation: the context timeframe's mechanical "last major break" may differ from the trader's.
