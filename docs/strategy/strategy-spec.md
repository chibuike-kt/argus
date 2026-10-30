# ARGUS Strategy Specification

- Version: 0.7.1
- Status: Approved for implementation (batch 1: swings and market structure; batch 2: liquidity sweeps and protected structure)
- Last updated: 27 October 2026
- Related: ADR-009 (analysis engine), ADR-011 (immutable decisions), ADR-019 (reproducibility)

This is the versioned, approved specification the deterministic analysis engine is
built against. Rules not stated here are not yet decided and must not be guessed
(see CLAUDE.md and ADR-009). Each numbered decision is approved by the product owner.

## Decision provenance and open items

Every decision below carries a provenance tag, so a reader sees where it comes from, not only
what it is:

- [S] Source-confirmed: explicitly stated in the trader's transcripts or a supplied chart.
- [A] Architecturally necessary: required for determinism, replay, data integrity, or
  correctness, independent of the strategy.
- [R] Recommendation: a reasoned deterministic default where the source is silent or loose.
- [U] Unresolved: a hypothesis that could materially change signals or backtests, not yet
  verified against the source.

A tag records provenance, not confidence. An [R] or [U] is still implemented and deterministic;
it is simply not source-confirmed and may change when verified.

Open items register. These [U]s must be retired before the POI and entry batches read off them:

1. Second fib low anchor (section 9): the swept swing versus the protected-low extreme. Resolve
   with a marked chart showing where the structural-liquidity fib is drawn.
2. Transactional-liquidity update rule (section 10): every sweep-validated BOS, versus only a
   break that forms a new structural leg. Resolve by re-reading transcript 2's
   transactional-liquidity passage.
3. Anchor invalidation (section 7): the direction-flipping CHOCH (shipped) versus a close
   strictly through the protected level itself. Tied to open item 4.
4. Major versus minor break (sections 3 and 7): BOS/CHOCH on the latest confirmed swing
   (shipped) is a deterministic default; the trader states a stronger rule, that a real bullish
   break takes the previous lower high. Resolve by deciding whether to keep the mechanical
   version or implement the lower-high rule.

## 1. Swing detection

_Provenance: [R] the strict 2/2 fractal order (the source says "swings", not an order); [A] confirmation timing and the confirmed-only rule._

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

_Provenance: [S] direction and the BOS/CHOCH concept; [R] the event-driven state and the two-swing establishment rule; [U] open item 4, major versus minor break._

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

_Provenance: [S] BOS as continuation and CHOCH as the reversal; [R] referencing the latest confirmed swing; [U] open item 4._

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

_Provenance: [R] strict close-beyond with no buffer; [A] the determinism. The buffer is deferred with ATR._

- A break requires a finalized (closed) candle whose close is strictly beyond the reference
  level. A wick beyond the level without a close beyond is not a break. A close exactly at
  the level is not a break.
- No minimum break-distance buffer in this version. A buffer may be added later once
  backtesting shows real false breaks. That would introduce a distance metric (ticks,
  percentage, or ATR-relative) and is deferred together with the ATR decision.

## 5. Determinism and evidence

_Provenance: [A] throughout (ADR-011, ADR-019)._

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

_Provenance: [S] sweeps as the precondition; [R] the same-candle, strict-reclaim V1 encoding; [A] the append-only recording._

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

_Provenance: [S] the sweep-then-break sequence (Reading B); [A] two independent anchors; [R] with [U] on invalidation, open item 3._

This section models a sequence of two distinct events on opposite sides of the market, not
a property of a single swing. Liquidity is taken on one side (a sweep, section 6), then
price breaks structure on the other (a break, section 3). The two references are distinct
and the engine must keep them so:

- The protected anchors are the levels the sweeps establish.
- The break reference is the swing a BOS or CHOCH closes beyond, defined exactly as batch 1
  already defines it (sections 3 and 4). The sweep adds a validation condition; it never
  redefines the break target.

Protected anchors (one per side, independent):

- The engine maintains at most one current protected low and at most one current protected
  high, at the same time and fully independently. Both sides' liquidity can be live at once,
  and a later break validates against whichever anchor matches its direction, so the two must
  be able to coexist. Establishing, superseding, or invalidating one anchor never touches the
  other.
- Protected low: a sell-side sweep takes out a swing low and reclaims it; the reversal-origin
  low the sweep prints, once confirmed as a 2/2 fractal swing (section 1), becomes the
  protected low. Protected high: the buy-side mirror, a buy-side sweep of a swing high whose
  reversal-origin high confirms. A raw sweep extreme that never confirms as a fractal becomes
  no anchor.
- Each anchor is a designated level. It remains the protected level on its side as the trend
  extends and newer confirmed swings form. It is not "the latest confirmed swing" on that
  side.
- Invalidation. A protected anchor is invalidated by the CHOCH that flips direction away from
  the side it supports: the bullish protected low is cleared by a CHOCH to bearish, the bearish
  protected high by a CHOCH to bullish (section 3). A CHOCH requires a finalized close strictly
  beyond its reference swing, so a wick through a level never invalidates an anchor. Note that
  the CHOCH reference is the latest confirmed opposing swing, which may sit inside the protected
  range (a higher low above the protected low, or a lower high below the protected high); in
  this version the anchor is cleared on the direction flip itself, not only on a close through
  the protected level. Letting the anchor survive an internal CHOCH until a close goes strictly
  through its own level is a deferred refinement, tied to the major/minor swing distinction and
  not implemented in V1. A CHOCH invalidates only the anchor of the direction being left, never
  the one its own result validates against.
- Supersession. A protected low is replaced only by a newer swing that itself qualifies as a
  protected low under this section: a new sell-side sweep that reclaims and whose
  reversal-origin low confirms as a fractal. The protected high is superseded only by a newer
  qualifying buy-side sweep, independently. An incidental penetration of some other level, or
  a sweep that never produces a confirmed reversal-origin swing, resets nothing.

Sweep-validated break (the sequence):

- A break (BOS or CHOCH, detected exactly as in sections 3 and 4) is sweep-validated when the
  protected anchor for the break's resulting direction exists and is still valid (not
  invalidated or superseded) at the break: a protected low for a bullish result, a protected
  high for a bearish result. The validation reads only that one of the two independent
  anchors and is blind to whichever anchor is on the other side.
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
  exactly as it recomputes swings and events (section 5), with prior history preserved. Both
  protected anchors are reconstructed independently as-of the resume point during a correction
  replay.

## 8. Premium, discount, and equilibrium

_Provenance: [S] discount and premium and the 50% split; [R] the latest-high top anchor; [A] the truncation._

Premium, discount, and equilibrium split the active structure's range into a buy zone and a
sell zone. It is a pure derived classification, computed on demand from stored swing
revisions. This version persists nothing: no table and no new state.

Range:

- Bullish: the range is [protected low, latest confirmed swing high]. The low end is the
  protected low anchor (section 7); the high end is the latest confirmed swing high (section
  1), which moves up as new highs confirm while the protected low stays fixed until it is
  invalidated or superseded. Bearish mirror: [latest confirmed swing low, protected high].
- The range is defined only when the protected anchor on the trend side and the opposing
  latest-confirmed reference swing both exist and form a valid positive range (high strictly
  greater than low). It is undefined when direction is UNDETERMINED, when the required protected
  anchor is absent, or when the endpoints do not form a positive range.
- The engine must never substitute the latest opposing swing for a missing protected anchor.
  Absence of the protected anchor means premium/discount is undefined, not computed from a
  fallback. It is computed only when the protected anchor, the opposing reference swing, the
  direction, and the sweep-validation state are mutually consistent.

Equilibrium and zones:

- Equilibrium is the single 50% level of the range. There is no equilibrium band in this version.
- A price strictly below equilibrium is in discount, strictly above is in premium, and exactly
  at equilibrium is equilibrium, which is neither discount nor premium.
- In a bullish structure the buy zone is discount; in a bearish structure the sell zone is premium.
- The classification is undefined outside a valid structure (see Range).

Computation:

- `equilibrium = low + (high - low) / 2` on scaled int64 prices, integer division truncating
  toward the low. No floating point. The implementation validates `high > low` before computing
  and uses arithmetic that cannot overflow within supported price bounds.

Scope:

- Premium/discount/equilibrium recomputes deterministically from the active swing's two
  endpoints, with no append-only evidence and no new table. It is exposed as a computation for
  downstream POI logic (for example, "is this level in discount"). If a later batch needs the
  equilibrium value preserved as evidence on a POI or event, that batch defines the snapshot and
  provenance; this version stores nothing.

## 9. Structural liquidity

_Provenance: [S] the at-or-below-50% rule and the intact, obvious-shape identity; [R] the establishing-break top anchor and the N=3 proxy; [U] the low anchor, open item 1._

Batch 4, the first of its three liquidity groups. Transactional liquidity and inducement are
later sections; this one covers structural liquidity only. Structural liquidity is the low
(bullish) or high (bearish) the trend is expected to sweep before it continues. This section
identifies it. The sweep-and-continuation mechanic and the "no structural liquidity, no trade"
rule are applied in the setup and entry batch.

The second fib (the structural-liquidity range), a different fib from section 8's
premium/discount range:

- Bullish: drawn from the protected low to the broken swing high, the swing high the
  sweep-validated bullish break closed above (section 7). Bearish: from the protected high to
  the broken swing low.
- [Provisional] The low anchor (bullish) is the protected low, the reversal-origin extreme, not
  the swept swing. This is the one open item still to verify against a marked chart; the
  equilibrium and therefore the qualifying zone depend on it.
- Equilibrium is the 50% level, computed exactly as in section 8 (`low + (high - low) / 2`,
  truncating toward the low). The qualifying half is at or below equilibrium for bullish, at or
  above for bearish.

Identity:

- Bullish structural liquidity is the most recent confirmed swing low, by pivot time, that is
  all of: (a) in the qualifying half of the second fib (price at or below equilibrium),
  (b) intact, and (c) passing the shape test below. Bearish is the mirror: the most recent
  intact confirmed swing high in the premium half passing the shape test.
- Intact means not yet taken: no finalized candle since the swing's confirmation has traded
  below the structural-liquidity low (bullish) or above the structural-liquidity high
  (bearish). Once a finalized candle trades through it, it is no longer intact.

Shape test (the "obvious A or V" proxy):

- A single-candle extreme never qualifies. The swing's two legs, the move from the preceding
  confirmed swing into it and the move out to the following confirmed swing, must each span at
  least N candles, so the low or high is a multi-candle V or A rather than a lone wick.
- [Provisional] N = 3. The rule's shape is locked; the exact N is a backtesting tuning knob.

Classification and the gate:

- A structure has structural liquidity when at least one confirmed swing in the qualifying half
  is intact and passes the shape test; the most recent such swing is the structural liquidity.
  When none exists, the structure is structural-absent, which is the inducement case (a later
  section). This section records structural-present with the identified swing, or
  structural-absent. The tradeability rule "no structural liquidity, no trade" is applied in the
  setup and entry batch, not here.

Determinism and persistence:

- Structural liquidity is derived from stored swings, the active structure, and the candle
  series (for the intact check), so it recomputes deterministically and needs no new table in
  this section. If a later batch records the identified structural liquidity as evidence on a
  setup or POI, that batch defines the snapshot; this section persists nothing.

## 10. Transactional liquidity

_Provenance: [S] the taken-level identity and timing; [R] most-recent-break tracking; [U] the update rule, open item 2._

Batch 4, group two. Transactional liquidity is the level a sweep-validated break takes out, and
taking it is what signals the correction, the pullback back into the zone.

- Identity. Bullish transactional liquidity is the swing high taken out by a sweep-validated
  bullish break, which is that break's reference swing. Bearish is the swing low taken out by a
  sweep-validated bearish break.
- Timing. A level becomes transactional liquidity only when a finalized candle closes strictly
  beyond it as part of a sweep-validated break (sections 4, 7). Until taken it is an ordinary
  confirmed swing, not transactional liquidity; there is no "pending" transactional liquidity,
  so the current transactional liquidity is always already in the taken state.
- Reference selection. Transactional liquidity is the reference level of the most recent
  sweep-validated break in the established trend, and it updates with each successive
  sweep-validated break. It is not pinned to the establishing break. Only a sweep-validated
  break updates it: an unvalidated BOS does not overwrite the current level.
- Distinct from the second fib (section 9). The second fib's top (bullish) is the establishing
  break's reference level; transactional liquidity is the most recent sweep-validated break's
  reference level. In an extended trend these are different levels. Example: the establishing
  bullish break takes out 110, so the second fib's top stays at 110, while a later validated
  break takes out 118, so transactional liquidity updates to 118.
- Role and scope. Taking out transactional liquidity signals the correction phase (price pulls
  back into the zone). The "anticipate the correction and trade the pullback" action is setup
  and entry logic, deferred. Transactional liquidity is derived from the stored break events,
  with no migration and no new persistence; this section exposes the current
  transactional-liquidity level.

## 11. Inducement liquidity

_Provenance: [S] inducement as the structural-absent, expansion case; [A] mutual exclusivity with structural-present._

Batch 4, group three, and the thinnest: it is the structural-absent branch of section 9 with a
name and an exposed level, not a separate detector.

- Identity. Inducement liquidity is the protected anchor of a structure that section 9
  classifies structural-absent. Bullish: the protected low is the inducement, expected to be
  swept down before an up-expansion. Bearish: the protected high.
- Mutual exclusivity. A structure is exactly one of structural-present or inducement, which is
  the section-9 classification already. Inducement adds no new state or detection, only the
  label and the exposed level on the absent branch.
- Role and scope. With no structural liquidity, the protected level is expected to be taken
  (induced) before expansion, so there is no trade at that level until the inducement is swept
  ("no structural liquidity, no trade"). The expansion anticipation and the no-trade gate are
  setup and entry logic, deferred. Inducement is derived, with no migration and no new
  persistence; this section exposes the inducement level (the protected anchor) and the
  structural-present-versus-inducement classification.

## Scope of this version

In scope: single instrument, single timeframe. Batch 1: swing detection, structural
direction state, and BOS / CHOCH classification with the threshold and determinism rules
above. Batch 2: single-candle liquidity sweeps (strict, same-candle reclaim), two independent
protected anchors (one per side, each designated until invalidated), and sweep-validation of
BOS / CHOCH as the opposing-side-sweep-then-break sequence. Direction behaviour from batch 1
is unchanged by batch 2. Batch 3: premium, discount, and equilibrium over the active
structure's range, as a pure derived classification with no persistence. Batch 4: structural
liquidity (section 9), transactional liquidity (section 10), and inducement (section 11), all
derived classifications with no persistence, completing the liquidity layer.

Not yet decided, do not implement: supply/demand and support/resistance zones; equal highs and lows; order blocks;
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
- two independent anchors coexist: a protected high confirming mid-bullish-trend does not disturb the protected low, and a later bullish BOS still validates against that protected low (a single anchor slot fails this),
- the supersession guardrail: an incidental penetration that does not produce a confirmed reversal-origin swing does not reset the protected anchor,
- invalidation: a finalized close through the protected swing flips direction (CHOCH) and ends that anchor's eligibility,
- a protected level that requires fractal confirmation (a raw sweep extreme that never confirms does not become protected),
- a correction to a sweep candle that recomputes the sweep, the protected anchor, and any dependent break tag, with prior history preserved.

Batch 3:

- discount, premium, and exactly-equilibrium classification of prices within a valid bullish range, and the bearish mirror,
- equilibrium value correctness, including an odd `high - low` where truncation toward the low applies,
- undefined when direction is UNDETERMINED, when the protected anchor is absent (with no fallback to the latest opposing swing), and when the endpoints do not form a positive range,
- the range top tracking a newly confirmed higher swing high while the protected low stays fixed, shifting equilibrium accordingly.

Batch 4 (structural liquidity):

- a qualifying bullish structural-liquidity low (most recent intact confirmed swing low in the discount half of the second fib, passing the leg-span shape test) and the bearish mirror,
- a candidate disqualified because it sits above equilibrium (premium half) in a bullish structure,
- a candidate disqualified because it is no longer intact (a finalized candle has traded through it),
- a single-candle or short-leg extreme disqualified by the shape test (each leg must span at least N candles),
- a more recent qualifying swing superseding an earlier one as the structural liquidity,
- structural-absent: no qualifying swing in the zone, classified as the inducement case,
- the classification recomputing deterministically under a candle correction.

Batch 4 (transactional liquidity):

- transactional liquidity is the reference high taken by a sweep-validated bullish break, and the bearish mirror (the reference low),
- it updates to the more recent sweep-validated break's level and is not pinned to the establishing break (the 110-then-118 case), while the second fib's top stays at the establishing break,
- an unvalidated BOS does not update transactional liquidity (the current level is unchanged),
- a swing level not yet taken by a sweep-validated break is not transactional liquidity.

Batch 4 (inducement):

- a structural-absent structure exposes the protected anchor as the inducement level (bullish protected low, bearish protected high),
- a structural-present structure is not inducement (mutual exclusivity).
