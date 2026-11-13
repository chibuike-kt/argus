package hyperliquid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
)

func testProvider(t *testing.T, handler http.HandlerFunc) (*Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewProvider(clock.NewFixed(time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)), nil, WithBaseURL(srv.URL))
	return p, srv
}

func TestInstruments_ParsesMetaAndDerivesScales(t *testing.T) {
	p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var req metaRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Type != "meta" {
			t.Errorf("expected type=meta, got %q", req.Type)
		}
		_ = json.NewEncoder(w).Encode(metaResponse{
			Universe: []metaAsset{
				{Name: "BTC", SzDecimals: 5},
				{Name: "DOGE", SzDecimals: 0},
				{Name: "DELISTED", SzDecimals: 2, IsDelisted: true},
			},
		})
	})

	got, err := p.Instruments(t.Context())
	if err != nil {
		t.Fatalf("Instruments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 non-delisted instruments, got %d: %+v", len(got), got)
	}

	byName := map[string]struct {
		priceDecimals int32
		tick          string
		qtyStep       string
	}{}
	for _, inst := range got {
		byName[inst.Symbol] = struct {
			priceDecimals int32
			tick          string
			qtyStep       string
		}{inst.PriceDecimalPlaces, inst.TickSize, inst.QtyStepSize}
	}

	btc := byName["BTC"]
	if btc.priceDecimals != 1 { // 6 - 5
		t.Errorf("BTC price decimals = %d, want 1", btc.priceDecimals)
	}
	if btc.tick != "0.1" {
		t.Errorf("BTC tick = %q, want %q", btc.tick, "0.1")
	}
	if btc.qtyStep != "0.00001" {
		t.Errorf("BTC qty step = %q, want %q", btc.qtyStep, "0.00001")
	}

	doge := byName["DOGE"]
	if doge.priceDecimals != 6 { // 6 - 0
		t.Errorf("DOGE price decimals = %d, want 6", doge.priceDecimals)
	}
	if doge.qtyStep != "1" {
		t.Errorf("DOGE qty step = %q, want %q", doge.qtyStep, "1")
	}
}

func TestInstruments_SzDecimalsAboveMaxClampsPriceScaleToZero(t *testing.T) {
	// Defensive: the owner's own approved formula is max(0, 6 -
	// szDecimals). Hyperliquid's real data never sends szDecimals > 6
	// for a perp as far as this adapter's own research found, but
	// nothing in the wire format forbids it, and silently going
	// negative would be a real bug (a negative price_scale has no
	// meaning downstream).
	p, _ := testProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(metaResponse{Universe: []metaAsset{{Name: "X", SzDecimals: 9}}})
	})
	got, err := p.Instruments(t.Context())
	if err != nil {
		t.Fatalf("Instruments: %v", err)
	}
	if got[0].PriceDecimalPlaces != 0 {
		t.Errorf("price decimals = %d, want 0 (clamped)", got[0].PriceDecimalPlaces)
	}
	if got[0].TickSize != "1" {
		t.Errorf("tick = %q, want %q", got[0].TickSize, "1")
	}
}

func candleJSON(openTimeMs int64, o, h, l, c, v string) wireCandle {
	return wireCandle{T: openTimeMs, C: openTimeMs + 60_000, S: "BTC", I: "1m", O: o, H: h, L: l, Cl: c, V: v}
}

func TestHistoricalCandles_MarksAllButTheLastFinal(t *testing.T) {
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	candles := []wireCandle{
		candleJSON(base.UnixMilli(), "100", "101", "99", "100.5", "10"),
		candleJSON(base.Add(time.Minute).UnixMilli(), "100.5", "102", "100", "101", "12"),
		candleJSON(base.Add(2*time.Minute).UnixMilli(), "101", "101.5", "100.5", "101.2", "8"),
	}
	p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var req candleSnapshotRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Req.Coin != "BTC" {
			t.Errorf("coin = %q, want BTC", req.Req.Coin)
		}
		_ = json.NewEncoder(w).Encode(candles)
	})

	got, err := p.HistoricalCandles(t.Context(), "BTC", base, base.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("HistoricalCandles: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candles, want 3", len(got))
	}
	for i, c := range got {
		wantFinal := i < len(got)-1
		if c.IsFinal != wantFinal {
			t.Errorf("candle %d IsFinal = %v, want %v (never by the clock, only by a later row existing)", i, c.IsFinal, wantFinal)
		}
	}
	if got[0].Open != "100" || got[2].Close != "101.2" {
		t.Errorf("unexpected OHLC mapping: %+v", got)
	}
}

func TestHistoricalCandles_PagesUntilEmptyNeverAssumingACap(t *testing.T) {
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	// Server returns at most 2 candles per call, regardless of the
	// requested range -- simulating an undocumented per-request cap
	// this test measures, not assumes.
	const serverPageCap = 2
	var calls int32

	p, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var req candleSnapshotRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		n := atomic.AddInt32(&calls, 1)
		start := time.UnixMilli(req.Req.StartTime)
		end := time.UnixMilli(req.Req.EndTime)

		var page []wireCandle
		for t := start; t.Before(end) && len(page) < serverPageCap; t = t.Add(time.Minute) {
			page = append(page, candleJSON(t.UnixMilli(), "100", "101", "99", "100.5", "1"))
		}
		t.Logf("call %d: start=%s end=%s returned %d", n, start, end, len(page))
		_ = json.NewEncoder(w).Encode(page)
	})

	// Request a 5-minute range: expect 3 pages (2+2+1), never more than
	// serverPageCap items per page, and the full 5 candles stitched
	// together.
	got, err := p.HistoricalCandles(t.Context(), "BTC", base, base.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("HistoricalCandles: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d candles, want 5 (pagination must stitch every page together)", len(got))
	}
	if calls < 3 {
		t.Errorf("made %d requests, want at least 3 (a %d-candle cap over a 5-minute range cannot be satisfied in fewer)", calls, serverPageCap)
	}
}

func TestHistoricalCandles_EmptyResponseStopsCleanly(t *testing.T) {
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	p, _ := testProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]wireCandle{})
	})
	got, err := p.HistoricalCandles(t.Context(), "BTC", base, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("HistoricalCandles: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d candles, want 0", len(got))
	}
}

func TestPostInfo_BacksOffOn429ThenSucceeds(t *testing.T) {
	var calls int32
	p, _ := testProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(metaResponse{Universe: []metaAsset{{Name: "BTC", SzDecimals: 5}}})
	})
	// Shrink the backoff so the test does not actually wait 1s+.
	start := time.Now()
	_, err := p.Instruments(t.Context())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Instruments: %v", err)
	}
	if calls != 2 {
		t.Fatalf("made %d calls, want exactly 2 (one 429, one success -- never a tight retry loop)", calls)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("retried after only %v -- backoff must actually wait, not hammer the server", elapsed)
	}
}

func TestPostInfo_NonRateLimitErrorIsNotRetried(t *testing.T) {
	var calls int32
	p, _ := testProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	})
	_, err := p.Instruments(t.Context())
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}
	if calls != 1 {
		t.Errorf("made %d calls, want exactly 1 (a malformed-request error must not be retried)", calls)
	}
}

// pollSleep is a test-only sleepFunc: it never actually waits the full
// requested duration d, only a short real interval, so a goroutine
// parked in restLimiter.wait re-checks the (test-controlled) fake
// clock promptly instead of sleeping real wall-clock time that the
// test's own clock.Mutable.Advance call cannot wake it from.
func pollSleep(ctx context.Context, d time.Duration) error {
	const pollInterval = 5 * time.Millisecond
	wait := d
	if wait > pollInterval {
		wait = pollInterval
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

func TestRESTLimiter_BlocksUntilWindowFrees(t *testing.T) {
	clk := clock.NewMutable(time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	l := newRESTLimiter(40, time.Minute, clk, pollSleep)
	l.record(40) // budget fully spent

	done := make(chan error, 1)
	go func() { done <- l.wait(t.Context()) }()

	select {
	case <-done:
		t.Fatal("wait returned before the window freed any budget")
	case <-time.After(50 * time.Millisecond):
	}

	clk.Advance(time.Minute + time.Second)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not return after the window freed")
	}
}

func TestBackoffAttempt_ExponentialWithCap(t *testing.T) {
	base, capDuration := time.Second, 30*time.Second
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{5, 30 * time.Second}, // 32s would exceed cap
		{100, 30 * time.Second},
	}
	for _, c := range cases {
		got := backoffAttempt(c.attempt, base, capDuration)
		if got != c.want {
			t.Errorf("backoffAttempt(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

func TestDecimalString(t *testing.T) {
	cases := []struct {
		decimals int32
		want     string
	}{
		{0, "1"},
		{1, "0.1"},
		{3, "0.001"},
		{5, "0.00001"},
	}
	for _, c := range cases {
		got := decimalString(c.decimals)
		if got != c.want {
			t.Errorf("decimalString(%d) = %q, want %q", c.decimals, got, c.want)
		}
	}
}
