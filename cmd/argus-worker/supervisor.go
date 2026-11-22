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
	for i, t := range tracked {
		if states[t.ID].State == tracking.NeedsAttention {
			continue // D15.5: never ingested until the operator clears it
		}
		if i >= s.cap {
			s.setState(t.ID, tracking.Waiting, fmt.Sprintf("over the system cap of %d tracked symbols", s.cap), 0)
			continue
		}
		want[t.ID] = t.Symbol
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for id, symbol := range want {
		if p, ok := s.running[id]; ok {
			p.leftAt = time.Time{} // back in the set (an undo within the grace)
			continue
		}
		s.startLocked(ctx, id, symbol)
	}
	for id, p := range s.running {
		if _, ok := want[id]; ok {
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
	return nil
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
