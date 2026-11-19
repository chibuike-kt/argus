//go:build integration

package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/hub"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// statsFixture is a real stack for the live stats stream: the production
// router and middleware behind the production NewServer, a real Postgres
// (one enabled instrument with a full day of stored candles) and a real
// Redis, plus helpers that write a forming tick exactly the way
// ingestion does (store first, then publish the stored row).
type statsFixture struct {
	db        *sql.DB
	rdb       *redis.Client
	baseURL   string
	client    *http.Client
	cookie    string
	cookieVal string
	instID    int64
	store     *marketdata.Store
	publisher *hub.RedisPublisher
	anchor    time.Time
}

func newStatsFixture(t *testing.T, readTimeout, writeTimeout, heartbeat, statsInterval time.Duration) *statsFixture {
	t.Helper()
	db := testDB(t)
	rdb := testRedisForWS(t)
	deps, instrumentsStore, users, sessions := testWSDeps(t, db, rdb)
	deps.Live.HeartbeatInterval = heartbeat
	deps.Live.StatsMinInterval = statsInterval
	deps.Live.MaxStatsInstrumentsPerConnection = 50
	candles := marketdata.NewStore(db, clock.Real{})
	deps.Hub = hub.New(hub.NewRedisSubscriber(rdb), candles, instrumentsStore, clock.Real{}, deps.Live, deps.Logger)
	deps.Hub.EnableStats(candles)

	baseURL := startRealServer(t, NewRouter(deps), readTimeout, writeTimeout)

	priceScale, qtyScale := quant.Scale(2), quant.Scale(4)
	inst, err := instrumentsStore.Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: "stats-test", NativeSymbol: fmt.Sprintf("WS-STATS-%d", time.Now().UnixNano()), AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1,
		SupportedIntervals: []string{"1m"},
	})
	if err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE instruments SET enabled = TRUE WHERE id = $1`, inst.ID); err != nil {
		t.Fatalf("enable instrument: %v", err)
	}

	// 1441 closed minutes ending one minute before the anchor: close
	// 100.00 + i cents, volume 0.0002 each. The forming anchor's D3
	// reference (anchor - 24h - 1m) is i=0, close 100.00.
	anchor := time.Now().UTC().Truncate(time.Minute)
	start := anchor.Add(-1441 * time.Minute)
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO candles (instrument_id, open_time, close_time, open, high, low, close, volume,
		                     revision, is_closed, tick_aligned, provider, ingested_at)
		SELECT $1, $2::timestamptz + i * interval '1 minute', $2::timestamptz + (i + 1) * interval '1 minute',
		       10000 + i, 10000 + i, 10000 + i, 10000 + i, 2, 1, TRUE, TRUE, 'stats-test', now()
		FROM generate_series(0, 1440) AS i`, inst.ID, start); err != nil {
		t.Fatalf("seed candles: %v", err)
	}

	jar := authenticatedClient(t, baseURL, users, sessions)
	cookieName := deps.AuthPolicy.CookieName(false)
	var token string
	for _, c := range jar.Jar.Cookies(mustParseURL(t, baseURL)) {
		if c.Name == cookieName {
			token = c.Value
		}
	}
	f := &statsFixture{
		db: db, rdb: rdb, baseURL: baseURL, client: jar, cookie: cookieName, cookieVal: token,
		instID: inst.ID, store: candles, publisher: hub.NewRedisPublisher(rdb), anchor: anchor,
	}
	f.tick(t, 10000, 3) // the forming anchor exists before anyone subscribes
	return f
}

// tick stores a forming value for the anchor minute and publishes the
// stored row, as ingestion.Service does.
func (f *statsFixture) tick(t *testing.T, price quant.Price, volume quant.Qty) {
	t.Helper()
	if err := f.tryTick(price, volume); err != nil {
		t.Fatal(err)
	}
}

// tryTick is tick for a background goroutine (which must not call
// t.Fatal).
func (f *statsFixture) tryTick(price quant.Price, volume quant.Qty) error {
	c, err := f.store.UpsertForming(context.Background(), marketdata.Tick{
		InstrumentID: f.instID, OpenTime: clock.NewEventTime(f.anchor),
		Open: price, High: price, Low: price, Close: price, Volume: volume,
		TickAligned: true, Provider: "stats-test", IngestedAt: clock.NewIngestionTime(time.Now()),
	})
	if err != nil {
		return fmt.Errorf("upsert forming: %w", err)
	}
	if err := f.publisher.Publish(context.Background(), hub.CandleEvent{
		InstrumentID: c.InstrumentID, Kind: hub.EventForming,
		OpenTime: c.OpenTime.Time(), CloseTime: c.CloseTime.Time(),
		Open: c.Open, High: c.High, Low: c.Low, Close: c.Close, Volume: c.Volume, Revision: c.Revision,
		IngestedAt: c.IngestedAt.Time(),
	}); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	return nil
}

func (f *statsFixture) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), wsURL(f.baseURL)+"/ws", &websocket.DialOptions{
		HTTPHeader: wsDialHeader(f.cookie, f.cookieVal, testOrigin),
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func subscribeStats(t *testing.T, conn *websocket.Conn, ids ...int64) {
	t.Helper()
	payload, _ := json.Marshal(statsSubscribeRequest{InstrumentIDs: ids})
	req, _ := json.Marshal(hub.Envelope{V: 1, Type: hub.TypeSubscribeStats, Payload: payload})
	if err := conn.Write(context.Background(), websocket.MessageText, req); err != nil {
		t.Fatalf("write subscribe_stats: %v", err)
	}
}

func decodeStats(t *testing.T, env hub.Envelope) hub.StatsPayload {
	t.Helper()
	var p hub.StatsPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	return p
}

// restStats reads GET /api/v1/instruments/stats for the fixture's
// instrument through the production router.
func (f *statsFixture) restStats(t *testing.T) InstrumentStats {
	t.Helper()
	resp, err := f.client.Get(fmt.Sprintf("%s/api/v1/instruments/stats?ids=%d", f.baseURL, f.instID))
	if err != nil {
		t.Fatalf("GET stats: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET stats: status %d", resp.StatusCode)
	}
	var rows []InstrumentStats
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("GET stats: %d rows, want 1", len(rows))
	}
	return rows[0]
}

func derefS(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

// The socket and the REST endpoint compute the same figures from the
// same stored candles (one shared source, never two calculations).
func TestIntegration_WSStats_MatchesREST(t *testing.T) {
	f := newStatsFixture(t, 5*time.Second, 10*time.Second, 2*time.Second, 100*time.Millisecond)
	conn := f.dial(t)
	subscribeStats(t, conn, f.instID)

	first := decodeStats(t, readUntil(t, conn, hub.TypeStats, 5*time.Second))
	// Hand-computed: forming 100.00 vs reference i=0 100.00 -> 0.00;
	// volume: closed i=2..1440 (1439 x 0.0002) + forming 0.0003 = 0.2881.
	if derefS(first.LastPrice) != "100.00" || derefS(first.Change24h) != "0.00" || derefS(first.Volume24h) != "0.2881" {
		t.Fatalf("first stats = price %s change %s volume %s", derefS(first.LastPrice), derefS(first.Change24h), derefS(first.Volume24h))
	}

	// A live tick: 123.45, volume 0.0010. 23.45 / 100.00 = 23.45%;
	// volume 0.2878 + 0.0010 = 0.2888.
	f.tick(t, 12345, 10)
	var live hub.StatsPayload
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		live = decodeStats(t, readUntil(t, conn, hub.TypeStats, 5*time.Second))
		if derefS(live.LastPrice) == "123.45" {
			break
		}
	}
	if derefS(live.LastPrice) != "123.45" || derefS(live.Change24h) != "23.45" || derefS(live.Change24hPct) != "23.45" || derefS(live.Volume24h) != "0.2888" {
		t.Fatalf("live stats = %+v", live)
	}

	rest := f.restStats(t)
	if derefS(rest.LastPrice) != derefS(live.LastPrice) || derefS(rest.Change24h) != derefS(live.Change24h) ||
		derefS(rest.Change24hPct) != derefS(live.Change24hPct) || derefS(rest.Volume24h) != derefS(live.Volume24h) ||
		!rest.AsOf.Equal(*live.AsOf) || !rest.LastTickAt.Equal(*live.LastTickAt) || *rest.Volume24hMinutes != *live.Volume24hMinutes {
		t.Fatalf("REST %+v and WS %+v disagree", rest, live)
	}
}

func TestIntegration_WSStats_DisabledAndUnknownRejectedAlike(t *testing.T) {
	f := newStatsFixture(t, 5*time.Second, 10*time.Second, 2*time.Second, 100*time.Millisecond)
	disabled := testInstrument(t, f.db)
	conn := f.dial(t)
	subscribeStats(t, conn, disabled, 987654321)

	codes := map[string]string{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for len(codes) < 2 {
		env := readEnvelope(t, ctx, conn)
		if env.Type == hub.TypeError {
			var p hub.ErrorPayload
			_ = json.Unmarshal(env.Payload, &p)
			codes[env.Sub] = p.Code
		}
	}
	if codes[hub.StatsSub(disabled)] != hub.ErrorCodeNotSubscribable || codes[hub.StatsSub(987654321)] != hub.ErrorCodeNotSubscribable {
		t.Fatalf("errors = %v, want not_subscribable for both, addressed per id", codes)
	}
}

// A stats stream outlives every server timeout: production NewServer,
// production router and middleware, held well past read, write and
// heartbeat, with ticks throughout.
func TestIntegration_WSStats_SurvivesServerTimeouts_Fast(t *testing.T) {
	runStatsHold(t, 500*time.Millisecond, 800*time.Millisecond, 300*time.Millisecond, 3*time.Second, 150*time.Millisecond)
}

// The same at production values (read 15s, write 30s) for 75 seconds,
// past the 30s request timeout middleware too. Slow; opt in.
func TestIntegration_WSStats_SurvivesServerTimeouts_FullLength(t *testing.T) {
	if os.Getenv("ARGUS_RUN_SLOW_TESTS") != "1" {
		t.Skip("slow (75+ real seconds); set ARGUS_RUN_SLOW_TESTS=1 to run")
	}
	runStatsHold(t, 15*time.Second, 30*time.Second, 10*time.Second, 75*time.Second, time.Second)
}

func runStatsHold(t *testing.T, readTimeout, writeTimeout, heartbeat, hold, tickEvery time.Duration) {
	f := newStatsFixture(t, readTimeout, writeTimeout, heartbeat, 100*time.Millisecond)
	conn := f.dial(t)
	subscribeStats(t, conn, f.instID)
	readUntil(t, conn, hub.TypeStats, 5*time.Second)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(tickEvery)
		defer ticker.Stop()
		price := quant.Price(10000)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				price++
				if err := f.tryTick(price, quant.Qty(price-9990)); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()
	defer func() { close(stop); <-done }()

	start := time.Now()
	deadline := start.Add(hold)
	stats, heartbeats := 0, 0
	var lastAt time.Time
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), remaining)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			if time.Until(deadline) <= time.Millisecond {
				break
			}
			t.Fatalf("connection ended after %s, want it to survive %s (read %s, write %s, heartbeat %s): %v",
				time.Since(start), hold, readTimeout, writeTimeout, heartbeat, err)
		}
		var env hub.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		switch env.Type {
		case hub.TypeStats:
			stats++
			lastAt = time.Now()
		case hub.TypeHeartbeat:
			heartbeats++
		}
	}
	if stats < 2 || heartbeats == 0 {
		t.Fatalf("over %s: %d stats and %d heartbeats, want live stats and heartbeats throughout", hold, stats, heartbeats)
	}
	if gap := time.Since(lastAt); gap > hold/3 {
		t.Fatalf("last stats message %s before the end of the hold: delivery stopped", gap)
	}
	t.Logf("held %s past read=%s write=%s heartbeat=%s: %d stats, %d heartbeats, connection never closed",
		hold, readTimeout, writeTimeout, heartbeat, stats, heartbeats)
}

// Messages per second at the brief's scale: a 50-instrument watchlist
// with 5 instruments ticking at once (every 50ms each, far above a real
// feed) gives at most 5 stats messages per StatsMinInterval, never one
// per tick.
func TestIntegration_WSStats_FiftyWatchedFiveTicking(t *testing.T) {
	f := newStatsFixture(t, 5*time.Second, 10*time.Second, 2*time.Second, time.Second)
	ids := []int64{f.instID}
	ticking := []*statsFixture{f}
	for len(ids) < 50 {
		id := enabledTestInstrument(t, f.db)
		ids = append(ids, id)
		if len(ticking) < 5 {
			g := *f
			g.instID = id
			g.tick(t, 10000, 1)
			ticking = append(ticking, &g)
		}
	}
	conn := f.dial(t)
	subscribeStats(t, conn, ids...)

	// The initial figures for all 50.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	initial := map[string]bool{}
	for len(initial) < 50 {
		env := readEnvelope(t, ctx, conn)
		if env.Type == hub.TypeStats {
			initial[env.Sub] = true
		}
	}
	cancel()

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		n := quant.Price(0)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				n++
				for _, g := range ticking {
					if err := g.tryTick(10000+n, quant.Qty(n)); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}
	}()

	const window = 5 * time.Second
	counts := map[string]int{}
	end := time.Now().Add(window)
	for time.Now().Before(end) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(end))
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			break
		}
		var env hub.Envelope
		if json.Unmarshal(data, &env) == nil && env.Type == hub.TypeStats {
			counts[env.Sub]++
		}
	}
	close(stop)
	<-done

	total := 0
	for sub, n := range counts {
		if !strings.HasPrefix(sub, "stats:") {
			t.Fatalf("stats message with sub %q", sub)
		}
		total += n
		// window/StatsMinInterval flushes, plus one for the boundary.
		if n > int(window/time.Second)+1 {
			t.Fatalf("%s: %d messages in %s, want at most %d", sub, n, window, int(window/time.Second)+1)
		}
	}
	if len(counts) != 5 {
		t.Fatalf("%d instruments sent stats, want the 5 ticking ones: %v", len(counts), counts)
	}
	t.Logf("50 watched, 5 ticking every 50ms (100 ticks/s in total): %d stats messages in %s = %.1f msg/s on the client",
		total, window, float64(total)/window.Seconds())
}

// Redis loss (CLAUDE.md item 3, against the real dependency): the hub's
// real pub/sub connection is killed while a client streams stats, a tick
// is stored that the hub never hears about, and one published sequence
// number is lost. The client must still converge on the stored figures:
// the sequence gap makes the hub re-read them from Postgres.
func TestIntegration_WSStats_RedisDrop_Converges(t *testing.T) {
	f := newStatsFixture(t, 5*time.Second, 10*time.Second, 2*time.Second, 100*time.Millisecond)
	conn := f.dial(t)
	subscribeStats(t, conn, f.instID)
	readUntil(t, conn, hub.TypeStats, 5*time.Second)
	// A live feed: the hub has seen at least one event before the drop
	// (its sequence check starts from the first event it sees).
	f.tick(t, 10100, 4)
	for {
		if p := decodeStats(t, readUntil(t, conn, hub.TypeStats, 5*time.Second)); derefS(p.LastPrice) == "101.00" {
			break
		}
	}

	admin := redis.NewClient(f.rdb.Options())
	defer admin.Close()
	killed, err := admin.ClientKillByFilter(context.Background(), "TYPE", "pubsub").Result()
	if err != nil || killed == 0 {
		t.Fatalf("CLIENT KILL TYPE pubsub: killed %d, err %v", killed, err)
	}

	// During the outage the 24h reference candle (anchor - 24h - 1m,
	// close 100.00) is corrected to 50.00 and nothing is published: only
	// a re-read of the base from Postgres can see it. Then a lost seq.
	ref := f.anchor.Add(-24*time.Hour - time.Minute)
	if _, err := f.store.SupersedeCandle(context.Background(), f.instID, clock.NewEventTime(ref), marketdata.Correction{
		Open: 5000, High: 5000, Low: 5000, Close: 5000, Volume: 2, Provider: "stats-test", IngestedAt: clock.NewIngestionTime(time.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.rdb.Incr(context.Background(), fmt.Sprintf("argus:candles:seq:%d", f.instID)).Err(); err != nil {
		t.Fatal(err)
	}

	// Keep publishing real ticks until go-redis has resubscribed and the
	// hub sees one (with a seq that skips the lost one).
	deadline := time.Now().Add(15 * time.Second)
	var got hub.StatsPayload
	price := quant.Price(15000)
	for time.Now().Before(deadline) {
		price++
		f.tick(t, price, 41)
		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			continue
		}
		var env hub.Envelope
		if json.Unmarshal(data, &env) == nil && env.Type == hub.TypeStats {
			got = decodeStats(t, env)
			if derefS(got.LastPrice) == price.String(2) {
				break
			}
		}
	}
	if derefS(got.LastPrice) != price.String(2) {
		t.Fatalf("after the Redis drop the client's last stats price is %s, want the stored %s", derefS(got.LastPrice), price.String(2))
	}
	// Against the corrected reference: price - 50.00.
	wantChange := (price - 5000).String(2)
	if derefS(got.Change24h) != wantChange {
		t.Fatalf("change_24h = %s, want %s against the corrected reference (the base was not re-read)", derefS(got.Change24h), wantChange)
	}
	rest := f.restStats(t)
	if derefS(rest.LastPrice) != derefS(got.LastPrice) || derefS(rest.Volume24h) != derefS(got.Volume24h) || derefS(rest.Change24h) != derefS(got.Change24h) {
		t.Fatalf("after recovery REST %s/%s and WS %s/%s disagree", derefS(rest.LastPrice), derefS(rest.Volume24h), derefS(got.LastPrice), derefS(got.Volume24h))
	}
	t.Logf("pub/sub killed (%d connections); client converged on %s, change %s (corrected reference), volume %s, matching REST",
		killed, derefS(got.LastPrice), derefS(got.Change24h), derefS(got.Volume24h))
}
