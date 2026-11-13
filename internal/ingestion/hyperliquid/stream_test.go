package hyperliquid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ws "github.com/coder/websocket"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
)

// fakeHyperliquidWS is a minimal server-side stand-in for
// wss://api.hyperliquid.xyz/ws: it accepts one connection, reads and
// discards the subscribe frame, then sends whatever candle messages
// the test feeds it over send, in order, pacing left entirely to the
// test (send blocks until the client has read the previous message,
// since both ends share one small, unbuffered channel). disconnect
// simulates a dropped connection; a test that calls it directly (to
// exercise a mid-stream drop) and the t.Cleanup teardown both go
// through the same sync.Once, so closing done twice -- once
// deliberately, once as ordinary teardown -- is never a double-close
// panic.
type fakeHyperliquidWS struct {
	srv            *httptest.Server
	send           chan wireCandle
	sendRaw        chan []byte
	done           chan struct{}
	disconnectOnce sync.Once
}

func newFakeHyperliquidWS(t *testing.T) *fakeHyperliquidWS {
	t.Helper()
	f := &fakeHyperliquidWS{send: make(chan wireCandle), sendRaw: make(chan []byte), done: make(chan struct{})}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		ctx := r.Context()
		if _, _, err := conn.Read(ctx); err != nil { // the subscribe frame, discarded
			return
		}
		// The client never sends another data message after its one
		// subscribe frame, but it DOES eventually send a close frame
		// (when the client-side Close() runs), and that handshake can
		// only complete if something on this side is still reading to
		// receive and auto-ack it -- coder/websocket's own Close()
		// blocks waiting for the peer's own close frame back.
		// CloseRead is this library's documented answer to exactly
		// "no more data messages expected, but still respond to
		// ping/pong/close": without it, the client's own Close() blocks
		// for its own internal close-handshake timeout with nothing
		// here ever acking it -- the real cause of an intermittent
		// several-second delay found while writing these tests
		// (StreamCandles took a few real seconds to return after ctx
		// was canceled, flaky depending on test timing). Not a bug in
		// the adapter: a missing read loop in this fake.
		closeCtx := conn.CloseRead(ctx)
		for {
			select {
			case c, ok := <-f.send:
				if !ok {
					return
				}
				env := map[string]any{"channel": "candle", "data": c}
				payload, _ := json.Marshal(env)
				if err := conn.Write(ctx, ws.MessageText, payload); err != nil {
					return
				}
			case raw, ok := <-f.sendRaw:
				if !ok {
					return
				}
				if err := conn.Write(ctx, ws.MessageText, raw); err != nil {
					return
				}
			case <-f.done:
				return
			case <-closeCtx.Done():
				return
			case <-ctx.Done():
				return
			}
		}
	}))
	t.Cleanup(func() {
		f.disconnect()
		f.srv.Close()
	})
	return f
}

// disconnect simulates the connection dying: safe to call any number
// of times, from a test body and/or teardown.
func (f *fakeHyperliquidWS) disconnect() {
	f.disconnectOnce.Do(func() { close(f.done) })
}

func (f *fakeHyperliquidWS) wsURL() string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http")
}

func dialTest(ctx context.Context, url string) (*ws.Conn, error) {
	conn, _, err := ws.Dial(ctx, url, nil) //nolint:bodyclose // Dial's own doc comment: "You never need to close resp.Body yourself."
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func TestStreamCandles_ClosesPreviousMinuteOnlyWhenALaterOneArrives(t *testing.T) {
	fake := newFakeHyperliquidWS(t)
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

	var restCalls int
	restServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		restCalls++
		var req candleSnapshotRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		// The REST-authoritative value differs from what was streamed,
		// on purpose: this is what proves "the REST version wins" --
		// the caller receives THIS value as the closed candle, not
		// whatever was last streamed for that minute.
		_ = json.NewEncoder(w).Encode([]wireCandle{
			candleJSON(req.Req.StartTime, "100", "103", "99", "102.7", "999"),
		})
	}))
	t.Cleanup(restServer.Close)

	p := NewProvider(clock.NewFixed(base), nil, WithBaseURL(restServer.URL), WithWSURL(fake.wsURL()), WithDialFunc(dialTest))

	var received []ingestion.ProviderCandle
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		done <- p.StreamCandles(ctx, "BTC", func(_ context.Context, c ingestion.ProviderCandle) error {
			received = append(received, c)
			if len(received) == 4 {
				cancel() // enough to assert on; let ctx cancellation end the stream cleanly
			}
			return nil
		})
	}()

	fake.send <- candleJSON(base.UnixMilli(), "100", "101", "99", "100.5", "10")                    // minute 0, forming
	fake.send <- candleJSON(base.UnixMilli(), "100", "101.5", "99", "100.8", "11")                  // minute 0 again, still forming -- same open time, must NOT close it
	fake.send <- candleJSON(base.Add(time.Minute).UnixMilli(), "100.8", "102", "100", "101.5", "5") // minute 1 -- proves minute 0 closed

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StreamCandles: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StreamCandles did not return after ctx was canceled")
	}

	// Expected sequence: minute-0 forming (x2, same open time, neither
	// final), minute-0 CLOSED (injected the moment minute-1's own
	// later open time arrives), minute-1 forming.
	if len(received) != 4 {
		t.Fatalf("got %d candles, want 4 (two minute-0 forming updates, its close, then minute-1 forming)", len(received))
	}
	if received[0].IsFinal || received[1].IsFinal {
		t.Errorf("minute-0 forming updates must not be final before a later minute arrives: %+v / %+v", received[0], received[1])
	}
	if !received[2].IsFinal {
		t.Errorf("the third call must be minute-0's own close, got IsFinal=false: %+v", received[2])
	}
	if received[3].IsFinal {
		t.Errorf("minute-1's own forming update must not be final: %+v", received[3])
	}
	if restCalls != 1 {
		t.Errorf("made %d REST candleSnapshot calls, want exactly 1 (one close, not one per streamed message)", restCalls)
	}
}

// TestStreamCandles_MalformedMessagesAreSkippedNotFatal is the
// hostile-condition checklist's own "malformed data" item applied at
// this adapter's own parsing layer (not the Normalizer's -- that is
// already covered generically, for every provider, by
// internal/ingestion/normalize_test.go; this is specifically about
// StreamCandles' own JSON unmarshal of a raw frame never crashing or
// ending the stream over a message that is not even a well-formed
// candle in the first place). Every one of these must be skipped,
// silently, exactly like a subscription ack or a ping would be, with
// the stream continuing to process a valid message that arrives
// right after.
func TestStreamCandles_MalformedMessagesAreSkippedNotFatal(t *testing.T) {
	fake := newFakeHyperliquidWS(t)
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

	p := NewProvider(clock.NewFixed(base), nil, WithWSURL(fake.wsURL()), WithDialFunc(dialTest))

	var received []ingestion.ProviderCandle
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- p.StreamCandles(ctx, "BTC", func(_ context.Context, c ingestion.ProviderCandle) error {
			received = append(received, c)
			cancel() // the one valid message that must still get through
			return nil
		})
	}()

	fake.sendRaw <- []byte(`not valid json at all`)
	fake.sendRaw <- []byte(`{"channel":"subscriptionResponse","data":{}}`)               // a real message shape, just not a candle
	fake.sendRaw <- []byte(`{"channel":"candle","data":{"t":"not-a-number","o":"100"}}`) // candle-shaped, but a field has the wrong JSON type
	fake.send <- candleJSON(base.UnixMilli(), "100", "101", "99", "100.5", "10")         // a real, valid candle -- must still arrive

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StreamCandles: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out -- a malformed message appears to have ended the stream instead of being skipped")
	}

	if len(received) != 1 {
		t.Fatalf("got %d valid candles delivered, want exactly 1 (the malformed messages before it must not have been delivered OR killed the stream)", len(received))
	}
	if received[0].Close != "100.5" {
		t.Errorf("the one delivered candle = %+v, want the valid one sent last", received[0])
	}
}

func TestStreamCandles_RESTValueWinsOnClose(t *testing.T) {
	fake := newFakeHyperliquidWS(t)
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

	restServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req candleSnapshotRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode([]wireCandle{
			candleJSON(req.Req.StartTime, "100", "103", "99", "102.7", "999"),
		})
	}))
	t.Cleanup(restServer.Close)

	p := NewProvider(clock.NewFixed(base), nil, WithBaseURL(restServer.URL), WithWSURL(fake.wsURL()), WithDialFunc(dialTest))

	var calls []ingestion.ProviderCandle
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- p.StreamCandles(ctx, "BTC", func(_ context.Context, c ingestion.ProviderCandle) error {
			calls = append(calls, c)
			if len(calls) == 3 {
				cancel()
			}
			return nil
		})
	}()

	fake.send <- candleJSON(base.UnixMilli(), "100", "101", "99", "100.5", "10")                    // minute 0 forming, WS-streamed close = 100.5
	fake.send <- candleJSON(base.Add(time.Minute).UnixMilli(), "100.8", "102", "100", "101.5", "5") // minute 1 arrives -> closes minute 0

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StreamCandles: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}

	if len(calls) != 3 {
		t.Fatalf("got %d handle calls, want 3 (minute-0 forming, minute-0 closed, minute-1 forming)", len(calls))
	}
	closed := calls[1]
	if !closed.IsFinal {
		t.Fatalf("second call should be the closed minute-0 candle, got IsFinal=false: %+v", closed)
	}
	if closed.Close != "102.7" {
		t.Errorf("closed candle close = %q, want %q (the REST value, not the last streamed value 100.5)", closed.Close, "102.7")
	}
}

func TestStreamCandles_StreamDiesRightAfterAMinuteEnds_BackfillStillClosesItCorrectly(t *testing.T) {
	// Simulates exactly the scenario the brief's own added hostile test
	// asks for: the connection dies right after a minute's own data
	// finished forming, before the next message would have proven it
	// closed. StreamCandles alone can never close that minute (nothing
	// tells it to) -- the caller's own reconnect-and-backfill
	// (HistoricalCandles) must close it, using the same "a later row
	// exists" proof, now from a REST page instead of a WS message.
	fake := newFakeHyperliquidWS(t)
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

	restServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req candleSnapshotRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		// Two full minutes available for backfill: the one the stream
		// never closed, plus a later one proving it closed.
		_ = json.NewEncoder(w).Encode([]wireCandle{
			candleJSON(base.UnixMilli(), "100", "101", "99", "100.5", "10"),
			candleJSON(base.Add(time.Minute).UnixMilli(), "100.5", "102", "100", "101.2", "7"),
		})
	}))
	t.Cleanup(restServer.Close)

	p := NewProvider(clock.NewFixed(base), nil, WithBaseURL(restServer.URL), WithWSURL(fake.wsURL()), WithDialFunc(dialTest))

	ctx, cancel := context.WithCancel(t.Context())
	var streamed []ingestion.ProviderCandle
	done := make(chan error, 1)
	go func() {
		done <- p.StreamCandles(ctx, "BTC", func(_ context.Context, c ingestion.ProviderCandle) error {
			streamed = append(streamed, c)
			return nil
		})
	}()

	fake.send <- candleJSON(base.UnixMilli(), "100", "101", "99", "100.5", "10") // minute 0 forms...
	time.Sleep(50 * time.Millisecond)                                            // ...then the connection dies, with nothing proving it closed yet
	fake.disconnect()
	cancel()
	<-done

	if len(streamed) != 1 || streamed[0].IsFinal {
		t.Fatalf("expected exactly one, still-forming streamed candle before the drop, got %+v", streamed)
	}

	// Reconnect-and-backfill, driven by the caller (the worker wiring),
	// simulated directly here against the same provider.
	backfilled, err := p.HistoricalCandles(context.Background(), "BTC", base, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("HistoricalCandles: %v", err)
	}
	if len(backfilled) != 2 {
		t.Fatalf("got %d backfilled candles, want 2", len(backfilled))
	}
	if !backfilled[0].IsFinal {
		t.Errorf("the minute the stream never closed must be closed by backfill: %+v", backfilled[0])
	}
	if backfilled[0].Close != "100.5" {
		t.Errorf("backfilled minute-0 close = %q, want %q", backfilled[0].Close, "100.5")
	}
}
