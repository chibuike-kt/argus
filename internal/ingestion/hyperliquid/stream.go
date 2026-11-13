package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coder/websocket"

	"github.com/chibuike-kt/argus/internal/ingestion"
)

// dialFunc is the seam StreamCandles/StreamQuotes dial through --
// WithDialFunc lets a test substitute a dialer pointed at a local fake
// server, never the real exchange.
type dialFunc func(ctx context.Context, url string) (*websocket.Conn, error)

func dialReal(ctx context.Context, url string) (*websocket.Conn, error) {
	conn, _, err := websocket.Dial(ctx, url, nil) //nolint:bodyclose // Dial's own doc comment: "You never need to close resp.Body yourself."
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// StreamCandles dials wsURL once, subscribes to symbol's own 1m candle
// stream, and calls handle for every forming update -- blocking per
// ingestion.Provider's own documented contract (ctx canceled: return
// nil; handle errors: return that error unchanged; an unrecoverable
// connection error: return it, reconnect/backoff is the caller's own
// decision).
//
// Closed-candle rule (owner approved, see
// brief-hyperliquid-adapter.md): Hyperliquid's own `candle` message
// carries no closed/final flag. A minute is treated as final only once
// a message with a strictly later open time arrives for the same coin
// -- never because of the clock. At that point this method fetches the
// just-finished minute over REST candleSnapshot (the exchange's own
// authoritative value for a minute that can no longer change) and
// calls handle with THOSE values and IsFinal=true, before calling
// handle again for the new (now-current) forming message. If the REST
// value differs from whatever was last streamed for that minute,
// ingestion.Service's own existing applyCandle picks it up through the
// ordinary correction path (UpsertForming with the REST values,
// immediately followed by Close) -- this method does not need its own
// comparison or correction logic, only to supply the REST value as the
// thing being closed.
//
// If the connection drops before a later-open-time message ever
// arrives, the minute that was forming when it died is simply never
// closed by this method -- that is by design, not a bug: the caller's
// own reconnect-and-backfill (IngestHistorical for the gap) closes it
// correctly afterward, using the same "a later row exists" proof,
// generalized to a batch fetch (HistoricalCandles' own doc comment).
func (p *Provider) StreamCandles(ctx context.Context, symbol string, handle func(ctx context.Context, c ingestion.ProviderCandle) error) error {
	conn, err := p.wsDial(ctx, p.wsURL)
	if err != nil {
		return fmt.Errorf("hyperliquid: dial %s: %w", p.wsURL, err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	sub := wsSubscribeMessage{
		Method: "subscribe",
		Subscription: wsSubscription{
			Type:     "candle",
			Coin:     symbol,
			Interval: "1m",
		},
	}
	payload, err := json.Marshal(sub)
	if err != nil {
		return fmt.Errorf("hyperliquid: marshal subscribe: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("hyperliquid: write subscribe: %w", err)
	}

	var trackedOpenTime time.Time
	haveTracked := false

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("hyperliquid: read: %w", err)
		}

		c, ok, err := parseCandleMessage(data)
		if err != nil {
			// Malformed message from the exchange side: logged by the
			// caller's own Normalizer path if it ever reaches that far:
			// this method itself does not drop it silently, it is simply
			// not a candle message (e.g. a subscription ack) and is
			// skipped.
			continue
		}
		if !ok {
			continue
		}

		openTime := time.UnixMilli(c.T).UTC()

		if haveTracked && openTime.After(trackedOpenTime) {
			if err := p.closeTrackedMinute(ctx, symbol, trackedOpenTime, handle); err != nil {
				return err
			}
		}
		trackedOpenTime = openTime
		haveTracked = true

		if err := handle(ctx, toProviderCandle(c, symbol)); err != nil {
			return err
		}
	}
}

// closeTrackedMinute fetches openTime's own REST-authoritative candle
// and hands it to handle with IsFinal=true. A gap (REST has nothing
// for that exact minute) is logged and skipped, not invented -- the
// caller's own later backfill is what catches a real gap, not a guess
// here.
func (p *Provider) closeTrackedMinute(ctx context.Context, symbol string, openTime time.Time, handle func(ctx context.Context, c ingestion.ProviderCandle) error) error {
	final, ok, err := p.candleSnapshotOne(ctx, symbol, openTime)
	if err != nil {
		return err
	}
	if !ok {
		p.logger.Warn("hyperliquid: no REST candle for a minute the stream says closed", "symbol", symbol, "open_time", openTime)
		return nil
	}
	final.IsFinal = true
	return handle(ctx, final)
}

// parseCandleMessage extracts a wireCandle from one WebSocket text
// frame, if it is a candle message. ok is false for any other message
// type (a subscription ack, a pong, anything this method does not
// need) -- never an error, since an unrecognized message shape is
// expected traffic, not malformed input.
func parseCandleMessage(data []byte) (wireCandle, bool, error) {
	var env struct {
		Channel string     `json:"channel"`
		Data    wireCandle `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return wireCandle{}, false, fmt.Errorf("hyperliquid: unmarshal message: %w", err)
	}
	if env.Channel != "candle" {
		return wireCandle{}, false, nil
	}
	return env.Data, true, nil
}

// StreamQuotes bid/ask is not part of this brief. This never fakes a
// quote from candle data -- it simply never calls handle, matching the
// same "zero items to replay, block until ctx is done" convention
// fixture.Provider's own StreamQuotes already uses for a symbol with no
// recorded quotes (Provider's own contract: running out of new data is
// never itself a reason for a live stream to end).
func (p *Provider) StreamQuotes(ctx context.Context, _ string, _ func(ctx context.Context, q ingestion.ProviderQuote) error) error {
	<-ctx.Done()
	return nil
}
