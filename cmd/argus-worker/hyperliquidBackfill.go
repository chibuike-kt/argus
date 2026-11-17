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
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
)

// hyperliquidRetentionCandles is Hyperliquid's own documented limit on
// candleSnapshot history (info-endpoint docs, "Candle snapshot", read
// 16 November 2026): "Only the most recent 5000 candles are
// available". A fact about the exchange, not an ARGUS policy value.
const hyperliquidRetentionCandles = 5000

// retentionSafetyMargin keeps a backfill request this far inside the
// retention edge: the edge moves forward one minute per minute while a
// long request pages, and asking for a minute that has just aged out
// would look like a gap that is really the exchange's own limit.
// Implementation margin, not a business value.
const retentionSafetyMargin = 10 * time.Minute

// backfillOverlap is how far before the newest stored closed candle a
// backfill starts. [U] 5 minutes: re-reads the last few stored minutes
// so one that was stored from the live stream (and could differ from
// the exchange's final value) is re-checked through the ordinary
// idempotent correction path. Re-reading an unchanged minute writes
// nothing.
const backfillOverlap = 5 * time.Minute

// backfillState is what the backfill needs from the database: the
// newest closed candle already stored for a symbol (never process
// memory, so a restart or a reconnect after hours starts from what is
// really stored) and a place to record a hole the exchange can no
// longer fill.
type backfillState interface {
	newestClosed(ctx context.Context, symbol string) (instrumentID int64, newest *time.Time, err error)
	recordLostGap(ctx context.Context, instrumentID int64, start, end time.Time) error
}

// storeBackfillState is backfillState over the real stores.
type storeBackfillState struct {
	instruments *instruments.Store
	candles     *marketdata.Store
}

func (s storeBackfillState) newestClosed(ctx context.Context, symbol string) (int64, *time.Time, error) {
	inst, err := s.instruments.GetByProviderAndSymbol(ctx, hyperliquid.ProviderName, symbol)
	if err != nil {
		return 0, nil, fmt.Errorf("resolve instrument %s/%s: %w", hyperliquid.ProviderName, symbol, err)
	}
	latest, err := s.candles.GetLatestClosed(ctx, inst.ID)
	if errors.Is(err, marketdata.ErrCandleNotFound) {
		return inst.ID, nil, nil
	}
	if err != nil {
		return inst.ID, nil, err
	}
	t := latest.OpenTime.Time()
	return inst.ID, &t, nil
}

func (s storeBackfillState) recordLostGap(ctx context.Context, instrumentID int64, start, end time.Time) error {
	_, err := s.candles.RecordGap(ctx, instrumentID, clock.NewEventTime(start), clock.NewEventTime(end))
	return err
}

// backfillWindow is the pure window rule. now is wall-clock time;
// newest is the newest stored closed candle's open time (nil when
// nothing is stored yet). It returns where to start fetching, and --
// when the minutes right after newest are already older than the
// exchange keeps -- the lost range [lostStart, lostEnd) that no
// backfill can ever fill.
//
//   - nothing stored: start backfillDays before now (the first-run
//     history depth), clamped to retention;
//   - something stored: start backfillOverlap before newest, clamped to
//     retention; anything between newest+1m and the retention edge is
//     lost.
func backfillWindow(now time.Time, newest *time.Time, backfillDays int) (start time.Time, lost *[2]time.Time) {
	retentionStart := now.Truncate(time.Minute).
		Add(-time.Duration(hyperliquidRetentionCandles) * time.Minute).
		Add(retentionSafetyMargin)

	if newest == nil {
		start = now.Add(-time.Duration(backfillDays) * 24 * time.Hour)
		if start.Before(retentionStart) {
			start = retentionStart
		}
		return start, nil
	}

	start = newest.Add(-backfillOverlap)
	if !start.Before(retentionStart) {
		return start, nil
	}
	wantFrom := newest.Add(time.Minute)
	if wantFrom.Before(retentionStart) {
		lost = &[2]time.Time{wantFrom, retentionStart}
	}
	return retentionStart, lost
}

// backfillUntilCaughtUp closes the hole between the newest stored
// closed candle and now, retrying with the reconnect backoff until a
// fetch succeeds (or ctx ends). The window is recomputed from the
// database on every attempt, so a failed attempt never loses its
// window the way the old in-memory `since` did. Anything already older
// than the exchange's retention is logged at ERROR and recorded in
// candle_gaps -- reported, never silently skipped. The live stream is
// only (re)started after this returns, so a stream is never treated as
// healthy while a hole is still open.
//
// It returns false when the symbol can never be ingested (no instrument
// row for it -- not a transient failure, so retrying forever would only
// spam the log) and when ctx ends; the caller then stops that symbol.
func backfillUntilCaughtUp(ctx context.Context, reason, symbol string, svc *ingestion.Service, state backfillState, cfg config.Config, logger *slog.Logger, clk clock.Clock) bool {
	var lostRecordedTo time.Time
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		now := clk.Now()
		instrumentID, newest, err := state.newestClosed(ctx, symbol)
		if errors.Is(err, instruments.ErrNotFound) {
			logger.Error("hyperliquid: symbol has no instrument row -- not ingesting it; register the instrument first",
				"symbol", symbol, "error", err)
			return false
		}
		if err == nil {
			start, lost := backfillWindow(now, newest, cfg.Hyperliquid.BackfillDays)
			if lost != nil && lost[1].After(lostRecordedTo) {
				from := lost[0]
				if from.Before(lostRecordedTo) {
					from = lostRecordedTo
				}
				logger.Error("hyperliquid: gap older than the exchange's retention -- these minutes can never be backfilled",
					"symbol", symbol, "instrument_id", instrumentID, "gap_start", from, "gap_end", lost[1],
					"missing_minutes", int(lost[1].Sub(from)/time.Minute), "retention_candles", hyperliquidRetentionCandles)
				if recErr := state.recordLostGap(ctx, instrumentID, from, lost[1]); recErr != nil {
					logger.Error("hyperliquid: could not record the lost gap", "symbol", symbol, "error", recErr)
				} else {
					lostRecordedTo = lost[1]
				}
			}
			err = svc.IngestHistorical(ctx, symbol, start, now)
			if err == nil {
				if attempt > 0 || newest == nil || now.Sub(*newest) > 2*time.Minute {
					logger.Info("hyperliquid: backfill complete", "reason", reason, "symbol", symbol,
						"from", start, "to", now, "attempts", attempt+1)
				}
				return true
			}
		}
		if ctx.Err() != nil {
			return false
		}
		delay := reconnectBackoff(attempt, cfg.Live.ReconnectBackoffBase, cfg.Live.ReconnectBackoffCap)
		logger.Error("hyperliquid: backfill failed, will retry -- stream not restarted until it succeeds",
			"reason", reason, "symbol", symbol, "attempt", attempt+1, "retry_in", delay.String(), "error", err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(delay):
		}
	}
}
