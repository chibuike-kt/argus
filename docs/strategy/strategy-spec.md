# ARGUS Strategy Specification

- Version: 0.2.0
- Status: Approved for implementation (batch 1: swings and market structure; batch 2: liquidity sweeps and protected structure)
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

## 6. Liquidity sweeps

A liquidity sweep is a single finalized candle that takes a confirmed swing level and then
closes back inside it. It is the precondition the strategy requires before a structural
break is treated as a tradeable swing structure (used from batch 2 onward).

- Target level. A sweep is evaluated against the latest confirmed swing of the opposing
  side relative to the candle: the most recent confirmed swing low (for a buy-side sweep)
  or swing high (for a sell-side sweep) whose confirmation time is at or before the candle.
  Only confirmed swings (section 1) are sweep targets.
- Buy-side sweep of a swing low at price L: `candle.low < L` and `candle.close > L`. Both strict.
- Sell-side sweep of a swing high at price H: `candle.high > H` and `candle.close < H`. Both strict.
- Penetration may be by wick or body; the reclaim is decided only by the close. The open and
  the body are not consulted. A candle that merely touches the level (`low == L` or
  `high == H`) is not a sweep, and a close exactly on the level is not a reclaim.
- Reclaim window is the same candle only. The penetration and the close back inside must
  both occur on the one finalized candle. No multi-candle or delayed reclaim in this version.
- A sweep records: the swept swing reference, the sweep candle and its revision, the side
  (buy-side or sell-side), the penetration extreme and the reclaiming close, the algorithm
  version, and the evaluation time.

## 7. Protected structure

A protected level is the low (bullish) or high (bearish) that a sweep creates and that a
subsequent break makes structurally significant. It is the anchor the rest of the strategy
measures from in later batches (premium / discount, stop placement, structural liquidity).

- The protected low is the reversal-origin low: the confirmed swing low formed by the
  sweep, that is, the new swing low printed when price took out the prior swing low and
  reversed up. It sits below the swing low that was swept. The protected high is the mirror
  for bearish.
- The protected level must be a confirmed 2/2 fractal swing (section 1), not the raw
  sweep-candle extreme. A sweep extreme that never confirms as a fractal swing does not
  become a protected level.
- A structural break (section 3) is sweep-validated when its protected level is a sweep low
  (bullish) or a sweep high (bearish): the swing the break is built from was itself created
  by a sweep. Because validation is defined through the protected level and not through a
  time gap, there is no separate "candles between sweep and break" window.
- Direction logic (section 2) is unchanged by this batch. A CHOCH still flips direction and
  a BOS still extends it whether or not the break is sweep-validated. Every CHOCH and BOS is
  additionally tagged with whether it is sweep-validated and, when it is, with the sweep
  event and protected-level swing it references. The sweep tag is evidence, not a new gate on
  direction. Whether an unvalidated break is tradeable is decided in a later batch (POI and
  entry).
- Determinism. Sweeps and protected levels are a pure function of finalized candles and
  confirmed swings, so a candle correction recomputes them exactly as it recomputes swings
  and events (section 5), with prior history preserved.

## Scope of this version

In scope: single instrument, single timeframe. Batch 1: swing detection, structural
direction state, and BOS / CHOCH classification with the threshold and determinism rules
above. Batch 2: single-candle liquidity sweeps (strict, same-candle reclaim), protected
levels (reversal-origin confirmed swing), and sweep-tagging of BOS / CHOCH. Direction
behaviour from batch 1 is unchanged by batch 2.

Not yet decided, do not implement: premium / discount / equilibrium; liquidity
classification (transactional, structural, inducement) and structural-liquidity validity
rules; supply/demand and support/resistance zones; equal highs and lows; order blocks;
fair value gaps; displacement and ATR; POI (order block and QMR) and retests; higher-
timeframe context and multi-timeframe alignment; mandatory condition grouping; setup
lifecycle; entry / stop / target construction; and event precedence on collision. These are
later batches of this spec, in roughly that order.

## Golden fixtures required

The engine is validated against labelled fixtures (ADR-009, ADR-019). Each fixture asserts
the event and its evidence, not just the decision.

Batch 1:

- a valid confirmed swing high and swing low, and a candidate disqualified by an equal neighbor,
- pivot confirmation timing (a pivot is not known before its right-hand bars close),
- cold-start direction establishment (UNDETERMINED to BULLISH, and UNDETERMINED to BEARISH),
- a BOS extending an established direction,
- a CHOCH flipping direction,
- a lower high that does not change direction,
- a correction to a source candle that recomputes a structural event, with the prior event preserved.

Batch 2:

- a buy-side sweep (penetration below a confirmed swing low, close back above) and the sell-side mirror,
- a candle that only touches the level (no strict penetration) is not a sweep,
- a candle that penetrates and closes beyond the level (no reclaim) is not a sweep,
- a two-candle penetrate-then-reclaim sequence is not a sweep in this version (same-candle only),
- a sweep-validated CHOCH and a sweep-validated BOS, each tagging its sweep event and protected-level swing,
- a BOS or CHOCH with no qualifying prior sweep, tagged as not sweep-validated, with direction still updated,
- a protected level that requires fractal confirmation (a raw sweep extreme that never confirms does not become protected),
- a correction to a sweep candle that recomputes the sweep and any dependent protected-level tag, with prior history preserved.
