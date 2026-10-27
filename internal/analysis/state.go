package analysis

import (
	"database/sql"
	"fmt"

	"github.com/chibuike-kt/argus/internal/store"
)

// StateSnapshot is the domain view of structure_state: the maintained
// direction state (spec section 2), plus enough bookkeeping to resume
// incremental evaluation. It is a mutable projection, not an evidence
// record -- see 000005's comment on structure_state and the package doc.
//
// ProtectedLowSwingID/ProtectedHighSwingID are the batch-2 protected
// anchors (spec section 7, v0.4): two fully independent slots, not one
// shared slot with a side discriminator -- both sides' liquidity can
// be live at once (a buy-side sweep's reversal-origin high can confirm
// mid-bullish-trend without disturbing an already-live protected low),
// so collapsing them into "whichever established most recently" would
// lose real state. Deliberately separate from ReferenceSwingHighID/
// ReferenceSwingLowID too: a reference swing is "the latest confirmed
// swing of this type," always updated on every confirmation; a
// protected anchor persists across newer swings until explicitly
// invalidated or superseded, and the two are not generally the same
// swing once the trend extends. Nil means no anchor on that side.
type StateSnapshot struct {
	InstrumentID          int64
	Timeframe             string
	Direction             Direction
	ReferenceSwingHighID  *int64
	ReferenceSwingLowID   *int64
	LastEventID           *int64
	LastProcessedCandleID *int64
	ProtectedLowSwingID   *int64
	ProtectedHighSwingID  *int64
}

// coldStart is the snapshot an (instrument, timeframe) pair starts from
// before any candle has ever been evaluated, and the snapshot a
// correction's as-of reconstruction resumes from when the correction
// point precedes every existing event.
func coldStart(instrumentID int64, timeframe string) StateSnapshot {
	return StateSnapshot{InstrumentID: instrumentID, Timeframe: timeframe, Direction: DirectionUndetermined}
}

func fromStateRow(row store.StructureState) (StateSnapshot, error) {
	direction, err := parseDirection(row.Direction)
	if err != nil {
		return StateSnapshot{}, fmt.Errorf("analysis: structure state %d/%s: %w", row.InstrumentID, row.Timeframe, err)
	}
	s := StateSnapshot{
		InstrumentID: row.InstrumentID,
		Timeframe:    row.Timeframe,
		Direction:    direction,
	}
	if row.ReferenceSwingHighID.Valid {
		id := row.ReferenceSwingHighID.Int64
		s.ReferenceSwingHighID = &id
	}
	if row.ReferenceSwingLowID.Valid {
		id := row.ReferenceSwingLowID.Int64
		s.ReferenceSwingLowID = &id
	}
	if row.LastEventID.Valid {
		id := row.LastEventID.Int64
		s.LastEventID = &id
	}
	if row.LastProcessedCandleID.Valid {
		id := row.LastProcessedCandleID.Int64
		s.LastProcessedCandleID = &id
	}
	if row.ProtectedLowSwingID.Valid {
		id := row.ProtectedLowSwingID.Int64
		s.ProtectedLowSwingID = &id
	}
	if row.ProtectedHighSwingID.Valid {
		id := row.ProtectedHighSwingID.Int64
		s.ProtectedHighSwingID = &id
	}
	return s, nil
}

func ptrToNullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}
