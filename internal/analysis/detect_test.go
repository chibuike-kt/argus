package analysis

import (
	"testing"
	"time"
)

func mkCandle(id int64, openTime time.Time, high, low int64, closed bool) CandleInput {
	return CandleInput{
		ID: id, OpenTime: openTime, CloseTime: openTime.Add(time.Minute),
		High: high, Low: low, Close: (high + low) / 2, IsClosed: closed,
	}
}

func TestDetectPivot_ConfirmedHighAndLow(t *testing.T) {
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	// A classic symmetric fractal: center bar strictly highest and
	// strictly lowest among its 2 left / 2 right neighbors.
	window := []CandleInput{
		mkCandle(1, base, 100, 90, true),
		mkCandle(2, base.Add(time.Minute), 105, 85, true),
		mkCandle(3, base.Add(2*time.Minute), 110, 80, true), // center: high=110, low=80
		mkCandle(4, base.Add(3*time.Minute), 104, 86, true),
		mkCandle(5, base.Add(4*time.Minute), 99, 91, true),
	}
	isHigh, isLow := detectPivot(window, 2, 2)
	if !isHigh {
		t.Error("isHigh = false, want true: center is strictly highest")
	}
	if !isLow {
		t.Error("isLow = false, want true: center is strictly lowest")
	}
}

func TestDetectPivot_EqualNeighborDisqualifies(t *testing.T) {
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		window   []CandleInput
		wantHigh bool
		wantLow  bool
	}{
		{
			name: "equal high on the left disqualifies the high only",
			window: []CandleInput{
				mkCandle(1, base, 110, 90, true), // equal to center's high
				mkCandle(2, base.Add(time.Minute), 105, 85, true),
				mkCandle(3, base.Add(2*time.Minute), 110, 80, true),
				mkCandle(4, base.Add(3*time.Minute), 104, 86, true),
				mkCandle(5, base.Add(4*time.Minute), 99, 91, true),
			},
			wantHigh: false, wantLow: true,
		},
		{
			name: "equal high on the right disqualifies the high only",
			window: []CandleInput{
				mkCandle(1, base, 100, 90, true),
				mkCandle(2, base.Add(time.Minute), 105, 85, true),
				mkCandle(3, base.Add(2*time.Minute), 110, 80, true),
				mkCandle(4, base.Add(3*time.Minute), 104, 86, true),
				mkCandle(5, base.Add(4*time.Minute), 110, 91, true), // equal to center's high
			},
			wantHigh: false, wantLow: true,
		},
		{
			name: "equal low disqualifies the low only",
			window: []CandleInput{
				mkCandle(1, base, 100, 80, true), // equal to center's low
				mkCandle(2, base.Add(time.Minute), 105, 85, true),
				mkCandle(3, base.Add(2*time.Minute), 110, 80, true),
				mkCandle(4, base.Add(3*time.Minute), 104, 86, true),
				mkCandle(5, base.Add(4*time.Minute), 99, 91, true),
			},
			wantHigh: true, wantLow: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isHigh, isLow := detectPivot(tt.window, 2, 2)
			if isHigh != tt.wantHigh || isLow != tt.wantLow {
				t.Fatalf("detectPivot = (high=%v, low=%v), want (high=%v, low=%v): equality must disqualify, not merely a strictly-greater/lower neighbor",
					isHigh, isLow, tt.wantHigh, tt.wantLow)
			}
		})
	}
}

func TestDetectPivot_NotAPivot(t *testing.T) {
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	window := []CandleInput{
		mkCandle(1, base, 120, 90, true), // higher than center: disqualifies center's high
		mkCandle(2, base.Add(time.Minute), 105, 85, true),
		mkCandle(3, base.Add(2*time.Minute), 110, 80, true),
		mkCandle(4, base.Add(3*time.Minute), 104, 86, true),
		mkCandle(5, base.Add(4*time.Minute), 99, 91, true),
	}
	isHigh, _ := detectPivot(window, 2, 2)
	if isHigh {
		t.Error("isHigh = true, want false: a strictly higher left neighbor disqualifies")
	}
}

func TestPivotWindow_ConfirmationTiming(t *testing.T) {
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	byTime := map[time.Time]CandleInput{
		base:                      mkCandle(1, base, 100, 90, true),
		base.Add(time.Minute):     mkCandle(2, base.Add(time.Minute), 105, 85, true),
		base.Add(2 * time.Minute): mkCandle(3, base.Add(2*time.Minute), 110, 80, true),
		base.Add(3 * time.Minute): mkCandle(4, base.Add(3*time.Minute), 104, 86, true),
		// base.Add(4*time.Minute) deliberately absent: the pivot at
		// base.Add(2*time.Minute) is NOT YET confirmable -- its second
		// right-hand bar has not closed (spec section 1: "A pivot is
		// confirmed only after its two right-hand bars have closed").
	}
	_, ok := pivotWindow(byTime, base.Add(2*time.Minute), 2, 2, time.Minute)
	if ok {
		t.Fatal("pivotWindow = ok, want not-ok: the right-hand window is not fully closed yet")
	}
}

func TestPivotWindow_StillFormingRightBarWithholds(t *testing.T) {
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	byTime := map[time.Time]CandleInput{
		base:                      mkCandle(1, base, 100, 90, true),
		base.Add(time.Minute):     mkCandle(2, base.Add(time.Minute), 105, 85, true),
		base.Add(2 * time.Minute): mkCandle(3, base.Add(2*time.Minute), 110, 80, true),
		base.Add(3 * time.Minute): mkCandle(4, base.Add(3*time.Minute), 104, 86, true),
		base.Add(4 * time.Minute): mkCandle(5, base.Add(4*time.Minute), 99, 91, false), // present but still forming
	}
	_, ok := pivotWindow(byTime, base.Add(2*time.Minute), 2, 2, time.Minute)
	if ok {
		t.Fatal("pivotWindow = ok, want not-ok: the last right-hand bar is present but not closed")
	}
}

func TestPivotWindow_ConfirmedOnceLastBarCloses(t *testing.T) {
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	byTime := map[time.Time]CandleInput{
		base:                      mkCandle(1, base, 100, 90, true),
		base.Add(time.Minute):     mkCandle(2, base.Add(time.Minute), 105, 85, true),
		base.Add(2 * time.Minute): mkCandle(3, base.Add(2*time.Minute), 110, 80, true),
		base.Add(3 * time.Minute): mkCandle(4, base.Add(3*time.Minute), 104, 86, true),
		base.Add(4 * time.Minute): mkCandle(5, base.Add(4*time.Minute), 99, 91, true),
	}
	window, ok := pivotWindow(byTime, base.Add(2*time.Minute), 2, 2, time.Minute)
	if !ok {
		t.Fatal("pivotWindow = not-ok, want ok: all five bars are present and closed")
	}
	if len(window) != 5 || window[2].ID != 3 {
		t.Fatalf("window = %+v, want the 5-bar window centered on candle 3", window)
	}
}

func TestPivotWindow_GapWithholds(t *testing.T) {
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	byTime := map[time.Time]CandleInput{
		base: mkCandle(1, base, 100, 90, true),
		// base.Add(1m) missing: a real gap, not a gradually-forming edge.
		base.Add(2 * time.Minute): mkCandle(3, base.Add(2*time.Minute), 110, 80, true),
		base.Add(3 * time.Minute): mkCandle(4, base.Add(3*time.Minute), 104, 86, true),
		base.Add(4 * time.Minute): mkCandle(5, base.Add(4*time.Minute), 99, 91, true),
	}
	_, ok := pivotWindow(byTime, base.Add(2*time.Minute), 2, 2, time.Minute)
	if ok {
		t.Fatal("pivotWindow = ok, want not-ok: a gap in the window must withhold confirmation, not silently compare the wrong neighbor")
	}
}
