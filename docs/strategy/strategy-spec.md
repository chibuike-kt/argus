# ARGUS Strategy Specification

- Version: 0.1.0
- Status: Approved for implementation (batch 1: swings and market structure)
- Last updated: 27 October 2026
- Related: ADR-009 (analysis engine), ADR-011 (immutable decisions), ADR-019 (reproducibility)

This is the versioned, approved specification the deterministic analysis engine is
built against. Rules not stated here are not yet decided and must not be guessed
(see CLAUDE.md and ADR-009). Each numbered decision is approved by the product owner.

## 1. Swing detection

Pivots use strict 2-left / 2-right fractals.

- A swing high is a bar whose high is strictly greater than the highs of the two bars
  immediately to its left and the two immediately to its right.
- A swing low is a bar whose low is strictly lower than the lows of the two bars on each side.
- Equality disqualifies the candidate. A neighbor with an equal high (or low) means the
  bar is not a swing. Equal levels are a liquidity concept, handled separately, not structure.
- A pivot is confirmed only after its two right-hand bars have closed (finalized candles).
  Pivot occurrence time and confirmation time are distinct and both recorded.
- Only confirmed swings feed structural direction and break classification.

## 2. Structural direction

Direction is one of BULLISH, BEARISH, or UNDETERMINED. It is an event-driven state,
not a value recomputed on every pivot.

Establishment (cold start):

- Direction is UNDETERMINED until there are at least two confirmed swing highs and two
  confirmed swing lows.
- The first time the sequence forms a higher high and a higher low (latest confirmed high
  above the prior confirmed high, and latest confirmed low above the prior confirmed low),
  direction becomes BULLISH. A lower high and a lower low makes it BEARISH.
- This first determination is a direction-establishment event. It is not a CHOCH, because
  there is no prior direction to change.
- A mixed or insufficient sequence stays UNDETERMINED.

Transitions (once established):

- Direction is a maintained state. It is extended by BOS and flipped only by a confirmed
  CHOCH (section 3).
- A lone lower high or higher low does not change direction and does not revert it to
  UNDETERMINED. Only a CHOCH flips it.

## 3. Break classification

BOS (break of structure) is a continuation of the current direction. CHOCH (change of
character) is a break against it, and a CHOCH is the only event that authorizes a
direction flip.

Reference levels:

- In BULLISH: BOS is a close strictly above the most recent confirmed swing high.
  CHOCH is a close strictly below the most recent confirmed swing low (flips to BEARISH).
- In BEARISH: BOS is a close strictly below the most recent confirmed swing low.
  CHOCH is a close strictly above the most recent confirmed swing high (flips to BULLISH).
- BOS references the swing in the trend's direction. CHOCH references the swing against it.

## 4. Break threshold

- A break requires a finalized (closed) candle whose close is strictly beyond the reference
  level. A wick beyond the level without a close beyond is not a break. A close exactly at
  the level is not a break.
- No minimum break-distance buffer in this version. A buffer may be added later once
  backtesting shows real false breaks. That would introduce a distance metric (ticks,
  percentage, or ATR-relative) and is deferred together with the ATR decision.

## 5. Determinism and evidence

Every structural event (confirmed swing, direction establishment, BOS, CHOCH) records:

- the structural algorithm version,
- the pivot references it used,
- the source candle revisions it was computed from,
- its evaluation time,
- the resulting event and the direction state before and after.

Corrected market data (a new candle revision) must trigger deterministic recomputation of
the affected structural events. The prior event history is preserved, not overwritten
(ADR-011). The same algorithm version over the same candle revisions must produce identical
events (ADR-019). No look-ahead: an event may use only candle data and pivot confirmations
available at its evaluation time.

## Scope of this version

In scope: single instrument, single timeframe swing detection, structural direction state,
and BOS / CHOCH classification with the threshold and determinism rules above.

Not yet decided, do not implement: higher-timeframe context and multi-timeframe alignment,
supply/demand and support/resistance zones, equal highs and lows, liquidity sweeps, order
blocks, fair value gaps, displacement and ATR, POI retests, mandatory condition grouping,
setup lifecycle, entry / stop / target construction, and event precedence on collision.
These are later batches of this spec.

## Golden fixtures required

The engine is validated against labelled fixtures (ADR-009, ADR-019) covering at least:

- a valid confirmed swing high and swing low, and a candidate disqualified by an equal neighbor,
- pivot confirmation timing (a pivot is not known before its right-hand bars close),
- cold-start direction establishment (UNDETERMINED to BULLISH, and UNDETERMINED to BEARISH),
- a BOS extending an established direction,
- a CHOCH flipping direction,
- a lower high that does not change direction,
- a correction to a source candle that recomputes a structural event, with the prior event preserved.

Each fixture asserts the event and its evidence, not just the decision.
