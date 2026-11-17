package hyperliquid

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ws "github.com/coder/websocket"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
)

// heartbeatFakeWS accepts one connection, reads the subscribe frame,
// sends one candle message, then goes silent. If answerPings is set it
// replies {"channel":"pong"} to every {"method":"ping"} -- the
// behavior Hyperliquid documents -- and otherwise ignores pings, which
// is what an open-but-dead socket looks like from the client.
type heartbeatFakeWS struct {
	srv         *httptest.Server
	answerPings bool
	pingsSeen   atomic.Int64
}

func newHeartbeatFakeWS(t *testing.T, answerPings bool) *heartbeatFakeWS {
	t.Helper()
	f := &heartbeatFakeWS{answerPings: answerPings}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx := r.Context()
		if _, _, err := conn.Read(ctx); err != nil { // subscribe
			return
		}
		candle, _ := json.Marshal(map[string]any{"channel": "candle", "data": wireCandle{
			T: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC).UnixMilli(), S: "BTC", I: "1m",
			O: "100", Cl: "100", H: "100", L: "100", V: "1",
		}})
		if err := conn.Write(ctx, ws.MessageText, candle); err != nil {
			return
		}
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if strings.Contains(string(data), `"ping"`) {
				f.pingsSeen.Add(1)
				if f.answerPings {
					if err := conn.Write(ctx, ws.MessageText, []byte(`{"channel":"pong"}`)); err != nil {
						return
					}
				}
			}
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *heartbeatFakeWS) wsURL() string { return "ws" + strings.TrimPrefix(f.srv.URL, "http") }

func discardCandle(context.Context, ingestion.ProviderCandle) error { return nil }

// An open but silent socket must be detected and closed: StreamCandles
// returns ErrStreamStalled shortly after the stall timeout, so the
// worker reconnects. Before the watchdog existed it blocked until the
// 12-hour planned-reconnect age.
func TestStreamCandles_OpenButSilentSocketIsDetectedAsStalled(t *testing.T) {
	fake := newHeartbeatFakeWS(t, false)
	p := NewProvider(clock.Real{}, nil,
		WithWSURL(fake.wsURL()), WithDialFunc(dialTest), WithHeartbeat(50*time.Millisecond, 300*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	err := p.StreamCandles(ctx, "BTC", discardCandle)
	elapsed := time.Since(start)

	if ctx.Err() != nil {
		t.Fatalf("StreamCandles still blocked after %v on a silent socket -- stall not detected", elapsed)
	}
	if !errors.Is(err, ErrStreamStalled) {
		t.Fatalf("err = %v, want ErrStreamStalled", err)
	}
	if elapsed < 300*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("detected after %v, want about the 300ms stall timeout", elapsed)
	}
	if fake.pingsSeen.Load() == 0 {
		t.Fatal("no heartbeat ping was sent before giving up")
	}
}

// A quiet but healthy connection (no candles, pongs answered) must NOT
// be cut: the pongs themselves keep the stall timer from expiring.
func TestStreamCandles_QuietConnectionAnsweringPingsStaysUp(t *testing.T) {
	fake := newHeartbeatFakeWS(t, true)
	p := NewProvider(clock.Real{}, nil,
		WithWSURL(fake.wsURL()), WithDialFunc(dialTest), WithHeartbeat(50*time.Millisecond, 300*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := p.StreamCandles(ctx, "BTC", discardCandle)
	if err != nil {
		t.Fatalf("StreamCandles = %v, want it still running (nil on ctx cancel) after 1.5s of answered pings", err)
	}
	if n := fake.pingsSeen.Load(); n < 10 {
		t.Fatalf("pings seen = %d, want a steady heartbeat (~every 50ms)", n)
	}
}

// The ping frame is exactly Hyperliquid's documented heartbeat.
func TestPingPayload_MatchesHyperliquidDocs(t *testing.T) {
	var m map[string]string
	if err := json.Unmarshal(pingPayload, &m); err != nil || len(m) != 1 || m["method"] != "ping" {
		t.Fatalf("ping payload = %s, want {\"method\":\"ping\"}", pingPayload)
	}
}
