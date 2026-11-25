package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/store"
)

// Gap is a detected range of missing canonical minutes. Resolution is a
// free-text reason (ADR-006; the exact enumeration of resolution kinds is
// still open) and is only ever set alongside ResolvedAt, never before it.
type Gap struct {
	ID           int64
	InstrumentID int64
	Interval     Interval
	Start        clock.EventTime
	End          clock.EventTime
	DetectedAt   clock.ProcessingTime
	ResolvedAt   *clock.ProcessingTime
	Resolution   *string
}

func fromGapRow(row store.CandleGap) Gap {
	g := Gap{
		ID:           row.ID,
		InstrumentID: row.InstrumentID,
		Interval:     Interval(row.Interval),
		Start:        clock.NewEventTime(row.GapStart),
		End:          clock.NewEventTime(row.GapEnd),
		DetectedAt:   clock.NewProcessingTime(row.DetectedAt),
	}
	if row.ResolvedAt.Valid {
		t := clock.NewProcessingTime(row.ResolvedAt.Time)
		g.ResolvedAt = &t
	}
	if row.Resolution.Valid {
		v := row.Resolution.String
		g.Resolution = &v
	}
	return g
}

// RecordGap records a missing range of canonical minutes for instrumentID.
// It is idempotent: detecting the same (instrumentID, start, end) gap
// again, e.g. on a retried or duplicate detection event, returns the
// existing row rather than creating a duplicate or erroring (guide 14).
// Reconnecting alone is never enough to call a gap resolved (ADR-006):
// this only ever records that a gap exists; see ResolveGap for the only
// way to close one. This is the 1 minute series; RecordGapAt records a gap
// in any stored interval's series.
func (s *Store) RecordGap(ctx context.Context, instrumentID int64, start, end clock.EventTime) (Gap, error) {
	return s.RecordGapAt(ctx, Interval1m, instrumentID, start, end)
}

// RecordGapAt is RecordGap for interval's series: a missing range of
// interval candles, recorded with that interval (candle_gaps.interval).
func (s *Store) RecordGapAt(ctx context.Context, interval Interval, instrumentID int64, start, end clock.EventTime) (Gap, error) {
	if err := interval.Validate(); err != nil {
		return Gap{}, err
	}
	if !start.Time().Before(end.Time()) {
		return Gap{}, fmt.Errorf("marketdata: record gap instrument=%d: gap_start must be before gap_end", instrumentID)
	}
	row, err := s.q.RecordCandleGap(ctx, store.RecordCandleGapParams{
		InstrumentID: instrumentID,
		GapStart:     start.Time(),
		GapEnd:       end.Time(),
		DetectedAt:   s.clk.Now(),
		Interval:     string(interval),
	})
	if err != nil {
		return Gap{}, fmt.Errorf("marketdata: record gap instrument=%d start=%s end=%s: %w", instrumentID, start, end, err)
	}
	return fromGapRow(row), nil
}

// ResolveGap marks a gap resolved only once real confirmation exists --
// backfill succeeded, or the provider confirmed there is genuinely no data
// there (ADR-006). resolution is required and describes which. Calling
// this on an already-resolved gap returns ErrGapAlreadyResolved rather than
// silently re-describing it: a resolution, once recorded, is not something
// a later call gets to overwrite.
func (s *Store) ResolveGap(ctx context.Context, instrumentID int64, start, end clock.EventTime, resolution string) (Gap, error) {
	if resolution == "" {
		return Gap{}, ErrResolutionRequired
	}
	row, err := s.q.ResolveCandleGap(ctx, store.ResolveCandleGapParams{
		InstrumentID: instrumentID,
		GapStart:     start.Time(),
		GapEnd:       end.Time(),
		ResolvedAt:   nullTimeFrom(s.clk.Now()),
		Resolution:   sql.NullString{String: resolution, Valid: true},
		Interval:     string(Interval1m),
	})
	if err == nil {
		return fromGapRow(row), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Gap{}, fmt.Errorf("marketdata: resolve gap instrument=%d start=%s end=%s: %w", instrumentID, start, end, err)
	}

	// Zero rows: either the gap never existed, or it is already resolved.
	existing, getErr := s.q.GetCandleGap(ctx, store.GetCandleGapParams{
		InstrumentID: instrumentID,
		GapStart:     start.Time(),
		GapEnd:       end.Time(),
		Interval:     string(Interval1m),
	})
	switch {
	case errors.Is(getErr, sql.ErrNoRows):
		return Gap{}, fmt.Errorf("marketdata: resolve gap instrument=%d start=%s end=%s: %w", instrumentID, start, end, ErrGapNotFound)
	case getErr != nil:
		return Gap{}, fmt.Errorf("marketdata: resolve gap instrument=%d start=%s end=%s: %w", instrumentID, start, end, getErr)
	default:
		_ = existing
		return Gap{}, fmt.Errorf("marketdata: resolve gap instrument=%d start=%s end=%s: %w", instrumentID, start, end, ErrGapAlreadyResolved)
	}
}

// ListOpenGaps returns every unresolved 1 minute gap for instrumentID,
// ordered by start time. ListOpenGapsAt reads another interval's gaps.
func (s *Store) ListOpenGaps(ctx context.Context, instrumentID int64) ([]Gap, error) {
	return s.ListOpenGapsAt(ctx, Interval1m, instrumentID)
}

// ListOpenGapsAt is ListOpenGaps for interval's series.
func (s *Store) ListOpenGapsAt(ctx context.Context, interval Interval, instrumentID int64) ([]Gap, error) {
	if err := interval.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.q.ListOpenCandleGaps(ctx, store.ListOpenCandleGapsParams{InstrumentID: instrumentID, Interval: string(interval)})
	if err != nil {
		return nil, fmt.Errorf("marketdata: list open gaps instrument=%d: %w", instrumentID, err)
	}
	out := make([]Gap, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromGapRow(row))
	}
	return out, nil
}

// MissingRange is a run of consecutive slots of one interval with no
// current, closed candle: [Start, End), Slots candles long (minutes, for
// the 1 minute series).
type MissingRange struct {
	Start time.Time
	End   time.Time
	Slots int64
}

// MissingRanges returns every run of minutes in [from, to) with no current,
// closed 1 minute candle for instrumentID, oldest first, in one statement
// (ListMissingCandleRanges), starting at the first stored closed candle at
// or after from. Minutes before lostBefore already covered by an open
// recorded gap are not returned (known, unfetchable holes); minutes from
// lostBefore on are always returned when missing.
func (s *Store) MissingRanges(ctx context.Context, instrumentID int64, from, to, lostBefore time.Time) ([]MissingRange, error) {
	return s.MissingRangesAt(ctx, Interval1m, instrumentID, from, to, lostBefore)
}

// MissingRangesAt is MissingRanges for interval's series: the expected
// slots step by the interval, and only that interval's recorded gaps are
// excluded.
func (s *Store) MissingRangesAt(ctx context.Context, interval Interval, instrumentID int64, from, to, lostBefore time.Time) ([]MissingRange, error) {
	if err := interval.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.q.ListMissingCandleRanges(ctx, store.ListMissingCandleRangesParams{Interval: string(interval), InstrumentID: instrumentID, FromTime: from, ToTime: to, LostBefore: lostBefore})
	if err != nil {
		return nil, fmt.Errorf("marketdata: missing %s ranges instrument=%d [%s,%s): %w", interval, instrumentID, from, to, err)
	}
	out := make([]MissingRange, len(rows))
	for i, r := range rows {
		out[i] = MissingRange{Start: r.GapStart.UTC(), End: r.GapEnd.UTC(), Slots: r.Slots}
	}
	return out, nil
}

// CountClosed counts current, closed 1 minute candles for instrumentID
// with open_time in [from, to).
func (s *Store) CountClosed(ctx context.Context, instrumentID int64, from, to time.Time) (int64, error) {
	return s.CountClosedAt(ctx, Interval1m, instrumentID, from, to)
}

// CountClosedAt is CountClosed for interval's series.
func (s *Store) CountClosedAt(ctx context.Context, interval Interval, instrumentID int64, from, to time.Time) (int64, error) {
	if err := interval.Validate(); err != nil {
		return 0, err
	}
	n, err := s.q.CountClosedCandlesInRange(ctx, store.CountClosedCandlesInRangeParams{Interval: string(interval), InstrumentID: instrumentID, FromTime: from, ToTime: to})
	if err != nil {
		return 0, fmt.Errorf("marketdata: count closed instrument=%d [%s,%s): %w", instrumentID, from, to, err)
	}
	return n, nil
}
