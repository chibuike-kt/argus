package outcomes

import "errors"

// Sentinel errors. Use errors.Is.
var (
	// ErrPlanHasNoStatus is returned when Evaluate is called with a plan
	// whose Status is analysis.PlanNone: there is no plan to simulate an
	// outcome for.
	ErrPlanHasNoStatus = errors.New("outcomes: plan has no status (PlanNone)")

	// ErrDirectionUndetermined is returned when a plan's Direction is
	// not analysis.DirectionBullish or analysis.DirectionBearish --
	// section 16's rules are defined for those two only ("bullish;
	// bearish mirrors").
	ErrDirectionUndetermined = errors.New("outcomes: plan direction is undetermined")
)
