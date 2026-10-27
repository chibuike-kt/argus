package analysis

import (
	"fmt"
	"time"
)

// Direction is the maintained structural-direction state (spec section
// 2): an event-driven state, not a value recomputed from scratch on
// every pivot.
type Direction int

const (
	// DirectionUndetermined is the zero value / cold-start state.
	DirectionUndetermined Direction = iota
	// DirectionBullish means the maintained structural direction is up.
	DirectionBullish
	// DirectionBearish means the maintained structural direction is down.
	DirectionBearish
)

func (d Direction) String() string {
	switch d {
	case DirectionBullish:
		return "bullish"
	case DirectionBearish:
		return "bearish"
	default:
		return "undetermined"
	}
}

func parseDirection(s string) (Direction, error) {
	switch s {
	case "undetermined":
		return DirectionUndetermined, nil
	case "bullish":
		return DirectionBullish, nil
	case "bearish":
		return DirectionBearish, nil
	default:
		return 0, fmt.Errorf("analysis: unknown direction %q", s)
	}
}

// SwingType distinguishes a confirmed swing high from a confirmed swing
// low (spec section 1).
type SwingType int

const (
	// SwingHigh is a confirmed swing high.
	SwingHigh SwingType = iota
	// SwingLow is a confirmed swing low.
	SwingLow
)

func (t SwingType) String() string {
	if t == SwingLow {
		return "low"
	}
	return "high"
}

func parseSwingType(s string) (SwingType, error) {
	switch s {
	case "high":
		return SwingHigh, nil
	case "low":
		return SwingLow, nil
	default:
		return 0, fmt.Errorf("analysis: unknown swing type %q", s)
	}
}

// EventType distinguishes the three kinds of structural event the spec
// defines (sections 2-3). direction_established is a cold-start event,
// never a direction flip; BOS continues the current direction; CHOCH is
// the only event that flips it.
type EventType int

const (
	// EventDirectionEstablished is the cold-start event: UNDETERMINED
	// to BULLISH or BEARISH, never a flip.
	EventDirectionEstablished EventType = iota
	// EventBOS continues the current direction.
	EventBOS
	// EventCHOCH is the only event that flips direction.
	EventCHOCH
)

func (t EventType) String() string {
	switch t {
	case EventBOS:
		return "bos"
	case EventCHOCH:
		return "choch"
	default:
		return "direction_established"
	}
}

func parseEventType(s string) (EventType, error) {
	switch s {
	case "direction_established":
		return EventDirectionEstablished, nil
	case "bos":
		return EventBOS, nil
	case "choch":
		return EventCHOCH, nil
	default:
		return 0, fmt.Errorf("analysis: unknown event type %q", s)
	}
}

// Config captures this algorithm version's configurable parameters
// (ADR-009: "swing detection parameters... must be explicitly defined").
// The spec fixes LeftBars = RightBars = 2 for v0.1; a later algorithm
// version could change either independently, which is exactly why
// neither is a literal "2" inlined into the detection or recompute-
// window logic.
type Config struct {
	AlgorithmVersion string
	LeftBars         int
	RightBars        int
	Interval         time.Duration // 1 minute for this version
}

// Validate fails fast on a configuration that cannot produce a valid
// fractal window or a meaningful candle interval (guide 12).
func (c Config) Validate() error {
	if c.AlgorithmVersion == "" {
		return fmt.Errorf("analysis: algorithm version is required")
	}
	if c.LeftBars <= 0 || c.RightBars <= 0 {
		return fmt.Errorf("analysis: left and right bar counts must be positive, got left=%d right=%d", c.LeftBars, c.RightBars)
	}
	if c.Interval <= 0 {
		return fmt.Errorf("analysis: interval must be positive")
	}
	return nil
}
