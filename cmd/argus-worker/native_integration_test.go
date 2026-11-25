//go:build integration

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/hyperliquid"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// L1.1, native higher timeframe candles against real failures: the real
// Hyperliquid adapter, real Postgres (a New York session), and a local
// stand-in exchange that kills the TCP connection in the middle of a
// candleSnapshot request (a fetch, or one page of a paged backfill), serves
// changed values, and pages at a small size.

type nativeExchange struct {
	mu       sync.Mutex
	history  time.Duration
	pageSize int
	kill     map[string]map[int]bool // interval -> request numbers (1-based) to cut
	calls    map[string]int
	price    map[int64]string // open time (ms) -> price served instead
}

func newNativeExchange(history time.Duration, pageSize int) *nativeExchange {
	return &nativeExchange{history: history, pageSize: pageSize, kill: map[string]map[int]bool{}, calls: map[string]int{}, price: map[int64]string{}}
}

func (x *nativeExchange) rest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type string `json:"type"`
		Req  struct {
			Coin      string `json:"coin"`
			Interval  string `json:"interval"`
			StartTime int64  `json:"startTime"`
			EndTime   int64  `json:"endTime"`
		} `json:"req"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Type != "candleSnapshot" || body.Req.Interval == "1m" {
		_, _ = w.Write([]byte("[]"))
		return
	}
	x.mu.Lock()
	x.calls[body.Req.Interval]++
	n := x.calls[body.Req.Interval]
	cut := x.kill[body.Req.Interval][n]
	prices := make(map[int64]string, len(x.price))
	for k, v := range x.price {
		prices[k] = v
	}
	x.mu.Unlock()
	if cut {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
	}
	out := fakeNativeCandles(body.Req.Coin, body.Req.Interval, body.Req.StartTime, body.Req.EndTime, "10.0", x.history)
	for i := range out {
		if p, ok := prices[out[i].T]; ok {
			out[i].O, out[i].H, out[i].L, out[i].Cl = p, p, p, p
		}
	}
	if x.pageSize > 0 && len(out) > x.pageSize {
		out = out[:x.pageSize]
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (x *nativeExchange) callsFor(interval string) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.calls[interval]
}

type nativeRig struct {
	db     *sql.DB
	x      *nativeExchange
	svc    *ingestion.Service
	state  storeBackfillState
	md     *marketdata.Store
	id     int64
	symbol string
	cfg    config.Config
}

func newNativeRig(t *testing.T, x *nativeExchange) nativeRig {
	t.Helper()
	db := dbInSessionZone(t, "America/New_York")
	srv := httptest.NewServer(http.HandlerFunc(x.rest))
	t.Cleanup(srv.Close)
	clk := clock.Real{}
	provider := hyperliquid.NewProvider(clk, nil, hyperliquid.WithBaseURL(srv.URL))
	instStore := instruments.NewStore(db)
	symbol := fmt.Sprintf("NATIVE%d", time.Now().UnixNano())
	id := seedHyperliquidTestInstrument(t, instStore, symbol)
	md := marketdata.NewStore(db, clk)
	cache := ingestion.NewInstrumentCache(instStore)
	svc := ingestion.NewService(hyperliquid.ProviderName, provider, ingestion.NewNormalizer(cache, clk), cache, md, marketdata.ContinuousCalendar{}, quietLogger())
	cfg := config.Config{Live: config.LiveFeed{ReconnectBackoffBase: 10 * time.Millisecond, ReconnectBackoffCap: 50 * time.Millisecond}}
	return nativeRig{db: db, x: x, svc: svc, state: storeBackfillState{instruments: instStore, candles: md}, md: md, id: id, symbol: symbol, cfg: cfg}
}

// assertComplete checks one interval's stored series: no hole from its
// first stored candle to its newest, the newest is the last window that had
// closed when the fetch ran (at or after fetchedAt), and it starts where
// the exchange's history starts.
func (r nativeRig) assertComplete(t *testing.T, iv marketdata.Interval, fetchedAt time.Time) {
	t.Helper()
	if err := r.checkComplete(iv, fetchedAt); err != nil {
		t.Fatal(err)
	}
}

func (r nativeRig) checkComplete(iv marketdata.Interval, fetchedAt time.Time) error {
	ctx := context.Background()
	d, _ := iv.Duration()
	_, newest, err := r.state.newestClosedAt(ctx, iv, r.symbol)
	if err != nil || newest == nil {
		return fmt.Errorf("%s: no stored candle (%v)", iv, err)
	}
	if wantAtLeast := iv.WindowStart(fetchedAt).Add(-d); newest.Before(wantAtLeast) {
		return fmt.Errorf("%s: newest stored candle opens %s, want the last closed window %s or later", iv, newest, wantAtLeast)
	}
	missing, err := r.md.MissingRangesAt(ctx, iv, r.id, time.Unix(0, 0), newest.Add(d), time.Unix(0, 0))
	if err != nil || len(missing) != 0 {
		return fmt.Errorf("%s: holes in the stored series: %+v %v", iv, missing, err)
	}
	var first time.Time
	if err := r.db.QueryRow(`SELECT min(open_time) FROM candles WHERE instrument_id = $1 AND interval = $2 AND superseded_at IS NULL`, r.id, string(iv)).Scan(&first); err != nil {
		return err
	}
	if wantFirst := fetchedAt.Add(-r.x.history).Truncate(d); first.After(wantFirst.Add(d)) {
		return fmt.Errorf("%s: series starts %s, want the exchange's whole history from about %s", iv, first.UTC(), wantFirst)
	}
	return nil
}

// Real failure: the provider connection dies in the middle of a native
// fetch (the follower's) and in the middle of a paged native backfill
// (its second page). Both recover, and no candle is missing.
func TestIntegration_Native_KilledConnectionMidFetchAndMidBackfillRecovers(t *testing.T) {
	x := newNativeExchange(30*time.Hour, 40)
	x.kill["15m"] = map[int]bool{2: true}
	r := newNativeRig(t, x)
	ctx := context.Background()
	clk := clock.Real{}

	// The first-run backfill pages 15m in pages of 40; the second page's
	// connection is cut, the attempt fails and is retried with the slot rules.
	gate := make(chan struct{}, 2)
	fetchedAt := clk.Now()
	if !backfillNativeUntilCaughtUp(ctx, r.symbol, r.svc, r.state, r.cfg, quietLogger(), clk, backfillOptions{slot: gate, retryCap: 50 * time.Millisecond}) {
		t.Fatal("native backfill did not finish")
	}
	if x.callsFor("15m") < 5 {
		t.Fatalf("15m requests = %d, want the cut page and a full retry (at least 5)", x.callsFor("15m"))
	}
	for _, iv := range marketdata.NativeIntervals() {
		r.assertComplete(t, iv, fetchedAt)
	}
	if len(gate) != 0 {
		t.Fatal("a backfill slot is still held")
	}

	// Then the follower fetches the 1h candle whose window just closed (the
	// stored one is removed first, so only this fetch can restore it): the
	// first request is cut, the retry stores it.
	lastHour := marketdata.Interval1h.WindowStart(clk.Now()).Add(-time.Hour)
	if _, err := r.db.Exec(`DELETE FROM candles WHERE instrument_id = $1 AND interval = '1h' AND open_time = $2`, r.id, lastHour); err != nil {
		t.Fatal(err)
	}
	before := x.callsFor("1h")
	x.mu.Lock()
	x.kill["1h"] = map[int]bool{before + 1: true}
	x.mu.Unlock()
	f := newNativeFollower(r.symbol, r.svc, r.cfg, quietLogger(), clk)
	f.fetchClosed(ctx, marketdata.Interval1h, lastHour)
	if c, err := r.md.GetCurrentAt(ctx, marketdata.Interval1h, r.id, clock.NewEventTime(lastHour)); err != nil || !c.IsClosed {
		t.Fatalf("the 1h candle at %s was not stored after the cut request: %+v %v", lastHour, c, err)
	}
	if got := x.callsFor("1h") - before; got != 2 {
		t.Fatalf("1h follower requests = %d, want 2 (one cut, one retry)", got)
	}
	r.assertComplete(t, marketdata.Interval1h, fetchedAt)
}

// Regression (found by the test above's first version): a window that
// closes during the first-run backfill must not make the backfill think it
// is a later run. runNative starts the follower only after the backfill, so
// a closed window queued before it does not cut the history short.
func TestIntegration_Native_AWindowClosingDuringTheFirstBackfillKeepsTheWholeHistory(t *testing.T) {
	x := newNativeExchange(30*time.Hour, 0)
	r := newNativeRig(t, x)
	clk := clock.Real{}
	ctx, cancel := context.WithCancel(context.Background())
	f := newNativeFollower(r.symbol, r.svc, r.cfg, quietLogger(), clk)
	// The last minute of the most recent closed hour, queued before the
	// backfill has stored anything.
	f.notify(marketdata.Interval1h.WindowStart(clk.Now()).Add(-time.Minute))
	fetchedAt := clk.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runNative(ctx, r.symbol, r.svc, r.state, r.cfg, quietLogger(), clk, backfillOptions{slot: make(chan struct{}, 2)}, f)
	}()
	defer func() { cancel(); <-done }()
	var last error
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		last = nil
		for _, iv := range marketdata.NativeIntervals() {
			if err := r.checkComplete(iv, fetchedAt); err != nil {
				last = err
				break
			}
		}
		if last == nil {
			return
		}
	}
	t.Fatalf("the native series were not complete: %v", last)
}

// A long outage: the stored native series end hours ago. The startup
// backfill (after a restart) and the periodic re-read (a pipeline that kept
// running through it) both read from the newest stored candle and leave
// no hole.
func TestIntegration_Native_ReconnectAfterALongOutage(t *testing.T) {
	x := newNativeExchange(30*time.Hour, 0)
	r := newNativeRig(t, x)
	ctx := context.Background()
	clk := clock.Real{}
	opt := backfillOptions{slot: make(chan struct{}, 2)}
	if !backfillNativeUntilCaughtUp(ctx, r.symbol, r.svc, r.state, r.cfg, quietLogger(), clk, opt) {
		t.Fatal("first backfill did not finish")
	}
	outage := func() {
		// The last 9 hours never arrived (test rows in the test database).
		if _, err := r.db.Exec(`DELETE FROM candles WHERE instrument_id = $1 AND interval <> '1m' AND open_time > $2`, r.id, clk.Now().Add(-9*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	outage()
	fetchedAt := clk.Now()
	rereadNative(ctx, r.symbol, r.svc, r.state, quietLogger(), clk)
	for _, iv := range marketdata.NativeIntervals() {
		r.assertComplete(t, iv, fetchedAt)
	}

	outage()
	fetchedAt = clk.Now()
	if !backfillNativeUntilCaughtUp(ctx, r.symbol, r.svc, r.state, r.cfg, quietLogger(), clk, opt) {
		t.Fatal("restart backfill did not finish")
	}
	for _, iv := range marketdata.NativeIntervals() {
		r.assertComplete(t, iv, fetchedAt)
	}
}

// The periodic re-read applies the 1 minute revision rule: the provider
// changes a just-closed candle, the store gets revision 2.
func TestIntegration_Native_RereadAppliesAProviderCorrection(t *testing.T) {
	x := newNativeExchange(3*time.Hour, 0)
	r := newNativeRig(t, x)
	ctx := context.Background()
	clk := clock.Real{}
	if !backfillNativeUntilCaughtUp(ctx, r.symbol, r.svc, r.state, r.cfg, quietLogger(), clk, backfillOptions{}) {
		t.Fatal("backfill did not finish")
	}
	// Both of the two most recent closed candles change at the provider.
	last := marketdata.Interval15m.WindowStart(clk.Now()).Add(-15 * time.Minute)
	previous := last.Add(-15 * time.Minute)
	x.mu.Lock()
	x.price[last.UnixMilli()] = "12.0"
	x.price[previous.UnixMilli()] = "11.0"
	x.mu.Unlock()
	rereadNative(ctx, r.symbol, r.svc, r.state, quietLogger(), clk)
	for open, want := range map[time.Time]quant.Price{last: 120, previous: 110} {
		c, err := r.md.GetCurrentAt(ctx, marketdata.Interval15m, r.id, clock.NewEventTime(open))
		if err != nil || c.Revision != 2 || c.Close != want {
			t.Fatalf("15m at %s after the re-read = %+v %v, want revision 2 with close %d", open, c, err, want)
		}
	}
}

// The integrity scan steps by the interval: a recent hole is fetched
// again; a hole older than the provider's retention is recorded in
// candle_gaps with its interval, once.
func TestIntegration_Native_IntegrityScanFillsHolesAndRecordsLostOnes(t *testing.T) {
	x := newNativeExchange(72*time.Hour, 0)
	r := newNativeRig(t, x)
	ctx := context.Background()
	clk := clock.Real{}
	if !backfillNativeUntilCaughtUp(ctx, r.symbol, r.svc, r.state, r.cfg, quietLogger(), clk, backfillOptions{}) {
		t.Fatal("backfill did not finish")
	}
	iv := marketdata.Interval4h
	now := clk.Now()
	// A recent hole the exchange can still serve.
	recent := iv.WindowStart(now).Add(-12 * time.Hour)
	if _, err := r.db.Exec(`DELETE FROM candles WHERE instrument_id = $1 AND interval = '4h' AND open_time = $2`, r.id, recent); err != nil {
		t.Fatal(err)
	}
	// A candle stored long ago, older than the 5,000 candle retention: the
	// span between it and the retention edge can never be fetched.
	ancient := iv.WindowStart(now).Add(-6000 * 4 * time.Hour)
	if _, _, err := r.md.InsertClosed(ctx, iv, marketdata.Tick{InstrumentID: r.id, OpenTime: clock.NewEventTime(ancient), Open: 100, High: 100, Low: 100, Close: 100, Volume: 1, Provider: hyperliquid.ProviderName, IngestedAt: clock.NewIngestionTime(now)}, now); err != nil {
		t.Fatal(err)
	}

	nativeIntegrityScan(ctx, "test", r.symbol, iv, r.svc, r.state, r.cfg, quietLogger(), clk)
	if c, err := r.md.GetCurrentAt(ctx, iv, r.id, clock.NewEventTime(recent)); err != nil || !c.IsClosed {
		t.Fatalf("the recent 4h hole at %s was not fetched again: %v", recent, err)
	}
	gaps, err := r.md.ListOpenGapsAt(ctx, iv, r.id)
	edge := nativeRetentionStart(now, iv)
	if err != nil || len(gaps) != 1 || gaps[0].Interval != iv || !gaps[0].Start.Time().Equal(ancient.Add(4*time.Hour)) || !gaps[0].End.Time().Equal(edge) {
		t.Fatalf("4h gaps = %+v %v, want one 4h gap from %s to the retention edge %s", gaps, err, ancient.Add(4*time.Hour), edge)
	}
	if oneMinute, _ := r.md.ListOpenGaps(ctx, r.id); len(oneMinute) != 0 {
		t.Fatalf("the native lost range was recorded as a 1 minute gap: %+v", oneMinute)
	}
	// A second scan records nothing new.
	nativeIntegrityScan(ctx, "test", r.symbol, iv, r.svc, r.state, r.cfg, quietLogger(), clk)
	if again, _ := r.md.ListOpenGapsAt(ctx, iv, r.id); len(again) != 1 {
		t.Fatalf("after a second scan, 4h gaps = %+v, want still one", again)
	}
}

// After a fetch, a native candle is compared with the aggregate of its
// 1 minute candles when all of them are stored: a difference is counted and
// logged, and the native candle is kept as the provider sent it.
func TestIntegration_Native_FetchComparesWithTheOneMinuteAggregate(t *testing.T) {
	x := newNativeExchange(2*time.Hour, 0)
	r := newNativeRig(t, x)
	ctx := context.Background()
	clk := clock.Real{}
	seed := func(open time.Time, high int64) {
		for i := 0; i < 15; i++ {
			tick := marketdata.Tick{InstrumentID: r.id, OpenTime: clock.NewEventTime(open.Add(time.Duration(i) * time.Minute)),
				Open: 100, High: 100, Low: 100, Close: 100, Provider: hyperliquid.ProviderName, IngestedAt: clock.NewIngestionTime(clk.Now())}
			if i == 0 {
				tick.Volume = 1 // the 15 minutes sum to the native volume, 1
			}
			if i == 7 {
				tick.High = quant.Price(high)
			}
			if _, err := r.md.UpsertForming(ctx, tick); err != nil {
				t.Fatal(err)
			}
			if _, err := r.md.CloseWith(ctx, tick); err != nil {
				t.Fatal(err)
			}
		}
	}
	w2 := marketdata.Interval15m.WindowStart(clk.Now()).Add(-15 * time.Minute)
	w1 := w2.Add(-15 * time.Minute)
	seed(w1, 100) // matches the native candle: open/high/low/close 10.0, volume 1
	seed(w2, 105) // a 1 minute high of 10.5 the native candle does not have

	f := newNativeFollower(r.symbol, r.svc, r.cfg, quietLogger(), clk)
	f.fetchClosed(ctx, marketdata.Interval15m, w1)
	if r.svc.NativeAggregateMismatches() != 0 {
		t.Fatalf("mismatches after the matching window = %d, want 0", r.svc.NativeAggregateMismatches())
	}
	f.fetchClosed(ctx, marketdata.Interval15m, w2)
	if r.svc.NativeAggregateMismatches() != 1 {
		t.Fatalf("mismatches after the differing window = %d, want 1", r.svc.NativeAggregateMismatches())
	}
	c, err := r.md.GetCurrentAt(ctx, marketdata.Interval15m, r.id, clock.NewEventTime(w2))
	if err != nil || c.High != 100 || c.Revision != 1 {
		t.Fatalf("native 15m at %s = %+v %v, want the provider's high 10.0 kept at revision 1", w2, c, err)
	}
}
