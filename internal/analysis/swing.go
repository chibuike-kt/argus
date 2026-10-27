package analysis

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// Swing is the domain view of a confirmed pivot (spec section 1).
// Price is scaled by the owning instrument's price_scale, same as
// marketdata.Candle's OHLC fields.
type Swing struct {
	ID               int64
	InstrumentID     int64
	Timeframe        string
	Type             SwingType
	PivotTime        clock.EventTime // occurrence: the pivot candle's open_time
	Price            quant.Price
	ConfirmedAt      clock.EventTime      // deterministic: close_time of the last right-hand candle
	EvaluationTime   clock.EvaluationTime // when this algorithm run recorded the event
	AlgorithmVersion string
	SourceCandleIDs  []int64 // the fractal-window candle rows, left to right
	SupersededAt     *clock.ProcessingTime
	SupersededBy     *int64
}

func fromSwingRow(row store.ConfirmedSwing) (Swing, error) {
	swingType, err := parseSwingType(row.SwingType)
	if err != nil {
		return Swing{}, fmt.Errorf("analysis: swing row %d: %w", row.ID, err)
	}
	s := Swing{
		ID:               row.ID,
		InstrumentID:     row.InstrumentID,
		Timeframe:        row.Timeframe,
		Type:             swingType,
		PivotTime:        clock.NewEventTime(row.PivotTime),
		Price:            quant.Price(row.Price),
		ConfirmedAt:      clock.NewEventTime(row.ConfirmedAt),
		EvaluationTime:   clock.NewEvaluationTime(row.EvaluationTime),
		AlgorithmVersion: row.AlgorithmVersion,
		SourceCandleIDs:  row.SourceCandleIds,
	}
	if row.SupersededAt.Valid {
		t := clock.NewProcessingTime(row.SupersededAt.Time)
		s.SupersededAt = &t
	}
	if row.SupersededBy.Valid {
		id := row.SupersededBy.Int64
		s.SupersededBy = &id
	}
	return s, nil
}

// NewSwing describes a swing to be inserted. It has no ID/superseded
// fields: those are the store's concern, never the caller's.
type NewSwing struct {
	InstrumentID     int64
	Timeframe        string
	Type             SwingType
	PivotTime        clock.EventTime
	Price            quant.Price
	ConfirmedAt      clock.EventTime
	EvaluationTime   clock.EvaluationTime
	AlgorithmVersion string
	SourceCandleIDs  []int64
}

// equalEvidence reports whether two swings represent the identical
// confirmed fact (same price, same source candles) -- the comparison
// Engine uses to decide "unchanged, skip" versus "different, supersede"
// when re-evaluating a position it has already processed before.
func (n NewSwing) equalEvidence(s Swing) bool {
	if n.Price != s.Price || !n.ConfirmedAt.Time().Equal(s.ConfirmedAt.Time()) {
		return false
	}
	if len(n.SourceCandleIDs) != len(s.SourceCandleIDs) {
		return false
	}
	for i := range n.SourceCandleIDs {
		if n.SourceCandleIDs[i] != s.SourceCandleIDs[i] {
			return false
		}
	}
	return true
}

func nullTimeFrom(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: true} }
