//go:build integration

package marketdata

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/quant"
)

// adr-004-ws-contract-review.md, review item 5: the per-interval join
// point. Exercises LatestCompleteWindow against the real schema
// (SupersedeCandle's handshake specifically needs it, same reason every
// other file in this directory is an integration test -- guide 10).

func seedClosedMinute(t *testing.T, store *Store, instrumentID int64, openTime clock.EventTime, closePrice int64) {
	t.Helper()
	if _, err := store.UpsertForming(context.Background(), Tick{
		InstrumentID: instrumentID, OpenTime: openTime,
		Open: 100, High: 110, Low: 95, Close: quant.Price(closePrice), Volume: 10,
		Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatalf("UpsertForming: %v", err)
	}
	if _, err := store.Close(context.Background(), instrumentID, openTime); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestIntegration_LatestCompleteWindow_OneMinute_EqualsLatestClosed(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	base := et(2026, time.October, 27, 18, 0)

	for i := 0; i < 3; i++ {
		seedClosedMinute(t, store, instrumentID, clock.NewEventTime(base.Time().Add(time.Duration(i)*time.Minute)), int64(100+i))
	}

	got, err := store.LatestCompleteWindow(context.Background(), instrumentID, time.Minute)
	if err != nil {
		t.Fatalf("LatestCompleteWindow: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want the latest closed 1-minute candle")
	}
	wantOpen := base.Time().Add(2 * time.Minute)
	if !got.Window.Start.Time().Equal(wantOpen) {
		t.Errorf("Window.Start = %s, want %s (the latest closed minute)", got.Window.Start.Time(), wantOpen)
	}
	if got.Close != 102 {
		t.Errorf("Close = %d, want 102", got.Close)
	}
}

func TestIntegration_LatestCompleteWindow_DerivedInterval_LatestCompleteNotPartial(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	// Aligned to a clean 5-minute boundary.
	base := time.Date(2026, time.October, 27, 18, 0, 0, 0, time.UTC)

	// Window [18:00,18:05): all 5 minutes closed -- complete.
	for i := 0; i < 5; i++ {
		seedClosedMinute(t, store, instrumentID, clock.NewEventTime(base.Add(time.Duration(i)*time.Minute)), 100)
	}
	// Window [18:05,18:10): only the first 2 minutes closed -- still
	// forming as a whole, must not be returned as the join point.
	for i := 5; i < 7; i++ {
		seedClosedMinute(t, store, instrumentID, clock.NewEventTime(base.Add(time.Duration(i)*time.Minute)), 105)
	}

	got, err := store.LatestCompleteWindow(context.Background(), instrumentID, 5*time.Minute)
	if err != nil {
		t.Fatalf("LatestCompleteWindow: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want the [18:00,18:05) window")
	}
	if !got.Window.Start.Time().Equal(base) {
		t.Errorf("Window.Start = %s, want %s: the partial [18:05,18:10) window must not be returned", got.Window.Start.Time(), base)
	}
}

func TestIntegration_LatestCompleteWindow_DerivedRevisionReflectsCorrection(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	base := time.Date(2026, time.October, 27, 19, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		seedClosedMinute(t, store, instrumentID, clock.NewEventTime(base.Add(time.Duration(i)*time.Minute)), 100)
	}

	before, err := store.LatestCompleteWindow(context.Background(), instrumentID, 5*time.Minute)
	if err != nil {
		t.Fatalf("LatestCompleteWindow (before correction): %v", err)
	}
	if before == nil || before.Revision != 1 {
		t.Fatalf("before = %+v, want revision 1", before)
	}

	correctedMinute := clock.NewEventTime(base.Add(2 * time.Minute))
	if _, err := store.SupersedeCandle(context.Background(), instrumentID, correctedMinute, Correction{
		Open: 100, High: 150, Low: 95, Close: 140, Volume: 20,
		TickAligned: true, Provider: "test", IngestedAt: it(time.Now()),
	}); err != nil {
		t.Fatalf("SupersedeCandle: %v", err)
	}

	after, err := store.LatestCompleteWindow(context.Background(), instrumentID, 5*time.Minute)
	if err != nil {
		t.Fatalf("LatestCompleteWindow (after correction): %v", err)
	}
	if after == nil || after.Revision != 2 {
		t.Fatalf("after = %+v, want revision 2 (one superseded source minute)", after)
	}
	if after.High != 150 {
		t.Errorf("High = %d, want 150 (the corrected value folded in)", after.High)
	}
}

func TestIntegration_LatestCompleteWindow_NoClosedCandleYet(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})

	got, err := store.LatestCompleteWindow(context.Background(), instrumentID, time.Minute)
	if err != nil {
		t.Fatalf("LatestCompleteWindow: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil: no closed candle exists yet", got)
	}
}
