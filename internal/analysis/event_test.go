package analysis

import "testing"

func TestDecideBreak_Bullish(t *testing.T) {
	tests := []struct {
		name          string
		close         int64
		wantEvent     EventType
		wantDirection Direction
		wantOK        bool
	}{
		{"close strictly above high is BOS", 111, EventBOS, DirectionBullish, true},
		{"close exactly at high is not a break", 110, 0, DirectionBullish, false},
		{"close strictly below low is CHOCH, flips bearish", 89, EventCHOCH, DirectionBearish, true},
		{"close exactly at low is not a break", 90, 0, DirectionBullish, false},
		{"close inside the range is nothing", 100, 0, DirectionBullish, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, event, ok := decideBreak(DirectionBullish, 110, 90, tt.close)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if event != tt.wantEvent || dir != tt.wantDirection {
				t.Fatalf("got (%v, %v), want (%v, %v)", dir, event, tt.wantDirection, tt.wantEvent)
			}
		})
	}
}

func TestDecideBreak_Bearish(t *testing.T) {
	tests := []struct {
		name          string
		close         int64
		wantEvent     EventType
		wantDirection Direction
		wantOK        bool
	}{
		{"close strictly below low is BOS", 89, EventBOS, DirectionBearish, true},
		{"close strictly above high is CHOCH, flips bullish", 111, EventCHOCH, DirectionBullish, true},
		{"close inside the range is nothing", 100, 0, DirectionBearish, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, event, ok := decideBreak(DirectionBearish, 110, 90, tt.close)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if event != tt.wantEvent || dir != tt.wantDirection {
				t.Fatalf("got (%v, %v), want (%v, %v)", dir, event, tt.wantDirection, tt.wantEvent)
			}
		})
	}
}

func TestDecideBreak_Undetermined_NeverBreaks(t *testing.T) {
	_, _, ok := decideBreak(DirectionUndetermined, 110, 90, 200)
	if ok {
		t.Fatal("decideBreak fired for DirectionUndetermined, want false: establishment is a separate decision, not a break")
	}
}

func TestDecideEstablishment(t *testing.T) {
	tests := []struct {
		name                  string
		priorHigh, latestHigh int64
		priorLow, latestLow   int64
		wantDirection         Direction
		wantOK                bool
	}{
		{"higher high + higher low establishes bullish", 100, 110, 50, 60, DirectionBullish, true},
		{"lower high + lower low establishes bearish", 100, 90, 50, 40, DirectionBearish, true},
		{"higher high but lower low is mixed: not established", 100, 110, 50, 40, DirectionUndetermined, false},
		{"lower high but higher low is mixed: not established", 100, 90, 50, 60, DirectionUndetermined, false},
		{"equal high is not higher: not established", 100, 100, 50, 60, DirectionUndetermined, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, ok := decideEstablishment(tt.priorHigh, tt.latestHigh, tt.priorLow, tt.latestLow)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && dir != tt.wantDirection {
				t.Fatalf("direction = %v, want %v", dir, tt.wantDirection)
			}
		})
	}
}

func TestDecideSwingAction(t *testing.T) {
	existing := Swing{ID: 1, Price: 100}
	candidateSame := NewSwing{Price: 100}
	candidateDiff := NewSwing{Price: 200}

	tests := []struct {
		name      string
		current   *Swing
		candidate NewSwing
		qualifies bool
		want      action
	}{
		{"nothing stored, qualifies: insert", nil, candidateSame, true, actionInsert},
		{"nothing stored, does not qualify: none", nil, candidateSame, false, actionNone},
		{"stored, qualifies, identical: skip", &existing, candidateSame, true, actionSkip},
		{"stored, qualifies, different: replace", &existing, candidateDiff, true, actionReplace},
		{"stored, does not qualify: retract", &existing, candidateSame, false, actionRetract},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideSwingAction(tt.current, tt.candidate, tt.qualifies); got != tt.want {
				t.Fatalf("decideSwingAction = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecideEventAction(t *testing.T) {
	existing := StructureEvent{ID: 1, Type: EventBOS, DirectionBefore: DirectionBullish, DirectionAfter: DirectionBullish}
	same := &NewStructureEvent{Type: EventBOS, DirectionBefore: DirectionBullish, DirectionAfter: DirectionBullish}
	different := &NewStructureEvent{Type: EventCHOCH, DirectionBefore: DirectionBullish, DirectionAfter: DirectionBearish}

	tests := []struct {
		name      string
		current   *StructureEvent
		candidate *NewStructureEvent
		want      action
	}{
		{"nothing stored, no candidate: none", nil, nil, actionNone},
		{"nothing stored, a candidate: insert", nil, same, actionInsert},
		{"stored, identical candidate: skip", &existing, same, actionSkip},
		{"stored, different candidate: replace", &existing, different, actionReplace},
		{"stored, no candidate: retract", &existing, nil, actionRetract},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideEventAction(tt.current, tt.candidate); got != tt.want {
				t.Fatalf("decideEventAction = %v, want %v", got, tt.want)
			}
		})
	}
}
