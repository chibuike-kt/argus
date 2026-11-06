package setups

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
)

// InvalidateBreached is spec section 15.2's stop-breach invalidation
// (v0.12.1): a separate pass over the just-closed candle, against
// every non-terminal setup of (instrumentID, timeframe), directly
// against each one's OWN recorded stop. Unlike Reconcile, which only
// ever evaluates whichever identity is currently resolved as the live
// POI, this evaluates every non-terminal setup directly, independent
// of whether its own POI is still the one section 13 resolves and
// independent of whether its own live protected anchor still exists.
// That independence is the whole reason this exists: section 7's
// close-through invalidation (v0.12.0) clears the live protected
// anchor on the very candle that breaches it, so by the time Reconcile
// runs for this same candle the live plan for that structure has
// already gone to PlanNone -- which Reconcile always treats as a
// no-op -- and the setup's own persisted record becomes the only place
// left where the breach can still be recognized.
//
// candleOpenTime/candleClose are the just-closed candle's own values.
// A setup is invalidated when candleClose is strictly beyond its own
// snapshot's stop in its own direction (below for bullish, above for
// bearish) AND candleOpenTime is strictly later than the setup's own
// POI formation candle's open_time -- never on or before the candle
// that formed it.
//
// Idempotent and replay-safe for free, not by a second guard here:
// ListNonTerminalForBreachCheck excludes invalidated setups at the
// query itself, so a setup this function (or Reconcile's own
// creation-state path, seeing PlanInvalidated directly) already moved
// to invalidated is simply absent from the next call's result set --
// re-processing the same candle, or a full replay after a cursor wipe,
// appends nothing a second time.
//
// version/asOf/now match Reconcile's own parameters: asOf is the
// evaluation point recorded on the appended event and the updated row
// (the just-closed candle's own close_time, the same asOf the caller's
// own reconcileSetup call used for this candle); now is wall-clock,
// for updated_at only.
func InvalidateBreached(ctx context.Context, db *sql.DB, s *Store, instrumentID int64, timeframe string, candleOpenTime time.Time, candleClose quant.Price, version string, asOf, now time.Time) (invalidatedCount int, err error) {
	candidates, err := s.ListNonTerminalForBreachCheck(ctx, instrumentID, timeframe)
	if err != nil {
		return 0, fmt.Errorf("setups: invalidate breached: %w", err)
	}

	for _, candidate := range candidates {
		if !candleOpenTime.After(candidate.POIOpenTime) {
			continue
		}
		breached, err := stopBreached(candidate.Direction, candidate.Plan.Stop, candleClose)
		if err != nil {
			return invalidatedCount, fmt.Errorf("setups: invalidate breached: setup %d: %w", candidate.ID, err)
		}
		if !breached {
			continue
		}

		if err := invalidateOne(ctx, db, s, candidate.Setup, version, asOf, now); err != nil {
			return invalidatedCount, fmt.Errorf("setups: invalidate breached: setup %d: %w", candidate.ID, err)
		}
		invalidatedCount++
	}
	return invalidatedCount, nil
}

// stopBreached is the direction-aware stop comparison InvalidateBreached
// and section 14's own live PlanInvalidated check apply to the same
// quantity (the recorded stop), just at different times and from
// different sources (a persisted snapshot here, the live protected
// anchor there). direction is never anything but "bullish" or
// "bearish" for a real setup identity (spec section 15.3's identity
// tuple has no third value); a different string is an invariant
// violation, surfaced rather than guessed at (CLAUDE.md rule 3).
func stopBreached(direction string, stop, candleClose quant.Price) (bool, error) {
	switch direction {
	case "bullish":
		return candleClose < stop, nil
	case "bearish":
		return candleClose > stop, nil
	default:
		return false, fmt.Errorf("setups: setup has unknown direction %q", direction)
	}
}

// invalidateOne appends the invalidated event and updates setup's row
// to it, in one transaction -- the same two-step write reconcileNew
// and reconcileExisting each use for a lifecycle transition (an
// InsertEvent, then an Update carrying its id as last_event_id), not a
// second code path that writes events differently. A separate
// transaction per setup, not one shared across every breach this
// candle produces, so one setup's failure never rolls back another's
// already-committed invalidation.
func invalidateOne(ctx context.Context, db *sql.DB, s *Store, setup Setup, version string, asOf, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txStore := s.WithTx(tx)

	ev, err := txStore.InsertEvent(ctx, newSetupEvent{
		SetupID:                 setup.ID,
		Type:                    EventInvalidated,
		Plan:                    setup.Plan,
		POISourceCandleID:       setup.POISourceCandleID,
		POISourceCandleRevision: setup.POISourceCandleRevision,
		EvaluationTime:          asOf,
		AlgorithmVersion:        version,
	})
	if err != nil {
		return err
	}
	if _, err := txStore.Update(ctx, setup.ID, update{
		State:                   StateInvalidated,
		POISourceCandleRevision: setup.POISourceCandleRevision,
		Plan:                    setup.Plan,
		LastEventID:             ev.ID,
		EvaluationTime:          asOf,
		AlgorithmVersion:        version,
	}, now); err != nil {
		return err
	}

	return tx.Commit()
}
