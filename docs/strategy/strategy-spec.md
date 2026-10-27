# ARGUS Strategy Specification

- Version: 0.3.0
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

Liquidity-side glossary (fixed for the whole spec). Sell-side liquidity rests below swing
lows (resting sell stops); a sell-side sweep takes it out and precedes bullish moves.
Buy-side liquidity rests above swing highs; a buy-side sweep takes it out and precedes
bearish moves. The sweep is named for the liquidity it takes, not for the trade direction
it sets up. In code, the sweep side is represented by the level it takes (a sweep of a low
is sell-side, a sweep of a high is buy-side); this glossary fixes the mapping so the two
namings never drift.

A liquidity sweep is a single finalized candle that takes a confirmed swing level and then
closes back inside it. It is the precondition the strategy requires before a structural
break is treated as a sweep-validated swing structure (section 7).

- Target level. A sweep is evaluated against the latest confirmed swing of the relevant
  side relative to the candle: the most recent confirmed swing low (for a sell-side sweep)
  or swing high (for a buy-side sweep) whose confirmation time is at or before the candle.
  Only confirmed swings (section 1) are sweep targets.
- Sell-side sweep of a swing low at price L: `candle.low < L` and `candle.close > L`. Both strict.
- Buy-side sweep of a swing high at price H: `candle.high > H` and `candle.close < H`. Both strict.
- Penetration may be by wick or body; the reclaim is decided only by the close. The open and
  the body are not consulted. A candle that merely touches the level (`low == L` or
  `high == H`) is not a sweep, and a close exactly on the level is not a reclaim.
- Reclaim window is the same candle only. The penetration and the close back inside must
  both occur on the one finalized candle. No multi-candle or delayed reclaim in this version.
- A sweep records: the swept swing reference, the sweep candle and its revision, the side
  (sell-side or buy-side), the penetration extreme and the reclaiming close, the algorithm
  version, and the evaluation time.

## 7. Protected structure and sweep-validated breaks

This section models a sequence of two distinct events on opposite sides of the market, not
a property of a single swing. Liquidity is taken on one side (a sweep, section 6), then
price breaks structure on the other (a break, section 3). The two references are distinct
and the engine must keep them so:

- The protected swing is the anchor the sweep establishes.
- The break reference is the swing a BOS or CHOCH closes beyond, defined exactly as batch 1
  already defines it (sections 3 and 4). The sweep adds a validation condition; it never
  redefines the break target.

Protected swing (the anchor):

- Bullish: a sell-side sweep takes out a swing low and reclaims it. The reversal-origin low
  the sweep prints, once confirmed as a 2/2 fractal swing (section 1), becomes the protected
  low. Bearish mirror: a buy-side sweep of a swing high, whose reversal-origin high becomes
  the protected high. The raw sweep-candle extreme that never confirms as a fractal does not
  become a protected swing.
- The protected swing is a designated anchor. It remains the protected level as the trend
  extends and newer confirmed swings form. It is not "the latest confirmed opposing swing."
- Invalidation. The protected swing is invalidated only when a finalized candle closes
  strictly through it (below a protected low, above a protected high). That close is the
  CHOCH that flips direction (section 3).
- Supersession. A protected anchor is replaced only by a newer swing that itself qualifies
  as a protected anchor under this section: a new opposing-side sweep that reclaims and
  whose reversal-origin swing confirms as a fractal. An incidental penetration of some other
  level, or a sweep that never produces a confirmed reversal-origin swing, does not reset
  protection.

Sweep-validated break (the sequence):

- A break (BOS or CHOCH, detected exactly as in sections 3 and 4) is sweep-validated when it
  completes the sequence: a valid protected anchor exists on the opposite side from the
  break, that anchor is still valid (not invalidated or superseded) at the break, and the
  break is in the anchor's direction.
  - Bullish: a sell-side sweep established a protected low; a later finalized candle closes
    strictly above the latest confirmed swing high.
  - Bearish: a buy-side sweep established a protected high; a later finalized candle closes
    strictly below the latest confirmed swing low.
- The sweep and the break are on opposite sides and are generally different candles and
  different levels. Breaking a swing that was itself the swept level does not by itself
  satisfy this sequence: a break is sweep-validated only when a valid opposing-side protected
  anchor exists, never merely because the broken swing happens to carry a same-side sweep.
- No candle-count window. The sweep stays eligible to validate a break until its protected
  anchor is invalidated or superseded, as defined above. Eligibility is governed by structure
  events, not elapsed candles.
- Direction logic (section 2) is unchanged. A break updates direction (BOS extends, CHOCH
  flips) whether or not it is sweep-validated. The tag is additional evidence recorded on the
  event: whether it is sweep-validated, and when it is, the sweep event and the protected
  swing it references. Whether an unvalidated break is tradeable is decided in a later batch
  (POI and entry).
- Determinism. Sweeps, protected anchors, and the validation tag are a pure function of
  finalized candles and confirmed swings, so a candle correction recomputes all of them
  exactly as it recomputes swings and events (section 5), with prior history preserved. The
  protected anchor is reconstructed as-of the resume point during a correction replay.

## Scope of this version

In scope: single instrument, single timeframe. Batch 1: swing detection, structural
direction state, and BOS / CHOCH classification with the threshold and determinism rules
above. Batch 2: single-candle liquidity sweeps (strict, same-candle reclaim), the protected
anchor as designated-until-invalidated state, and sweep-validation of BOS / CHOCH as the
opposing-side-sweep-then-break sequence. Direction behaviour from batch 1 is unchanged by
batch 2.

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

- a sell-side sweep (penetration below a confirmed swing low, close back above) and the buy-side mirror, each recorded with the correct side per the section 6 glossary,
- a candle that only touches the level (no strict penetration) is not a sweep,
- a candle that penetrates and closes beyond the level (no reclaim) is not a sweep,
- a two-candle penetrate-then-reclaim sequence is not a sweep in this version (same-candle only),
- a sweep-validated bullish break: a sell-side sweep establishes a protected low, then a later close above the latest confirmed swing high; the event is tagged validated, referencing that sweep and the protected low. Bearish mirror,
- the explicit negative case: a break of a swing that was itself the swept level, with no valid opposing-side protected anchor, is NOT sweep-validated (this is the discarded same-swing reading and must fail to validate),
- a BOS or CHOCH with no qualifying opposing-side sweep, tagged not sweep-validated, with direction still updated,
- a protected anchor that persists as the trend extends: a later same-direction BOS still references the original protected swing until it is invalidated,
- the supersession guardrail: an incidental penetration that does not produce a confirmed reversal-origin swing does not reset the protected anchor,
- invalidation: a finalized close through the protected swing flips direction (CHOCH) and ends that anchor's eligibility,
- a protected level that requires fractal confirmation (a raw sweep extreme that never confirms does not become protected),
- a correction to a sweep candle that recomputes the sweep, the protected anchor, and any dependent break tag, with prior history preserved.
