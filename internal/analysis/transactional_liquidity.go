package analysis

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ResolveTransactionalLiquidity resolves the active structure's
// transactional liquidity (spec section 10): the reference swing of
// the most recent current, sweep-validated break whose direction_after
// is the current direction (bullish: the broken high; bearish: the
// broken low). ok is false -- a distinct undefined result, never an
// error -- when:
//   - there is no active structure (direction is UNDETERMINED), or
//   - no sweep-validated break has occurred yet in the current trend.
//
// Point-in-time: asOf bounds which breaks are already known (the
// break's trigger candle must have closed at or before asOf), the same
// discipline as the structural-liquidity look-ahead fix -- callers pass
// the evaluation point explicitly (guide 13/16).
//
// Deliberately NOT pinned to the establishing break (the one
// ResolveSecondFib uses for section 9's second fib top): it tracks
// every successive sweep-validated break, so the two diverge in an
// extended trend (spec section 10's 110-then-118 example). Only
// sweep-validated breaks update it -- the sweep_validated filter on
// the underlying query means an unvalidated BOS is never selected.
func ResolveTransactionalLiquidity(ctx context.Context, store *Store, state StateSnapshot, asOf time.Time) (Swing, bool, error) {
	if state.Direction == DirectionUndetermined {
		return Swing{}, false, nil
	}

	event, err := store.LatestSweepValidatedEventKnownAsOf(ctx, state.InstrumentID, state.Timeframe, state.Direction, asOf)
	switch {
	case errors.Is(err, ErrEventNotFound):
		return Swing{}, false, nil
	case err != nil:
		return Swing{}, false, err
	}
	if len(event.ReferenceSwingIDs) == 0 {
		// Invariant violation, not a legitimate "undefined" outcome: a
		// sweep-validated event is always a BOS or CHOCH (establishment
		// is never tagged), and every BOS/CHOCH records exactly one
		// reference swing id (decideBreak's single refID). CLAUDE.md
		// rule 3 -- stop and flag rather than guess at a substitute.
		return Swing{}, false, fmt.Errorf("analysis: sweep-validated event %d has no reference swing id", event.ID)
	}

	swing, err := store.GetSwingByID(ctx, event.ReferenceSwingIDs[0])
	if err != nil {
		return Swing{}, false, err
	}
	return swing, true, nil
}
