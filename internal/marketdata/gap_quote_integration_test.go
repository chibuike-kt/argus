//go:build integration

package marketdata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

func TestIntegration_RecordGap_IsIdempotent(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	start, end := et(2026, time.October, 26, 19, 0), et(2026, time.October, 26, 19, 5)

	first, err := store.RecordGap(context.Background(), instrumentID, start, end)
	if err != nil {
		t.Fatalf("first RecordGap: %v", err)
	}
	second, err := store.RecordGap(context.Background(), instrumentID, start, end)
	if err != nil {
		t.Fatalf("second (duplicate) RecordGap: %v, want idempotent success", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second.ID = %d, want the same row as first.ID = %d", second.ID, first.ID)
	}
	if !second.DetectedAt.Time().Equal(first.DetectedAt.Time()) {
		t.Errorf("DetectedAt changed on a duplicate detection: first=%s second=%s, want the original to stick", first.DetectedAt, second.DetectedAt)
	}
}

func TestIntegration_ResolveGap_RequiresExistingOpenGap(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})

	_, err := store.ResolveGap(context.Background(), instrumentID,
		et(2026, time.October, 26, 20, 0), et(2026, time.October, 26, 20, 5), "backfilled")
	if !errors.Is(err, ErrGapNotFound) {
		t.Fatalf("ResolveGap on a nonexistent gap: err = %v, want ErrGapNotFound", err)
	}
}

func TestIntegration_ResolveGap_CannotResolveTwice(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})
	start, end := et(2026, time.October, 26, 21, 0), et(2026, time.October, 26, 21, 5)

	if _, err := store.RecordGap(context.Background(), instrumentID, start, end); err != nil {
		t.Fatalf("RecordGap: %v", err)
	}
	if _, err := store.ResolveGap(context.Background(), instrumentID, start, end, "backfilled"); err != nil {
		t.Fatalf("first ResolveGap: %v", err)
	}

	// Reconnecting is not resolution, and neither is calling Resolve
	// again: a resolution once recorded must not be silently overwritten.
	_, err := store.ResolveGap(context.Background(), instrumentID, start, end, "provider_confirmed_no_data")
	if !errors.Is(err, ErrGapAlreadyResolved) {
		t.Fatalf("second ResolveGap: err = %v, want ErrGapAlreadyResolved", err)
	}
}

func TestIntegration_UpsertQuote_RejectsStaleUpdate(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})

	newer := et(2026, time.October, 26, 22, 0)
	older := et(2026, time.October, 26, 21, 0)

	if _, err := store.UpsertQuote(context.Background(), BidAsk{
		InstrumentID: instrumentID, Bid: 100, Ask: 101,
		QuoteTime: newer, IngestedAt: it(time.Now()), Provider: "test",
	}); err != nil {
		t.Fatalf("UpsertQuote (newer): %v", err)
	}

	_, err := store.UpsertQuote(context.Background(), BidAsk{
		InstrumentID: instrumentID, Bid: 999, Ask: 999,
		QuoteTime: older, IngestedAt: it(time.Now()), Provider: "test",
	})
	if !errors.Is(err, ErrStaleQuote) {
		t.Fatalf("UpsertQuote (older): err = %v, want ErrStaleQuote", err)
	}

	// The stored quote must be unchanged by the rejected write.
	current, err := store.GetQuote(context.Background(), instrumentID)
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if current.Bid != 100 || current.Ask != 101 {
		t.Fatalf("GetQuote after a rejected stale write = %+v, want the original (100, 101)", current)
	}
}

func TestIntegration_UpsertQuote_CrossedMarketIsAccepted(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db, clock.Real{})

	// A crossed quote (ask < bid) is real market data, not rejected
	// (approved decision); IsCrossed is how the caller is meant to
	// notice it.
	q, err := store.UpsertQuote(context.Background(), BidAsk{
		InstrumentID: instrumentID, Bid: 101, Ask: 100,
		QuoteTime: et(2026, time.October, 26, 22, 0), IngestedAt: it(time.Now()), Provider: "test",
	})
	if err != nil {
		t.Fatalf("UpsertQuote (crossed): %v", err)
	}
	if !q.IsCrossed() {
		t.Error("IsCrossed() = false for a stored bid > ask quote")
	}
}
