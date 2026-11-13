package fixture

import (
	"context"
	"errors"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/ingestion"
)

// ErrScriptedDisconnect is what StreamCandles returns when a Script's
// own ScriptedEvent.Disconnect fires -- simulating the provider itself
// having an unrecoverable error (ingestion.Provider's own documented
// contract for StreamCandles), never wrapped, so a caller's errors.Is
// against it works unchanged.
var ErrScriptedDisconnect = errors.New("fixture: scripted disconnect")

// ScriptedEvent is one step of a deterministic replay script (live-
// chart-pipeline brief, Phase 2 -- "the test harness for everything
// after it"). Exactly one of Candle or Disconnect applies:
//
//   - A forming update, a close (Candle.IsFinal true), a duplicate (two
//     events, identical Candle), a corrected closed candle (a later
//     event for the same OpenTime with different values, IsFinal true),
//     and a late-but-unchanged message after close (a later event for
//     the same OpenTime with the SAME values, IsFinal true -- distinct
//     from a correction, and already handled as a no-op by
//     ingestion.Service's own existing applyCandle, see its own doc
//     comment on candleValuesEqual) are all just Candle entries, in the
//     order the script author wrote them -- out of order is simply an
//     entry whose OpenTime is earlier than the previous one's.
//   - A gap (a minute with no event at all) needs no representation
//     here: it is the absence of an entry for that OpenTime, nothing
//     Script itself has to model.
//   - Disconnect ends the replay for this StreamCandles call with
//     ErrScriptedDisconnect; no Candle is sent for this entry.
type ScriptedEvent struct {
	Candle     ingestion.ProviderCandle
	Disconnect bool
}

// Script is one symbol's own deterministic event sequence.
type Script struct {
	Symbol string
	Events []ScriptedEvent
}

// ScriptedProvider replays Scripts through the same ingestion.Provider
// interface the real fixture Provider implements, for exactly one
// purpose: a controllable, deterministic test harness for everything
// downstream of ingestion (the hub, the WebSocket, the frontend). Same
// script, same output, every time -- content and order come only from
// the script; Instruments/HistoricalCandles/StreamQuotes delegate to an
// embedded, ordinary fixture Provider (composition, not reimplementing
// the rest of the interface for a concern this type does not touch).
type ScriptedProvider struct {
	*Provider
	scripts map[string]Script
	clk     clock.Clock
	speed   float64
	sleep   func(ctx context.Context, d time.Duration)
}

// NewScriptedProvider wires a ScriptedProvider. speed governs pacing
// only, never content or order: <= 0 means no delay at all (the default
// for tests -- same script, same output, instantly); a positive value
// multiplies how fast simulated time passes relative to real time
// (speed=1 paces each event at its own real candle-to-candle interval;
// speed=60 paces the same script 60x faster). clk and sleep are both
// injected seams, never read from directly by this type's own pacing
// math (clk is available to a caller that wants to log/derive "now";
// sleep is the one call a test replaces with a no-op or a recorder to
// run an entire scenario without waiting in real time).
func NewScriptedProvider(scripts []Script, clk clock.Clock, speed float64, sleep func(ctx context.Context, d time.Duration)) *ScriptedProvider {
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		}
	}
	bySymbol := make(map[string]Script, len(scripts))
	for _, s := range scripts {
		bySymbol[s.Symbol] = s
	}
	return &ScriptedProvider{
		Provider: New(Data{}, 0),
		scripts:  bySymbol,
		clk:      clk,
		speed:    speed,
		sleep:    sleep,
	}
}

// StreamCandles replays symbol's own Script, in order, deterministically:
// pacing (derived from the gap between successive events' own OpenTime,
// divided by speed) is the only thing real time affects. A symbol with
// no script at all replays zero events, then blocks on ctx.Done() --
// matching the embedded Provider's own "no new data right now is never
// itself a reason to end" contract, not an error.
func (p *ScriptedProvider) StreamCandles(ctx context.Context, symbol string, handle func(ctx context.Context, c ingestion.ProviderCandle) error) error {
	script := p.scripts[symbol]

	var prevOpenTime time.Time
	for i, ev := range script.Events {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if i > 0 && p.speed > 0 {
			gap := ev.Candle.OpenTime.Sub(prevOpenTime)
			if gap < 0 {
				gap = -gap // an out-of-order event is still paced by magnitude, not treated as zero delay
			}
			if wait := time.Duration(float64(gap) / p.speed); wait > 0 {
				p.sleep(ctx, wait)
			}
		}

		if ev.Disconnect {
			return ErrScriptedDisconnect
		}

		if err := handle(ctx, ev.Candle); err != nil {
			return err
		}
		prevOpenTime = ev.Candle.OpenTime
	}

	<-ctx.Done()
	return nil
}

var _ ingestion.Provider = (*ScriptedProvider)(nil)
