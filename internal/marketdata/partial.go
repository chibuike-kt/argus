package marketdata

import (
	"context"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// PartialAggregate is a derived-interval candle computed from whatever
// 1-minute rows currently exist in the window -- forming or closed,
// complete or not. It exists for exactly one purpose Aggregate
// deliberately refuses: the live "forming" display of a derived
// interval (design doc section 6: "Derived interval forming candles are
// computed server side from the 1 minute forming and closed rows").
// Never used for REST history or any other authoritative read -- those
// go through Aggregate/AggregateRange, which refuse an incomplete
// window rather than guess.
type PartialAggregate struct {
	Window Window
	Open   quant.Price
	High   quant.Price
	Low    quant.Price
	Close  quant.Price
	Volume quant.Qty
	// Revision follows the same derived-revision rule Aggregate uses
	// (1 + sum(revision-1)), computed over whatever rows are present --
	// meaningful only as "this reflects what is known as of now," since
	// the window itself may still be incomplete.
	Revision int32
	// HasData is false when no 1-minute row at all exists yet anywhere
	// in the window -- distinct from a window that has some rows but is
	// not yet complete, which still has meaningful OHLCV.
	HasData bool
}

// AggregatePartial computes w's own PartialAggregate for instrumentID,
// from every current row (forming or closed) with open_time in w,
// ordered by open_time. Never returns IncompleteWindow -- there is no
// such thing as "incomplete" for a partial read, by definition.
func AggregatePartial(ctx context.Context, r candleReader, instrumentID int64, w Window) (PartialAggregate, error) {
	rows, err := r.ListCurrentCandlesInRange(ctx, instrumentID, w.Start.Time(), w.End.Time())
	if err != nil {
		return PartialAggregate{}, fmt.Errorf("marketdata: aggregate partial instrument=%d window=[%s,%s): %w", instrumentID, w.Start, w.End, err)
	}
	return partialFromRows(w, rows)
}

// AggregatePartial is the free function of the same name, bound to this
// store's own candle data -- the method a caller outside this package
// (internal/hub) calls, for exactly the same reason AggregateRange has
// its own bound method: speaks only in this package's own exported
// types, never the unexported candleReader interface or
// sqlc-generated store.Candle.
func (s *Store) AggregatePartial(ctx context.Context, instrumentID int64, w Window) (PartialAggregate, error) {
	return AggregatePartial(ctx, s, instrumentID, w)
}

func partialFromRows(w Window, rows []store.Candle) (PartialAggregate, error) {
	expected, err := w.minutes()
	if err != nil {
		return PartialAggregate{}, err
	}

	present := make(map[time.Time]store.Candle, len(rows))
	for _, row := range rows {
		if !row.OpenTime.Before(w.Start.Time()) && row.OpenTime.Before(w.End.Time()) {
			present[row.OpenTime] = row
		}
	}

	agg := PartialAggregate{Window: w}
	var volume int64
	revision := int32(1)
	first := true
	for i := 0; i < expected; i++ {
		slot := w.Start.Time().Add(time.Duration(i) * time.Minute)
		row, ok := present[slot]
		if !ok {
			continue // a gap, or a minute that has not happened yet: skip, do not guess
		}
		agg.HasData = true
		if first {
			agg.Open = quant.Price(row.Open)
			agg.High = quant.Price(row.High)
			agg.Low = quant.Price(row.Low)
			first = false
		}
		if quant.Price(row.High) > agg.High {
			agg.High = quant.Price(row.High)
		}
		if quant.Price(row.Low) < agg.Low {
			agg.Low = quant.Price(row.Low)
		}
		agg.Close = quant.Price(row.Close)
		volume += row.Volume
		revision += row.Revision - 1
	}
	agg.Volume = quant.Qty(volume)
	agg.Revision = revision
	return agg, nil
}
