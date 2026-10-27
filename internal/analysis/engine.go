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

	if err := e.reconcileSwing(ctx, txStore, instrumentID, timeframe, SwingHigh, pivot, isHigh, confirmedAt, sourceIDs, now, &state.ReferenceSwingHighID); err != nil {
		return err
	}
	if err := e.reconcileSwing(ctx, txStore, instrumentID, timeframe, SwingLow, pivot, isLow, confirmedAt, sourceIDs, now, &state.ReferenceSwingLowID); err != nil {
		return err
	}
	return nil
}

func (e *Engine) reconcileSwing(ctx context.Context, txStore *Store, instrumentID int64, timeframe string, swingType SwingType, pivot CandleInput, qualifies bool, confirmedAt time.Time, sourceIDs []int64, now time.Time, refID **int64) error {
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
		return err
	default:
		current = &row
	}

	switch decideSwingAction(current, candidate, qualifies) {
	case actionNone:
		return nil // never was a pivot here; still isn't

	case actionInsert:
		inserted, err := txStore.InsertSwing(ctx, candidate)
		if err != nil {
			return err
		}
		id := inserted.ID
		*refID = &id
		return nil

	case actionSkip:
		*refID = &current.ID // unchanged, but still the latest -- keep the pointer current
		return nil

	case actionReplace:
		replaced, err := txStore.SupersedeSwing(ctx, current.ID, now, &candidate)
		if err != nil {
			return err
		}
		*refID = &replaced.ID
		return nil

	default: // actionRetract: a correction retracted this swing
		if _, err := txStore.SupersedeSwing(ctx, current.ID, now, nil); err != nil {
			return err
		}
		if *refID == nil || **refID != current.ID {
			return nil // the retracted swing wasn't the reference pointer
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
			return err
		default:
			*refID = &earlier.ID
		}
		return nil
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
			// Spec section 3: BOS references the swing in the trend's
			// own direction; CHOCH references the swing against it.
			var refs []int64
			switch {
			case state.Direction == DirectionBullish && eventType == EventBOS:
				refs = []int64{high.ID} // bullish continuation: references the high it broke above
			case state.Direction == DirectionBullish && eventType == EventCHOCH:
				refs = []int64{low.ID} // bullish->bearish flip: references the low it broke below
			case state.Direction == DirectionBearish && eventType == EventBOS:
				refs = []int64{low.ID} // bearish continuation: references the low it broke below
			default: // DirectionBearish && EventCHOCH
				refs = []int64{high.ID} // bearish->bullish flip: references the high it broke above
			}
			candidate = &NewStructureEvent{
				InstrumentID: instrumentID, Timeframe: timeframe, Type: eventType,
				DirectionBefore: state.Direction, DirectionAfter: newDirection,
				TriggerCandleID: c.ID, ReferenceSwingIDs: refs,
				EvaluationTime: clock.NewEvaluationTime(now), AlgorithmVersion: e.cfg.AlgorithmVersion,
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

	return snap, nil
}
