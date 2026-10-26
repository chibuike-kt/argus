# Feasibility risks and explicit trade-offs

- Source: ARGUS Master ADR, section 17
- Version: 0.1.0-draft
- Last updated: 26 October 2026

---

The following conflicts must not be hidden by implementation convenience.

## 17.1 Scale versus budget

The eventual target of 1,000 instruments and 1,000 active users may be incompatible with the initial single-host, US$50 monthly deployment.

The target remains an ambition until validated. The project should use phased coverage, workload measurements and controlled limits.

## 17.2 Data rights versus free API access

A provider may expose an API without granting the rights ARGUS needs for a public commercial product.

Provider access and commercial licensing must be evaluated separately.

## 17.3 Historical reproducibility versus storage cost

Reproducible historical decisions require retaining relevant revisions, strategy definitions and evidence. This adds storage and processing cost.

Retention policy must preserve the required audit trail without retaining every raw input indefinitely by default.

## 17.4 Latest quotes versus historical fill accuracy

Latest-only quote persistence cannot provide a complete historical bid/ask record.

Paper-trading and cost estimates must communicate this limitation and avoid overstating their accuracy.

## 17.5 Complete drawing tools versus implementation cost

A custom drawing system provides control but requires significant engineering. A licensed charting solution may reduce that work but introduces access and commercial restrictions.

The required experience is confirmed; the technology choice remains open.

## 17.6 Deterministic analysis versus AI interpretation

AI may explain ambiguity but must not override mandatory deterministic conditions.

The system must be able to reproduce the exact rule-based decision independently of the explanatory text.

## 17.7 Recovery objectives versus backup frequency

A 15-minute RPO requires more than a daily backup. A tested recovery strategy and an affordable mechanism for recovering recent writes are required.
