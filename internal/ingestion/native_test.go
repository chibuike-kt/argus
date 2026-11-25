package ingestion

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/store"
)

// L1.1 decision 1: native volume float noise.
func TestParseNativeVolume(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		scale       quant.Scale
		divisor     int64
		want        quant.Qty
		wantRounded bool
		wantErr     bool
	}{
		{"fits the scale exactly", "530937.81", 2, 1000, 53093781, false, false},
		{"real SOL 1h noise above (L1.0)", "530937.8100000001", 2, 1000, 53093781, true, false},
		{"real SOL 1h noise below (L1.0)", "736808.0699999999", 2, 1000, 73680807, true, false},
		{"real SOL 4h noise (L1.0)", "2509084.9500000002", 2, 1000, 250908495, true, false},
		// unit 0.01, 1/1000 of it 0.00001
		{"just below 1/1000 of a unit", "2.00000999", 2, 1000, 200, true, false},
		{"exactly 1/1000 of a unit is not noise", "2.00001", 2, 1000, 0, false, true},
		{"a real extra digit is rejected", "0.125", 2, 1000, 0, false, true},
		{"the divisor is configurable", "2.00001", 2, 100, 200, true, false},
		{"an excess just under half a unit is still rejected", "2.0049999999", 2, 1000, 0, false, true},
		{"malformed", "1,5", 2, 1000, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, rounded, err := ParseNativeVolume(tt.in, tt.scale, tt.divisor)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseNativeVolume(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err != nil {
				if tt.name != "malformed" && !errors.Is(err, quant.ErrPrecisionLoss) {
					t.Fatalf("err = %v, want ErrPrecisionLoss (the 1 minute path's own rejection)", err)
				}
				return
			}
			if got != tt.want || rounded != tt.wantRounded {
				t.Fatalf("ParseNativeVolume(%q) = %d rounded=%v, want %d rounded=%v", tt.in, got, rounded, tt.want, tt.wantRounded)
			}
		})
	}
}

func nativeTestNormalizer() *Normalizer {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{"fixture/BTCUSDT": fixtureInstrument()}}
	return newTestNormalizer(lookup)
}

func TestNormalizeNative_AlignmentAndOneMinuteUnchanged(t *testing.T) {
	n := nativeTestNormalizer()
	ctx := context.Background()
	at := func(h, m int) time.Time { return time.Date(2026, 11, 20, h, m, 0, 0, time.UTC) }
	c := ProviderCandle{Symbol: "BTCUSDT", OpenTime: at(16, 0), Open: "1.00", High: "2.00", Low: "1.00", Close: "1.50", Volume: "12.0000000000000001"}

	nc, rounded, err := n.NormalizeNative(ctx, "fixture", marketdata.Interval4h, c, DefaultNativeVolumeNoiseDivisor)
	if err != nil || !rounded || nc.Volume != 12000000 || !nc.IsFinal {
		t.Fatalf("NormalizeNative = %+v rounded=%v err=%v; want volume 12.000000 rounded", nc, rounded, err)
	}
	// A 4h candle opening at 17:00 UTC is not on a 4h boundary.
	c.OpenTime = at(17, 0)
	if _, _, err := n.NormalizeNative(ctx, "fixture", marketdata.Interval4h, c, DefaultNativeVolumeNoiseDivisor); !errors.Is(err, marketdata.ErrNotIntervalAligned) {
		t.Fatalf("misaligned 4h = %v, want ErrNotIntervalAligned", err)
	}
	// Prices are never rounded, native or not.
	c.OpenTime = at(16, 0)
	c.Close = "1.505"
	if _, _, err := n.NormalizeNative(ctx, "fixture", marketdata.Interval4h, c, DefaultNativeVolumeNoiseDivisor); !errors.Is(err, ErrScaleMismatch) {
		t.Fatalf("a price with an extra digit = %v, want ErrScaleMismatch", err)
	}
	// 1 minute ingestion is unchanged: the same float noise is rejected.
	c.Close = "1.50"
	c.OpenTime = at(16, 1)
	if _, err := n.NormalizeCandle(ctx, "fixture", c); !errors.Is(err, ErrScaleMismatch) {
		t.Fatalf("1m NormalizeCandle with float noise = %v, want ErrScaleMismatch (1m never rounds)", err)
	}
}

// fakeNativeProvider serves a fixed set of native candles.
type fakeNativeProvider struct {
	historicalOnlyProvider
	candles map[marketdata.Interval][]ProviderCandle
	err     error
	calls   int
}

func (p *fakeNativeProvider) HistoricalIntervalCandles(_ context.Context, _ string, interval marketdata.Interval, start, end time.Time) ([]ProviderCandle, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	d, _ := interval.Duration()
	var out []ProviderCandle
	for _, c := range p.candles[interval] {
		if c.OpenTime.Add(d).After(start) && c.OpenTime.Before(end) {
			out = append(out, c)
		}
	}
	return out, nil
}

// fakeNativeStore is an in-memory nativeStore: current candles per
// (interval, open time), with revisions, plus 1 minute rows for the
// aggregate.
type fakeNativeStore struct {
	byKey  map[string]marketdata.Candle
	minute []store.Candle
	nextID int64
}

func newFakeNativeStore() *fakeNativeStore {
	return &fakeNativeStore{byKey: map[string]marketdata.Candle{}}
}

func nkey(i marketdata.Interval, id int64, t time.Time) string {
	return fmt.Sprintf("%s/%d/%d", i, id, t.UTC().UnixNano())
}

func (f *fakeNativeStore) InsertClosed(_ context.Context, interval marketdata.Interval, t marketdata.Tick, now time.Time) (marketdata.Candle, bool, error) {
	d, _ := interval.Duration()
	if t.OpenTime.Time().Add(d).After(now) {
		return marketdata.Candle{}, false, marketdata.ErrNativeCandleNotClosed
	}
	k := nkey(interval, t.InstrumentID, t.OpenTime.Time())
	if c, ok := f.byKey[k]; ok {
		return c, false, nil
	}
	f.nextID++
	c := marketdata.Candle{ID: f.nextID, InstrumentID: t.InstrumentID, OpenTime: t.OpenTime, CloseTime: clock.NewEventTime(t.OpenTime.Time().Add(d)),
		Open: t.Open, High: t.High, Low: t.Low, Close: t.Close, Volume: t.Volume, Revision: 1, IsClosed: true, Interval: interval}
	f.byKey[k] = c
	return c, true, nil
}

func (f *fakeNativeStore) SupersedeCandleAt(_ context.Context, interval marketdata.Interval, id int64, open clock.EventTime, corr marketdata.Correction) (marketdata.Candle, error) {
	k := nkey(interval, id, open.Time())
	old, ok := f.byKey[k]
	if !ok {
		return marketdata.Candle{}, marketdata.ErrCandleNotFound
	}
	f.nextID++
	c := old
	c.ID, c.Revision = f.nextID, old.Revision+1
	c.Open, c.High, c.Low, c.Close, c.Volume = corr.Open, corr.High, corr.Low, corr.Close, corr.Volume
	f.byKey[k] = c
	return c, nil
}

func (f *fakeNativeStore) GetCurrentAt(_ context.Context, interval marketdata.Interval, id int64, open clock.EventTime) (marketdata.Candle, error) {
	c, ok := f.byKey[nkey(interval, id, open.Time())]
	if !ok {
		return marketdata.Candle{}, marketdata.ErrCandleNotFound
	}
	return c, nil
}

func (f *fakeNativeStore) ListCurrentCandlesInRange(_ context.Context, id int64, start, end time.Time) ([]store.Candle, error) {
	var out []store.Candle
	for _, c := range f.minute {
		if c.InstrumentID == id && !c.OpenTime.Before(start) && c.OpenTime.Before(end) {
			out = append(out, c)
		}
	}
	return out, nil
}

func nativeTestService(p Provider, st *fakeNativeStore) *Service {
	lookup := &fakeInstrumentLookup{byKey: map[string]instruments.Instrument{"fixture/BTCUSDT": fixtureInstrument()}}
	cache := NewInstrumentCache(lookup)
	return &Service{
		providerName: "fixture", provider: p, normalizer: NewNormalizer(cache, clock.Real{}), instruments: cache,
		calendar: marketdata.ContinuousCalendar{}, logger: discardLogger(),
		native: st, nativeNoiseDivisor: DefaultNativeVolumeNoiseDivisor,
	}
}

func pc(open time.Time, closePrice, volume string) ProviderCandle {
	return ProviderCandle{Symbol: "BTCUSDT", OpenTime: open, Open: "1.00", High: "9.00", Low: "1.00", Close: closePrice, Volume: volume}
}

func TestIngestNative_StoresClosedOnlyAndAppliesTheRevisionRule(t *testing.T) {
	ctx := context.Background()
	h := func(n int) time.Time { return time.Date(2026, 11, 20, n, 0, 0, 0, time.UTC) }
	p := &fakeNativeProvider{candles: map[marketdata.Interval][]ProviderCandle{marketdata.Interval1h: {
		pc(h(10), "2.00", "5"),
		pc(h(11), "3.00", "6.0000000000001"), // float noise: rounded
		pc(h(12), "4.00", "7.0000001"),       // a real seventh decimal: rejected
		pc(h(13), "5.00", "8"),               // closes at 14:00: forming at now
	}}}
	st := newFakeNativeStore()
	svc := nativeTestService(p, st)
	now := h(13).Add(30 * time.Minute)

	res, err := svc.IngestNative(ctx, "BTCUSDT", marketdata.Interval1h, h(10), now, now)
	if err != nil {
		t.Fatal(err)
	}
	want := NativeResult{Fetched: 4, Forming: 1, Inserted: 2, Rejected: 1, Rounded: 1}
	if res != want {
		t.Fatalf("first IngestNative = %+v, want %+v", res, want)
	}
	if _, err := st.GetCurrentAt(ctx, marketdata.Interval1h, 1, clock.NewEventTime(h(13))); !errors.Is(err, marketdata.ErrCandleNotFound) {
		t.Fatal("the forming 13:00 candle was stored")
	}
	if svc.NativeVolumeRoundings() != 1 {
		t.Fatalf("roundings = %d, want 1", svc.NativeVolumeRoundings())
	}

	// The provider corrects 10:00; 11:00 is unchanged; 13:00 has closed.
	p.candles[marketdata.Interval1h][0] = pc(h(10), "2.50", "5")
	later := h(14).Add(time.Minute)
	res, err = svc.IngestNative(ctx, "BTCUSDT", marketdata.Interval1h, h(10), later, later)
	if err != nil {
		t.Fatal(err)
	}
	want = NativeResult{Fetched: 4, Inserted: 1, Unchanged: 1, Corrected: 1, Rejected: 1, Rounded: 1}
	if res != want {
		t.Fatalf("second IngestNative = %+v, want %+v", res, want)
	}
	c, _ := st.GetCurrentAt(ctx, marketdata.Interval1h, 1, clock.NewEventTime(h(10)))
	if c.Revision != 2 || c.Close != 250 {
		t.Fatalf("corrected 10:00 = revision %d close %d, want revision 2 close 2.50", c.Revision, c.Close)
	}

	// A provider without native candles is refused, not retried as data.
	plain := nativeTestService(historicalOnlyProvider{}, newFakeNativeStore())
	if _, err := plain.IngestNative(ctx, "BTCUSDT", marketdata.Interval1h, h(10), later, later); !errors.Is(err, ErrNativeUnsupported) || plain.SupportsNative() {
		t.Fatalf("IngestNative without native support = %v (supports %v), want ErrNativeUnsupported", err, plain.SupportsNative())
	}
}

func TestCompareNativeWithAggregate_CountsAndNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	open := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	st := newFakeNativeStore()
	// 15 closed 1 minute candles: open 100, high 900 at 10:07, low 50, close 300, volume 15.
	for i := 0; i < 15; i++ {
		m := open.Add(time.Duration(i) * time.Minute)
		c := store.Candle{InstrumentID: 1, OpenTime: m, CloseTime: m.Add(time.Minute), Open: 100, High: 200, Low: 100, Close: 150, Volume: 1, Revision: 1, IsClosed: true, Interval: "1m"}
		if i == 7 {
			c.High = 900
		}
		if i == 3 {
			c.Low = 50
		}
		if i == 14 {
			c.Close = 300
		}
		st.minute = append(st.minute, c)
	}
	svc := nativeTestService(&fakeNativeProvider{}, st)
	far := open.Add(time.Hour)
	if _, _, err := st.InsertClosed(ctx, marketdata.Interval15m, marketdata.Tick{InstrumentID: 1, OpenTime: clock.NewEventTime(open), Open: 100, High: 900, Low: 50, Close: 300, Volume: 15}, far); err != nil {
		t.Fatal(err)
	}
	got, err := svc.CompareNativeWithAggregate(ctx, "BTCUSDT", marketdata.Interval15m, 1, open)
	if err != nil || !got.Compared || len(got.Differs) != 0 || svc.NativeAggregateMismatches() != 0 {
		t.Fatalf("matching candle: %+v %v mismatches=%d", got, err, svc.NativeAggregateMismatches())
	}
	// The native candle disagrees on high and volume: counted, logged, kept.
	k := nkey(marketdata.Interval15m, 1, open)
	c := st.byKey[k]
	c.High, c.Volume = 950, 16
	st.byKey[k] = c
	got, err = svc.CompareNativeWithAggregate(ctx, "BTCUSDT", marketdata.Interval15m, 1, open)
	if err != nil || !got.Compared || !reflect.DeepEqual(got.Differs, []string{"high", "volume"}) || svc.NativeAggregateMismatches() != 1 {
		t.Fatalf("differing candle: %+v %v mismatches=%d", got, err, svc.NativeAggregateMismatches())
	}
	if st.byKey[k].High != 950 || st.byKey[k].Revision != 1 {
		t.Fatalf("the native candle was overwritten: %+v", st.byKey[k])
	}
	// A missing minute: not compared, not counted.
	st.minute = st.minute[1:]
	got, err = svc.CompareNativeWithAggregate(ctx, "BTCUSDT", marketdata.Interval15m, 1, open)
	if err != nil || got.Compared || svc.NativeAggregateMismatches() != 1 {
		t.Fatalf("incomplete window: %+v %v mismatches=%d", got, err, svc.NativeAggregateMismatches())
	}
}

// streamingProvider streams fixed candles then returns.
type streamingProvider struct {
	historicalOnlyProvider
	stream []ProviderCandle
}

func (p streamingProvider) StreamCandles(ctx context.Context, _ string, handle func(context.Context, ProviderCandle) error) error {
	for _, c := range p.stream {
		if err := handle(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func TestWatchCandlesNotify_ReportsOnlyClosedMinutes(t *testing.T) {
	m := time.Date(2026, 11, 20, 13, 59, 0, 0, time.UTC)
	forming := ProviderCandle{Symbol: "BTCUSDT", OpenTime: m, Open: "1.00", High: "1.00", Low: "1.00", Close: "1.00", Volume: "1"}
	final := forming
	final.IsFinal = true
	p := streamingProvider{stream: []ProviderCandle{forming, forming, final}}
	svc := testService(t, newFakeCandleStore(), p, marketdata.ContinuousCalendar{})
	var got []time.Time
	if err := svc.WatchCandlesNotify(context.Background(), "BTCUSDT", func(open time.Time) { got = append(got, open) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Equal(m) {
		t.Fatalf("onClosed calls = %v, want exactly one, for 13:59", got)
	}
}

// L1.1 decision 3: stale when now - newest close > interval + crypto max age.
func TestFeedHealth_EvaluateNative(t *testing.T) {
	now := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
	cfg := FreshnessConfig{CryptoPerpMaxAge: 2 * time.Minute, ForexMaxAge: time.Minute}
	h := NewFeedHealth(cfg, fakeCalendar{status: marketdata.MarketStatusOpen}, clock.NewFixed(now))
	if got, _ := cfg.NativeMaxAge("crypto_perp", marketdata.Interval1h); got != time.Hour+2*time.Minute {
		t.Fatalf("NativeMaxAge(1h) = %s, want 1h2m", got)
	}
	cases := []struct {
		newestClose time.Time
		want        FeedStatus
	}{
		{now.Add(-time.Hour - 2*time.Minute), FeedStatusFresh},
		{now.Add(-time.Hour - 2*time.Minute - time.Second), FeedStatusStale},
	}
	for _, c := range cases {
		got, err := h.EvaluateNative(context.Background(), "crypto_perp", marketdata.Interval1h, clock.NewEventTime(c.newestClose))
		if err != nil || got != c.want {
			t.Fatalf("EvaluateNative(newest close %s) = %v, %v; want %v", c.newestClose, got, err, c.want)
		}
	}
	if _, err := h.EvaluateNative(context.Background(), "crypto_perp", marketdata.Interval("5m"), clock.NewEventTime(now)); err == nil {
		t.Fatal("EvaluateNative accepted 5m, which is never stored")
	}
}
