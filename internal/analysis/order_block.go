package analysis

import (
	"context"
	"errors"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
)

// OrderBlockStatus is section 12's classification: present (with the
// identified source candle and zone) or absent (no qualifying
// opposite-colour candle in the impulse window). This is a defined,
// legitimate classification -- distinct from "undefined" (ResolveOrderBlock's
// ok == false), which means there was no break or no protected anchor to
// anchor the search to at all.
type OrderBlockStatus int

const (
	// OrderBlockAbsent means the impulse window held no qualifying
	// opposite-colour candle.
	OrderBlockAbsent OrderBlockStatus = iota
	// OrderBlockPresent means the result identifies the order-block candle.
	OrderBlockPresent
)

func (s OrderBlockStatus) String() string {
	if s == OrderBlockPresent {
		return "order-block-present"
	}
	return "order-block-absent"
}

// OrderBlock is the result of classifying the active structure's order
// block (spec section 12). The candle/zone fields are only meaningful
// when Status == OrderBlockPresent.
type OrderBlock struct {
	Status    OrderBlockStatus
	Direction Direction
	// BreakEventID is the sweep-validated break (B's event) this order
	// block is anchored to.
	BreakEventID int64
	// ProtectedSwingID is the current protected anchor used as the
	// impulse window's floor (spec section 12: "[R] the protected-anchor
	// floor" -- a modeling choice, not yet source-confirmed).
	ProtectedSwingID int64
	// SourceCandleID and SourceRevision name the specific current candle
	// row the order block was identified from -- current by open_time,
	// not by this id, since a correction mints a new row id for the
	// same position (the same discipline confirmed_swings/structure_events
	// use; see 000007's comment on structure_events). SourceRevision is
	// candles.revision, carried through for correction-tracking evidence.
	SourceCandleID int64
	SourceRevision int32
	// Low and High are the zone boundaries: the full candle range.
	// [R] modeling choice (spec section 12), not yet source-confirmed --
	// body-only (open-to-close) is the other established convention; a
	// marked chart showing the zone drawn open-to-close would override
	// this.
	Low  quant.Price
	High quant.Price
}

// ResolveOrderBlock resolves the active structure's order block (spec
// section 12): the origin candle of the impulse that produced the most
// recent sweep-validated break -- the last opposite-colour candle
// before that impulse. ok is false -- undefined, never OrderBlockAbsent
// -- when:
//   - there is no active structure (direction is UNDETERMINED),
//   - no sweep-validated break has occurred yet in the current trend
//     ([S] identity: this is the exact same break transactional
//     liquidity tracks, section 10), or
//   - the trend-side protected anchor is currently absent (read
//     directly from state via secondFibLowAnchor/secondFibHighAnchor,
//     the same single, clearly-named sources section 9's second fib
//     uses -- [R] the protected-anchor floor is a modeling choice: the
//     floor is the structure's origin, so the search never crosses it
//     into an earlier, unrelated leg).
//
// Point-in-time: asOf bounds which break is already known (the same
// LatestSweepValidatedEventKnownAsOf transactional liquidity uses,
// bounded by the trigger candle's close_time <= asOf). Every window
// candle is strictly before B's open_time, which is itself strictly
// before B's close_time <= asOf, so the window is safe by construction;
// the explicit close_time <= asOf check in the walk below is defensive,
// not load-bearing, kept because section 12 states the discipline
// explicitly rather than leaving it implied.
func ResolveOrderBlock(ctx context.Context, store *Store, candles CandleSource, state StateSnapshot, asOf time.Time) (OrderBlock, bool, error) {
	if state.Direction == DirectionUndetermined {
		return OrderBlock{}, false, nil
	}

	event, err := store.LatestSweepValidatedEventKnownAsOf(ctx, state.InstrumentID, state.Timeframe, state.Direction, asOf)
	switch {
	case errors.Is(err, ErrEventNotFound):
		return OrderBlock{}, false, nil
	case err != nil:
		return OrderBlock{}, false, err
	}

	var anchorID *int64
	switch state.Direction {
	case DirectionBullish:
		anchorID = secondFibLowAnchor(state)
	case DirectionBearish:
		anchorID = secondFibHighAnchor(state)
	}
	if anchorID == nil {
		return OrderBlock{}, false, nil
	}
	anchor, err := store.GetSwingByID(ctx, *anchorID)
	if err != nil {
		return OrderBlock{}, false, err
	}

	// Impulse window: [floor pivot_time, B's open_time), floor
	// inclusive, B exclusive -- B is the move's last candle, not a
	// candidate for its own origin.
	window, err := candles.ListCandles(ctx, state.InstrumentID, anchor.PivotTime.Time(), event.TriggerOpenTime)
	if err != nil {
		return OrderBlock{}, false, err
	}

	// Walk most-recent-first: window is ascending by open_time (the
	// CandleSource contract), so iterate it in reverse. The first
	// opposite-colour candle found is the order block; a doji
	// (close == open) or same-colour candle is part of the impulse run
	// and is skipped, never stopping or qualifying the walk.
	for i := len(window) - 1; i >= 0; i-- {
		c := window[i]
		if !c.IsClosed || c.CloseTime.After(asOf) {
			continue
		}
		switch state.Direction {
		case DirectionBullish:
			if c.Close < c.Open {
				return newOrderBlock(state.Direction, event.ID, anchor.ID, c), true, nil
			}
		case DirectionBearish:
			if c.Close > c.Open {
				return newOrderBlock(state.Direction, event.ID, anchor.ID, c), true, nil
			}
		}
	}

	return OrderBlock{
		Status: OrderBlockAbsent, Direction: state.Direction,
		BreakEventID: event.ID, ProtectedSwingID: anchor.ID,
	}, true, nil
}

func newOrderBlock(direction Direction, breakEventID, protectedSwingID int64, c CandleInput) OrderBlock {
	return OrderBlock{
		Status: OrderBlockPresent, Direction: direction,
		BreakEventID: breakEventID, ProtectedSwingID: protectedSwingID,
		SourceCandleID: c.ID, SourceRevision: c.Revision,
		Low: quant.Price(c.Low), High: quant.Price(c.High),
	}
}
