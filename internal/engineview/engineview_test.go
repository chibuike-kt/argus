package engineview

import (
	"errors"
	"testing"
	"time"
)

func mins(base time.Time, ms ...int) []time.Time {
	out := make([]time.Time, len(ms))
	for i, m := range ms {
		out[i] = base.Add(time.Duration(m) * time.Minute)
	}
	return out
}

func TestCutTime(t *testing.T) {
	base := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		limit int
		kinds [][]time.Time
		want  int // minute of the cut; -1 for none
	}{
		{"nothing cut", 3, [][]time.Time{mins(base, 0, 1, 2), mins(base, 5)}, -1},
		{"one kind cut at its extra row", 3, [][]time.Time{mins(base, 0, 1, 2, 9), mins(base, 5, 6)}, 9},
		{"earliest cut across kinds wins", 2, [][]time.Time{mins(base, 0, 4, 8), mins(base, 1, 2, 3)}, 3},
		{"two rows at the cut time stay together", 2, [][]time.Time{mins(base, 0, 7, 7)}, 7},
		{"empty kinds", 2, [][]time.Time{nil, nil}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := cutTime(c.limit, c.kinds...)
			if c.want < 0 {
				if ok {
					t.Fatalf("cut at %s, want none", got)
				}
				return
			}
			if !ok || !got.Equal(base.Add(time.Duration(c.want)*time.Minute)) {
				t.Fatalf("cut = %s ok=%v, want minute %d", got, ok, c.want)
			}
		})
	}
}

func TestCursor_RoundTripsInUTC(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	// A local time and its UTC instant encode the same cursor.
	at := time.Date(2026, 3, 8, 1, 59, 59, 123456000, ny)
	c := Cursor{At: at, Kind: KindStructure, ID: 42}
	if c.Encode() != (Cursor{At: at.UTC(), Kind: KindStructure, ID: 42}).Encode() {
		t.Fatal("encoding depends on the time's location")
	}
	got, err := DecodeCursor(c.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if !got.At.Equal(at) || got.Kind != KindStructure || got.ID != 42 || got.At.Location() != time.UTC {
		t.Fatalf("decoded %+v, want %s structure 42 in UTC", got, at.UTC())
	}
}

func TestDecodeCursor_RejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "!!!", Cursor{At: time.Unix(0, 0), Kind: "other", ID: 1}.Encode(), "MTIzOnNldHVw", "eDpzZXR1cDox"} {
		if _, err := DecodeCursor(s); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("DecodeCursor(%q) err = %v, want ErrInvalidCursor", s, err)
		}
	}
}

func TestBehindSeconds(t *testing.T) {
	at := func(m int) *time.Time {
		v := time.Date(2026, 11, 22, 1, m, 0, 0, time.UTC)
		return &v
	}
	if s, ok := (InstrumentStatus{EvaluatedThrough: at(40), LatestClosedCloseTime: at(46)}).BehindSeconds(); !ok || s != 360 {
		t.Fatalf("behind = %d ok=%v, want 360", s, ok)
	}
	if s, ok := (InstrumentStatus{EvaluatedThrough: at(46), LatestClosedCloseTime: at(46)}).BehindSeconds(); !ok || s != 0 {
		t.Fatalf("caught up = %d ok=%v, want 0", s, ok)
	}
	// A cursor past the newest stored candle (a candle later removed) is
	// never a negative lag.
	if s, ok := (InstrumentStatus{EvaluatedThrough: at(47), LatestClosedCloseTime: at(46)}).BehindSeconds(); !ok || s != 0 {
		t.Fatalf("ahead = %d ok=%v, want 0", s, ok)
	}
	if _, ok := (InstrumentStatus{LatestClosedCloseTime: at(46)}).BehindSeconds(); ok {
		t.Fatal("never evaluated must have no lag figure")
	}
}
