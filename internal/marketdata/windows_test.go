package marketdata

import (
	"testing"
	"time"
)

func TestWindowsInRange_Ascending_BoundaryAlignment(t *testing.T) {
	// from is not itself 5-minute aligned (18:02); the first window must
	// still snap to the clean boundary (18:00), not start at 18:02.
	from := utc(2026, time.October, 26, 18, 2)
	to := utc(2026, time.October, 26, 18, 16) // exclusive
	got := WindowsInRange(from, to, 5*time.Minute, false, 100)

	wantStarts := []time.Time{
		utc(2026, time.October, 26, 18, 0),
		utc(2026, time.October, 26, 18, 5),
		utc(2026, time.October, 26, 18, 10),
		utc(2026, time.October, 26, 18, 15),
	}
	if len(got) != len(wantStarts) {
		t.Fatalf("got %d windows, want %d: %+v", len(got), len(wantStarts), got)
	}
	for i, w := range got {
		if !w.Start.Time().Equal(wantStarts[i]) {
			t.Errorf("window[%d].Start = %s, want %s", i, w.Start.Time(), wantStarts[i])
		}
		if !w.End.Time().Equal(wantStarts[i].Add(5 * time.Minute)) {
			t.Errorf("window[%d].End = %s, want %s", i, w.End.Time(), wantStarts[i].Add(5*time.Minute))
		}
	}
}

func TestWindowsInRange_Descending_NewestFirst(t *testing.T) {
	from := utc(2026, time.October, 26, 18, 0)
	to := utc(2026, time.October, 26, 18, 15) // exclusive, exactly aligned
	got := WindowsInRange(from, to, 5*time.Minute, true, 100)

	wantStarts := []time.Time{
		utc(2026, time.October, 26, 18, 10), // newest first
		utc(2026, time.October, 26, 18, 5),
		utc(2026, time.October, 26, 18, 0),
	}
	if len(got) != len(wantStarts) {
		t.Fatalf("got %d windows, want %d: %+v", len(got), len(wantStarts), got)
	}
	for i, w := range got {
		if !w.Start.Time().Equal(wantStarts[i]) {
			t.Errorf("window[%d].Start = %s, want %s", i, w.Start.Time(), wantStarts[i])
		}
	}
}

func TestWindowsInRange_Descending_UnalignedTo(t *testing.T) {
	// to (18:17) is not itself aligned: the newest window must be the
	// last one that FITS before to (18:10-18:15), not a partial
	// 18:15-18:20 window that hasn't even closed as of to.
	from := utc(2026, time.October, 26, 18, 0)
	to := utc(2026, time.October, 26, 18, 17)
	got := WindowsInRange(from, to, 5*time.Minute, true, 1)

	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1: %+v", len(got), got)
	}
	want := utc(2026, time.October, 26, 18, 10)
	if !got[0].Start.Time().Equal(want) {
		t.Errorf("window[0].Start = %s, want %s", got[0].Start.Time(), want)
	}
}

func TestWindowsInRange_LimitCaps(t *testing.T) {
	from := utc(2026, time.October, 26, 0, 0)
	to := from.Add(24 * time.Hour) // 1440 one-minute windows available
	got := WindowsInRange(from, to, time.Minute, false, 5)
	if len(got) != 5 {
		t.Fatalf("got %d windows, want 5 (limit cap)", len(got))
	}

	got = WindowsInRange(from, to, time.Minute, true, 5)
	if len(got) != 5 {
		t.Fatalf("got %d windows (desc), want 5 (limit cap)", len(got))
	}
}

func TestWindowsInRange_InvalidRangeOrLimit(t *testing.T) {
	from := utc(2026, time.October, 26, 18, 0)
	to := utc(2026, time.October, 26, 18, 5)

	if got := WindowsInRange(to, from, 5*time.Minute, false, 10); got != nil {
		t.Errorf("to before from: got %+v, want nil", got)
	}
	if got := WindowsInRange(from, from, 5*time.Minute, false, 10); got != nil {
		t.Errorf("empty range: got %+v, want nil", got)
	}
	if got := WindowsInRange(from, to, 5*time.Minute, false, 0); got != nil {
		t.Errorf("zero limit: got %+v, want nil", got)
	}
}
