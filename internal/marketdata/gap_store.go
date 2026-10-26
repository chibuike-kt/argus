package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/store"
)

// Gap is a detected range of missing canonical minutes. Resolution is a
// free-text reason (ADR-006; the exact enumeration of resolution kinds is
// still open) and is only ever set alongside ResolvedAt, never before it.
type Gap struct {
	ID           int64
	InstrumentID int64
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
// way to close one.
func (s *Store) RecordGap(ctx context.Context, instrumentID int64, start, end clock.EventTime) (Gap, error) {
	if !start.Time().Before(end.Time()) {
		return Gap{}, fmt.Errorf("marketdata: record gap instrument=%d: gap_start must be before gap_end", instrumentID)
	}
	row, err := s.q.RecordCandleGap(ctx, store.RecordCandleGapParams{
		InstrumentID: instrumentID,
		GapStart:     start.Time(),
		GapEnd:       end.Time(),
		DetectedAt:   s.clk.Now(),
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

// ListOpenGaps returns every unresolved gap for instrumentID, ordered by
// start time.
func (s *Store) ListOpenGaps(ctx context.Context, instrumentID int64) ([]Gap, error) {
	rows, err := s.q.ListOpenCandleGaps(ctx, instrumentID)
	if err != nil {
		return nil, fmt.Errorf("marketdata: list open gaps instrument=%d: %w", instrumentID, err)
	}
	out := make([]Gap, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromGapRow(row))
	}
	return out, nil
}
