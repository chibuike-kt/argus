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
	// Revision is the derived-candle revision rule
	// (adr-004-ws-contract-review.md, review item 4): 1 + the sum of
	// (revision - 1) over every source 1-minute candle in Window. It
	// starts at 1 (every source candle at its own revision 1
	// contributes 0) and rises by exactly 1 for every supersession of
	// any source minute inside the window -- identical wherever the
	// same window is computed, so the REST endpoint and the WebSocket
	// snapshot/candle_corrected payload (not built in this phase) never
	// disagree about it. For a 1-minute window (a single source
	// candle), this reduces to exactly that candle's own revision.
	Revision int32
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
	rows, err := r.ListCurrentCandlesInRange(ctx, instrumentID, w.Start.Time(), w.End.Time())
	if err != nil {
		return nil, nil, fmt.Errorf("marketdata: aggregate instrument=%d window=[%s,%s): %w", instrumentID, w.Start, w.End, err)
	}
	return aggregateFromRows(instrumentID, w, rows)
}

// rowsByOpenTime buckets rows once, keyed by UnixNano of the UTC
// instant -- never by time.Time itself. time.Time equality (and so
// map-key equality) considers the *Location pointer, not just the
// instant: lib/pq sets each row's own Location by calling
// time.LoadLocation on the session's "TimeZone" server parameter
// (conn.go's own processParameterStatus), and that function returns
// the time.UTC singleton only for the literal name "UTC" -- a server
// configured with the equally-valid, equally-UTC-offset zone name
// "Etc/UTC" (a common Postgres default) gets back a different
// *time.Location object. reduceWindow's own lookups are built through
// clock.EventTime, whose constructor always normalizes via .UTC() to
// the time.UTC singleton -- so keying this map by the raw time.Time
// would silently never match a single row fetched from such a server,
// and every window would be reported incomplete regardless of what is
// actually stored. UnixNano has no Location to disagree about.
//
// Built once per request (by Aggregate for its own one window, by
// AggregateRange once for every window in the request) rather than
// once per window: a row belongs to at most one open_time, so one
// shared map serves any number of windows without re-scanning rows
// per window (see reduceWindow's own doc comment for the complexity
// this avoids).
func rowsByOpenTime(rows []store.Candle) map[int64]store.Candle {
	present := make(map[int64]store.Candle, len(rows))
	for _, row := range rows {
		present[row.OpenTime.UTC().UnixNano()] = row
	}
	return present
}

// aggregateFromRows is Aggregate's own in-memory half: bucket rows
// once, then reduce the one window from that bucket. AggregateRange
// instead buckets once for every window in the request and calls
// reduceWindow directly per window, sharing the one bucket (addendum
// item B's own follow-up: bucketing per request, not per window, is
// what actually keeps this at O(rows), see reduceWindow).
func aggregateFromRows(instrumentID int64, w Window, rows []store.Candle) (*AggregateOHLCV, *IncompleteWindow, error) {
	return reduceWindow(instrumentID, w, rowsByOpenTime(rows))
}

// reduceWindow computes w's own AggregateOHLCV from present, a bucket
// already keyed by rowsByOpenTime -- never re-scanning the full row
// set this bucket came from. Before this, AggregateRange built a fresh
// bucket by scanning every fetched row for every window it reduced:
// O(windows * total_rows) instead of O(total_rows) to bucket once plus
// O(sum of window sizes) to reduce, which is why a 500-bar hourly
// request spent more time reducing in memory (profiled ~70% of the
// end-to-end call) than running its own single database query.
func reduceWindow(instrumentID int64, w Window, present map[int64]store.Candle) (*AggregateOHLCV, *IncompleteWindow, error) {
	expected, err := w.minutes()
	if err != nil {
		return nil, nil, err
	}

	var missing []clock.EventTime
	for i := 0; i < expected; i++ {
		slot := w.Start.Time().Add(time.Duration(i) * time.Minute)
		row, ok := present[slot.UTC().UnixNano()]
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
		ordered[i] = present[slot.UTC().UnixNano()]
	}

	agg := AggregateOHLCV{
		Window: w,
		Open:   quant.Price(ordered[0].Open),
		High:   quant.Price(ordered[0].High),
		Low:    quant.Price(ordered[0].Low),
		Close:  quant.Price(ordered[len(ordered)-1].Close),
	}
	var volume int64
	revision := int32(1)
	for _, c := range ordered {
		if quant.Price(c.High) > agg.High {
			agg.High = quant.Price(c.High)
		}
		if quant.Price(c.Low) < agg.Low {
			agg.Low = quant.Price(c.Low)
		}
		volume += c.Volume
		revision += c.Revision - 1
	}
	agg.Volume = quant.Qty(volume)
	agg.Revision = revision

	return &agg, nil, nil
}
