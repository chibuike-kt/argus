package analysis

import (
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// Sweep is the domain view of a confirmed liquidity sweep (spec section
// 6): a single finalized candle that penetrates a confirmed opposing
// swing and closes back inside it, same candle only.
type Sweep struct {
	ID                 int64
	InstrumentID       int64
	Timeframe          string
	Side               SweepSide
	SweptSwingID       int64     // the confirmed swing this sweep took out
	TriggerOpenTime    time.Time // the position key: stable across a correction to this candle
	TriggerCandleID    int64     // evidence only: which candle revision this was computed from
	PenetrationExtreme quant.Price
	ReclaimClose       quant.Price
	EvaluationTime     clock.EvaluationTime
	AlgorithmVersion   string
	SupersededAt       *clock.ProcessingTime
	SupersededBy       *int64
}

func fromSweepRow(row store.LiquiditySweep) (Sweep, error) {
	side, err := parseSweepSide(row.Side)
	if err != nil {
		return Sweep{}, fmt.Errorf("analysis: sweep row %d: %w", row.ID, err)
	}
	sw := Sweep{
		ID:                 row.ID,
		InstrumentID:       row.InstrumentID,
		Timeframe:          row.Timeframe,
		Side:               side,
		SweptSwingID:       row.SweptSwingID,
		TriggerOpenTime:    row.TriggerOpenTime,
		TriggerCandleID:    row.TriggerCandleID,
		PenetrationExtreme: quant.Price(row.PenetrationExtreme),
		ReclaimClose:       quant.Price(row.ReclaimClose),
		EvaluationTime:     clock.NewEvaluationTime(row.EvaluationTime),
		AlgorithmVersion:   row.AlgorithmVersion,
	}
	if row.SupersededAt.Valid {
		t := clock.NewProcessingTime(row.SupersededAt.Time)
		sw.SupersededAt = &t
	}
	if row.SupersededBy.Valid {
		id := row.SupersededBy.Int64
		sw.SupersededBy = &id
	}
	return sw, nil
}

// NewSweep describes a sweep to be inserted. TriggerOpenTime is the
// position key (see 000006's comment on liquidity_sweeps);
// TriggerCandleID is evidence only, never used for lookup.
type NewSweep struct {
	InstrumentID       int64
	Timeframe          string
	Side               SweepSide
	SweptSwingID       int64
	TriggerOpenTime    time.Time
	TriggerCandleID    int64
	PenetrationExtreme quant.Price
	ReclaimClose       quant.Price
	EvaluationTime     clock.EvaluationTime
	AlgorithmVersion   string
}

// equalEvidence reports whether candidate and stored carry the same
// evidence. Side and trigger_open_time are the reconciliation key
// (always equal when comparing a candidate against "current" at that
// key), so they are not part of the comparison -- the same convention
// NewSwing.equalEvidence and NewStructureEvent.equalEvidence follow.
func (n NewSweep) equalEvidence(s Sweep) bool {
	return n.SweptSwingID == s.SweptSwingID &&
		n.PenetrationExtreme == s.PenetrationExtreme &&
		n.ReclaimClose == s.ReclaimClose
}

// detectSweep is the pure decision for spec section 6. side determines
// which opposing level and which strict comparisons apply:
//
//   - SweepBuy sweeps a swing low at targetLevel: candleLow < targetLevel
//     (strict penetration, wick or body) and candleClose > targetLevel
//     (strict reclaim, close only).
//   - SweepSell sweeps a swing high at targetLevel: candleHigh > targetLevel
//     and candleClose < targetLevel.
//
// Both comparisons are strict: a touch (low == targetLevel) is not a
// penetration, and a close exactly on the level is not a reclaim. Only
// this one candle's own high/low/close are consulted -- the function
// has no memory of any other candle, which is what makes "same-candle
// only" (no multi-candle delayed reclaim) true by construction rather
// than by an explicit check.
func detectSweep(side SweepSide, targetLevel, candleHigh, candleLow, candleClose int64) (penetrationExtreme, reclaimClose int64, ok bool) {
	switch side {
	case SweepBuy:
		if candleLow < targetLevel && candleClose > targetLevel {
			return candleLow, candleClose, true
		}
	case SweepSell:
		if candleHigh > targetLevel && candleClose < targetLevel {
			return candleHigh, candleClose, true
		}
	}
	return 0, 0, false
}
