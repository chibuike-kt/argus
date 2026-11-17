package hyperliquid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coder/websocket"

	"github.com/chibuike-kt/argus/internal/ingestion"
)

// ErrStreamStalled is what StreamCandles returns (wrapped) when the
// connection stays open but delivers nothing -- not even a pong to its
// own heartbeat -- for the stall timeout (WithHeartbeat).
var ErrStreamStalled = errors.New("hyperliquid: stream stalled")

// pingPayload is Hyperliquid's documented heartbeat request.
var pingPayload = []byte(`{"method":"ping"}`)

// pingLoop sends pingPayload every interval until ctx ends or a write
// fails (the read loop then sees the dead connection on its own).
func pingLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := conn.Write(ctx, websocket.MessageText, pingPayload); err != nil {
				return
			}
		}
	}
}

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

	// Planned reconnect (owner's own precaution -- Hyperliquid
	// documents no connection-lifetime limit, unlike Binance's own
	// documented 24h): ageCtx is a CHILD of ctx with its own timeout,
	// so when it expires on its own (never because ctx itself also
	// expired), conn.Read below returns an error that is this
	// connection's own age limit, not a real failure -- distinguished
	// below by checking which of the two contexts is actually done.
	ageCtx, cancelAge := context.WithTimeout(ctx, p.maxConnectionAge)
	defer cancelAge()

	// szDecimals for this symbol's own price-format check below
	// (validatePriceFormat) -- strictly opt-in, via WithSzDecimals, a
	// plain map lookup with no REST call of its own: StreamCandles
	// must never make an extra network call a caller did not ask for,
	// just to run a check that exists to report on compliance, not to
	// gate anything. A symbol with no entry simply skips validation.
	szDecimals, haveSzDecimals := p.szDecimalsKnown[symbol]

	var trackedOpenTime time.Time
	haveTracked := false

	if p.pingInterval > 0 {
		pingCtx, stopPing := context.WithCancel(ageCtx)
		defer stopPing()
		go pingLoop(pingCtx, conn, p.pingInterval)
	}

	for {
		// Stall watchdog: each read gets its own deadline, so ANY
		// message (a candle, a pong, an ack) resets it. A read that
		// times out on this deadline alone -- not because ctx or the
		// planned-reconnect age expired -- means the socket is open
		// but silent: dead in practice (e.g. the network vanished while
		// the machine slept, with no close ever arriving).
		readCtx, cancelRead := ageCtx, context.CancelFunc(func() {})
		if p.stallTimeout > 0 {
			readCtx, cancelRead = context.WithTimeout(ageCtx, p.stallTimeout)
		}
		_, data, err := conn.Read(readCtx)
		stalled := err != nil && ageCtx.Err() == nil && errors.Is(readCtx.Err(), context.DeadlineExceeded)
		cancelRead()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if ageCtx.Err() != nil {
				p.logger.Info("hyperliquid: planned reconnect: max connection age reached", "symbol", symbol, "max_connection_age", p.maxConnectionAge)
				return nil
			}
			if stalled {
				p.logger.Warn("hyperliquid: stream stalled -- open but silent, closing it to reconnect",
					"symbol", symbol, "stall_timeout", p.stallTimeout)
				return fmt.Errorf("%w: no message for %s", ErrStreamStalled, p.stallTimeout)
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

		if haveSzDecimals {
			p.checkPriceFormat(symbol, c, szDecimals)
		}

		openTime := time.UnixMilli(c.T).UTC()

		if haveTracked {
			switch {
			case openTime.After(trackedOpenTime):
				if err := p.closeTrackedMinute(ctx, symbol, trackedOpenTime, handle); err != nil {
					return err
				}
				trackedOpenTime = openTime
			case openTime.Before(trackedOpenTime):
				// Stale: an older open_time than the minute already
				// tracked, arrived out of order. trackedOpenTime must
				// never move backwards -- it is this method's own
				// closed-candle proof ("a later open time arrived"),
				// and letting a stale message reset it to an earlier
				// value would let an EVEN LATER, correctly-ordered
				// message for the real current minute wrongly look
				// like it is "closing" a minute that is actually still
				// in the future relative to what this stale message
				// claims. Counted (p.StaleMessageCount) and dropped,
				// never forwarded to handle: ingestion.Service's own
				// applyCandle has no ordering guard against an
				// out-of-order correction (its own doc comment), so
				// forwarding stale data downstream risks exactly that
				// known gap, not just this method's own tracking.
				p.staleMessages.Add(1)
				p.logger.Warn("hyperliquid: dropped a stale message (older open_time than already tracked)",
					"symbol", symbol, "message_open_time", openTime, "tracked_open_time", trackedOpenTime)
				continue
			}
			// Equal: the same minute still forming -- falls through to
			// handle below, unchanged from before.
		} else {
			trackedOpenTime = openTime
			haveTracked = true
		}

		if err := handle(ctx, toProviderCandle(c, symbol)); err != nil {
			return err
		}
	}
}

// StaleMessageCount reports how many streamed messages this Provider
// has dropped for arriving with an older open_time than the minute it
// was already tracking for their own coin -- across every
// StreamCandles call this Provider instance has ever made, not reset
// between them. Exists for the real-run report (CLAUDE.md's own "say
// what you did not test" sibling: here, say what actually happened),
// not consumed by any other code path.
func (p *Provider) StaleMessageCount() int64 {
	return p.staleMessages.Load()
}

// checkPriceFormat validates c's own O/H/L/C against Hyperliquid's
// price-precision rule (validatePriceFormat), logging and counting
// each violation -- never correcting or dropping the value, which
// still reaches handle/the Normalizer exactly as streamed.
func (p *Provider) checkPriceFormat(symbol string, c wireCandle, szDecimals int32) {
	for field, raw := range map[string]string{"open": c.O, "high": c.H, "low": c.L, "close": c.Cl} {
		if err := validatePriceFormat(raw, szDecimals); err != nil {
			p.priceFormatViolations.Add(1)
			p.logger.Warn("hyperliquid: streamed price violates Hyperliquid's own price-format rule",
				"symbol", symbol, "field", field, "open_time", time.UnixMilli(c.T).UTC(), "error", err)
		}
	}
}

// PriceFormatViolationCount reports how many streamed O/H/L/C values
// (across every StreamCandles call this Provider instance has ever
// made) violated Hyperliquid's own price-precision rule
// (validatePriceFormat's own doc comment) -- the real-run report's
// own requirement to check every streamed price against it. A
// violation is only ever counted and logged, never corrected or
// dropped: the value still reaches handle/the Normalizer unchanged.
func (p *Provider) PriceFormatViolationCount() int64 {
	return p.priceFormatViolations.Load()
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
