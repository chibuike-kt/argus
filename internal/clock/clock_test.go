package clock

import (
	"sync"
	"testing"
	"time"
)

func TestReal_ReturnsUTC(t *testing.T) {
	now := Real{}.Now()
	if now.Location() != time.UTC {
		t.Fatalf("Real.Now() location = %v, want UTC", now.Location())
	}
	if diff := time.Since(now); diff < 0 || diff > time.Second {
		t.Fatalf("Real.Now() = %v, not close to wall clock (diff %v)", now, diff)
	}
}

func TestFixed_AlwaysReturnsSameInstant(t *testing.T) {
	local, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	src := time.Date(2026, 10, 26, 12, 0, 0, 0, local)
	f := NewFixed(src)

	want := src.UTC()
	for range 3 {
		if got := f.Now(); !got.Equal(want) || got.Location() != time.UTC {
			t.Fatalf("Fixed.Now() = %v, want %v in UTC", got, want)
		}
	}
}

func TestMutable_SetAndAdvance(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewMutable(start)

	if got := m.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}

	m.Advance(time.Hour)
	want := start.Add(time.Hour)
	if got := m.Now(); !got.Equal(want) {
		t.Fatalf("after Advance: Now() = %v, want %v", got, want)
	}

	next := time.Date(2026, 6, 1, 0, 0, 0, 0, time.FixedZone("x", 3600))
	m.Set(next)
	if got := m.Now(); !got.Equal(next.UTC()) || got.Location() != time.UTC {
		t.Fatalf("after Set: Now() = %v, want %v in UTC", got, next.UTC())
	}
}

func TestMutable_ConcurrentAccess(_ *testing.T) {
	m := NewMutable(time.Unix(0, 0))
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			m.Advance(time.Millisecond)
		}()
		go func() {
			defer wg.Done()
			_ = m.Now()
		}()
	}
	wg.Wait()
}
