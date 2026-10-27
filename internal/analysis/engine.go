package analysis

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
)

// Engine runs the swing and market-structure algorithm for one
// instrument and timeframe. EvaluateRange is the one implementation
// incremental evaluation, batch replay, and correction recompute all
// share (ADR-009): nothing in it branches on which of the three it is
// being used for. The distinction lives entirely in what range the
// caller passes and, for a correction, in HandleCandleCorrection's
// as-of state reconstruction before that range is replayed.
type Engine struct {
	db      *sql.DB
	candles CandleSource
	store   *Store
	clk     clock.Clock
	cfg     Config
}

// NewEngine wires an Engine. db is used to begin the per-candle atomic
// transaction (store, being a *Store built over the same db, is used
// inside it via WithTx); candles and clk are the consumer-defined
// boundaries (guide 4, 7, 13).
func NewEngine(db *sql.DB, candles CandleSource, analysisStore *Store, clk clock.Clock, cfg Config) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Engine{db: db, candles: candles, store: analysisStore, clk: clk, cfg: cfg}, nil
}

// OnCandleClosed is EvaluateRange's convenience wrapper for the live,
// one-candle-at-a-time case.
func (e *Engine) OnCandleClosed(ctx context.Context, instrumentID int64, timeframe string, c CandleInput) error {
	return e.EvaluateRange(ctx, instrumentID, timeframe, c.OpenTime, c.OpenTime.Add(e.cfg.Interval))
}

// EvaluateRange processes every closed candle with open_time in
// [start, end), in order, confirming swings and emitting structure
// events as their conditions are met (spec sections 1-3). It reads a
// left-padded window from CandleSource so pivots near start have full
// fractal context, and begins from whatever structure_state currently
// holds for (instrumentID, timeframe) -- correct for ordinary forward
// use, and exactly what HandleCandleCorrection overwrites first for a
// correction's replay.
func (e *Engine) EvaluateRange(ctx context.Context, instrumentID int64, timeframe string, start, end time.Time) error {
	padding := time.Duration(e.cfg.LeftBars+e.cfg.RightBars) * e.cfg.Interval
	readStart := start.Add(-padding)

	candles, err := e.candles.ListCandles(ctx, instrumentID, readStart, end)
	if err != nil {
		return fmt.Errorf("analysis: evaluate range instrument=%d/%s [%s,%s): list candles: %w", instrumentID, timeframe, start, end, err)
	}

	// byTime, not array position, is how every window lookup in this
	// engine locates a neighbor: the candle series can have gaps (a
	// real, expected condition -- see the ingestion milestone), and
	// array-index arithmetic would silently misalign across one.
	byTime := make(map[time.Time]CandleInput, len(candles))
	for _, c := range candles {
		byTime[c.OpenTime] = c
	}

	state, err := e.store.GetState(ctx, instrumentID, timeframe)
	if err != nil {
		return fmt.Errorf("analysis: evaluate range: get state: %w", err)
	}

	// The main loop walks only the candles actually present in
	// [start, end), in ascending time order -- "process every closed
	// candle that exists", not "assume every minute slot is populated".
	for _, c := range candles {
		if c.OpenTime.Before(start) {
			continue // left padding; context only, never processed itself
		}
		if !c.OpenTime.Before(end) {
			break
		}
		if !c.IsClosed {
			break // the live edge: nothing after this can be closed either
		}
		if err := e.step(ctx, instrumentID, timeframe, byTime, c, &state); err != nil {
			return fmt.Errorf("analysis: evaluate range: candle open_time=%s: %w", c.OpenTime, err)
		}
	}
	return nil
}

// step is the per-candle atomic unit (approved review point: the swing
// change, the structure-event change, and the state update commit or
// roll back together, exactly once per candle).
func (e *Engine) step(ctx context.Context, instrumentID int64, timeframe string, byTime map[time.Time]CandleInput, c CandleInput, state *StateSnapshot) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txStore := e.store.WithTx(tx)

	now := e.clk.Now()

	// Step 1: the pivot confirmed by c closing is at time
	// c.OpenTime - RightBars*interval, not LeftBars*interval -- c is
	// that pivot's LAST right-hand neighbor, which is what makes it
	// confirmable now, and using the wrong bar count here would
	// silently use the wrong window under any algorithm version where
	// LeftBars != RightBars.
	pivotTime := c.OpenTime.Add(-time.Duration(e.cfg.RightBars) * e.cfg.Interval)
	if err := e.applyPivotStep(ctx, txStore, instrumentID, timeframe, byTime, pivotTime, now, state); err != nil {
		return fmt.Errorf("pivot step: %w", err)
	}

	// Step 1.5: liquidity sweeps (spec section 6), using candle c's own
	// high/low/close -- same-candle, no RightBars lag, unlike pivot
	// confirmation. Runs after step 1 so a swing step 1 just confirmed
	// at this exact candle (confirmed_at == c.CloseTime) is a legal
	// sweep target, consistent with decideEstablishmentCandidate's own
	// "<=, not <" bound for the same reason.
	if err := e.applySweepStep(ctx, txStore, instrumentID, timeframe, c, now); err != nil {
		return fmt.Errorf("sweep step: %w", err)
	}

	// Step 2: break/establishment using candle c's own close, reading
	// state AFTER step 1's updates. Deliberate (approved review point):
	// a close can break a swing that step 1 just confirmed two bars
	// back, on this same candle.
	if err := e.applyBreakStep(ctx, txStore, instrumentID, timeframe, c, now, state); err != nil {
		return fmt.Errorf("break step: %w", err)
	}

	candleID := c.ID
	state.LastProcessedCandleID = &candleID
	if err := txStore.SetState(ctx, *state, now); err != nil {
		return fmt.Errorf("set state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// applyPivotStep decides the deterministic verdict for the pivot
// position at pivotTime and reconciles it against whatever is
// currently stored, covering all five outcomes symmetrically: nothing
// stored and it now qualifies (insert); stored and identical (skip,
// idempotent); stored and different (supersede-and-replace); stored
// and no longer qualifies (retract, no replacement); nothing stored and
// it still doesn't qualify (no-op). It updates state's reference
// pointers whenever a swing of that type is (re)confirmed.
//
// "Cannot evaluate yet" (pivotWindow's ok=false: a gap, or not enough
// right-hand context in this read) is deliberately not plumbed through
// to a retraction -- a gap in THIS read is not evidence the position no
// longer qualifies, only that there is not enough information to say,
// so applyPivotStep simply returns without touching any existing stored
// swing at this position.
func (e *Engine) applyPivotStep(ctx context.Context, txStore *Store, instrumentID int64, timeframe string, byTime map[time.Time]CandleInput, pivotTime time.Time, now time.Time, state *StateSnapshot) error {
	window, ok := pivotWindow(byTime, pivotTime, e.cfg.LeftBars, e.cfg.RightBars, e.cfg.Interval)
	if !ok {
		return nil
	}

	isHigh, isLow := detectPivot(window, e.cfg.LeftBars, e.cfg.RightBars)
	pivot := window[e.cfg.LeftBars]
	confirmedAt := window[len(window)-1].CloseTime
	sourceIDs := make([]int64, len(window))
	for i, c := range window {
		sourceIDs[i] = c.ID
	}

	priorHighID, currentHighID, err := e.reconcileSwing(ctx, txStore, instrumentID, timeframe, SwingHigh, pivot, isHigh, confirmedAt, sourceIDs, now, &state.ReferenceSwingHighID)
	if err != nil {
		return err
	}
	if err := e.reconcileProtectedAnchor(ctx, txStore, instrumentID, timeframe, SwingHigh, pivot.OpenTime, priorHighID, currentHighID, state); err != nil {
		return err
	}

	priorLowID, currentLowID, err := e.reconcileSwing(ctx, txStore, instrumentID, timeframe, SwingLow, pivot, isLow, confirmedAt, sourceIDs, now, &state.ReferenceSwingLowID)
	if err != nil {
		return err
	}
	if err := e.reconcileProtectedAnchor(ctx, txStore, instrumentID, timeframe, SwingLow, pivot.OpenTime, priorLowID, currentLowID, state); err != nil {
		return err
	}
	return nil
}

// reconcileSwing additionally returns the swing id that was current at
// this exact position before this call (nil if none) and the swing id
// current after (nil if none remains) -- reconcileProtectedAnchor needs
// position identity across this call without a second query.
func (e *Engine) reconcileSwing(ctx context.Context, txStore *Store, instrumentID int64, timeframe string, swingType SwingType, pivot CandleInput, qualifies bool, confirmedAt time.Time, sourceIDs []int64, now time.Time, refID **int64) (priorID, currentID *int64, err error) {
	price := pivot.High
	if swingType == SwingLow {
		price = pivot.Low
	}
	candidate := NewSwing{
		InstrumentID:     instrumentID,
		Timeframe:        timeframe,
		Type:             swingType,
		PivotTime:        clock.NewEventTime(pivot.OpenTime),
		Price:            quant.Price(price),
		ConfirmedAt:      clock.NewEventTime(confirmedAt),
		EvaluationTime:   clock.NewEvaluationTime(now),
		AlgorithmVersion: e.cfg.AlgorithmVersion,
		SourceCandleIDs:  sourceIDs,
	}

	row, err := txStore.GetCurrentSwing(ctx, instrumentID, timeframe, swingType, candidate.PivotTime)
	var current *Swing
	switch {
	case errors.Is(err, ErrSwingNotFound):
		current = nil
	case err != nil:
		return nil, nil, err
	default:
		current = &row
		id := current.ID
		priorID = &id
	}

	switch decideSwingAction(current, candidate, qualifies) {
	case actionNone:
		return priorID, nil, nil // never was a pivot here; still isn't

	case actionInsert:
		inserted, err := txStore.InsertSwing(ctx, candidate)
		if err != nil {
			return priorID, nil, err
		}
		id := inserted.ID
		*refID = &id
		return priorID, &id, nil

	case actionSkip:
		*refID = &current.ID // unchanged, but still the latest -- keep the pointer current
		return priorID, priorID, nil

	case actionReplace:
		replaced, err := txStore.SupersedeSwing(ctx, current.ID, now, &candidate)
		if err != nil {
			return priorID, nil, err
		}
		*refID = &replaced.ID
		id := replaced.ID
		return priorID, &id, nil

	default: // actionRetract: a correction retracted this swing
		if _, err := txStore.SupersedeSwing(ctx, current.ID, now, nil); err != nil {
			return priorID, nil, err
		}
		if *refID == nil || **refID != current.ID {
			return priorID, nil, nil // the retracted swing wasn't the reference pointer
		}
		// It was. The new reference is whatever is still current and
		// strictly earlier than this position -- NOT "re-query the
		// latest overall", which could pick up a later position's
		// stale, not-yet-reconciled-in-this-walk row (this walk
		// processes positions in increasing time order, so anything at
		// or after candidate.PivotTime has either already been handled
		// this step or hasn't been reached yet; bounding by pivot_time
		// strictly before excludes both correctly).
		earlier, err := txStore.LatestSwingBefore(ctx, instrumentID, timeframe, swingType, candidate.PivotTime.Time())
		switch {
		case errors.Is(err, ErrSwingNotFound):
			*refID = nil
		case err != nil:
			return priorID, nil, err
		default:
			*refID = &earlier.ID
		}
		return priorID, nil, nil
	}
}

// applySweepStep decides the deterministic verdict for each side's
// liquidity sweep against candle c (spec section 6) and reconciles each
// against whatever is currently stored for (c, side), with the same
// five-outcome symmetry as applyPivotStep and applyBreakStep.
func (e *Engine) applySweepStep(ctx context.Context, txStore *Store, instrumentID int64, timeframe string, c CandleInput, now time.Time) error {
	if err := e.reconcileSweep(ctx, txStore, instrumentID, timeframe, SweepLow, SwingLow, c, now); err != nil {
		return err
	}
	if err := e.reconcileSweep(ctx, txStore, instrumentID, timeframe, SweepHigh, SwingHigh, c, now); err != nil {
		return err
	}
	return nil
}

// reconcileSweep is applySweepStep's per-side worker. targetType is the
// swing type side takes (SweepLow takes a swing low, SweepHigh a swing
// high -- spec section 6 glossary). The target level is the latest
// confirmed swing of that type with
// confirmed_at <= c.CloseTime -- "known" by the time c's own close is
// evaluated, the same bound and the same reason as
// decideEstablishmentCandidate's maxPivotTime: an unbounded or
// occurrence-bounded lookup could read a swing a correction replay has
// not reconciled yet in this pass.
func (e *Engine) reconcileSweep(ctx context.Context, txStore *Store, instrumentID int64, timeframe string, side SweepSide, targetType SwingType, c CandleInput, now time.Time) error {
	target, err := txStore.LatestSwingConfirmedBefore(ctx, instrumentID, timeframe, targetType, c.CloseTime)
	var candidate *NewSweep
	switch {
	case errors.Is(err, ErrSwingNotFound):
		candidate = nil // nothing confirmed to sweep against yet
	case err != nil:
		return err
	default:
		if extreme, reclaim, ok := detectSweep(side, int64(target.Price), c.High, c.Low, c.Close); ok {
			candidate = &NewSweep{
				InstrumentID: instrumentID, Timeframe: timeframe, Side: side,
				SweptSwingID: target.ID, TriggerOpenTime: c.OpenTime, TriggerCandleID: c.ID,
				PenetrationExtreme: quant.Price(extreme), ReclaimClose: quant.Price(reclaim),
				EvaluationTime: clock.NewEvaluationTime(now), AlgorithmVersion: e.cfg.AlgorithmVersion,
			}
		}
	}

	row, err := txStore.GetCurrentSweepByTrigger(ctx, instrumentID, timeframe, c.OpenTime, side)
	var current *Sweep
	switch {
	case errors.Is(err, ErrSweepNotFound):
		current = nil
	case err != nil:
		return err
	default:
		current = &row
	}

	switch decideSweepAction(current, candidate) {
	case actionNone, actionSkip:
		return nil
	case actionInsert:
		_, err := txStore.InsertSweep(ctx, *candidate)
		return err
	case actionReplace:
		_, err := txStore.SupersedeSweep(ctx, current.ID, now, candidate)
		return err
	default: // actionRetract: a correction means this candle no longer sweeps
		_, err := txStore.SupersedeSweep(ctx, current.ID, now, nil)
		return err
	}
}

// reconcileProtectedAnchor updates state's single protected-anchor slot
// for one swing position, immediately after that position's own swing
// reconciliation (spec section 7, "Reading B"). This is the
// establishment/supersession half of the anchor's lifecycle;
// invalidation is a separate, break-driven check in applyBreakStep.
//
// swingType is the position just reconciled; anchorSide/sweepSide are
// what that type would create if it qualifies (SwingLow -> ProtectedLow
// via a SweepLow/"sell-side" sweep; SwingHigh -> ProtectedHigh via
// SweepHigh/"buy-side"). priorID/currentID are reconcileSwing's own
// before/after ids at this exact position -- needed because the anchor
// is a single shared slot: establishing or superseding a NEW qualifying
// position always wins (the walk processes positions in strictly
// increasing time order, so anything reached here is, by construction,
// newer than whatever the slot already held), but clearing the slot on
// a disqualification must only happen when the slot is currently
// pointing AT THIS EXACT position, never at some other swing of the
// same type.
func (e *Engine) reconcileProtectedAnchor(ctx context.Context, txStore *Store, instrumentID int64, timeframe string, swingType SwingType, pivotTime time.Time, priorID, currentID *int64, state *StateSnapshot) error {
	anchorSide := ProtectedLow
	sweepSide := SweepLow
	if swingType == SwingHigh {
		anchorSide = ProtectedHigh
		sweepSide = SweepHigh
	}

	qualifies := false
	if currentID != nil {
		_, err := txStore.GetCurrentSweepByTrigger(ctx, instrumentID, timeframe, pivotTime, sweepSide)
		switch {
		case errors.Is(err, ErrSweepNotFound):
			qualifies = false
		case err != nil:
			return err
		default:
			qualifies = true
		}
	}

	if qualifies {
		id := *currentID
		side := anchorSide
		state.ProtectedSwingID = &id
		state.ProtectedSwingSide = &side
		return nil
	}

	if state.ProtectedSwingSide != nil && *state.ProtectedSwingSide == anchorSide &&
		state.ProtectedSwingID != nil && priorID != nil && *state.ProtectedSwingID == *priorID {
		state.ProtectedSwingID = nil
		state.ProtectedSwingSide = nil
	}
	return nil
}

// decideSweepValidation resolves spec section 7's validation sequence
// for a break resulting in newDirection: sweep-validated iff a valid
// protected anchor currently exists on the side OPPOSITE newDirection
// -- a bullish result needs a protected low (from a sell-side sweep),
// a bearish result needs a protected high (from a buy-side sweep).
// This is Reading B's central correction over the discarded reading:
// the anchor checked here is never the swing this break itself broke
// -- it is whatever the engine's own persistent anchor state currently
// holds, which is generally a different level on the opposite side.
// sweepEventID is found by the same reversal-origin relationship
// establishment relies on (the anchor's own pivot_time, the matching
// sweep side); this query is safe at this point in the walk for the
// same reason LatestSwingBefore's retraction fallback is: the anchor's
// pivot_time is strictly earlier than the candle being processed, so
// the walk has already reconciled it.
func (e *Engine) decideSweepValidation(ctx context.Context, txStore *Store, instrumentID int64, timeframe string, state *StateSnapshot, newDirection Direction) (validated bool, sweepEventID, protectedSwingID *int64, err error) {
	wantSide := ProtectedLow
	sweepSide := SweepLow
	if newDirection == DirectionBearish {
		wantSide = ProtectedHigh
		sweepSide = SweepHigh
	}
	if newDirection == DirectionUndetermined ||
		state.ProtectedSwingID == nil || state.ProtectedSwingSide == nil || *state.ProtectedSwingSide != wantSide {
		return false, nil, nil, nil
	}

	anchor, err := txStore.GetSwingByID(ctx, *state.ProtectedSwingID)
	if err != nil {
		return false, nil, nil, err
	}
	sweep, err := txStore.GetCurrentSweepByTrigger(ctx, instrumentID, timeframe, anchor.PivotTime.Time(), sweepSide)
	switch {
	case errors.Is(err, ErrSweepNotFound):
		return false, nil, nil, nil
	case err != nil:
		return false, nil, nil, err
	}
	sweepID, swingID := sweep.ID, anchor.ID
	return true, &sweepID, &swingID, nil
}

// invalidateProtectedAnchor clears state's protected anchor when it is
// on the side that supported the direction a CHOCH just flipped away
// from (spec section 7: invalidation is specifically tied to a CHOCH,
// never a BOS, since only a CHOCH represents a close through the
// regime being left). fromDirection is the CHOCH's DirectionBefore --
// deliberately not DirectionAfter, which is the side decideSweepValidation
// just checked for this same event: the two are different anchors by
// construction (a CHOCH always changes direction), so there is no
// ordering conflict between validating and invalidating in the same step.
func invalidateProtectedAnchor(state *StateSnapshot, fromDirection Direction) {
	invalidatedSide := ProtectedLow
	if fromDirection == DirectionBearish {
		invalidatedSide = ProtectedHigh
	}
	if state.ProtectedSwingSide != nil && *state.ProtectedSwingSide == invalidatedSide {
		state.ProtectedSwingID = nil
		state.ProtectedSwingSide = nil
	}
}

// applyBreakStep decides the deterministic verdict for candle c's close
// (direction establishment, BOS, or CHOCH) and reconciles it against
// whatever is currently stored for c as a trigger candle, with the same
// five-outcome symmetry as applyPivotStep -- including retraction
// (approved review point): if c previously triggered an event and, under
// the current reference swings, it no longer does, that event is
// superseded with no replacement, not left in place.
func (e *Engine) applyBreakStep(ctx context.Context, txStore *Store, instrumentID int64, timeframe string, c CandleInput, now time.Time, state *StateSnapshot) error {
	if !c.IsClosed {
		return nil
	}

	var candidate *NewStructureEvent
	switch state.Direction {
	case DirectionUndetermined:
		c2, err := e.decideEstablishmentCandidate(ctx, txStore, instrumentID, timeframe, c, now)
		if err != nil {
			return err
		}
		candidate = c2
	default:
		if state.ReferenceSwingHighID == nil || state.ReferenceSwingLowID == nil {
			break // established but missing a reference pointer: nothing to break yet
		}
		high, err := txStore.GetSwingByID(ctx, *state.ReferenceSwingHighID)
		if err != nil {
			return err
		}
		low, err := txStore.GetSwingByID(ctx, *state.ReferenceSwingLowID)
		if err != nil {
			return err
		}
		newDirection, eventType, ok := decideBreak(state.Direction, int64(high.Price), int64(low.Price), c.Close)
		if ok {
			// Spec section 3 (batch 1, unchanged by Reading B): BOS
			// references the swing in the trend's own direction; CHOCH
			// references the swing against it. This is the break
			// target, never the sweep-validation input -- section 7's
			// whole point is that those are generally different levels.
			var refID int64
			switch {
			case state.Direction == DirectionBullish && eventType == EventBOS:
				refID = high.ID // bullish continuation: references the high it broke above
			case state.Direction == DirectionBullish && eventType == EventCHOCH:
				refID = low.ID // bullish->bearish flip: references the low it broke below
			case state.Direction == DirectionBearish && eventType == EventBOS:
				refID = low.ID // bearish continuation: references the low it broke below
			default: // DirectionBearish && EventCHOCH
				refID = high.ID // bearish->bullish flip: references the high it broke above
			}

			// Reading B (spec section 7): validated iff a valid anchor
			// exists on the side OPPOSITE this break's own result --
			// never derived from refID above.
			validated, sweepEventID, protectedSwingID, err := e.decideSweepValidation(ctx, txStore, instrumentID, timeframe, state, newDirection)
			if err != nil {
				return err
			}
			candidate = &NewStructureEvent{
				InstrumentID: instrumentID, Timeframe: timeframe, Type: eventType,
				DirectionBefore: state.Direction, DirectionAfter: newDirection,
				TriggerCandleID: c.ID, ReferenceSwingIDs: []int64{refID},
				EvaluationTime: clock.NewEvaluationTime(now), AlgorithmVersion: e.cfg.AlgorithmVersion,
				SweepValidated: validated, SweepEventID: sweepEventID, ProtectedSwingID: protectedSwingID,
			}

			// Invalidation: only a CHOCH ends an anchor's eligibility,
			// clearing whichever side supported the regime just left
			// (DirectionBefore, i.e. state.Direction here -- state has
			// not been updated yet in this function).
			if eventType == EventCHOCH {
				invalidateProtectedAnchor(state, state.Direction)
			}
		}
	}

	row, err := txStore.GetCurrentEvent(ctx, instrumentID, timeframe, c.ID)
	var current *StructureEvent
	switch {
	case errors.Is(err, ErrEventNotFound):
		current = nil
	case err != nil:
		return err
	default:
		current = &row
	}

	switch decideEventAction(current, candidate) {
	case actionNone:
		return nil
	case actionInsert:
		inserted, err := txStore.InsertEvent(ctx, *candidate)
		if err != nil {
			return err
		}
		state.Direction = inserted.DirectionAfter
		id := inserted.ID
		state.LastEventID = &id
		return nil
	case actionSkip:
		state.Direction = current.DirectionAfter
		return nil
	case actionReplace:
		replaced, err := txStore.SupersedeEvent(ctx, current.ID, now, candidate)
		if err != nil {
			return err
		}
		state.Direction = replaced.DirectionAfter
		id := replaced.ID
		state.LastEventID = &id
		return nil
	default: // actionRetract
		if _, err := txStore.SupersedeEvent(ctx, current.ID, now, nil); err != nil {
			return err
		}
		// state.Direction is deliberately left unchanged here, not reset
		// from current.DirectionBefore: this walk processes candles in
		// strict chronological order and nothing but this function ever
		// writes state.Direction, so the value already in state on entry
		// to this call IS the correct pre-this-candle direction, by
		// construction. Reading it back from the stale row being
		// retracted would be redundant at best, and the wrong source of
		// truth if it ever disagreed.
		return nil
	}
}

// decideEstablishmentCandidate returns the direction-establishment
// event that should exist for candle c, or nil if the cold-start
// conditions (spec section 2) are not yet met -- which requires at
// least two confirmed swings of each type to even be evaluable, not
// just a mixed sequence. A real store error is returned, never treated
// the same as "not enough swings yet": those are different outcomes.
//
// maxPivotTime bounds both swing queries to positions confirmable by c
// -- c.OpenTime - RightBars*interval, the same position step 1 just
// reconciled earlier in this same step (hence <=, not <: that position
// is fully written by the time step 2 runs). Without this bound, a
// correction replay that lands back in Undetermined could read a
// stale, not-yet-reconciled-in-this-pass future swing left over from
// before the correction and establish a direction prematurely -- the
// same class of bug reconcileSwing's retraction fallback already
// guards against for a different query.
func (e *Engine) decideEstablishmentCandidate(ctx context.Context, txStore *Store, instrumentID int64, timeframe string, c CandleInput, now time.Time) (*NewStructureEvent, error) {
	maxPivotTime := c.OpenTime.Add(-time.Duration(e.cfg.RightBars) * e.cfg.Interval)

	highs, err := txStore.ListLatestSwings(ctx, instrumentID, timeframe, SwingHigh, maxPivotTime, 2)
	if err != nil {
		return nil, err
	}
	if len(highs) < 2 {
		return nil, nil
	}
	lows, err := txStore.ListLatestSwings(ctx, instrumentID, timeframe, SwingLow, maxPivotTime, 2)
	if err != nil {
		return nil, err
	}
	if len(lows) < 2 {
		return nil, nil
	}
	// ListLatestSwings orders pivot_time DESC: index 0 is latest, 1 is prior.
	direction, ok := decideEstablishment(int64(highs[1].Price), int64(highs[0].Price), int64(lows[1].Price), int64(lows[0].Price))
	if !ok {
		return nil, nil
	}
	return &NewStructureEvent{
		InstrumentID: instrumentID, Timeframe: timeframe, Type: EventDirectionEstablished,
		DirectionBefore: DirectionUndetermined, DirectionAfter: direction,
		TriggerCandleID:   c.ID,
		ReferenceSwingIDs: []int64{highs[1].ID, highs[0].ID, lows[1].ID, lows[0].ID},
		EvaluationTime:    clock.NewEvaluationTime(now), AlgorithmVersion: e.cfg.AlgorithmVersion,
	}, nil
}

// HandleCandleCorrection is called when the candle at correctedOpenTime
// (instrument, timeframe) has been superseded by a new revision. It is
// not a second algorithm: it reconstructs the as-of state the replay
// must resume from, overwrites the live projection with it, and then
// calls EvaluateRange exactly as any other caller would.
func (e *Engine) HandleCandleCorrection(ctx context.Context, instrumentID int64, timeframe string, correctedOpenTime time.Time) error {
	// RightBars, not LeftBars: a pivot at p depends on candles
	// [p-LeftBars, p+RightBars], so correctedOpenTime at position c can
	// affect p when c-RightBars <= p <= c+LeftBars -- the earliest
	// affected p is c-RightBars. Consistent with step's pivotIdx formula.
	affectedFrom := correctedOpenTime.Add(-time.Duration(e.cfg.RightBars) * e.cfg.Interval)

	asOf, err := e.reconstructStateAsOf(ctx, instrumentID, timeframe, affectedFrom)
	if err != nil {
		return fmt.Errorf("analysis: handle candle correction: reconstruct as-of state: %w", err)
	}
	now := e.clk.Now()
	if err := e.store.SetState(ctx, asOf, now); err != nil {
		return fmt.Errorf("analysis: handle candle correction: seed as-of state: %w", err)
	}

	latest, err := e.candles.LatestClosed(ctx, instrumentID)
	if err != nil {
		return fmt.Errorf("analysis: handle candle correction: latest closed candle: %w", err)
	}

	return e.EvaluateRange(ctx, instrumentID, timeframe, affectedFrom, latest.CloseTime)
}

// reconstructStateAsOf returns the StateSnapshot that was true
// immediately before asOf, read from the (still-current, unaffected)
// event log -- never from the live structure_state row, which reflects
// the full history including whatever is about to be superseded.
//
// The reference-swing lookups are bounded by confirmed_at, not
// pivot_time: a swing only becomes a usable reference once it is
// CONFIRMED, and confirmation lags occurrence by (RightBars+1)*interval.
// A swing with pivot_time just before asOf but confirmed_at at or after
// asOf did not exist as a reference at the resume instant; seeding it
// anyway can emit a wrong BOS or CHOCH on the very first replayed
// candles. The event lookup has no equivalent lag -- a structure event
// becomes known exactly at its trigger candle's close, synchronously,
// so bounding by the trigger candle's open_time (LatestEventBefore's
// own bound) is already correct.
func (e *Engine) reconstructStateAsOf(ctx context.Context, instrumentID int64, timeframe string, asOf time.Time) (StateSnapshot, error) {
	snap := coldStart(instrumentID, timeframe)

	event, err := e.store.LatestEventBefore(ctx, instrumentID, timeframe, asOf)
	switch {
	case errors.Is(err, ErrEventNotFound):
		// No event before asOf: direction is whatever it was before any
		// event ever fired -- undetermined, the cold-start default.
	case err != nil:
		return StateSnapshot{}, err
	default:
		snap.Direction = event.DirectionAfter
		snap.LastEventID = &event.ID
	}

	if high, err := e.store.LatestSwingConfirmedBefore(ctx, instrumentID, timeframe, SwingHigh, asOf); err == nil {
		snap.ReferenceSwingHighID = &high.ID
	} else if !errors.Is(err, ErrSwingNotFound) {
		return StateSnapshot{}, err
	}
	if low, err := e.store.LatestSwingConfirmedBefore(ctx, instrumentID, timeframe, SwingLow, asOf); err == nil {
		snap.ReferenceSwingLowID = &low.ID
	} else if !errors.Is(err, ErrSwingNotFound) {
		return StateSnapshot{}, err
	}

	protectedSwingID, protectedSwingSide, err := e.reconstructProtectedAnchorAsOf(ctx, instrumentID, timeframe, asOf)
	if err != nil {
		return StateSnapshot{}, err
	}
	snap.ProtectedSwingID = protectedSwingID
	snap.ProtectedSwingSide = protectedSwingSide

	return snap, nil
}

// reconstructProtectedAnchorAsOf rebuilds structure_state's single
// protected-anchor slot as it stood immediately before asOf (spec
// section 7) -- the subtle part of Reading B's correction path, since
// the anchor is not simply "the latest confirmed swing of some type"
// the way reference swings are.
//
//  1. Find the latest qualifying candidate on each side: a confirmed
//     swing, confirmed_at <= asOf (the same lag-aware bound as the
//     reference-swing lookups above, for the same reason), whose own
//     pivot_time has a current sweep of the matching side. Establishment
//     and supersession both happen at swing confirmation, so whichever
//     candidate has the LATER confirmed_at is whichever side most
//     recently won the single slot, regardless of which side it is --
//     if the walk had reached a later qualifying position than this on
//     either side, that position would itself be the latest candidate,
//     not this one, by construction.
//  2. That candidate survived to asOf unless a CHOCH flipping away from
//     the direction it supports occurred strictly after its own
//     confirmed_at and strictly before asOf. If one did, the slot was
//     cleared there and nothing has re-established since -- otherwise
//     that later establishment would itself have been the latest
//     candidate found in step 1, a contradiction.
func (e *Engine) reconstructProtectedAnchorAsOf(ctx context.Context, instrumentID int64, timeframe string, asOf time.Time) (*int64, *ProtectedSwingSide, error) {
	type anchorCandidate struct {
		swing    Swing
		side     ProtectedSwingSide
		supports Direction
	}
	var best *anchorCandidate

	if lowAnchor, err := e.store.LatestQualifyingProtectedAnchorBefore(ctx, instrumentID, timeframe, SwingLow, SweepLow, asOf); err == nil {
		best = &anchorCandidate{swing: lowAnchor, side: ProtectedLow, supports: DirectionBullish}
	} else if !errors.Is(err, ErrSwingNotFound) {
		return nil, nil, err
	}

	if highAnchor, err := e.store.LatestQualifyingProtectedAnchorBefore(ctx, instrumentID, timeframe, SwingHigh, SweepHigh, asOf); err == nil {
		if best == nil || highAnchor.ConfirmedAt.Time().After(best.swing.ConfirmedAt.Time()) {
			best = &anchorCandidate{swing: highAnchor, side: ProtectedHigh, supports: DirectionBearish}
		}
	} else if !errors.Is(err, ErrSwingNotFound) {
		return nil, nil, err
	}

	if best == nil {
		return nil, nil, nil
	}

	invalidated, err := e.store.LatestInvalidatingChochBefore(ctx, instrumentID, timeframe, best.supports, best.swing.ConfirmedAt.Time(), asOf)
	if err != nil {
		return nil, nil, err
	}
	if invalidated {
		return nil, nil, nil
	}

	id := best.swing.ID
	side := best.side
	return &id, &side, nil
}
