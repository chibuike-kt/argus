package hyperliquid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
)

// restLimiter is a sliding-window weight budget for POST /info calls
// (hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/rate-limits-and-user-limits,
// read directly: 1200 weight per minute per IP). candleSnapshot's own
// weight depends on how many items the response actually carries, so
// this cannot be a simple request counter: wait checks the window
// BEFORE a call (blocking if the budget is already spent), record adds
// the call's REAL weight AFTER the response is known. This can let one
// call slightly overshoot the budget if its own response turns out
// large -- Hyperliquid's docs describe no penalty structure precise
// enough to pre-compute an exact reservation, and a best-effort
// pre-check plus an accurate post-record is the honest thing to build
// against that, not a guessed formula.
type restLimiter struct {
	budget int
	window time.Duration
	clk    clock.Clock
	sleep  sleepFunc

	mu    sync.Mutex
	spent []weightEvent
}

type weightEvent struct {
	at     time.Time
	weight int
}

func newRESTLimiter(budget int, window time.Duration, clk clock.Clock, sleep sleepFunc) *restLimiter {
	return &restLimiter{budget: budget, window: window, clk: clk, sleep: sleep}
}

// wait blocks until the trailing window's own spent weight is below
// budget, or ctx is done. It sleeps through the injected sleep seam,
// never time.After directly -- a fake clock advanced by a test would
// otherwise never wake a goroutine parked in a REAL time.After, which
// is exactly the mismatch a test needs this seam to avoid (the same
// problem fixture.ScriptedProvider's own injected sleep exists to
// solve).
func (l *restLimiter) wait(ctx context.Context) error {
	for {
		now := l.clk.Now()
		l.mu.Lock()
		l.pruneLocked(now)
		total := l.totalLocked()
		var oldestAt time.Time
		if total >= l.budget && len(l.spent) > 0 {
			oldestAt = l.spent[0].at
		}
		l.mu.Unlock()

		if total < l.budget {
			return nil
		}

		wait := oldestAt.Add(l.window).Sub(now)
		if wait <= 0 {
			continue
		}
		if err := l.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (l *restLimiter) record(weight int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.spent = append(l.spent, weightEvent{at: l.clk.Now(), weight: weight})
}

func (l *restLimiter) pruneLocked(now time.Time) {
	cutoff := now.Add(-l.window)
	i := 0
	for ; i < len(l.spent); i++ {
		if l.spent[i].at.After(cutoff) {
			break
		}
	}
	l.spent = l.spent[i:]
}

func (l *restLimiter) totalLocked() int {
	total := 0
	for _, e := range l.spent {
		total += e.weight
	}
	return total
}

// rateLimitedStatus reports whether statusCode is Hyperliquid's own
// signal to back off. Its docs give no specific code for IP limits
// (confirmed in the verify-first report); 429 and 418 are the owner's
// own instruction to treat as a stop-and-wait regardless, and a 5xx is
// treated the same way since a transient server error and an
// undocumented rate-limit response are observationally identical from
// this client's own side.
func rateLimitedStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == 418 || code >= 500
}

// backoffAttempt returns the delay before retry attempt n (0-indexed),
// exponential with a cap, reusing the same base/cap philosophy
// config.LiveFeed.ReconnectBackoffBase/Cap already uses for the
// WebSocket side of this package -- standard practice, not a measured
// value, since Hyperliquid's own docs specify no Retry-After to read
// instead.
func backoffAttempt(n int, base, capDuration time.Duration) time.Duration {
	d := base
	for i := 0; i < n; i++ {
		d *= 2
		if d >= capDuration {
			return capDuration
		}
	}
	return d
}

// postInfo posts body to baseURL+"/info" and decodes a 200 response
// into out. On a rate-limited status (rateLimitedStatus), it retries
// with exponential backoff until success or ctx is done -- never in a
// tight loop. Any other non-200 status is returned as a hard error,
// not retried: a malformed request retrying itself is never going to
// start succeeding.
func (p *Provider) postInfo(ctx context.Context, weight int, body any, out any) error {
	if err := p.limiter.wait(ctx); err != nil {
		return err
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("hyperliquid: marshal /info request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/info", bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("hyperliquid: build /info request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		debugStart := time.Now()
		resp, err := p.httpClient.Do(req)
		p.logger.Warn("DEBUG httpClient.Do", "elapsed", time.Since(debugStart), "err", err)
		if err != nil {
			return fmt.Errorf("hyperliquid: POST /info: %w", err)
		}
		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			if readErr != nil {
				return fmt.Errorf("hyperliquid: read /info response: %w", readErr)
			}
			if out != nil {
				if err := json.Unmarshal(respBody, out); err != nil {
					return fmt.Errorf("hyperliquid: decode /info response: %w: %s", err, truncate(respBody, 200))
				}
			}
			itemWeight := responseItemWeight(out)
			p.limiter.record(weight + itemWeight)
			return nil
		}

		if !rateLimitedStatus(resp.StatusCode) {
			return fmt.Errorf("hyperliquid: POST /info: unexpected status %d: %s", resp.StatusCode, truncate(respBody, 200))
		}

		wait := backoffAttempt(attempt, time.Second, 30*time.Second)
		p.logger.Warn("hyperliquid: rate limited, backing off", "status", resp.StatusCode, "attempt", attempt, "wait", wait)
		if err := p.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// sleepFunc is the real-time-wait seam every backoff/pacing delay in
// this package goes through, never time.After directly -- a fake clock
// a test advances would otherwise never wake a goroutine parked in a
// real time.After (the same reasoning fixture.ScriptedProvider's own
// injected sleep already documents for this exact problem).
type sleepFunc func(ctx context.Context, d time.Duration) error

func realSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// responseItemWeight adds candleSnapshot's own per-60-items weight
// (rate-limits page, read directly: "candleSnapshot adds weight per 60
// items returned"). out is nil or not a candle slice for every other
// call this package makes, in which case this contributes nothing.
func responseItemWeight(out any) int {
	candles, ok := out.(*[]wireCandle)
	if !ok || candles == nil {
		return 0
	}
	n := len(*candles)
	return (n + 59) / 60
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// Instruments returns Hyperliquid's own perpetual metadata (POST /info
// {"type":"meta"}), for every non-delisted asset -- filtering to the
// configured symbol list is the caller's job (ingestion.Provider's own
// contract: returning an instrument here does not register or enable
// it).
func (p *Provider) Instruments(ctx context.Context) ([]ingestion.ProviderInstrument, error) {
	var resp metaResponse
	if err := p.postInfo(ctx, 20, metaRequest{Type: "meta"}, &resp); err != nil {
		return nil, fmt.Errorf("hyperliquid: instruments: %w", err)
	}

	out := make([]ingestion.ProviderInstrument, 0, len(resp.Universe))
	for _, a := range resp.Universe {
		if a.IsDelisted {
			continue
		}
		priceDecimals := maxDecimalsPerp - a.SzDecimals
		if priceDecimals < 0 {
			priceDecimals = 0
		}
		out = append(out, ingestion.ProviderInstrument{
			Symbol:             a.Name,
			AssetClass:         "crypto_perp",
			BaseAsset:          a.Name,
			QuoteAsset:         "USD",
			ContractType:       "perpetual",
			PriceDecimalPlaces: priceDecimals,
			QtyDecimalPlaces:   a.SzDecimals,
			// TickSize: Hyperliquid asserts no fixed tick (see the
			// package doc and brief-hyperliquid-adapter.md's own "price
			// scale and tick" section) -- this is the smallest
			// representable unit AT PriceDecimalPlaces, i.e. 1 at that
			// scale, the mathematical consequence of the owner-approved
			// price_scale = max(0, 6 - szDecimals) upper bound, not a
			// provider-asserted fact the way QtyStepSize below is.
			TickSize:           decimalString(priceDecimals),
			QtyStepSize:        decimalString(a.SzDecimals),
			ContractSize:       "",
			PipSize:            "",
			SupportedIntervals: []string{"1m", "5m", "15m", "1h", "4h", "1d"},
		})
	}
	return out, nil
}

// maxDecimalsPerp is Hyperliquid's own MAX_DECIMALS for perpetuals
// (tick-and-lot-size page, read directly: "MAX_DECIMALS is 6 for perps
// and 8 for spot"). This package only ever handles perps.
const maxDecimalsPerp = 6

// decimalString renders "1 at the n-th decimal place" as a plain
// decimal string, e.g. decimalString(3) == "0.001", decimalString(0)
// == "1" -- the smallest representable step at that many decimal
// places, used as QtyStepSize since Hyperliquid's own size rule
// (tick-and-lot-size: "sizes are rounded to the szDecimals of that
// asset") is a true fixed step, unlike price.
func decimalString(decimals int32) string {
	if decimals <= 0 {
		return "1"
	}
	b := make([]byte, 0, decimals+2)
	b = append(b, '0', '.')
	for i := int32(1); i < decimals; i++ {
		b = append(b, '0')
	}
	b = append(b, '1')
	return string(b)
}

// HistoricalCandles pages candleSnapshot (POST /info
// {"type":"candleSnapshot","req":{coin,interval,startTime,endTime}})
// across [start, end), advancing startTime by the last returned
// candle's own open time plus one minute each time, until a page comes
// back empty or the cursor reaches end -- never assuming a per-request
// cap (the owner's own instruction: Hyperliquid's docs do not state
// one for this endpoint).
//
// Closed-candle rule, generalized from the streaming rule
// (StreamCandles' own doc comment) to a batch fetch, never the clock:
// every candle in the full paged result EXCEPT THE LAST is marked
// IsFinal, because a later-open-time row existing in this same fetched
// set is exactly the same proof of closure the streaming rule uses --
// it only happens to arrive as an array instead of one message at a
// time. The last candle in the result is left IsFinal=false: nothing
// in this fetch proves it is closed yet. In the normal case (backfill
// before a live StreamCandles subscription starts), that candle's own
// closure is proven moments later by the stream's first message for
// this coin, which will have a strictly later open time -- exactly the
// "stream dies right after a minute ends, backfill still closes it
// correctly" scenario this package's own tests exercise.
func (p *Provider) HistoricalCandles(ctx context.Context, symbol string, start, end time.Time) ([]ingestion.ProviderCandle, error) {
	var all []wireCandle
	cursor := start
	var measuredPageSize int

	for cursor.Before(end) {
		var page []wireCandle
		req := candleSnapshotRequest{
			Type: "candleSnapshot",
			Req: candleSnapshotReq{
				Coin:      symbol,
				Interval:  "1m",
				StartTime: cursor.UnixMilli(),
				EndTime:   end.UnixMilli(),
			},
		}
		if err := p.postInfo(ctx, 20, req, &page); err != nil {
			return nil, fmt.Errorf("hyperliquid: historical candles %s [%s,%s): %w", symbol, cursor, end, err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > measuredPageSize {
			measuredPageSize = len(page)
			p.logger.Info("hyperliquid: candleSnapshot page size observed", "symbol", symbol, "items", measuredPageSize)
		}

		all = append(all, page...)
		last := page[len(page)-1]
		next := time.UnixMilli(last.T).Add(time.Minute)
		if !next.After(cursor) {
			// Defensive: a server that ever returns a page not strictly
			// advancing past cursor would otherwise loop forever. Not
			// expected from real Hyperliquid responses; cheap to guard.
			break
		}
		cursor = next
	}

	sort.Slice(all, func(i, j int) bool { return all[i].T < all[j].T })

	out := make([]ingestion.ProviderCandle, len(all))
	for i, c := range all {
		out[i] = toProviderCandle(c, symbol)
		out[i].IsFinal = i < len(all)-1
	}
	return out, nil
}

func toProviderCandle(c wireCandle, symbol string) ingestion.ProviderCandle {
	return ingestion.ProviderCandle{
		Symbol:   symbol,
		OpenTime: time.UnixMilli(c.T).UTC(),
		Open:     c.O,
		High:     c.H,
		Low:      c.L,
		Close:    c.Cl,
		Volume:   c.V,
	}
}

// candleSnapshotOne fetches exactly the one minute at openTime for
// symbol via candleSnapshot -- used by StreamCandles to fetch the
// REST-authoritative values for a minute the stream has just proven
// closed (a later open time arrived). Returns (candle, true, nil) when
// found, (zero, false, nil) when Hyperliquid has nothing for that exact
// minute (a gap this package does not attempt to invent a value for --
// the caller's own gap detection, via a later IngestHistorical backfill,
// is what catches this, not a guess here).
func (p *Provider) candleSnapshotOne(ctx context.Context, symbol string, openTime time.Time) (ingestion.ProviderCandle, bool, error) {
	var page []wireCandle
	req := candleSnapshotRequest{
		Type: "candleSnapshot",
		Req: candleSnapshotReq{
			Coin:      symbol,
			Interval:  "1m",
			StartTime: openTime.UnixMilli(),
			EndTime:   openTime.Add(time.Minute).UnixMilli(),
		},
	}
	if err := p.postInfo(ctx, 20, req, &page); err != nil {
		return ingestion.ProviderCandle{}, false, fmt.Errorf("hyperliquid: candle snapshot %s open_time=%s: %w", symbol, openTime, err)
	}
	for _, c := range page {
		if time.UnixMilli(c.T).UTC().Equal(openTime.UTC()) {
			return toProviderCandle(c, symbol), true, nil
		}
	}
	return ingestion.ProviderCandle{}, false, nil
}
