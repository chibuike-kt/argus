package analysis

import (
	"fmt"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/store"
)

// StructureEvent is the domain view of a direction-establishment, BOS,
// or CHOCH event (spec sections 2-3, 5).
type StructureEvent struct {
	ID                int64
	InstrumentID      int64
	Timeframe         string
	Type              EventType
	DirectionBefore   Direction
	DirectionAfter    Direction
	TriggerCandleID   int64
	ReferenceSwingIDs []int64
	EvaluationTime    clock.EvaluationTime
	AlgorithmVersion  string
	SupersededAt      *clock.ProcessingTime
	SupersededBy      *int64
}

func fromEventRow(row store.StructureEvent) (StructureEvent, error) {
	eventType, err := parseEventType(row.EventType)
	if err != nil {
		return StructureEvent{}, fmt.Errorf("analysis: structure event row %d: %w", row.ID, err)
	}
	before, err := parseDirection(row.DirectionBefore)
	if err != nil {
		return StructureEvent{}, fmt.Errorf("analysis: structure event row %d: %w", row.ID, err)
	}
	after, err := parseDirection(row.DirectionAfter)
	if err != nil {
		return StructureEvent{}, fmt.Errorf("analysis: structure event row %d: %w", row.ID, err)
	}
	e := StructureEvent{
		ID:                row.ID,
		InstrumentID:      row.InstrumentID,
		Timeframe:         row.Timeframe,
		Type:              eventType,
		DirectionBefore:   before,
		DirectionAfter:    after,
		TriggerCandleID:   row.TriggerCandleID,
		ReferenceSwingIDs: row.ReferenceSwingIds,
		EvaluationTime:    clock.NewEvaluationTime(row.EvaluationTime),
		AlgorithmVersion:  row.AlgorithmVersion,
	}
	if row.SupersededAt.Valid {
		t := clock.NewProcessingTime(row.SupersededAt.Time)
		e.SupersededAt = &t
	}
	if row.SupersededBy.Valid {
		id := row.SupersededBy.Int64
		e.SupersededBy = &id
	}
	return e, nil
}

// NewStructureEvent describes an event to be inserted.
type NewStructureEvent struct {
	InstrumentID      int64
	Timeframe         string
	Type              EventType
	DirectionBefore   Direction
	DirectionAfter    Direction
	TriggerCandleID   int64
	ReferenceSwingIDs []int64
	EvaluationTime    clock.EvaluationTime
	AlgorithmVersion  string
}

// equalEvidence is StructureEvent's counterpart to NewSwing's: whether a
// freshly decided event is identical to what is already stored for this
// trigger candle.
func (n NewStructureEvent) equalEvidence(e StructureEvent) bool {
	if n.Type != e.Type || n.DirectionBefore != e.DirectionBefore || n.DirectionAfter != e.DirectionAfter {
		return false
	}
	if len(n.ReferenceSwingIDs) != len(e.ReferenceSwingIDs) {
		return false
	}
	for i := range n.ReferenceSwingIDs {
		if n.ReferenceSwingIDs[i] != e.ReferenceSwingIDs[i] {
			return false
		}
	}
	return true
}

// decideBreak evaluates a closed candle's close against the current
// direction and reference levels (spec sections 2-3). ok is false when
// neither a BOS nor a CHOCH applies -- including when direction is
// DirectionUndetermined, which decideEstablishment handles separately,
// since establishment depends on swing history, not a single close.
func decideBreak(direction Direction, refHigh, refLow, closePrice int64) (newDirection Direction, eventType EventType, ok bool) {
	switch direction {
	case DirectionBullish:
		switch {
		case closePrice > refHigh:
			return DirectionBullish, EventBOS, true
		case closePrice < refLow:
			return DirectionBearish, EventCHOCH, true
		}
	case DirectionBearish:
		switch {
		case closePrice < refLow:
			return DirectionBearish, EventBOS, true
		case closePrice > refHigh:
			return DirectionBullish, EventCHOCH, true
		}
	}
	return direction, 0, false
}

// decideEstablishment implements spec section 2's cold-start rule: the
// first time the latest two confirmed highs form a higher high AND the
// latest two confirmed lows form a higher low, direction becomes
// BULLISH; a lower high and lower low makes it BEARISH. priorHigh/
// latestHigh and priorLow/latestLow are, respectively, the two most
// recent confirmed swings of each type ordered by pivot_time ascending
// (prior, then latest). A mixed or insufficient sequence is not
// established (ok=false): "insufficient" is the caller's job to detect
// by not calling this until it has two of each.
func decideEstablishment(priorHigh, latestHigh, priorLow, latestLow int64) (Direction, bool) {
	if latestHigh > priorHigh && latestLow > priorLow {
		return DirectionBullish, true
	}
	if latestHigh < priorHigh && latestLow < priorLow {
		return DirectionBearish, true
	}
	return DirectionUndetermined, false
}
