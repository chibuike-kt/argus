package analysis

// action is the outcome of reconciling a freshly computed candidate
// against whatever is currently stored for a position (a swing) or a
// trigger candle (a structure event). Extracted as a pure function of
// (current, candidate) specifically so the five-way branch is directly
// unit-testable without a database: the orchestration code that acts on
// an action (reconcileSwing, applyBreakStep) is thin enough that its
// correctness is instead proven by the integration tests exercising the
// real store, triggers and handshake.
type action int

const (
	// actionNone: nothing stored, candidate does not qualify either.
	actionNone action = iota
	// actionInsert: nothing stored, candidate qualifies.
	actionInsert
	// actionSkip: something stored, candidate is identical evidence.
	actionSkip
	// actionReplace: something stored, candidate is different evidence.
	actionReplace
	// actionRetract: something stored, candidate does not qualify.
	actionRetract
)

// decideSwingAction is the five-way decision applyPivotStep's
// reconciliation makes for one swing position.
func decideSwingAction(current *Swing, candidate NewSwing, qualifies bool) action {
	switch {
	case current == nil && qualifies:
		return actionInsert
	case current == nil && !qualifies:
		return actionNone
	case current != nil && qualifies && candidate.equalEvidence(*current):
		return actionSkip
	case current != nil && qualifies:
		return actionReplace
	default: // current != nil && !qualifies
		return actionRetract
	}
}

// decideEventAction is decideSwingAction's counterpart for a structure
// event at one trigger candle. candidate is nil when nothing about this
// candle's close produces an event (no break, no establishment).
func decideEventAction(current *StructureEvent, candidate *NewStructureEvent) action {
	switch {
	case current == nil && candidate == nil:
		return actionNone
	case current == nil && candidate != nil:
		return actionInsert
	case current != nil && candidate != nil && candidate.equalEvidence(*current):
		return actionSkip
	case current != nil && candidate != nil:
		return actionReplace
	default: // current != nil && candidate == nil
		return actionRetract
	}
}
