package analysis

import (
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

func TestDirection_StringParseRoundTrip(t *testing.T) {
	for _, d := range []Direction{DirectionUndetermined, DirectionBullish, DirectionBearish} {
		got, err := parseDirection(d.String())
		if err != nil {
			t.Fatalf("parseDirection(%q): %v", d.String(), err)
		}
		if got != d {
			t.Fatalf("round trip %v -> %q -> %v", d, d.String(), got)
		}
	}
	if _, err := parseDirection("sideways"); err == nil {
		t.Fatal("parseDirection(garbage) = nil error, want an error")
	}
}

func TestSwingType_StringParseRoundTrip(t *testing.T) {
	for _, s := range []SwingType{SwingHigh, SwingLow} {
		got, err := parseSwingType(s.String())
		if err != nil || got != s {
			t.Fatalf("round trip %v -> %q -> %v, %v", s, s.String(), got, err)
		}
	}
	if _, err := parseSwingType("sideways"); err == nil {
		t.Fatal("parseSwingType(garbage) = nil error, want an error")
	}
}

func TestEventType_StringParseRoundTrip(t *testing.T) {
	for _, e := range []EventType{EventDirectionEstablished, EventBOS, EventCHOCH} {
		got, err := parseEventType(e.String())
		if err != nil || got != e {
			t.Fatalf("round trip %v -> %q -> %v, %v", e, e.String(), got, err)
		}
	}
	if _, err := parseEventType("garbage"); err == nil {
		t.Fatal("parseEventType(garbage) = nil error, want an error")
	}
}

func TestSweepSide_StringParseRoundTrip(t *testing.T) {
	for _, s := range []SweepSide{SweepLow, SweepHigh} {
		got, err := parseSweepSide(s.String())
		if err != nil || got != s {
			t.Fatalf("round trip %v -> %q -> %v, %v", s, s.String(), got, err)
		}
	}
	if _, err := parseSweepSide("garbage"); err == nil {
		t.Fatal("parseSweepSide(garbage) = nil error, want an error")
	}
}

func TestSweepSide_GlossaryMapping(t *testing.T) {
	// Spec section 6 glossary, fixed: a sweep of a low is sell-side; a
	// sweep of a high is buy-side. Naming the constants by the level
	// taken (not the trade direction) means this is the one place that
	// ever needs to state the mapping explicitly.
	if SweepLow.String() != "sell" {
		t.Fatalf(`SweepLow.String() = %q, want "sell"`, SweepLow.String())
	}
	if SweepHigh.String() != "buy" {
		t.Fatalf(`SweepHigh.String() = %q, want "buy"`, SweepHigh.String())
	}
}

func TestProtectedSwingSide_StringParseRoundTrip(t *testing.T) {
	for _, s := range []ProtectedSwingSide{ProtectedLow, ProtectedHigh} {
		got, err := parseProtectedSwingSide(s.String())
		if err != nil || got != s {
			t.Fatalf("round trip %v -> %q -> %v, %v", s, s.String(), got, err)
		}
	}
	if _, err := parseProtectedSwingSide("garbage"); err == nil {
		t.Fatal("parseProtectedSwingSide(garbage) = nil error, want an error")
	}
}

func TestConfig_Validate(t *testing.T) {
	valid := Config{AlgorithmVersion: "v1", LeftBars: 2, RightBars: 2, Interval: time.Minute}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() on a valid config: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(c *Config)
	}{
		{"missing algorithm version", func(c *Config) { c.AlgorithmVersion = "" }},
		{"zero left bars", func(c *Config) { c.LeftBars = 0 }},
		{"negative right bars", func(c *Config) { c.RightBars = -1 }},
		{"zero interval", func(c *Config) { c.Interval = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := valid
			tt.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
		})
	}
}

func TestNewSwing_EqualEvidence(t *testing.T) {
	confirmedAt := clock.NewEventTime(time.Date(2026, time.October, 27, 12, 3, 0, 0, time.UTC))
	stored := Swing{
		Price: 100, ConfirmedAt: confirmedAt, SourceCandleIDs: []int64{1, 2, 3, 4, 5},
	}
	same := NewSwing{Price: 100, ConfirmedAt: confirmedAt, SourceCandleIDs: []int64{1, 2, 3, 4, 5}}
	diffPrice := same
	diffPrice.Price = 200
	diffIDs := same
	diffIDs.SourceCandleIDs = []int64{1, 2, 3, 4, 6}
	diffLen := same
	diffLen.SourceCandleIDs = []int64{1, 2, 3, 4}

	if !same.equalEvidence(stored) {
		t.Error("identical evidence reported as different")
	}
	if diffPrice.equalEvidence(stored) {
		t.Error("different price reported as identical")
	}
	if diffIDs.equalEvidence(stored) {
		t.Error("different source candle ids reported as identical")
	}
	if diffLen.equalEvidence(stored) {
		t.Error("different-length source candle ids reported as identical")
	}
}

func TestNewStructureEvent_EqualEvidence(t *testing.T) {
	stored := StructureEvent{
		Type: EventBOS, DirectionBefore: DirectionBullish, DirectionAfter: DirectionBullish,
		ReferenceSwingIDs: []int64{7},
	}
	same := &NewStructureEvent{Type: EventBOS, DirectionBefore: DirectionBullish, DirectionAfter: DirectionBullish, ReferenceSwingIDs: []int64{7}}
	diffType := &NewStructureEvent{Type: EventCHOCH, DirectionBefore: DirectionBullish, DirectionAfter: DirectionBearish, ReferenceSwingIDs: []int64{7}}
	diffRefs := &NewStructureEvent{Type: EventBOS, DirectionBefore: DirectionBullish, DirectionAfter: DirectionBullish, ReferenceSwingIDs: []int64{8}}

	if !same.equalEvidence(stored) {
		t.Error("identical evidence reported as different")
	}
	if diffType.equalEvidence(stored) {
		t.Error("different event type/direction reported as identical")
	}
	if diffRefs.equalEvidence(stored) {
		t.Error("different reference swing ids reported as identical")
	}
}
