package analysis

import "testing"

func TestDetectSweep_Buy(t *testing.T) {
	tests := []struct {
		name                     string
		high, low, close         int64
		wantOK                   bool
		wantExtreme, wantReclaim int64
	}{
		{"strict penetration and reclaim is a sweep", 105, 95, 102, true, 95, 102},
		{"touch only (low == level) is not a sweep", 105, 100, 102, false, 0, 0},
		{"penetrates but closes beyond (no reclaim) is not a sweep", 105, 95, 99, false, 0, 0},
		{"close exactly on the level is not a reclaim", 105, 95, 100, false, 0, 0},
		{"no penetration at all is not a sweep", 110, 101, 105, false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extreme, reclaim, ok := detectSweep(SweepBuy, 100, tt.high, tt.low, tt.close)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if extreme != tt.wantExtreme || reclaim != tt.wantReclaim {
				t.Fatalf("got (extreme=%d, reclaim=%d), want (extreme=%d, reclaim=%d)", extreme, reclaim, tt.wantExtreme, tt.wantReclaim)
			}
		})
	}
}

func TestDetectSweep_Sell(t *testing.T) {
	tests := []struct {
		name             string
		high, low, close int64
		wantOK           bool
		wantExtreme      int64
		wantReclaim      int64
	}{
		{"strict penetration and reclaim is a sweep", 105, 95, 98, true, 105, 98},
		{"touch only (high == level) is not a sweep", 100, 95, 98, false, 0, 0},
		{"penetrates but closes beyond (no reclaim) is not a sweep", 105, 95, 101, false, 0, 0},
		{"close exactly on the level is not a reclaim", 105, 95, 100, false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extreme, reclaim, ok := detectSweep(SweepSell, 100, tt.high, tt.low, tt.close)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if extreme != tt.wantExtreme || reclaim != tt.wantReclaim {
				t.Fatalf("got (extreme=%d, reclaim=%d), want (extreme=%d, reclaim=%d)", extreme, reclaim, tt.wantExtreme, tt.wantReclaim)
			}
		})
	}
}

func TestDecideSweepAction(t *testing.T) {
	existing := Sweep{ID: 1, SweptSwingID: 10, PenetrationExtreme: 95, ReclaimClose: 102}
	same := &NewSweep{SweptSwingID: 10, PenetrationExtreme: 95, ReclaimClose: 102}
	different := &NewSweep{SweptSwingID: 10, PenetrationExtreme: 90, ReclaimClose: 103}

	tests := []struct {
		name      string
		current   *Sweep
		candidate *NewSweep
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
			if got := decideSweepAction(tt.current, tt.candidate); got != tt.want {
				t.Fatalf("decideSweepAction = %v, want %v", got, tt.want)
			}
		})
	}
}
