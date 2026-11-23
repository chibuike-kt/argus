package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/tracking"
)

// The symbol supervisor (Phase 4.1a, D15.2): the worker ingests the
// tracked set -- the configured floor plus every enabled Hyperliquid
// instrument on at least one watchlist -- and starts and stops one
// pipeline per symbol as that set changes, with no restart.
//
//   - At most cap symbols run (D15.3, 8: today's adapter opens one
//     WebSocket connection per symbol and Hyperliquid allows 10 per IP).
//     The rest are marked waiting, over the cap.
//   - A new symbol's history backfill waits its turn (D15.4: at most
//     BackfillConcurrency at once), so a burst of adds cannot exhaust the
//     provider's REST budget.
//   - A symbol that leaves the set keeps running for the removal grace
//     (D15.4, 10 minutes), so a remove then an undo does not restart it.
//     Grace pipelines count against the cap: one is stopped early when a
//     wanted symbol needs its slot.
//   - A pipeline that ended on its own (Failed) is started again on the
//     next pass while its symbol is still wanted.
//   - A symbol marked needs_attention (D15.5, price precision changed) is
//     never started, and a running one is stopped at once.

// trackingStateStore is what the supervisor needs from tracking.Store.
type trackingStateStore interface {
	Tracked(ctx context.Context, floor []string) ([]tracking.Tracked, error)
	States(ctx context.Context, ids []int64) (map[int64]tracking.Status, error)
	SetState(ctx context.Context, instrumentID int64, state tracking.State, reason string, attempt int, at time.Time) error
}

// startPipeline runs one symbol's pipeline until ctx ends (blocking).
type startPipeline func(ctx context.Context, symbol string, hooks symbolHooks)

type pipeline struct {
	symbol string
	cancel context.CancelFunc
	done   chan struct{}
	// leftAt is when the symbol left the tracked set (zero while in it).
	leftAt time.Time
}

type supervisor struct {
	floor  []string
	cap    int
	grace  time.Duration
	store  trackingStateStore
	start  startPipeline
	gate   chan struct{}
	clk    clock.Clock
	logger *slog.Logger

	mu      sync.Mutex
	running map[int64]*pipeline
}

func newSupervisor(floor []string, capSymbols int, grace time.Duration, backfillConcurrency int, store trackingStateStore, start startPipeline, clk clock.Clock, logger *slog.Logger) *supervisor {
	if backfillConcurrency < 1 {
		backfillConcurrency = 1
	}
	return &supervisor{
		floor: floor, cap: capSymbols, grace: grace, store: store, start: start,
		gate: make(chan struct{}, backfillConcurrency), clk: clk, logger: logger,
		running: map[int64]*pipeline{},
	}
}

// run reconciles every interval until ctx ends, then stops every pipeline
// and waits for them.
func (s *supervisor) run(ctx context.Context, interval time.Duration) {
	defer s.stopAll()
	s.reconcileLogged(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcileLogged(ctx)
		}
	}
}

func (s *supervisor) reconcileLogged(ctx context.Context) {
	if err := s.reconcile(ctx); err != nil && ctx.Err() == nil {
		s.logger.Error("worker: symbol supervisor could not read the tracked set; keeping the pipelines as they are", "error", err)
	}
}

// reconcile brings the running pipelines in line with the tracked set.
// One pass: two statements and a start or stop per changed symbol.
func (s *supervisor) reconcile(ctx context.Context) error {
	tracked, err := s.store.Tracked(ctx, s.floor)
	if err != nil {
		return err
	}
	ids := make([]int64, len(tracked))
	for i, t := range tracked {
		ids[i] = t.ID
	}
	states, err := s.store.States(ctx, ids)
	if err != nil {
		return err
	}
	now := s.clk.Now()

	want := map[int64]string{}
	var order []int64 // wanted ids in cap order
	for i, t := range tracked {
		if states[t.ID].State == tracking.NeedsAttention {
			continue // D15.5: never ingested until the operator clears it
		}
		if i >= s.cap {
			s.setState(t.ID, tracking.Waiting, fmt.Sprintf("over the system cap of %d tracked symbols", s.cap), 0)
			continue
		}
		want[t.ID] = t.Symbol
		order = append(order, t.ID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. A pipeline whose start function returned (it reported Failed,
	// e.g. no instrument row) is dropped; if its symbol is still wanted,
	// step 3 starts it again.
	for id, p := range s.running {
		select {
		case <-p.done:
			delete(s.running, id)
			if _, ok := want[id]; ok {
				s.logger.Warn("worker: a symbol pipeline ended on its own; starting it again", "symbol", p.symbol)
			}
		default:
		}
	}

	// 2. Pipelines no longer wanted: back in the set (an undo within the
	// grace), into the grace, or stopped.
	for id, p := range s.running {
		if _, ok := want[id]; ok {
			p.leftAt = time.Time{}
			continue
		}
		attention := states[id].State == tracking.NeedsAttention
		if p.leftAt.IsZero() && !attention {
			p.leftAt = now
			s.logger.Info("worker: symbol left the tracked set; keeping it for the removal grace", "symbol", p.symbol, "grace", s.grace.String())
			continue
		}
		if attention || now.Sub(p.leftAt) >= s.grace {
			s.stopLocked(id, p)
			if attention {
				s.logger.Error("worker: stopped a symbol whose price precision changed (D15.5)", "symbol", p.symbol, "reason", states[id].Reason)
			} else {
				s.setState(id, tracking.Stopped, "removed from every watchlist", 0)
				s.logger.Info("worker: stopped a symbol no longer on any watchlist", "symbol", p.symbol)
			}
		}
	}

	// 3. Start the wanted symbols, in cap order. A pipeline in its
	// removal grace still holds a connection, so running never exceeds
	// the cap (Hyperliquid allows 10 connections per IP): when a wanted
	// symbol needs a slot and running is at the cap, the grace pipeline
	// that left the set first is stopped to make room.
	for _, id := range order {
		if _, ok := s.running[id]; ok {
			continue
		}
		for len(s.running) >= s.cap {
			if !s.evictOldestGraceLocked() {
				break
			}
		}
		if len(s.running) >= s.cap {
			// Cannot happen while want holds at most cap symbols; kept so
			// a mistake can never open an eleventh connection.
			s.logger.Error("worker: no free pipeline slot; not starting a tracked symbol", "symbol", want[id], "cap", s.cap)
			continue
		}
		s.startLocked(ctx, id, want[id])
	}
	return nil
}

// evictOldestGraceLocked stops the pipeline in its removal grace that left
// the tracked set first (ties: lowest id); false when none is in its grace.
func (s *supervisor) evictOldestGraceLocked() bool {
	var victim int64
	var vp *pipeline
	for id, p := range s.running {
		if p.leftAt.IsZero() {
			continue
		}
		if vp == nil || p.leftAt.Before(vp.leftAt) || (p.leftAt.Equal(vp.leftAt) && id < victim) {
			victim, vp = id, p
		}
	}
	if vp == nil {
		return false
	}
	s.stopLocked(victim, vp)
	s.setState(victim, tracking.Stopped, "removed from every watchlist (its removal grace was cut short to make room for another symbol)", 0)
	s.logger.Info("worker: stopped a symbol in its removal grace to stay within the cap", "symbol", vp.symbol, "cap", s.cap)
	return true
}

func (s *supervisor) startLocked(parent context.Context, id int64, symbol string) {
	ctx, cancel := context.WithCancel(parent)
	p := &pipeline{symbol: symbol, cancel: cancel, done: make(chan struct{})}
	s.running[id] = p
	hooks := symbolHooks{
		gate: s.gate,
		report: func(state tracking.State, reason string, attempt int) {
			if ctx.Err() != nil {
				return // stopped: the supervisor records the final state
			}
			s.setState(id, state, reason, attempt)
		},
	}
	s.logger.Info("worker: starting symbol pipeline", "symbol", symbol, "instrument_id", id)
	go func() {
		defer close(p.done)
		s.start(ctx, symbol, hooks)
	}()
}

func (s *supervisor) stopLocked(id int64, p *pipeline) {
	p.cancel()
	<-p.done
	delete(s.running, id)
}

func (s *supervisor) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range s.running {
		s.stopLocked(id, p)
	}
}

// runningSymbols is the set of symbols with a pipeline, for tests and logs.
func (s *supervisor) runningSymbols() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.running))
	for _, p := range s.running {
		out[p.symbol] = true
	}
	return out
}

func (s *supervisor) setState(id int64, state tracking.State, reason string, attempt int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.SetState(ctx, id, state, reason, attempt, s.clk.Now()); err != nil {
		s.logger.Error("worker: could not record symbol state", "instrument_id", id, "state", state, "error", err)
	}
}
