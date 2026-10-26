package ingestion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
)

// fakeInstrumentLookup is a handwritten fake for instrumentLookup (guide
// 10: no mock framework for pure logic).
type fakeInstrumentLookup struct {
	byKey map[string]instruments.Instrument
	calls int
}

func (f *fakeInstrumentLookup) GetByProviderAndSymbol(_ context.Context, provider, symbol string) (instruments.Instrument, error) {
	f.calls++
	inst, ok := f.byKey[provider+"/"+symbol]
	if !ok {
		return instruments.Instrument{}, instruments.ErrNotFound
	}
	return inst, nil
}

func fixtureInstrument() instruments.Instrument {
	return instruments.Instrument{
		ID:           1,
		Provider:     "fixture",
		NativeSymbol: "BTCUSDT",
		PriceScale:   2,
		QtyScale:     6,
		TickSize:     1, // 0.01 at scale 2
		QtyStepSize:  1,
	}
}

func newTestNormalizer(lookup instrumentLookup) *Normalizer {
	cache := NewInstrumentCache(lookup)
	fixedClock := clock.NewFixed(time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC))
	return NewNormalizer(cache, fixedClock)
}

func TestNormalizeCandle_Valid(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	n := newTestNormalizer(lookup)

	c := ProviderCandle{
		Symbol:   "BTCUSDT",
		OpenTime: time.Date(2026, time.October, 27, 11, 59, 0, 0, time.UTC),
		Open:     "64250.50", High: "64300.00", Low: "64200.00", Close: "64280.25",
		Volume:  "12.5",
		IsFinal: true,
	}
	got, err := n.NormalizeCandle(context.Background(), "fixture", c)
	if err != nil {
		t.Fatalf("NormalizeCandle: %v", err)
	}

	if got.InstrumentID != 1 {
		t.Errorf("InstrumentID = %d, want 1", got.InstrumentID)
	}
	wantOpen, _ := quant.ParsePrice("64250.50", 2)
	if got.Open != wantOpen {
		t.Errorf("Open = %d, want %d", got.Open, wantOpen)
	}
	wantVolume, _ := quant.ParseQty("12.5", 6)
	if got.Volume != wantVolume {
		t.Errorf("Volume = %d, want %d", got.Volume, wantVolume)
	}
	if !got.IsFinal {
		t.Error("IsFinal = false, want the provider's claim (true) passed through")
	}
	if !got.TickAligned {
		t.Error("TickAligned = false, want true: all four prices are multiples of tick_size 0.01")
	}
	if !got.OpenTime.Time().Equal(c.OpenTime) {
		t.Errorf("OpenTime = %s, want %s", got.OpenTime, c.OpenTime)
	}
}

func TestNormalizeCandle_UnregisteredInstrument(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{}}
	n := newTestNormalizer(lookup)

	_, err := n.NormalizeCandle(context.Background(), "fixture", ProviderCandle{
		Symbol:   "NOPE",
		OpenTime: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, instruments.ErrNotFound) {
		t.Fatalf("NormalizeCandle for an unregistered symbol: err = %v, want it to wrap instruments.ErrNotFound", err)
	}
}

func TestNormalizeCandle_PrecisionExceedsScale(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(), // price_scale = 2
	}}
	n := newTestNormalizer(lookup)

	_, err := n.NormalizeCandle(context.Background(), "fixture", ProviderCandle{
		Symbol:   "BTCUSDT",
		OpenTime: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC),
		Open:     "64250.505", High: "64300.00", Low: "64200.00", Close: "64280.25", // open has 3 fractional digits
		Volume: "1",
	})
	if !errors.Is(err, ErrScaleMismatch) {
		t.Fatalf("NormalizeCandle with excess precision: err = %v, want ErrScaleMismatch", err)
	}
	if !errors.Is(err, quant.ErrPrecisionLoss) {
		t.Fatalf("NormalizeCandle with excess precision: err = %v, want it to also wrap quant.ErrPrecisionLoss", err)
	}
}

func TestNormalizeCandle_MalformedDecimal_IsNotReportedAsScaleMismatch(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	n := newTestNormalizer(lookup)

	_, err := n.NormalizeCandle(context.Background(), "fixture", ProviderCandle{
		Symbol:   "BTCUSDT",
		OpenTime: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC),
		Open:     "not-a-number", High: "64300.00", Low: "64200.00", Close: "64280.25",
		Volume: "1",
	})
	if !errors.Is(err, quant.ErrMalformedDecimal) {
		t.Fatalf("NormalizeCandle with malformed open: err = %v, want quant.ErrMalformedDecimal", err)
	}
	if errors.Is(err, ErrScaleMismatch) {
		t.Fatal("a malformed decimal was reported as ErrScaleMismatch -- these are different problems with different remedies, must not be conflated")
	}
}

func TestNormalizeCandle_NegativeValue(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	n := newTestNormalizer(lookup)

	_, err := n.NormalizeCandle(context.Background(), "fixture", ProviderCandle{
		Symbol:   "BTCUSDT",
		OpenTime: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC),
		Open:     "-1.00", High: "64300.00", Low: "64200.00", Close: "64280.25",
		Volume: "1",
	})
	if !errors.Is(err, quant.ErrNegativeValue) {
		t.Fatalf("NormalizeCandle with a negative open: err = %v, want quant.ErrNegativeValue", err)
	}
}

func TestNormalizeCandle_NotMinuteAligned(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	n := newTestNormalizer(lookup)

	_, err := n.NormalizeCandle(context.Background(), "fixture", ProviderCandle{
		Symbol:   "BTCUSDT",
		OpenTime: time.Date(2026, time.October, 27, 12, 0, 30, 0, time.UTC), // :30 seconds
		Open:     "1", High: "1", Low: "1", Close: "1", Volume: "1",
	})
	if !errors.Is(err, ErrNotMinuteAligned) {
		t.Fatalf("NormalizeCandle with a non-minute-aligned open_time: err = %v, want ErrNotMinuteAligned", err)
	}
}

func TestNormalizeCandle_NonUTCOpenTimeIsNormalized(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	n := newTestNormalizer(lookup)

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// 08:00 in America/New_York (UTC-4 in October) is 12:00 UTC, which is
	// minute-aligned -- this must be accepted, with OpenTime normalized
	// to UTC, not rejected for "not being UTC already".
	local := time.Date(2026, time.October, 27, 8, 0, 0, 0, loc)

	got, err := n.NormalizeCandle(context.Background(), "fixture", ProviderCandle{
		Symbol: "BTCUSDT", OpenTime: local,
		Open: "1.00", High: "1.00", Low: "1.00", Close: "1.00", Volume: "1",
	})
	if err != nil {
		t.Fatalf("NormalizeCandle with a non-UTC but minute-aligned open_time: %v", err)
	}
	wantUTC := local.UTC()
	if !got.OpenTime.Time().Equal(wantUTC) || got.OpenTime.Time().Location() != time.UTC {
		t.Fatalf("OpenTime = %s, want %s in UTC", got.OpenTime, wantUTC)
	}
}

func TestNormalizeCandle_OHLCInconsistent(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	n := newTestNormalizer(lookup)

	_, err := n.NormalizeCandle(context.Background(), "fixture", ProviderCandle{
		Symbol:   "BTCUSDT",
		OpenTime: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC),
		Open:     "100.00", High: "50.00", Low: "40.00", Close: "100.00", // high below open
		Volume: "1",
	})
	if !errors.Is(err, ErrOHLCInconsistent) {
		t.Fatalf("NormalizeCandle with high below open: err = %v, want ErrOHLCInconsistent", err)
	}
}

func TestNormalizeCandle_TickMisalignmentIsAQualitySignalNotAnError(t *testing.T) {
	// tick_size = 5 (0.05 at scale 2): a price of 64250.52 is a valid,
	// in-scale decimal, but not a multiple of 0.05.
	misaligned := fixtureInstrument()
	misaligned.TickSize = 5
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": misaligned,
	}}
	n := newTestNormalizer(lookup)

	got, err := n.NormalizeCandle(context.Background(), "fixture", ProviderCandle{
		Symbol:   "BTCUSDT",
		OpenTime: time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC),
		Open:     "64250.52", High: "64250.52", Low: "64250.52", Close: "64250.52",
		Volume: "1",
	})
	if err != nil {
		t.Fatalf("NormalizeCandle with a tick-misaligned but otherwise valid price: %v, want no error", err)
	}
	if got.TickAligned {
		t.Error("TickAligned = true, want false: 64250.52 is not a multiple of tick_size 0.05")
	}
}

func TestNormalizeQuote_Valid(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	n := newTestNormalizer(lookup)

	got, err := n.NormalizeQuote(context.Background(), "fixture", ProviderQuote{
		Symbol: "BTCUSDT", Bid: "64250.00", Ask: "64250.50",
		QuoteTime: time.Date(2026, time.October, 27, 12, 0, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NormalizeQuote: %v", err)
	}
	if got.InstrumentID != 1 {
		t.Errorf("InstrumentID = %d, want 1", got.InstrumentID)
	}
	if got.Bid >= got.Ask {
		t.Errorf("Bid=%d Ask=%d, want Bid < Ask for this fixture", got.Bid, got.Ask)
	}
}

func TestInstrumentCache_CachesAfterFirstLookup(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	cache := NewInstrumentCache(lookup)

	for range 5 {
		if _, err := cache.Get(context.Background(), "fixture", "BTCUSDT"); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	if lookup.calls != 1 {
		t.Fatalf("underlying lookup called %d times, want exactly 1 (every repeat should hit the cache)", lookup.calls)
	}
}

func TestInstrumentCache_MissIsNotCached(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{}}
	cache := NewInstrumentCache(lookup)

	for range 3 {
		if _, err := cache.Get(context.Background(), "fixture", "NOPE"); err == nil {
			t.Fatal("Get for an unregistered symbol succeeded, want an error")
		}
	}
	if lookup.calls != 3 {
		t.Fatalf("underlying lookup called %d times, want exactly 3: a miss must never be remembered as a false negative", lookup.calls)
	}
}

func TestInstrumentCache_Invalidate_ForcesReQuery(t *testing.T) {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{
		"fixture/BTCUSDT": fixtureInstrument(),
	}}
	cache := NewInstrumentCache(lookup)

	if _, err := cache.Get(context.Background(), "fixture", "BTCUSDT"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := cache.Get(context.Background(), "fixture", "BTCUSDT"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("underlying lookup called %d times before Invalidate, want 1", lookup.calls)
	}

	cache.Invalidate("fixture", "BTCUSDT")

	if _, err := cache.Get(context.Background(), "fixture", "BTCUSDT"); err != nil {
		t.Fatalf("Get after Invalidate: %v", err)
	}
	if lookup.calls != 2 {
		t.Fatalf("underlying lookup called %d times after Invalidate, want 2 (one re-query)", lookup.calls)
	}
}
