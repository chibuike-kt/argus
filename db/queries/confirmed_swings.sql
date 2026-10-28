-- name: GetCurrentSwing :one
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND pivot_time = $4
  AND superseded_at IS NULL;

-- GetSwingByID looks up a swing directly by id, regardless of whether it
-- is current or superseded -- used to resolve a structure_state
-- reference pointer (which names a specific row, not a position) back
-- to its price.
-- name: GetSwingByID :one
SELECT * FROM confirmed_swings WHERE id = $1;

-- name: InsertSwing :one
INSERT INTO confirmed_swings (
    instrument_id, timeframe, swing_type, pivot_time, price,
    confirmed_at, evaluation_time, algorithm_version, source_candle_ids
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- The two statements below are the supersede handshake's two steps (see
-- 000005's trg_confirmed_swings_enforce_lifecycle comment). Step 1
-- (begin) removes the old row from idx_confirmed_swings_current, making
-- room for a replacement row if one is being inserted; a pure
-- retraction (no replacement) stops after step 1 and never calls step 2.

-- name: BeginSupersedeSwing :execrows
UPDATE confirmed_swings
SET superseded_at = $2
WHERE id = $1 AND superseded_at IS NULL;

-- name: ResolveSupersedeSwing :execrows
UPDATE confirmed_swings
SET superseded_by = $2
WHERE id = $1 AND superseded_at IS NOT NULL AND superseded_by IS NULL;

-- ListLatestCurrentSwings is bounded by max_pivot_time, not merely "the
-- latest current rows overall": during a correction replay, an
-- unbounded query can pick up a swing the walk has not reconciled yet
-- in this pass (a stale row from before the correction that still
-- happens to be "current" only because the replay has not reached its
-- position to supersede or retract it). max_pivot_time must be the
-- latest position this candle's own processing step is entitled to
-- see -- see decideEstablishmentCandidate's call site for the exact
-- bound and why it is <=, not <.
-- name: ListLatestCurrentSwings :many
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND superseded_at IS NULL
  AND pivot_time <= $4
ORDER BY pivot_time DESC
LIMIT $5;

-- LatestCurrentSwingBefore bounds by pivot_time, which is only a safe
-- "already reconciled by the walk" bound relative to the POSITION
-- currently being reconciled (see reconcileSwing's retraction
-- fallback) -- it is NOT safe as a bound relative to a resume instant
-- like affectedFrom, because confirmation lags occurrence by
-- RightBars+1 intervals. Use LatestCurrentSwingConfirmedBefore for that.
-- name: LatestCurrentSwingBefore :one
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND superseded_at IS NULL
  AND pivot_time < $4
ORDER BY pivot_time DESC
LIMIT 1;

-- LatestCurrentSwingConfirmedBefore bounds by confirmed_at, not
-- pivot_time: a swing only becomes a usable reference once it is
-- CONFIRMED, and confirmation lags its own pivot_time by
-- (RightBars+1)*interval. Used specifically to reconstruct the as-of
-- state a correction's recompute must resume from -- seeding by
-- occurrence instead of confirmation can hand the resumed walk a
-- reference swing that, at the resume instant, was not actually known
-- yet, and that can emit a wrong BOS or CHOCH on the very first
-- replayed candles.
-- name: LatestCurrentSwingConfirmedBefore :one
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND superseded_at IS NULL
  AND confirmed_at <= $4
ORDER BY pivot_time DESC
LIMIT 1;

-- EarliestCurrentSwingAfter finds the swing immediately following a
-- given pivot_time -- the shape test's up-leg endpoint (spec section 9:
-- "this low out to the following confirmed swing high"). Strictly
-- after, mirroring LatestCurrentSwingBefore's strictly-before sibling.
-- Bounded by confirmed_at <= $5: this is the up-leg's point-in-time
-- eligibility bound (not merely "exists"), since a pivot can occur
-- before an evaluation point (asOf) but not confirm until after it --
-- without this bound, a point-in-time caller (a replay or backtest at
-- a historical asOf) could complete a shape test with a swing that, at
-- that instant, was not yet known (no look-ahead, guide 16). This
-- query is new and used only by structural liquidity's shape test, so
-- the bound is added here directly rather than as a separate variant.
-- name: EarliestCurrentSwingAfter :one
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND superseded_at IS NULL
  AND pivot_time > $4 AND confirmed_at <= $5
ORDER BY pivot_time ASC
LIMIT 1;

-- LatestCurrentSwingBeforeKnownAsOf is LatestCurrentSwingBefore's
-- point-in-time-safe sibling: pivot_time < $4 (the candidate's own
-- pivot, for the shape test's down-leg) AND confirmed_at <= $5 (known
-- as of asOf). Once the candidate itself is bounded by confirmed_at <=
-- asOf, the preceding swing is already confirmed by asOf by
-- implication (it pivots strictly earlier still, and confirmation
-- never precedes pivot), so this bound is not load-bearing today -- it
-- is added for symmetry with EarliestCurrentSwingAfter's bound and so
-- the invariant is explicit here rather than accidental.
-- name: LatestCurrentSwingBeforeKnownAsOf :one
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND superseded_at IS NULL
  AND pivot_time < $4 AND confirmed_at <= $5
ORDER BY pivot_time DESC
LIMIT 1;

-- ListLatestCurrentSwingsConfirmedBefore is ListLatestCurrentSwings'
-- point-in-time-safe sibling for structural liquidity's candidate walk
-- specifically -- do not use this for ListLatestCurrentSwings' own
-- caller (decideEstablishmentCandidate), whose max_pivot_time bound is
-- already correct for a live, same-pass walk and must not change.
-- Bounded by BOTH pivot_time <= $4 (the pagination cursor) AND
-- confirmed_at <= $5 (point-in-time eligibility): a swing can pivot
-- before asOf but confirm after it (confirmation lags the pivot by
-- RightBars+1 intervals), and such a swing must never be treated as
-- "the most recent confirmed swing" as of a historical asOf.
-- name: ListLatestCurrentSwingsConfirmedBefore :many
SELECT * FROM confirmed_swings
WHERE instrument_id = $1 AND timeframe = $2 AND swing_type = $3 AND superseded_at IS NULL
  AND pivot_time <= $4 AND confirmed_at <= $5
ORDER BY pivot_time DESC
LIMIT $6;

-- LatestQualifyingProtectedAnchorBefore finds the latest confirmed
-- swing of swing_type that is ALSO a protected-anchor-qualifying
-- position: a current sweep of side exists with trigger_open_time
-- equal to this swing's own pivot_time (the reversal-origin
-- relationship, spec section 7). Bounded by confirmed_at <= $5, the
-- same reason as LatestCurrentSwingConfirmedBefore -- a swing is only
-- usable once confirmed, not merely once it occurs. Used only to
-- reconstruct the protected anchor as of a correction's resume point;
-- the live sweep table is a safe source for "as of" here because
-- nothing strictly before the resume point has been touched by the
-- correction yet when this runs.
-- name: LatestQualifyingProtectedAnchorBefore :one
SELECT cs.* FROM confirmed_swings cs
JOIN liquidity_sweeps ls
    ON ls.instrument_id = cs.instrument_id
   AND ls.timeframe = cs.timeframe
   AND ls.trigger_open_time = cs.pivot_time
WHERE cs.instrument_id = $1 AND cs.timeframe = $2 AND cs.swing_type = $3
  AND cs.superseded_at IS NULL AND cs.confirmed_at <= $4
  AND ls.side = $5 AND ls.superseded_at IS NULL
ORDER BY cs.pivot_time DESC
LIMIT 1;
