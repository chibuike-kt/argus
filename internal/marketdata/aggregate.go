package marketdata

import (
	"context"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// Window is an already-aligned aggregation window: [Start, End). Alignment
// to a particular derived interval's boundary (UTC midnight for crypto, a
// session boundary for forex) is the calendar's responsibility, not
// Aggregate's: this is pure candle math over an explicit window, so a
// future session-aware calendar can supply different boundaries without
// Aggregate changing. UTCWindow below is a correct helper only for
// continuous markets (crypto) today -- it must not be reused for forex
// once a session-aware calendar exists (flagged, not built).
type Window struct {
	Start clock.EventTime
	End   clock.EventTime // exclusive
}

// Minutes returns the number of whole 1-minute slots in w, or an error if
// w is not a positive, whole-minute-aligned range.
func (w Window) minutes() (int, error) {
	d := w.End.Time().Sub(w.Start.Time())
	if d <= 0 {
		return 0, fmt.Errorf("marketdata: window end %s is not after start %s", w.End, w.Start)
	}
	if d%time.Minute != 0 {
		return 0, fmt.Errorf("marketdata: window duration %s is not a whole number of minutes", d)
	}
	return int(d / time.Minute), nil
}

// UTCWindow returns the Window of length d starting at the UTC boundary
// obtained by truncating t down to a multiple of d since the Unix epoch
// (time.Time.Truncate semantics). This is correct for continuous markets
// (crypto) only: it is UTC-midnight-equivalent alignment, not
// session-based, and must not be used for forex's 5m/15m/1h/4h/1d
// intervals once a session-aware calendar exists.
func UTCWindow(t clock.EventTime, d time.Duration) Window {
	start := t.Time().UTC().Truncate(d)
	return Window{
		Start: clock.NewEventTime(start),
		End:   clock.NewEventTime(start.Add(d)),
	}
}

// AggregateOHLCV is a derived-interval candle computed from the canonical
// 1-minute series. It is never persisted (ADR-006: derived intervals are
// computed on read); Open/High/Low/Close/Volume share the owning
// instrument's scale, same as Candle.
type AggregateOHLCV struct {
	Window Window
	Open   quant.Price
	High   quant.Price
	Low    quant.Price
	Close  quant.Price
	Volume quant.Qty
}

// IncompleteWindow explains why a window could not be aggregated: at least
// one source minute was not available as a current, closed candle. Missing
// lists every such minute's open_time, ascending, so the caller can act
// (backfill, wait, or surface the specific gap) rather than being told
// only that something was wrong.
type IncompleteWindow struct {
	InstrumentID int64
	Window       Window
	Missing      []clock.EventTime
}

func (i IncompleteWindow) Error() string {
	return fmt.Sprintf("marketdata: aggregate instrument=%d window=[%s,%s): %d of %d source minutes unavailable",
		i.InstrumentID, i.Window.Start, i.Window.End, len(i.Missing), mustMinutes(i.Window))
}

func mustMinutes(w Window) int {
	n, err := w.minutes()
	if err != nil {
		return 0
	}
	return n
}

// candleReader is the minimal boundary Aggregate depends on. Store
// implements it; tests can fake it directly (guide 4, 10: an interface
// defined by the consumer, kept small, with a handwritten fake rather than
// a mock framework).
type candleReader interface {
	ListCurrentCandlesInRange(ctx context.Context, instrumentID int64, start, end time.Time) ([]store.Candle, error)
}

// ListCurrentCandlesInRange adapts Store to candleReader.
func (s *Store) ListCurrentCandlesInRange(ctx context.Context, instrumentID int64, start, end time.Time) ([]store.Candle, error) {
	return s.q.ListCurrentCandlesInRange(ctx, store.ListCurrentCandlesInRangeParams{
		InstrumentID: instrumentID,
		OpenTime:     start,
		OpenTime_2:   end,
	})
}

var _ candleReader = (*Store)(nil)

// Aggregate computes the derived-interval OHLCV for instrumentID over w
// from the canonical 1-minute series. It never returns a plausible but
// wrong number: if any source minute in w is missing, or present but still
// forming (is_closed = false -- its values can still change, so it is not
// a reliable input yet), Aggregate returns a non-nil *IncompleteWindow
// naming every such minute instead of a partial or guessed OHLCV. The
// returned *AggregateOHLCV is non-nil only when *IncompleteWindow is nil,
// and vice versa -- exactly one of the two carries meaning.
func Aggregate(ctx context.Context, r candleReader, instrumentID int64, w Window) (*AggregateOHLCV, *IncompleteWindow, error) {
	expected, err := w.minutes()
	if err != nil {
		return nil, nil, err
	}

	rows, err := r.ListCurrentCandlesInRange(ctx, instrumentID, w.Start.Time(), w.End.Time())
	if err != nil {
		return nil, nil, fmt.Errorf("marketdata: aggregate instrument=%d window=[%s,%s): %w", instrumentID, w.Start, w.End, err)
	}

	present := make(map[time.Time]store.Candle, len(rows))
	for _, row := range rows {
		present[row.OpenTime] = row
	}

	var missing []clock.EventTime
	for i := 0; i < expected; i++ {
		slot := w.Start.Time().Add(time.Duration(i) * time.Minute)
		row, ok := present[slot]
		if !ok || !row.IsClosed {
			// Absent, or present but still forming: either way, not yet a
			// reliable source for this window.
			missing = append(missing, clock.NewEventTime(slot))
		}
	}

	if len(missing) > 0 {
		return nil, &IncompleteWindow{InstrumentID: instrumentID, Window: w, Missing: missing}, nil
	}

	// Complete: every slot has a closed, current candle. Reduce in
	// open_time order -- rows came back ordered from the query, and the
	// loop above only confirmed presence, not order, so sort by the same
	// key defensively rather than assume the map iteration or query order
	// survived.
	ordered := make([]store.Candle, expected)
	for i := 0; i < expected; i++ {
		slot := w.Start.Time().Add(time.Duration(i) * time.Minute)
		ordered[i] = present[slot]
	}

	agg := AggregateOHLCV{
		Window: w,
		Open:   quant.Price(ordered[0].Open),
		High:   quant.Price(ordered[0].High),
		Low:    quant.Price(ordered[0].Low),
		Close:  quant.Price(ordered[len(ordered)-1].Close),
	}
	var volume int64
	for _, c := range ordered {
		if quant.Price(c.High) > agg.High {
			agg.High = quant.Price(c.High)
		}
		if quant.Price(c.Low) < agg.Low {
			agg.Low = quant.Price(c.Low)
		}
		volume += c.Volume
	}
	agg.Volume = quant.Qty(volume)

	return &agg, nil, nil
}
