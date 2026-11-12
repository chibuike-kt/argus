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

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u
}
