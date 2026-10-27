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
// ProtectedSwingID/ProtectedSwingSide are the batch-2 protected anchor
// (spec section 7) -- deliberately separate from ReferenceSwingHighID/
// ReferenceSwingLowID: a reference swing is "the latest confirmed swing
// of this type," always updated on every confirmation; the protected
// anchor is a designated anchor that persists across newer swings until
// explicitly invalidated or superseded, and the two are not generally
// the same swing once the trend extends. Nil/nil means no anchor is
// currently protected.
type StateSnapshot struct {
	InstrumentID          int64
	Timeframe             string
	Direction             Direction
	ReferenceSwingHighID  *int64
	ReferenceSwingLowID   *int64
	LastEventID           *int64
	LastProcessedCandleID *int64
	ProtectedSwingID      *int64
	ProtectedSwingSide    *ProtectedSwingSide
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
	if row.ProtectedSwingID.Valid {
		id := row.ProtectedSwingID.Int64
		s.ProtectedSwingID = &id
	}
	if row.ProtectedSwingSide.Valid {
		side, err := parseProtectedSwingSide(row.ProtectedSwingSide.String)
		if err != nil {
			return StateSnapshot{}, fmt.Errorf("analysis: structure state %d/%s: %w", row.InstrumentID, row.Timeframe, err)
		}
		s.ProtectedSwingSide = &side
	}
	return s, nil
}

func protectedSideToNullString(s *ProtectedSwingSide) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: s.String(), Valid: true}
}

func ptrToNullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}
