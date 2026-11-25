//go:build integration

package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/hub"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// L1.2: GET /instruments/{id}/candles serves 15m, 1h and 4h from the
// stored native candles, a window without one filled from its complete
// 1 minute aggregate only, native winning on the same open time; and the
// closed candle the hub sends for a window agrees with what the endpoint
// serves for it. Behind the production router and middleware (NewRouter,
// a real session), in a UTC and a New York session.

// testDBZone is testDB in a given session time zone.
func testDBZone(t *testing.T, zone string) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus?sslmode=disable"
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("postgres", dsn+sep+"options="+url.QueryEscape("-c timezone="+zone))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var got string
	if err := db.QueryRow("SHOW TimeZone").Scan(&got); err != nil {
		t.Skipf("integration database unreachable: %v", err)
	}
	if got != zone {
		t.Fatalf("session time zone %q, want %q", got, zone)
	}
	return db
}

type nativeChart struct {
	db       *sql.DB
	md       *marketdata.Store
	id       int64
	provider string
	symbol   string
	client   *http.Client
	base     string
}

func newNativeChart(t *testing.T, db *sql.DB) (nativeChart, Deps, *httptest.Server) {
	t.Helper()
	rdb := testRedisForWS(t)
	deps, instrumentsStore, users, sessions := testWSDeps(t, db, rdb)
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	provider := fmt.Sprintf("native-chart-%d", time.Now().UnixNano())
	symbol := "NATIVE-PERP"
	ps, qs := quant.Scale(2), quant.Scale(4)
	inst, err := instrumentsStore.Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: provider, NativeSymbol: symbol, AssetClass: "crypto_perp",
		PriceScale: &ps, QtyScale: &qs, TickSize: 1, QtyStepSize: 1,
		SupportedIntervals: []string{"1m", "5m", "15m", "1h", "4h", "1d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE instruments SET enabled = TRUE WHERE id = $1`, inst.ID); err != nil {
		t.Fatal(err)
	}
	return nativeChart{db: db, md: marketdata.NewStore(db, clock.Real{}), id: inst.ID, provider: provider, symbol: symbol,
		client: authenticatedClient(t, srv.URL, users, sessions), base: srv.URL}, deps, srv
}

var farFuture = time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)

// minute stores one closed 1 minute candle (prices in cents, volume in
// 1/10000).
func (c nativeChart) minute(t *testing.T, open time.Time, o, h, l, cl, v int64) {
	t.Helper()
	tick := marketdata.Tick{InstrumentID: c.id, OpenTime: clock.NewEventTime(open), Open: quant.Price(o), High: quant.Price(h), Low: quant.Price(l), Close: quant.Price(cl), Volume: quant.Qty(v), Provider: c.provider, IngestedAt: clock.NewIngestionTime(time.Now())}
	if _, err := c.md.UpsertForming(context.Background(), tick); err != nil {
		t.Fatal(err)
	}
	if _, err := c.md.CloseWith(context.Background(), tick); err != nil {
		t.Fatal(err)
	}
}

// hour stores the 60 closed minutes of the hour at open, all at price p
// except minute 30's high (p+500) and minute 59's close (p+100); volume
// 1.0000 each, skipping the minutes listed.
func (c nativeChart) hour(t *testing.T, open time.Time, p int64, skip ...int) {
	t.Helper()
	skipped := map[int]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	for i := 0; i < 60; i++ {
		if skipped[i] {
			continue
		}
		h, cl := p, p
		if i == 30 {
			h = p + 500
		}
		if i == 59 {
			h, cl = p+100, p+100
		}
		c.minute(t, open.Add(time.Duration(i)*time.Minute), p, h, p, cl, 10000)
	}
}

// native stores a closed native candle.
func (c nativeChart) native(t *testing.T, iv marketdata.Interval, open time.Time, o, h, l, cl, v int64) {
	t.Helper()
	if _, _, err := c.md.InsertClosed(context.Background(), iv, marketdata.Tick{InstrumentID: c.id, OpenTime: clock.NewEventTime(open), Open: quant.Price(o), High: quant.Price(h), Low: quant.Price(l), Close: quant.Price(cl), Volume: quant.Qty(v), Provider: c.provider, IngestedAt: clock.NewIngestionTime(time.Now())}, farFuture); err != nil {
		t.Fatal(err)
	}
}

type candleResp struct {
	status int
	body   ListInstrumentCandles200JSONResponse
}

func (c nativeChart) get(t *testing.T, query string) candleResp {
	t.Helper()
	resp, err := c.client.Get(fmt.Sprintf("%s/api/v1/instruments/%d/candles?%s", c.base, c.id, query))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out candleResp
	out.status = resp.StatusCode
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out.body); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func q(params ...string) string {
	return strings.Join(params, "&")
}

func ts(t time.Time) string { return url.QueryEscape(t.UTC().Format(time.RFC3339)) }

func TestIntegration_Candles_NativeIntervals(t *testing.T) {
	for _, zone := range []string{"Etc/UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			c, _, _ := newNativeChart(t, testDBZone(t, zone))
			// A 4h boundary in the past.
			base := time.Now().UTC().Truncate(4 * time.Hour).Add(-48 * time.Hour)
			h := func(n int) time.Time { return base.Add(time.Duration(n) * time.Hour) }

			// 1h: native at +0..+9 except +5 (a hole the 1m aggregate fills);
			// +7 native AND complete 1m with other values (native wins); +10
			// has no native and one 1m minute missing (left out); +11 has no
			// native and complete 1m (the window that just closed: filled).
			for n := 0; n <= 9; n++ {
				if n == 5 {
					continue
				}
				c.native(t, marketdata.Interval1h, h(n), 1000, 1200, 900, 1100, 600000)
			}
			c.hour(t, h(5), 5000)
			c.hour(t, h(7), 7000)
			c.hour(t, h(10), 8000, 17)
			c.hour(t, h(11), 9000)
			// 4h: native at +0 and +4.
			c.native(t, marketdata.Interval4h, h(0), 1000, 1300, 800, 1200, 2400000)
			c.native(t, marketdata.Interval4h, h(4), 1200, 1400, 1100, 1300, 2400000)
			// 15m: native for the first hour.
			for m := 0; m < 4; m++ {
				c.native(t, marketdata.Interval15m, base.Add(time.Duration(m)*15*time.Minute), 1000, 1100, 900, 1050, 150000)
			}

			wantHours := []string{"+0", "+1", "+2", "+3", "+4", "+5", "+6", "+7", "+8", "+9", "+11"}
			label := func(cs []Candle) []string {
				out := make([]string, len(cs))
				for i, x := range cs {
					out[i] = fmt.Sprintf("+%d", int(x.OpenTime.Sub(base)/time.Hour))
				}
				return out
			}
			reverse := func(s []string) []string {
				out := make([]string, len(s))
				for i := range s {
					out[i] = s[len(s)-1-i]
				}
				return out
			}

			// The latest N, newest first.
			r := c.get(t, q("interval=1h", "order=desc", "limit=500"))
			if r.status != 200 || fmt.Sprint(label(r.body.Candles)) != fmt.Sprint(reverse(wantHours)) {
				t.Fatalf("1h latest N = %d %v, want %v", r.status, label(r.body.Candles), reverse(wantHours))
			}
			byHour := map[string]Candle{}
			for _, x := range r.body.Candles {
				byHour[fmt.Sprintf("+%d", int(x.OpenTime.Sub(base)/time.Hour))] = x
			}
			if x := byHour["+5"]; x.Open != "50.00" || x.High != "55.00" || x.Close != "51.00" || x.Volume != "60.0000" || x.Revision != 1 || !x.IsClosed {
				t.Fatalf("+5 (filled from 1m) = %+v, want 50.00/55.00/50.00/51.00 volume 60.0000", x)
			}
			if x := byHour["+7"]; x.Open != "10.00" || x.High != "12.00" || x.Close != "11.00" || x.Volume != "60.0000" {
				t.Fatalf("+7 = %+v, want the native candle (10.00/12.00/9.00/11.00), not the 1m aggregate", x)
			}
			if !x11(byHour["+11"]) {
				t.Fatalf("+11 (just closed, no native yet) = %+v, want its 1m aggregate", byHour["+11"])
			}
			if r.body.LastClosedOpenTime == nil || !r.body.LastClosedOpenTime.Equal(h(11)) || *r.body.LastClosedRevision != 1 {
				t.Fatalf("join point = %v rev %v, want +11 (the later of native +9 and the 1m aggregate +11)", r.body.LastClosedOpenTime, r.body.LastClosedRevision)
			}

			// Ascending pages over [from, to).
			r = c.get(t, q("interval=1h", "order=asc", "from="+ts(h(0)), "to="+ts(h(12)), "limit=500"))
			if r.status != 200 || fmt.Sprint(label(r.body.Candles)) != fmt.Sprint(wantHours) {
				t.Fatalf("1h asc = %d %v, want %v", r.status, label(r.body.Candles), wantHours)
			}
			r = c.get(t, q("interval=1h", "order=asc", "from="+ts(h(4)), "to="+ts(h(12)), "limit=3"))
			if fmt.Sprint(label(r.body.Candles)) != "[+4 +5 +6]" {
				t.Fatalf("1h asc limit 3 from +4 = %v, want [+4 +5 +6]", label(r.body.Candles))
			}
			// desc with to alone: the newest before it (scrolling back).
			r = c.get(t, q("interval=1h", "order=desc", "to="+ts(h(6)), "limit=2"))
			if r.status != 200 || fmt.Sprint(label(r.body.Candles)) != "[+5 +4]" {
				t.Fatalf("1h desc to +6 limit 2 = %d %v, want [+5 +4]", r.status, label(r.body.Candles))
			}

			// 4h and 15m read their own native series; the 4h window after
			// +4 has only part of its minutes stored: left out.
			r = c.get(t, q("interval=4h", "order=desc", "limit=500"))
			if fmt.Sprint(label(r.body.Candles)) != "[+4 +0]" || r.body.Candles[0].Close != "13.00" {
				t.Fatalf("4h = %v %+v, want the two native candles", label(r.body.Candles), r.body.Candles)
			}
			r = c.get(t, q("interval=15m", "order=asc", "from="+ts(h(0)), "to="+ts(h(1)), "limit=500"))
			if len(r.body.Candles) != 4 || r.body.Candles[3].Close != "10.50" {
				t.Fatalf("15m = %+v, want the four native candles", r.body.Candles)
			}

			// 5m is unchanged: aggregated from 1m.
			r = c.get(t, q("interval=5m", "order=asc", "from="+ts(h(5)), "to="+ts(h(6)), "limit=500"))
			if len(r.body.Candles) != 12 || r.body.Candles[0].Open != "50.00" {
				t.Fatalf("5m over +5 = %d candles %+v, want 12 aggregated", len(r.body.Candles), r.body.Candles)
			}

			// The limit is 5000.
			if r := c.get(t, q("interval=1h", "order=desc", "limit=5000")); r.status != 200 {
				t.Fatalf("limit 5000 = %d, want 200", r.status)
			}
			if r := c.get(t, q("interval=1h", "order=desc", "limit=5001")); r.status != 400 {
				t.Fatalf("limit 5001 = %d, want 400", r.status)
			}

			// A native candle newer than any complete 1 minute window (the
			// worker fetched it, the 1m minutes are not all there): the join
			// point is that native candle, with its own revision.
			c.native(t, marketdata.Interval1h, h(12), 1000, 1200, 900, 1150, 600000)
			r = c.get(t, q("interval=1h", "order=desc", "limit=3"))
			if r.body.LastClosedOpenTime == nil || !r.body.LastClosedOpenTime.Equal(h(12)) || r.body.Candles[0].Close != "11.50" {
				t.Fatalf("join point %v, newest %+v; want the native +12", r.body.LastClosedOpenTime, r.body.Candles[0])
			}
		})
	}
}

func x11(x Candle) bool {
	return x.Open == "90.00" && x.High == "95.00" && x.Low == "90.00" && x.Close == "91.00" && x.Volume == "60.0000"
}

// The handoff (L1.2): a window closes while the chart is open. The hub's
// candle_closed for that hour and what the endpoint serves for it -- from
// the 1m aggregate before the worker has fetched the native candle, and
// from the native candle after -- are the same candle (open time, OHLCV,
// revision), and the snapshot's join point is the endpoint's.
func TestIntegration_WS_NativeHandoffAgrees(t *testing.T) {
	for _, zone := range []string{"Etc/UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			db := testDBZone(t, zone)
			c, deps, srv := newNativeChart(t, db)
			hourOpen := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
			// The first 59 minutes are stored; the 60th closes live.
			c.hour(t, hourOpen, 6000, 59)

			conn := dialNativeWS(t, srv, deps, c)
			defer func() { _ = conn.CloseNow() }()
			sub, _ := json.Marshal(subscribeRequest{InstrumentID: c.id, Interval: "1h"})
			req, _ := json.Marshal(hub.Envelope{V: 1, Type: hub.TypeSubscribe, Payload: sub})
			if err := conn.Write(context.Background(), websocket.MessageText, req); err != nil {
				t.Fatal(err)
			}
			readUntil(t, conn, hub.TypeSnapshot, 5*time.Second)

			// The worker side: the last minute closes through a real
			// ingestion.Service publishing to the hub over Redis.
			rdb := testRedisForWS(t)
			instStore := instruments.NewStore(db)
			cache := ingestion.NewInstrumentCache(instStore)
			last := hourOpen.Add(59 * time.Minute)
			script := fixture.Script{Symbol: c.symbol, Events: []fixture.ScriptedEvent{
				{Candle: ingestion.ProviderCandle{Symbol: c.symbol, OpenTime: last, Open: "60.00", High: "61.00", Low: "60.00", Close: "61.00", Volume: "1.0000", IsFinal: true}},
			}}
			svc := ingestion.NewService(c.provider, fixture.NewScriptedProvider([]fixture.Script{script}, clock.Real{}, 0, nil), ingestion.NewNormalizer(cache, clock.Real{}), cache, c.md, marketdata.ContinuousCalendar{}, slog.Default())
			svc.SetPublisher(hub.NewRedisPublisher(rdb))
			go func() { _ = svc.WatchCandles(context.Background(), c.symbol) }()

			env := readUntil(t, conn, hub.TypeCandleClosed, 10*time.Second)
			var closed hub.CandlePayload
			if err := json.Unmarshal(env.Payload, &closed); err != nil {
				t.Fatal(err)
			}
			if !closed.OpenTime.Equal(hourOpen) {
				t.Fatalf("candle_closed for %s, want the hour %s", closed.OpenTime, hourOpen)
			}
			same := func(stage string) {
				r := c.get(t, q("interval=1h", "order=desc", "limit=5"))
				var served *Candle
				n := 0
				for i, x := range r.body.Candles {
					if x.OpenTime.Equal(hourOpen) {
						served = &r.body.Candles[i]
						n++
					}
				}
				if n != 1 {
					t.Fatalf("%s: the hour appears %d times in the history, want exactly once", stage, n)
				}
				if served.Open != closed.Open || served.High != closed.High || served.Low != closed.Low || served.Close != closed.Close || served.Volume != closed.Volume || served.Revision != closed.Revision {
					t.Fatalf("%s: the endpoint serves %+v for the hour, the hub sent %+v", stage, *served, closed)
				}
				if r.body.LastClosedOpenTime == nil || !r.body.LastClosedOpenTime.Equal(hourOpen) {
					t.Fatalf("%s: join point %v, want the hour that just closed", stage, r.body.LastClosedOpenTime)
				}
			}
			same("before the native candle is stored (1m aggregate)")

			// The worker's follower then stores the provider's native hour,
			// the same values (L1.0 measured them identical on real data).
			c.native(t, marketdata.Interval1h, hourOpen, 6000, 6500, 6000, 6100, 600000)
			same("after the native candle is stored")

			// A reconnecting client's snapshot joins at the same hour.
			conn2 := dialNativeWS(t, srv, deps, c)
			defer func() { _ = conn2.CloseNow() }()
			if err := conn2.Write(context.Background(), websocket.MessageText, req); err != nil {
				t.Fatal(err)
			}
			snapEnv := readUntil(t, conn2, hub.TypeSnapshot, 5*time.Second)
			var snap hub.SnapshotPayload
			if err := json.Unmarshal(snapEnv.Payload, &snap); err != nil {
				t.Fatal(err)
			}
			if snap.LastClosedOpenTime == nil || !snap.LastClosedOpenTime.Equal(hourOpen) || snap.LastClosedRevision == nil || *snap.LastClosedRevision != closed.Revision {
				t.Fatalf("snapshot join point %v rev %v, want the hour %s rev %d", snap.LastClosedOpenTime, snap.LastClosedRevision, hourOpen, closed.Revision)
			}

			// The native candle of the next hour arrives before its 1m
			// minutes: the snapshot and the endpoint both join at it.
			next := hourOpen.Add(time.Hour)
			c.native(t, marketdata.Interval1h, next, 6100, 6200, 6000, 6150, 600000)
			conn3 := dialNativeWS(t, srv, deps, c)
			defer func() { _ = conn3.CloseNow() }()
			if err := conn3.Write(context.Background(), websocket.MessageText, req); err != nil {
				t.Fatal(err)
			}
			snapEnv = readUntil(t, conn3, hub.TypeSnapshot, 5*time.Second)
			snap = hub.SnapshotPayload{}
			if err := json.Unmarshal(snapEnv.Payload, &snap); err != nil {
				t.Fatal(err)
			}
			r := c.get(t, q("interval=1h", "order=desc", "limit=1"))
			if snap.LastClosedOpenTime == nil || !snap.LastClosedOpenTime.Equal(next) || r.body.LastClosedOpenTime == nil || !r.body.LastClosedOpenTime.Equal(next) {
				t.Fatalf("snapshot joins at %v, the endpoint at %v; want both at the native %s", snap.LastClosedOpenTime, r.body.LastClosedOpenTime, next)
			}
		})
	}
}

func dialNativeWS(t *testing.T, srv *httptest.Server, deps Deps, c nativeChart) *websocket.Conn {
	t.Helper()
	var token string
	for _, ck := range c.client.Jar.Cookies(mustParseURL(t, srv.URL)) {
		if ck.Name == deps.AuthPolicy.CookieName(false) {
			token = ck.Value
		}
	}
	if token == "" {
		t.Fatal("no session cookie")
	}
	h := wsDialHeader(deps.AuthPolicy.CookieName(false), token, testOrigin)
	conn, resp, err := websocket.Dial(context.Background(), wsURL(srv.URL)+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}
