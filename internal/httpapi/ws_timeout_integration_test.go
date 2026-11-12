//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/chibuike-kt/argus/internal/hub"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
)

// startRealServer runs handler behind a real net/http.Server built with
// the production NewServer -- not httptest.NewServer, which sets no
// timeouts of its own and so could never have reproduced the bug this
// file proves fixed (middleware.Timeout(30s) wrapping /ws's own
// request context, and a stale ReadTimeout/WriteTimeout deadline
// surviving Hijack). Returns the server's own http://host:port base URL.
func startRealServer(t *testing.T, handler http.Handler, readTimeout, writeTimeout time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := NewServer(ln.Addr().String(), handler, readTimeout, writeTimeout)
	go func() {
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
	})
	return "http://" + ln.Addr().String()
}

// wsTimeoutTestFixture wires one enabled instrument, a real server, and
// a subscribed real WebSocket client, returning the live connection and
// a publish func the test drives on its own schedule.
func wsTimeoutTestFixture(t *testing.T, readTimeout, writeTimeout, heartbeatInterval time.Duration) (conn *websocket.Conn, publish func(seqOffset int)) {
	t.Helper()
	db := testDB(t)
	rdb := testRedisForWS(t)
	deps, instrumentsStore, users, sessions := testWSDeps(t, db, rdb)
	deps.Live.HeartbeatInterval = heartbeatInterval

	baseURL := startRealServer(t, NewRouter(deps), readTimeout, writeTimeout)

	priceScale, qtyScale := quant.Scale(2), quant.Scale(4)
	symbol := fmt.Sprintf("WS-TIMEOUT-%d", time.Now().UnixNano())
	inst, err := instrumentsStore.Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: "timeout-test", NativeSymbol: symbol, AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1,
	})
	if err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE instruments SET enabled = TRUE WHERE id = $1`, inst.ID); err != nil {
		t.Fatalf("enable instrument: %v", err)
	}

	jarClient := authenticatedClient(t, baseURL, users, sessions)
	var rawToken string
	for _, c := range jarClient.Jar.Cookies(mustParseURL(t, baseURL)) {
		if c.Name == deps.AuthPolicy.CookieName(false) {
			rawToken = c.Value
		}
	}

	h := wsDialHeader(deps.AuthPolicy.CookieName(false), rawToken, testOrigin)
	conn, _, err = websocket.Dial(context.Background(), wsURL(baseURL)+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })

	subPayload, _ := json.Marshal(subscribeRequest{InstrumentID: inst.ID, Interval: "1m"})
	req, _ := json.Marshal(hub.Envelope{V: 1, Type: hub.TypeSubscribe, Payload: subPayload})
	if err := conn.Write(context.Background(), websocket.MessageText, req); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	readUntil(t, conn, hub.TypeSnapshot, 5*time.Second)

	publisher := hub.NewRedisPublisher(rdb)
	base := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	publish = func(seqOffset int) {
		openTime := base.Add(time.Duration(seqOffset) * time.Minute)
		if err := publisher.Publish(context.Background(), hub.CandleEvent{
			InstrumentID: inst.ID, Kind: hub.EventForming,
			OpenTime: openTime, CloseTime: openTime.Add(time.Minute),
			Open: 100, High: 105, Low: 100, Close: 103, Volume: 5, Revision: 1,
		}); err != nil {
			t.Errorf("publish: %v", err)
		}
	}
	return conn, publish
}

// readLoopCounts drains conn for exactly duration, counting candle and
// heartbeat messages and failing the test immediately if the
// connection closes or errors before duration elapses.
func readLoopCounts(t *testing.T, conn *websocket.Conn, duration time.Duration) (candles, heartbeats int) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return candles, heartbeats
		}
		ctx, cancel := context.WithTimeout(context.Background(), remaining)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil && time.Now().After(deadline.Add(-time.Millisecond)) {
				// The read's own per-call context expired, not the
				// connection itself -- expected once duration is used
				// up between messages; loop will exit on the next
				// remaining<=0 check.
				return candles, heartbeats
			}
			t.Fatalf("connection closed or errored after %s (wanted it to survive %s): %v", duration-time.Until(deadline), duration, err)
		}
		var env hub.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		switch env.Type {
		case hub.TypeCandle:
			candles++
		case hub.TypeHeartbeat:
			heartbeats++
		}
	}
}

// TestIntegration_WS_SurvivesPastRequestTimeout_Fast is the same proof
// as the full-length test below, at a scale the normal suite can run
// in a few seconds: readTimeout/writeTimeout/heartbeat are all small,
// and the connection is held well past all three, continuously
// publishing live candles throughout.
func TestIntegration_WS_SurvivesPastRequestTimeout_Fast(t *testing.T) {
	const (
		readTimeout  = 500 * time.Millisecond
		writeTimeout = 800 * time.Millisecond
		heartbeat    = 300 * time.Millisecond
		hold         = 3 * time.Second // several multiples of all three above
	)
	conn, publish := wsTimeoutTestFixture(t, readTimeout, writeTimeout, heartbeat)

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				i++
				publish(i)
			}
		}
	}()
	defer close(stop)

	candles, heartbeats := readLoopCounts(t, conn, hold)
	if candles == 0 {
		t.Error("got 0 candle messages over the hold period, want at least 1 -- live delivery must not stop")
	}
	if heartbeats == 0 {
		t.Error("got 0 heartbeat messages over the hold period, want at least 1")
	}
	t.Logf("fast run: held open %s past read=%s write=%s heartbeat=%s, received %d candles and %d heartbeats, connection never closed",
		hold, readTimeout, writeTimeout, heartbeat, candles, heartbeats)
}

// TestIntegration_WS_SurvivesPastRequestTimeout_FullLength is the
// literal repro at production scale: NewServer with the real
// ReadTimeout (15s, config.Config.HTTPReadTimeout's own default) and
// WriteTimeout (30s, NewServer's own hardcoded value), held open for
// over a minute past both, and past the 30s middleware.Timeout this
// bug report's own root cause used to apply to /ws. This is slow by
// design (over 70 real seconds) and is skipped unless
// ARGUS_RUN_SLOW_TESTS=1 is set, so the normal suite stays fast; run it
// explicitly (go test -tags=integration -run FullLength -timeout 120s
// ./internal/httpapi/...) when verifying this fix, and see the report
// for one real run's own numbers.
func TestIntegration_WS_SurvivesPastRequestTimeout_FullLength(t *testing.T) {
	if os.Getenv("ARGUS_RUN_SLOW_TESTS") != "1" {
		t.Skip("slow (70+ real seconds); set ARGUS_RUN_SLOW_TESTS=1 to run")
	}
	const (
		readTimeout  = 15 * time.Second // config.Config.HTTPReadTimeout's own default
		writeTimeout = 30 * time.Second // NewServer's own production value
		heartbeat    = 10 * time.Second
		hold         = 75 * time.Second // past 70s, and past both timeouts above with margin
	)
	conn, publish := wsTimeoutTestFixture(t, readTimeout, writeTimeout, heartbeat)

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				i++
				publish(i)
			}
		}
	}()
	defer close(stop)

	start := time.Now()
	candles, heartbeats := readLoopCounts(t, conn, hold)
	elapsed := time.Since(start)
	if candles == 0 {
		t.Error("got 0 candle messages over 75 real seconds, want several")
	}
	if heartbeats == 0 {
		t.Error("got 0 heartbeat messages over 75 real seconds, want several")
	}
	t.Logf("full-length run: held open %s (production read=%s write=%s), received %d candles and %d heartbeats, connection never closed",
		elapsed, readTimeout, writeTimeout, candles, heartbeats)
}
