//go:build integration

package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/hub"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/session"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/quant"
)

// testRedisForWS mirrors auth_integration_test.go's own
// testRedisForAuth: a real Redis, a dedicated logical DB so this
// package's tests never collide with another suite's keys/channels,
// flushed before use. ARGUS_TEST_REDIS_URL overrides the default for a
// CI environment that places Redis somewhere other than localhost.
func testRedisForWS(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("ARGUS_TEST_REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379/15"
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("integration test redis unreachable: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// enabledTestInstrument is testInstrument (setups_integration_test.go)
// plus the one thing it deliberately never does itself: flipping
// enabled to TRUE directly over SQL. instruments.Store never gets a
// path to do this (ADR-007; brief's own "Out of scope" note) -- that
// rule is about the store's own exported API, not about a test fixture
// preparing state the hub's own enabled-check (brief rule: "Only
// instruments the user may see (enabled) can be subscribed") needs to
// exercise the "enabled" branch at all.
func enabledTestInstrument(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	id := testInstrument(t, db)
	if _, err := db.ExecContext(context.Background(), `UPDATE instruments SET enabled = TRUE WHERE id = $1`, id); err != nil {
		t.Fatalf("enable test instrument: %v", err)
	}
	return id
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func wsDialHeader(cookieName, cookieValue, origin string) http.Header {
	h := http.Header{}
	h.Set("Origin", origin)
	h.Set("Cookie", fmt.Sprintf("%s=%s", cookieName, cookieValue))
	return h
}

func readEnvelope(t *testing.T, ctx context.Context, conn *websocket.Conn) hub.Envelope {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var env hub.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	return env
}

// readUntil reads envelopes until one of type want arrives (skipping
// heartbeat and any other type in between), or deadline elapses.
func readUntil(t *testing.T, conn *websocket.Conn, want hub.MessageType, timeout time.Duration) hub.Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		env := readEnvelope(t, ctx, conn)
		if env.Type == want {
			return env
		}
	}
}

// testOrigin is the allowed Origin these tests dial with. It need not
// match the httptest.Server's own address (the Origin check only
// compares the request's claimed Origin against the configured
// allow-list, independent of what host:port the server itself listens
// on) -- fixed so it can be set in AuthPolicy.AllowedOrigins before the
// server (and so its randomly-assigned port) exists.
const testOrigin = "http://localhost:5173"

func testWSDeps(t *testing.T, db *sql.DB, rdb *redis.Client) (Deps, *instruments.Store, *identity.Store, *session.Store) {
	deps, users, sessions := testAuthDeps(db)
	deps.Logger, deps.DB, deps.Redis = slog.Default(), db, rdb
	deps.AuthPolicy.AllowedOrigins = []string{testOrigin}
	deps.Live = config.LiveFeed{
		MaxUpdateRatePerSecond:        1000,
		MaxSubscriptionsPerConnection: 10,
		MaxConnectionsPerUser:         50,
		MaxConnectionsPerIP:           50,
		MaxSubscribeMessagesPerSecond: 100,
		HeartbeatInterval:             2 * time.Second,
		MaxMessageSize:                4096,
	}
	instrumentsStore := instruments.NewStore(db)
	candles := marketdata.NewStore(db, clock.Real{})
	deps.Hub = hub.New(hub.NewRedisSubscriber(rdb), candles, instrumentsStore, clock.Real{}, deps.Live, deps.Logger)
	return deps, instrumentsStore, users, sessions
}

func TestIntegration_WS_OriginRejected(t *testing.T) {
	db := testDB(t)
	rdb := testRedisForWS(t)
	deps, _, _, _ := testWSDeps(t, db, rdb)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	for _, tc := range []struct {
		name   string
		origin string
	}{
		{"missing origin", ""},
		{"wrong origin", "https://evil.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.origin != "" {
				h.Set("Origin", tc.origin)
			}
			_, resp, err := websocket.Dial(context.Background(), wsURL(srv.URL)+"/ws", &websocket.DialOptions{HTTPHeader: h})
			if err == nil {
				t.Fatal("dial succeeded, want rejection before the handshake")
			}
			if resp == nil || resp.StatusCode != http.StatusForbidden {
				status := -1
				if resp != nil {
					status = resp.StatusCode
				}
				t.Fatalf("status = %d, want 403", status)
			}
		})
	}
}

func TestIntegration_WS_UnauthenticatedRejected(t *testing.T) {
	db := testDB(t)
	rdb := testRedisForWS(t)
	deps, _, _, _ := testWSDeps(t, db, rdb)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	h := http.Header{}
	h.Set("Origin", testOrigin)
	_, resp, err := websocket.Dial(context.Background(), wsURL(srv.URL)+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if err == nil {
		t.Fatal("dial succeeded with no session cookie, want rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want 401", status)
	}
}

func TestIntegration_WS_DisabledInstrument_NotSubscribable(t *testing.T) {
	db := testDB(t)
	rdb := testRedisForWS(t)
	deps, _, users, sessions := testWSDeps(t, db, rdb)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	disabledID := testInstrument(t, db) // testInstrument's own rows are always disabled
	jarClient := authenticatedClient(t, srv.URL, users, sessions)
	cookies := jarClient.Jar.Cookies(mustParseURL(t, srv.URL))
	var rawToken string
	for _, c := range cookies {
		if c.Name == deps.AuthPolicy.CookieName(false) {
			rawToken = c.Value
		}
	}
	if rawToken == "" {
		t.Fatal("no session cookie found on the authenticated client")
	}

	h := wsDialHeader(deps.AuthPolicy.CookieName(false), rawToken, testOrigin)
	conn, _, err := websocket.Dial(context.Background(), wsURL(srv.URL)+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	subPayload, _ := json.Marshal(subscribeRequest{InstrumentID: disabledID, Interval: "1m"})
	req, _ := json.Marshal(hub.Envelope{V: 1, Type: hub.TypeSubscribe, Payload: subPayload})
	if err := conn.Write(context.Background(), websocket.MessageText, req); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	env := readUntil(t, conn, hub.TypeError, 5*time.Second)
	var errPayload hub.ErrorPayload
	if err := json.Unmarshal(env.Payload, &errPayload); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if errPayload.Code != hub.ErrorCodeNotSubscribable {
		t.Errorf("error code = %q, want %q", errPayload.Code, hub.ErrorCodeNotSubscribable)
	}
}

// TestIntegration_WS_EndToEnd_SubscribeFormingClosed proves the full
// real-stack path the brief's own Phase 3 addendum item D is about:
// a worker-side ingestion.Service (fed by a deterministic
// fixture.ScriptedProvider, Phase 2's own harness) publishes through a
// real hub.RedisPublisher over a real Redis connection; the API
// process's own Hub (hub.RedisSubscriber) picks it up and fans it out
// to a real *websocket.Conn client, in order, with consecutive seq
// numbers and the closed message never dropped.
func TestIntegration_WS_EndToEnd_SubscribeFormingClosed(t *testing.T) {
	db := testDB(t)
	rdb := testRedisForWS(t)
	deps, instrumentsStore, users, sessions := testWSDeps(t, db, rdb)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	providerName := fmt.Sprintf("ws-e2e-%d", time.Now().UnixNano())
	symbol := "WS-E2E-PERP"
	priceScale, qtyScale := quant.Scale(2), quant.Scale(4)
	inst, err := instrumentsStore.Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: providerName, NativeSymbol: symbol, AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1,
		SupportedIntervals: []string{"1m"},
	})
	if err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE instruments SET enabled = TRUE WHERE id = $1`, inst.ID); err != nil {
		t.Fatalf("enable instrument: %v", err)
	}

	jarClient := authenticatedClient(t, srv.URL, users, sessions)
	var rawToken string
	for _, c := range jarClient.Jar.Cookies(mustParseURL(t, srv.URL)) {
		if c.Name == deps.AuthPolicy.CookieName(false) {
			rawToken = c.Value
		}
	}

	h := wsDialHeader(deps.AuthPolicy.CookieName(false), rawToken, testOrigin)
	conn, _, err := websocket.Dial(context.Background(), wsURL(srv.URL)+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	subPayload, _ := json.Marshal(subscribeRequest{InstrumentID: inst.ID, Interval: "1m"})
	req, _ := json.Marshal(hub.Envelope{V: 1, Type: hub.TypeSubscribe, Payload: subPayload})
	if err := conn.Write(context.Background(), websocket.MessageText, req); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	snapEnv := readUntil(t, conn, hub.TypeSnapshot, 5*time.Second)
	var snap hub.SnapshotPayload
	if err := json.Unmarshal(snapEnv.Payload, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if snap.LastClosedOpenTime != nil {
		t.Fatalf("snapshot already has a closed candle before anything was published")
	}

	// Drive the worker side: a scripted provider streaming one forming
	// update then a close, through a real ingestion.Service wired to a
	// real hub.RedisPublisher over the same Redis the API process's own
	// Hub subscribes through.
	cache := ingestion.NewInstrumentCache(instrumentsStore)
	normalizer := ingestion.NewNormalizer(cache, clock.Real{})
	base := time.Now().UTC().Truncate(time.Minute).Add(time.Minute) // a fresh, not-yet-seen minute
	script := fixture.Script{Symbol: symbol, Events: []fixture.ScriptedEvent{
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: base, Open: "100.00", High: "101.00", Low: "99.50", Close: "100.50", Volume: "10.0000", IsFinal: false}},
		{Candle: ingestion.ProviderCandle{Symbol: symbol, OpenTime: base, Open: "100.00", High: "102.00", Low: "99.00", Close: "101.50", Volume: "20.0000", IsFinal: true}},
	}}
	provider := fixture.NewScriptedProvider([]fixture.Script{script}, clock.Real{}, 0, nil)
	svc := ingestion.NewService(providerName, provider, normalizer, cache, marketdata.NewStore(db, clock.Real{}), marketdata.ContinuousCalendar{}, slog.Default())
	svc.SetPublisher(hub.NewRedisPublisher(rdb))

	go func() {
		_ = svc.WatchCandles(context.Background(), symbol)
	}()

	formingEnv := readUntil(t, conn, hub.TypeCandle, 10*time.Second)
	closedEnv := readUntil(t, conn, hub.TypeCandleClosed, 10*time.Second)

	if formingEnv.Seq == 0 || closedEnv.Seq <= formingEnv.Seq {
		t.Fatalf("seqs not consecutive/ordered: forming=%d closed=%d", formingEnv.Seq, closedEnv.Seq)
	}
	var closedPayload hub.CandlePayload
	if err := json.Unmarshal(closedEnv.Payload, &closedPayload); err != nil {
		t.Fatalf("unmarshal closed payload: %v", err)
	}
	if closedPayload.Close != "101.50" {
		t.Errorf("closed candle Close = %q, want %q", closedPayload.Close, "101.50")
	}
}

// TestIntegration_WS_RedisDrop_RecoversViaSequenceGap is review item
// 7's own real repro, now fixed: kill the hub's pubsub connection with
// CLIENT KILL TYPE pubsub while a client is connected and subscribed,
// publish during the outage, and confirm the client still gets
// status:degraded then a fresh snapshot that includes what was
// published while it was disconnected -- without depending on
// anything go-redis reports about the reconnect itself (a real repro
// against this same Redis showed that signal never arrives: delivery
// just silently resumed, with no degraded notice at all). Recovery is
// now driven by a gap in CandleEvent.Seq, which this test forces
// deterministically (wasting a sequence number) rather than racing
// go-redis's own reconnect timing, since either a slow reconnect or an
// outright lost message produces the exact same gap from the hub's
// side.
func TestIntegration_WS_RedisDrop_RecoversViaSequenceGap(t *testing.T) {
	db := testDB(t)
	rdb := testRedisForWS(t)
	deps, instrumentsStore, users, sessions := testWSDeps(t, db, rdb)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	priceScale, qtyScale := quant.Scale(2), quant.Scale(4)
	symbol := fmt.Sprintf("WS-REDISDROP-%d", time.Now().UnixNano())
	inst, err := instrumentsStore.Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: "redisdrop-test", NativeSymbol: symbol, AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1,
		SupportedIntervals: []string{"1m"},
	})
	if err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE instruments SET enabled = TRUE WHERE id = $1`, inst.ID); err != nil {
		t.Fatalf("enable instrument: %v", err)
	}

	jarClient := authenticatedClient(t, srv.URL, users, sessions)
	var rawToken string
	for _, c := range jarClient.Jar.Cookies(mustParseURL(t, srv.URL)) {
		if c.Name == deps.AuthPolicy.CookieName(false) {
			rawToken = c.Value
		}
	}

	h := wsDialHeader(deps.AuthPolicy.CookieName(false), rawToken, testOrigin)
	conn, _, err := websocket.Dial(context.Background(), wsURL(srv.URL)+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	subPayload, _ := json.Marshal(subscribeRequest{InstrumentID: inst.ID, Interval: "1m"})
	req, _ := json.Marshal(hub.Envelope{V: 1, Type: hub.TypeSubscribe, Payload: subPayload})
	if err := conn.Write(context.Background(), websocket.MessageText, req); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	readUntil(t, conn, hub.TypeSnapshot, 5*time.Second) // the normal subscribe-time snapshot

	publisher := hub.NewRedisPublisher(rdb)
	base := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)

	// A normal event, delivered before any drop, to prove the stream
	// is alive and the hub's own sequence tracking has started.
	if err := publisher.Publish(context.Background(), hub.CandleEvent{
		InstrumentID: inst.ID, Kind: hub.EventForming,
		OpenTime: base, CloseTime: base.Add(time.Minute),
		Open: 100, High: 105, Low: 100, Close: 103, Volume: 5, Revision: 1,
	}); err != nil {
		t.Fatalf("publish (before drop): %v", err)
	}
	readUntil(t, conn, hub.TypeCandle, 5*time.Second)

	// Kill the pubsub connection -- the literal repro. go-redis will
	// transparently reconnect and resubscribe on its own (its own doc
	// comment says so); this test does not wait for or depend on that
	// in any way.
	admin := redis.NewClient(rdb.Options())
	defer admin.Close()
	killed, err := admin.ClientKillByFilter(context.Background(), "TYPE", "pubsub").Result()
	if err != nil {
		t.Fatalf("CLIENT KILL TYPE pubsub: %v", err)
	}
	if killed == 0 {
		t.Fatal("CLIENT KILL TYPE pubsub killed 0 connections -- the hub's own subscription was not in pubsub mode when expected")
	}

	// Seed the candle that was "missed during the outage" directly in
	// the database -- the resnapshot recovery reads from here, not
	// from whatever Redis did or did not deliver.
	missedOpen := base.Add(time.Minute)
	missedStore := marketdata.NewStore(db, clock.Real{})
	if _, err := missedStore.UpsertForming(context.Background(), marketdata.Tick{
		InstrumentID: inst.ID, OpenTime: clock.NewEventTime(missedOpen),
		Open: 110, High: 115, Low: 108, Close: 112, Volume: 7,
		Provider: "redisdrop-test", IngestedAt: clock.NewIngestionTime(time.Now()),
	}); err != nil {
		t.Fatalf("seed missed candle: %v", err)
	}
	if _, err := missedStore.Close(context.Background(), inst.ID, clock.NewEventTime(missedOpen)); err != nil {
		t.Fatalf("close missed candle: %v", err)
	}

	// Deterministically force the gap this publish will land on:
	// waste the next sequence number, exactly simulating a published
	// event that never arrived (whether lost in transit or published
	// while nothing was subscribed) -- mirrors RedisPublisher's own
	// key format (redis.go's seqKey), which is otherwise unexported.
	if err := rdb.Incr(context.Background(), fmt.Sprintf("argus:candles:seq:%d", inst.ID)).Err(); err != nil {
		t.Fatalf("waste a sequence number: %v", err)
	}

	// Publish the next real event. Whatever go-redis's own reconnect
	// has or has not finished doing by now, the hub will see this
	// arrive with a seq that skips the one just wasted.
	if err := publisher.Publish(context.Background(), hub.CandleEvent{
		InstrumentID: inst.ID, Kind: hub.EventClosed,
		OpenTime: base.Add(2 * time.Minute), CloseTime: base.Add(3 * time.Minute),
		Open: 100, High: 106, Low: 99, Close: 104, Volume: 6, Revision: 1,
	}); err != nil {
		t.Fatalf("publish (after drop): %v", err)
	}

	degradedEnv := readUntil(t, conn, hub.TypeStatus, 15*time.Second)
	var status hub.StatusPayload
	if err := json.Unmarshal(degradedEnv.Payload, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.State != "degraded" {
		t.Fatalf("status state = %q, want degraded", status.State)
	}

	recoveryEnv := readUntil(t, conn, hub.TypeSnapshot, 5*time.Second)
	var recovery hub.SnapshotPayload
	if err := json.Unmarshal(recoveryEnv.Payload, &recovery); err != nil {
		t.Fatalf("unmarshal recovery snapshot: %v", err)
	}
	if recovery.LastClosedOpenTime == nil || !recovery.LastClosedOpenTime.Equal(missedOpen) {
		t.Fatalf("recovery snapshot LastClosedOpenTime = %v, want %v -- the candle seeded during the outage must not be silently lost", recovery.LastClosedOpenTime, missedOpen)
	}
}

// dialAuthenticatedWS is the dial sequence TestIntegration_WS_DisabledInstrument_NotSubscribable
// and others already repeat inline -- factored out for the hostile-
// condition tests below, which do not otherwise care about that
// boilerplate.
func dialAuthenticatedWS(t *testing.T, srv *httptest.Server, deps Deps, users *identity.Store, sessions *session.Store) *websocket.Conn {
	t.Helper()
	jarClient := authenticatedClient(t, srv.URL, users, sessions)
	cookies := jarClient.Jar.Cookies(mustParseURL(t, srv.URL))
	var rawToken string
	for _, c := range cookies {
		if c.Name == deps.AuthPolicy.CookieName(false) {
			rawToken = c.Value
		}
	}
	if rawToken == "" {
		t.Fatal("no session cookie found on the authenticated client")
	}
	h := wsDialHeader(deps.AuthPolicy.CookieName(false), rawToken, testOrigin)
	conn, _, err := websocket.Dial(context.Background(), wsURL(srv.URL)+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

// TestIntegration_WS_OversizedMessageClosesConnection is a hostile-
// condition test: ws.go's own wsConn.SetReadLimit(h.live.MaxMessageSize)
// is configured (testWSDeps sets it to 4096) but was never actually
// exercised by anything sending a message over that limit -- a
// malicious or merely buggy client flooding the connection with a
// huge payload must not be allowed to consume unbounded server memory
// reading it.
func TestIntegration_WS_OversizedMessageClosesConnection(t *testing.T) {
	db := testDB(t)
	rdb := testRedisForWS(t)
	deps, _, users, sessions := testWSDeps(t, db, rdb)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	conn := dialAuthenticatedWS(t, srv, deps, users, sessions)
	defer conn.CloseNow()

	// One byte past the configured 4096-byte limit.
	oversized := make([]byte, deps.Live.MaxMessageSize+1)
	for i := range oversized {
		oversized[i] = 'a'
	}
	// The write itself may or may not return an error depending on
	// buffering/timing -- the real assertion is what happens next: the
	// connection must be closed by the server, not left half-alive
	// consuming memory for an oversized frame it never finishes
	// reading.
	_ = conn.Write(context.Background(), websocket.MessageText, oversized)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("Read after an oversized message = nil error, want the connection closed")
	}
}

// TestIntegration_WS_MalformedJSON_ConnectionSurvives is a hostile-
// condition test for ws.go's own readLoop: garbage bytes that do not
// even parse as JSON are silently ignored (the handler's own
// json.Unmarshal failure path, continue rather than close) -- proving
// that holds against the REAL server, not just reading the source, and
// that the connection is still fully usable afterward (a real
// subscribe still works), not left in some half-broken state.
func TestIntegration_WS_MalformedJSON_ConnectionSurvives(t *testing.T) {
	db := testDB(t)
	rdb := testRedisForWS(t)
	deps, _, users, sessions := testWSDeps(t, db, rdb)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	instID := enabledTestInstrument(t, db)
	conn := dialAuthenticatedWS(t, srv, deps, users, sessions)
	defer conn.CloseNow()

	if err := conn.Write(context.Background(), websocket.MessageText, []byte("{not valid json at all")); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`"just a json string, not an envelope object"`)); err != nil {
		t.Fatalf("write non-object json: %v", err)
	}

	// The connection must still be alive and able to subscribe
	// normally -- proof the garbage did not leave readLoop in a broken
	// state, not just that it did not crash the process.
	subPayload, _ := json.Marshal(subscribeRequest{InstrumentID: instID, Interval: "1m"})
	req, _ := json.Marshal(hub.Envelope{V: 1, Type: hub.TypeSubscribe, Payload: subPayload})
	if err := conn.Write(context.Background(), websocket.MessageText, req); err != nil {
		t.Fatalf("write subscribe after garbage: %v", err)
	}
	readUntil(t, conn, hub.TypeSnapshot, 5*time.Second) // would time out and fail the test if the connection were broken
}

// TestIntegration_WS_GracefulShutdown_ClosesOpenConnections is the
// brief's own required "graceful shutdown" test, against the real
// http.Server (CLAUDE.md's own "long lived connections" rule: the
// production NewServer, not a bare httptest server) -- proving what a
// client sees when the process receives a shutdown signal with a live
// subscription still open.
func TestIntegration_WS_GracefulShutdown_ClosesOpenConnections(t *testing.T) {
	db := testDB(t)
	rdb := testRedisForWS(t)
	deps, _, users, sessions := testWSDeps(t, db, rdb)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := NewServer(ln.Addr().String(), NewRouter(deps), 5*time.Second, 5*time.Second)
	go func() { _ = srv.Serve(ln) }()
	baseURL := "http://" + ln.Addr().String()

	jarClient := authenticatedClient(t, baseURL, users, sessions)
	cookies := jarClient.Jar.Cookies(mustParseURL(t, baseURL))
	var rawToken string
	for _, c := range cookies {
		if c.Name == deps.AuthPolicy.CookieName(false) {
			rawToken = c.Value
		}
	}
	if rawToken == "" {
		t.Fatal("no session cookie found on the authenticated client")
	}
	h := wsDialHeader(deps.AuthPolicy.CookieName(false), rawToken, testOrigin)
	conn, _, err := websocket.Dial(context.Background(), wsURL(baseURL)+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Confirm the connection is genuinely open and live-streaming
	// before shutdown: a heartbeat must arrive first.
	readUntil(t, conn, hub.TypeHeartbeat, 5*time.Second)

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Mirrors cmd/argus-api/main.go's own shutdown sequence: Shutdown
		// does not close hijacked connections on its own, so CloseAll
		// runs first, within the same deadline.
		deps.Hub.CloseAll(ctx, "server shutting down")
		shutdownDone <- srv.Shutdown(ctx)
	}()

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown: %v (graceful shutdown must not itself error out)", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Shutdown did not return within its own 5s deadline -- an open WebSocket connection blocked it from completing, want it closed instead")
	}

	// The client side must see its own connection actually close with a
	// real WebSocket close frame, status 1001 (going away) -- proof
	// that Hub.CloseAll ran, not just any error (a half-open TCP
	// connection timing out, or this test's own read timeout, would
	// both also produce "an error" without proving the server did
	// anything). A single Read right after Shutdown is not reliable
	// either way: an in-flight heartbeat already queued before the
	// close can legitimately be read first (confirmed flaky under real
	// timing -- about 1 run in 3 got that heartbeat instead of the
	// close on the very first Read). Read in a loop instead, for up to
	// an overall 3s budget, and only fail if no error ever arrives in
	// that window. A short per-read timeout is deliberately not itself
	// treated as that error: if the connection is merely silent (still
	// open, nothing to read yet), each short attempt times out on its
	// own without anything having gone wrong server-side, and that must
	// not be mistaken for the close this test is actually looking for
	// -- only once the overall budget itself is spent does "no error
	// arrived" become the real, reported failure.
	budget, cancelBudget := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelBudget()
	var closeErr error
	for budget.Err() == nil {
		readCtx, cancelRead := context.WithTimeout(budget, 500*time.Millisecond)
		_, _, err := conn.Read(readCtx)
		cancelRead()
		if err == nil {
			continue // a real message (e.g. an in-flight heartbeat) -- keep reading
		}
		if errors.Is(err, context.DeadlineExceeded) && budget.Err() == nil {
			continue // this read's own short timeout, not a real close -- the overall budget is not exhausted yet
		}
		closeErr = err
		break
	}
	if closeErr == nil {
		t.Fatal("no error arrived from the server within 3s after Shutdown -- the connection was never actually closed")
	}
	if status := websocket.CloseStatus(closeErr); status != websocket.StatusGoingAway {
		t.Fatalf("connection closed with %v (status %v), want a real close frame with status 1001 (going away) -- Hub.CloseAll did not run, or ran with the wrong code", closeErr, status)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u
}
