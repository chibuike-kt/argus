package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/ingestion"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// integrityState is what the integrity scan needs from the database on top
// of backfillState.
type integrityState interface {
	backfillState
	missingRanges(ctx context.Context, instrumentID int64, from, to, lostBefore time.Time) ([]marketdata.MissingRange, error)
	closedCount(ctx context.Context, instrumentID int64, from, to time.Time) (int64, error)
}

func (s storeBackfillState) missingRanges(ctx context.Context, instrumentID int64, from, to, lostBefore time.Time) ([]marketdata.MissingRange, error) {
	return s.candles.MissingRanges(ctx, instrumentID, from, to, lostBefore)
}

func (s storeBackfillState) closedCount(ctx context.Context, instrumentID int64, from, to time.Time) (int64, error) {
	return s.candles.CountClosed(ctx, instrumentID, from, to)
}

// integrityScanAttempts bounds one scan's retries of a failing REST fetch
// (with the reconnect backoff between attempts); after that the hole waits
// for the next scan. Implementation bound, not a business value.
const integrityScanAttempts = 5

// integrityScan finds and repairs holes INSIDE the stored series: the
// backfill only starts from the newest stored candle, so a hole left
// earlier (a stream that dropped while the machine slept, say) would never
// be revisited otherwise.
//
// Window: the last BackfillDays, through the newest stored closed candle.
// Each missing range is fetched again through the ordinary IngestHistorical
// path; any part older than Hyperliquid's retention is recorded in
// candle_gaps and logged at ERROR instead -- never silently skipped, and
// (recorded gaps are excluded from the scan) never retried forever.
//
// Nothing missing means nothing is fetched or written. Safe alongside the
// live stream: every write goes through applyCandle, which never lets a
// fetched forming value overwrite a closed candle.
func integrityScan(ctx context.Context, reason, symbol string, svc *ingestion.Service, state integrityState, cfg config.Config, logger *slog.Logger, clk clock.Clock) {
	instrumentID, newest, err := state.newestClosed(ctx, symbol)
	if err != nil || newest == nil {
		return // nothing stored yet (the backfill handles that), or logged elsewhere
	}
	now := clk.Now()
	from := now.Truncate(time.Minute).Add(-time.Duration(cfg.Hyperliquid.BackfillDays) * 24 * time.Hour)
	to := newest.Add(time.Minute) // through the newest closed candle
	if !from.Before(to) {
		return
	}
	// The same retention edge the backfill uses (hyperliquidBackfill.go).
	retentionStart := now.Truncate(time.Minute).Add(-time.Duration(hyperliquidRetentionCandles) * time.Minute).Add(retentionSafetyMargin)
	ranges, err := state.missingRanges(ctx, instrumentID, from, to, retentionStart)
	if err != nil {
		logger.Error("hyperliquid: integrity scan query failed", "reason", reason, "symbol", symbol, "error", err)
		return
	}
	if len(ranges) == 0 {
		logger.Debug("hyperliquid: integrity scan clean", "reason", reason, "symbol", symbol, "from", from, "to", to)
		return
	}
	var missing int64
	desc := make([]string, len(ranges))
	for i, r := range ranges {
		missing += r.Slots
		desc[i] = fmt.Sprintf("%s..%s (%dm)", r.Start.Format(time.RFC3339), r.End.Format(time.RFC3339), r.Slots)
	}
	logger.Warn("hyperliquid: integrity scan found holes in the stored candles",
		"reason", reason, "symbol", symbol, "ranges", desc, "missing_minutes", missing)

	var healed, lost int64
	for _, r := range ranges {
		start := r.Start
		if start.Before(retentionStart) {
			lostEnd := r.End
			if retentionStart.Before(lostEnd) {
				lostEnd = retentionStart
			}
			lost += int64(lostEnd.Sub(start) / time.Minute)
			logger.Error("hyperliquid: hole older than the exchange's retention -- these minutes can never be backfilled",
				"symbol", symbol, "instrument_id", instrumentID, "gap_start", start, "gap_end", lostEnd,
				"missing_minutes", int(lostEnd.Sub(start)/time.Minute), "retention_candles", hyperliquidRetentionCandles)
			if err := state.recordLostGap(ctx, instrumentID, start, lostEnd); err != nil {
				logger.Error("hyperliquid: could not record the lost gap", "symbol", symbol, "error", err)
			}
			start = lostEnd
			if !start.Before(r.End) {
				continue
			}
		}
		// Fetch one minute past the hole: the newest candle of a fetch is
		// marked not-final, so this way that is the already-stored closed
		// minute after the hole (dropped by applyCandle), not the hole's own
		// last minute (which would be left forming).
		var fetchErr error
		for attempt := 0; attempt < integrityScanAttempts; attempt++ {
			if fetchErr = svc.IngestHistorical(ctx, symbol, start, r.End.Add(time.Minute)); fetchErr == nil || ctx.Err() != nil {
				break
			}
			delay := reconnectBackoff(attempt, cfg.Live.ReconnectBackoffBase, cfg.Live.ReconnectBackoffCap)
			logger.Warn("hyperliquid: integrity scan fetch failed, will retry", "symbol", symbol,
				"range_start", start, "range_end", r.End, "attempt", attempt+1, "retry_in", delay.String(), "error", fetchErr)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		if fetchErr != nil {
			logger.Error("hyperliquid: integrity scan could not fetch a hole; the next scan retries it",
				"symbol", symbol, "range_start", start, "range_end", r.End, "error", fetchErr)
			continue
		}
		n, err := state.closedCount(ctx, instrumentID, start, r.End)
		if err == nil {
			healed += n
		}
	}

	remaining, err := state.missingRanges(ctx, instrumentID, from, to, retentionStart)
	var still int64
	if err == nil {
		for _, r := range remaining {
			still += r.Slots
		}
	}
	logger.Info("hyperliquid: integrity scan repaired holes",
		"reason", reason, "symbol", symbol, "missing_minutes", missing, "healed_minutes", healed,
		"lost_beyond_retention", lost, "still_missing_unrecorded", still)
}

// runPeriodicIntegrityScan runs integrityScan every interval while ctx is
// alive (canceled the instant the stream ends, like the periodic backfill).
// interval <= 0 disables it.
func runPeriodicIntegrityScan(ctx context.Context, symbol string, svc *ingestion.Service, state integrityState, cfg config.Config, logger *slog.Logger, clk clock.Clock) {
	interval := cfg.Hyperliquid.IntegrityScanInterval
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
			integrityScan(ctx, "periodic", symbol, svc, state, cfg, logger, clk)
			if ns, ok := state.(nativeState); ok && svc.SupportsNative() {
				for _, iv := range marketdata.NativeIntervals() {
					nativeIntegrityScan(ctx, "periodic", symbol, iv, svc, ns, cfg, logger, clk)
				}
			}
		}
	}
}
