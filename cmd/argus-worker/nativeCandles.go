package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/ingestion/hyperliquid"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// Native higher timeframe candles in the symbol pipeline (L1.1; ADR-006
// "Higher timeframe candles", decision D11). For each tracked symbol and
// each native interval (15m, 1h, 4h):
//
//   - first run: backfill the provider's whole retention (5,000 candles),
//     after the symbol's 1 minute startup backfill, holding a backfill slot
//     per attempt only (the same slot rules and limiter as the 1 minute
//     backfill: at most BackfillConcurrency at once);
//   - keeping current: when the 1 minute stream closes the last minute of
//     an interval window, fetch that one candle by REST (retry with backoff)
//     and store it once its close time has passed, then compare it with the
//     aggregate of its 1 minute candles when all of them are stored (log and
//     count a mismatch, never overwrite). No new WebSocket subscription;
//   - every NativeRereadInterval: re-read the two most recent closed
//     candles and apply the same revision rule as the 1 minute series;
//   - the integrity scan steps by the interval over the whole stored series;
//     anything older than retention is recorded in candle_gaps with its
//     interval.
//
// Only closed native candles are stored. The watchlist row keeps
// reflecting the 1 minute pipeline: a native failure is logged, never
// shown as the symbol's state.

// nativeState is what the native path reads from and records in the
// database.
type nativeState interface {
	newestClosedAt(ctx context.Context, interval marketdata.Interval, symbol string) (instrumentID int64, newestOpen *time.Time, err error)
	missingRangesAt(ctx context.Context, interval marketdata.Interval, instrumentID int64, from, to, lostBefore time.Time) ([]marketdata.MissingRange, error)
	recordLostGapAt(ctx context.Context, interval marketdata.Interval, instrumentID int64, start, end time.Time) error
}

var _ nativeState = storeBackfillState{}

func (s storeBackfillState) newestClosedAt(ctx context.Context, interval marketdata.Interval, symbol string) (int64, *time.Time, error) {
	inst, err := s.instruments.GetByProviderAndSymbol(ctx, hyperliquid.ProviderName, symbol)
	if err != nil {
		return 0, nil, fmt.Errorf("resolve instrument %s/%s: %w", hyperliquid.ProviderName, symbol, err)
	}
	latest, err := s.candles.GetLatestClosedAt(ctx, interval, inst.ID)
	if errors.Is(err, marketdata.ErrCandleNotFound) {
		return inst.ID, nil, nil
	}
	if err != nil {
		return inst.ID, nil, err
	}
	t := latest.OpenTime.Time()
	return inst.ID, &t, nil
}

func (s storeBackfillState) missingRangesAt(ctx context.Context, interval marketdata.Interval, instrumentID int64, from, to, lostBefore time.Time) ([]marketdata.MissingRange, error) {
	return s.candles.MissingRangesAt(ctx, interval, instrumentID, from, to, lostBefore)
}

func (s storeBackfillState) recordLostGapAt(ctx context.Context, interval marketdata.Interval, instrumentID int64, start, end time.Time) error {
	_, err := s.candles.RecordGapAt(ctx, interval, instrumentID, clock.NewEventTime(start), clock.NewEventTime(end))
	return err
}

// nativeRetentionStart is the oldest window start of interval the provider
// still serves at now: hyperliquidRetentionCandles (5,000) candles back from
// the window containing now, plus one interval of margin (the edge moves
// forward one interval per interval, as retentionSafetyMargin explains for
// the 1 minute series; one interval is that margin at this resolution).
func nativeRetentionStart(now time.Time, interval marketdata.Interval) time.Time {
	d, _ := interval.Duration()
	return interval.WindowStart(now).Add(-time.Duration(hyperliquidRetentionCandles) * d).Add(d)
}

// nativeBackfillStart is where a native backfill of interval starts: the
// whole retention when nothing is stored yet (L1.1 decision 4), otherwise
// the newest stored closed candle, re-read once (the revision rule applies
// to it like any other), never before the retention edge.
func nativeBackfillStart(now time.Time, newestOpen *time.Time, interval marketdata.Interval) time.Time {
	edge := nativeRetentionStart(now, interval)
	if newestOpen == nil || newestOpen.Before(edge) {
		return edge
	}
	return *newestOpen
}

// backfillNativeUntilCaughtUp backfills every native interval of symbol in
// turn, each retried with the same backoff and slot rules as the 1 minute
// startup backfill (opt: a slot per attempt, never across the delay). It
// returns false when ctx ends.
func backfillNativeUntilCaughtUp(ctx context.Context, symbol string, svc *ingestion.Service, state nativeState, cfg config.Config, logger *slog.Logger, clk clock.Clock, opt backfillOptions) bool {
	for _, interval := range marketdata.NativeIntervals() {
		for attempt := 0; ; attempt++ {
			if ctx.Err() != nil {
				return false
			}
			if !opt.acquire(ctx) {
				return false
			}
			res, err := backfillNativeAttempt(ctx, symbol, interval, svc, state, clk)
			opt.release()
			if err == nil {
				logger.Info("hyperliquid: native backfill complete", "symbol", symbol, "interval", interval,
					"fetched", res.Fetched, "inserted", res.Inserted, "unchanged", res.Unchanged, "corrected", res.Corrected,
					"forming_skipped", res.Forming, "rejected", res.Rejected, "volume_rounded", res.Rounded, "attempts", attempt+1)
				break
			}
			if ctx.Err() != nil {
				return false
			}
			delay := opt.retryDelay(attempt, cfg.Live)
			logger.Error("hyperliquid: native backfill failed, will retry", "symbol", symbol, "interval", interval,
				"attempt", attempt+1, "retry_in", delay.String(), "error", err)
			select {
			case <-ctx.Done():
				return false
			case <-time.After(delay):
			}
		}
	}
	return true
}

func backfillNativeAttempt(ctx context.Context, symbol string, interval marketdata.Interval, svc *ingestion.Service, state nativeState, clk clock.Clock) (ingestion.NativeResult, error) {
	now := clk.Now()
	_, newest, err := state.newestClosedAt(ctx, interval, symbol)
	if err != nil {
		return ingestion.NativeResult{}, err
	}
	return svc.IngestNative(ctx, symbol, interval, nativeBackfillStart(now, newest, interval), now, now)
}

// nativeIntegrityScan finds and repairs holes inside symbol's stored
// interval series, stepping by the interval over the whole stored series
// (the query starts at the first stored candle). A hole, or the part of
// one, older than the provider's retention is recorded in candle_gaps with
// its interval and logged at ERROR (recorded gaps are not reported again);
// the rest is fetched again through IngestNative, up to
// integrityScanAttempts times with backoff.
func nativeIntegrityScan(ctx context.Context, reason, symbol string, interval marketdata.Interval, svc *ingestion.Service, state nativeState, cfg config.Config, logger *slog.Logger, clk clock.Clock) {
	instrumentID, newest, err := state.newestClosedAt(ctx, interval, symbol)
	if err != nil || newest == nil {
		return
	}
	d, _ := interval.Duration()
	now := clk.Now()
	retentionStart := nativeRetentionStart(now, interval)
	ranges, err := state.missingRangesAt(ctx, interval, instrumentID, time.Unix(0, 0).UTC(), newest.Add(d), retentionStart)
	if err != nil {
		logger.Error("hyperliquid: native integrity scan query failed", "reason", reason, "symbol", symbol, "interval", interval, "error", err)
		return
	}
	if len(ranges) == 0 {
		return
	}
	logger.Warn("hyperliquid: native integrity scan found holes", "reason", reason, "symbol", symbol, "interval", interval, "ranges", len(ranges))
	for _, r := range ranges {
		start := r.Start
		if start.Before(retentionStart) {
			lostEnd := r.End
			if retentionStart.Before(lostEnd) {
				lostEnd = retentionStart
			}
			logger.Error("hyperliquid: native hole older than the exchange's retention -- these candles can never be backfilled",
				"symbol", symbol, "interval", interval, "instrument_id", instrumentID, "gap_start", start, "gap_end", lostEnd,
				"missing_candles", int64(lostEnd.Sub(start)/d), "retention_candles", hyperliquidRetentionCandles)
			if err := state.recordLostGapAt(ctx, interval, instrumentID, start, lostEnd); err != nil {
				logger.Error("hyperliquid: could not record the native lost gap", "symbol", symbol, "interval", interval, "error", err)
			}
			start = lostEnd
			if !start.Before(r.End) {
				continue
			}
		}
		var fetchErr error
		for attempt := 0; attempt < integrityScanAttempts; attempt++ {
			if _, fetchErr = svc.IngestNative(ctx, symbol, interval, start, r.End, clk.Now()); fetchErr == nil || ctx.Err() != nil {
				break
			}
			delay := reconnectBackoff(attempt, cfg.Live.ReconnectBackoffBase, cfg.Live.ReconnectBackoffCap)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		if fetchErr != nil {
			logger.Error("hyperliquid: native integrity scan could not fetch a hole; the next scan retries it",
				"symbol", symbol, "interval", interval, "range_start", start, "range_end", r.End, "error", fetchErr)
		}
	}
}

// nativeFollower fetches each native candle as soon as the 1 minute stream
// shows its window closed (L1.1 decision 6). notify is called from the
// stream's own goroutine and never blocks: a full queue drops the
// notification (logged), and the periodic re-read covers that window.
type nativeFollower struct {
	symbol string
	svc    *ingestion.Service
	cfg    config.Config
	logger *slog.Logger
	clk    clock.Clock
	closed chan time.Time
}

// nativeFollowerQueue bounds the minutes waiting for the follower.
// Implementation bound: one notification per minute, so 64 is about an
// hour of fetches stuck behind a slow one.
const nativeFollowerQueue = 64

func newNativeFollower(symbol string, svc *ingestion.Service, cfg config.Config, logger *slog.Logger, clk clock.Clock) *nativeFollower {
	return &nativeFollower{symbol: symbol, svc: svc, cfg: cfg, logger: logger, clk: clk, closed: make(chan time.Time, nativeFollowerQueue)}
}

// notify records that the 1 minute candle at minuteOpen has closed.
func (f *nativeFollower) notify(minuteOpen time.Time) {
	select {
	case f.closed <- minuteOpen:
	default:
		f.logger.Warn("hyperliquid: native follower queue full, dropped a closed minute (the periodic re-read covers it)",
			"symbol", f.symbol, "minute_open", minuteOpen)
	}
}

// run handles closed minutes until ctx ends.
func (f *nativeFollower) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-f.closed:
			f.minuteClosed(ctx, m)
		}
	}
}

// closedWindows lists the native windows whose last minute is the one at
// minuteOpen: for each interval, the window ending at minuteOpen + 1m when
// that is an interval boundary.
func closedWindows(minuteOpen time.Time) []struct {
	interval marketdata.Interval
	open     time.Time
} {
	end := minuteOpen.Add(time.Minute)
	var out []struct {
		interval marketdata.Interval
		open     time.Time
	}
	for _, iv := range marketdata.NativeIntervals() {
		if iv.Aligned(end) {
			d, _ := iv.Duration()
			out = append(out, struct {
				interval marketdata.Interval
				open     time.Time
			}{iv, end.Add(-d)})
		}
	}
	return out
}

func (f *nativeFollower) minuteClosed(ctx context.Context, minuteOpen time.Time) {
	for _, w := range closedWindows(minuteOpen) {
		f.fetchClosed(ctx, w.interval, w.open)
	}
}

// fetchClosed fetches the native candle of interval opening at open, with
// retry and backoff (integrityScanAttempts at most; the periodic re-read
// and the integrity scan cover a window this gives up on), stores it once
// its close time has passed, and compares it with its 1 minute aggregate.
func (f *nativeFollower) fetchClosed(ctx context.Context, interval marketdata.Interval, open time.Time) {
	d, _ := interval.Duration()
	var (
		res ingestion.NativeResult
		err error
	)
	for attempt := 0; attempt < integrityScanAttempts; attempt++ {
		res, err = f.svc.IngestNative(ctx, f.symbol, interval, open, open.Add(d), f.clk.Now())
		if err == nil || ctx.Err() != nil {
			break
		}
		delay := reconnectBackoff(attempt, f.cfg.Live.ReconnectBackoffBase, f.cfg.Live.ReconnectBackoffCap)
		f.logger.Warn("hyperliquid: native candle fetch failed, will retry", "symbol", f.symbol, "interval", interval,
			"open_time", open, "attempt", attempt+1, "retry_in", delay.String(), "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
	if err != nil {
		if ctx.Err() == nil {
			f.logger.Error("hyperliquid: native candle fetch gave up; the periodic re-read and the integrity scan cover it",
				"symbol", f.symbol, "interval", interval, "open_time", open, "error", err)
		}
		return
	}
	if res.Inserted+res.Unchanged+res.Corrected == 0 {
		return
	}
	facts, err := f.svc.InstrumentID(ctx, f.symbol)
	if err != nil {
		return
	}
	if _, err := f.svc.CompareNativeWithAggregate(ctx, f.symbol, interval, facts, open); err != nil && ctx.Err() == nil {
		f.logger.Warn("hyperliquid: native versus aggregate comparison failed", "symbol", f.symbol, "interval", interval, "open_time", open, "error", err)
	}
}

// rereadNative re-reads the two most recent closed candles of every native
// interval of symbol (and skips the forming one), so a provider correction
// to a just-closed candle reaches the store through the revision rule. When
// the newest stored candle is older than that (a long outage while the
// pipeline kept running: the machine asleep, the network down), it reads
// from the newest stored candle instead, so the series catches up in one
// pass rather than leaving a hole for the integrity scan to find later.
func rereadNative(ctx context.Context, symbol string, svc *ingestion.Service, state nativeState, logger *slog.Logger, clk clock.Clock) {
	now := clk.Now()
	for _, iv := range marketdata.NativeIntervals() {
		d, _ := iv.Duration()
		start := iv.WindowStart(now).Add(-2 * d)
		if _, newest, err := state.newestClosedAt(ctx, iv, symbol); err == nil && newest != nil && newest.Before(start) {
			start = nativeBackfillStart(now, newest, iv)
		}
		if _, err := svc.IngestNative(ctx, symbol, iv, start, now, now); err != nil && ctx.Err() == nil {
			logger.Warn("hyperliquid: native re-read failed, will retry next interval", "symbol", symbol, "interval", iv, "error", err)
		}
	}
}

// runPeriodicNativeReread runs rereadNative every interval until ctx ends.
// interval <= 0 disables it.
func runPeriodicNativeReread(ctx context.Context, symbol string, svc *ingestion.Service, state nativeState, interval time.Duration, logger *slog.Logger, clk clock.Clock) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rereadNative(ctx, symbol, svc, state, logger, clk)
		}
	}
}

// startNative runs the native part of symbol's pipeline in the background
// (runNative) until ctx ends.
func startNative(ctx context.Context, symbol string, svc *ingestion.Service, state nativeState, cfg config.Config, logger *slog.Logger, clk clock.Clock, opt backfillOptions, follower *nativeFollower) {
	go runNative(ctx, symbol, svc, state, cfg, logger, clk, opt, follower)
}

// runNative is the native part of symbol's pipeline: the first-run
// backfill (slot per attempt), one integrity scan per interval, then the
// follower and the periodic re-read, until ctx ends.
//
// The follower starts only once the backfill has finished. The backfill
// decides "first run" from whether anything is stored; a candle the
// follower stored first (a window that closed during the backfill) would
// make it start from that candle and skip the provider's whole history.
// Windows that close meanwhile wait in the follower's queue, and the
// periodic re-read catches up from the newest stored candle in any case.
func runNative(ctx context.Context, symbol string, svc *ingestion.Service, state nativeState, cfg config.Config, logger *slog.Logger, clk clock.Clock, opt backfillOptions, follower *nativeFollower) {
	if !backfillNativeUntilCaughtUp(ctx, symbol, svc, state, cfg, logger, clk, opt) {
		return
	}
	for _, iv := range marketdata.NativeIntervals() {
		nativeIntegrityScan(ctx, "startup", symbol, iv, svc, state, cfg, logger, clk)
	}
	go follower.run(ctx)
	runPeriodicNativeReread(ctx, symbol, svc, state, cfg.Hyperliquid.NativeRereadInterval, logger, clk)
}
