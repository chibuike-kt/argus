package outcomes

import (
	"math/big"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
	"github.com/chibuike-kt/argus/internal/quant"
)

// base is an arbitrary epoch every test candle's OpenTime/CloseTime is an
// offset from, so candle n is simply base + n minutes -- the only thing
// that matters to Evaluate is ordering and the asOf/IsClosed bounds, never
// the absolute wall-clock value.
var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(minute int) time.Time { return base.Add(time.Duration(minute) * time.Minute) }

// candle builds one bullish-oriented, closed 1-minute candle: id equals
// minute+1 (1-based, matching a real serial primary key never being 0), so
// every candle in a test table has a distinct, recognizable id.
func candle(minute int, open, high, low, cls int64, revision int32) analysis.CandleInput {
	return analysis.CandleInput{
		ID:        int64(minute) + 1,
		OpenTime:  at(minute),
		CloseTime: at(minute + 1),
		Open:      open,
		High:      high,
		Low:       low,
		Close:     cls,
		Revision:  revision,
		IsClosed:  true,
	}
}

// forming is a candle still in progress: IsClosed is false, which Evaluate
// must skip exactly as resolvePlanStatus does (spec section 16: "never
// from a forming candle").
func forming(minute int, open, high, low, cls int64) analysis.CandleInput {
	c := candle(minute, open, high, low, cls, 1)
	c.IsClosed = false
	return c
}

// pivot is the price every bearish-mirrored test scenario reflects
// bullish prices around (mirror(v) = 2*pivot - v): entry, stop, target
// levels and every candle's OHLC all mirror the same way, so a bullish
// scenario and its bearish mirror describe geometrically identical price
// action, just upside-down.
const pivot = 10000

func mirror(v int64) int64 { return 2*pivot - v }

// mirrorCandle reflects a bullish-oriented candle into its bearish
// counterpart. High and Low swap under reflection (the bullish low, the
// candle's minimum, becomes the bearish high, its maximum) -- open and
// close do not swap with each other, since neither is constrained to be
// the extreme.
func mirrorCandle(c analysis.CandleInput) analysis.CandleInput {
	m := c
	m.Open = mirror(c.Open)
	m.Close = mirror(c.Close)
	m.High = mirror(c.Low)
	m.Low = mirror(c.High)
	return m
}

// target builds a bullish-oriented Target at level, with RR computed the
// same way analysis.computeRR does: (target-entry)/(entry-stop).
func target(level, entry, stop int64) analysis.Target {
	return analysis.Target{
		Level: quant.Price(level),
		RR:    big.NewRat(level-entry, entry-stop),
	}
}

// mirrorTarget reflects a bullish Target into its bearish counterpart.
// RR is direction-invariant (computeRR's own bearish branch divides the
// same two mirrored distances, which have the same magnitude and sign as
// the bullish ones), so it is carried over unchanged, not recomputed.
func mirrorTarget(t analysis.Target) analysis.Target {
	return analysis.Target{Level: quant.Price(mirror(int64(t.Level))), RR: t.RR}
}

// wantCandleID names an expected decisive candle by the id the candle
// helper assigned it (minute+1), so the table below can say "candle 3"
// instead of reconstructing a whole CandleRef to compare against.
type wantTarget struct {
	index int
	id    int64
}

type want struct {
	status                  Status
	fillID                  int64 // 0 means "no fill"
	firstTarget             *wantTarget
	laterTargets            []wantTarget
	stopID                  int64 // 0 means "Stop not set"
	ambiguousResolvedAsStop bool
	rAtFirstTarget          *big.Rat
}

type scenario struct {
	entry, stop int64
	targetLvls  []int64 // bullish-oriented target levels, nearest first
	tick        int64

	candles       []analysis.CandleInput // bullish-oriented, in order
	invalidatedAt *time.Time
	asOf          time.Time

	want want
}

func (s scenario) plan(direction analysis.Direction, mirrored bool) analysis.TradePlan {
	entry, stop := s.entry, s.stop
	targets := make([]analysis.Target, len(s.targetLvls))
	for i, lvl := range s.targetLvls {
		targets[i] = target(lvl, entry, stop)
	}
	if mirrored {
		entry, stop = mirror(entry), mirror(stop)
		for i := range targets {
			targets[i] = mirrorTarget(targets[i])
		}
	}
	return analysis.TradePlan{
		Status:    analysis.PlanTriggered,
		Direction: direction,
		Entry:     quant.Price(entry),
		Stop:      quant.Price(stop),
		Targets:   targets,
	}
}

func (s scenario) inputCandles(mirrored bool) []analysis.CandleInput {
	out := make([]analysis.CandleInput, len(s.candles))
	for i, c := range s.candles {
		if mirrored {
			out[i] = mirrorCandle(c)
		} else {
			out[i] = c
		}
	}
	return out
}

func run(t *testing.T, s scenario) {
	t.Helper()
	t.Run("bullish", func(t *testing.T) {
		checkScenario(t, s, analysis.DirectionBullish, false)
	})
	t.Run("bearish", func(t *testing.T) {
		checkScenario(t, s, analysis.DirectionBearish, true)
	})
}

func checkScenario(t *testing.T, s scenario, direction analysis.Direction, mirrored bool) {
	t.Helper()
	plan := s.plan(direction, mirrored)
	candles := s.inputCandles(mirrored)
	asOf := s.asOf
	if asOf.IsZero() {
		asOf = at(len(s.candles) + 1000) // far enough forward to include every candle above
	}

	got, err := Evaluate(plan, candles, s.invalidatedAt, quant.Price(s.tick), asOf)
	if err != nil {
		t.Fatalf("Evaluate: unexpected error: %v", err)
	}

	if got.Status != s.want.status {
		t.Errorf("Status = %v, want %v", got.Status, s.want.status)
	}
	if got.RulesVersion != RulesVersion {
		t.Errorf("RulesVersion = %q, want %q", got.RulesVersion, RulesVersion)
	}

	if s.want.fillID == 0 {
		if got.Fill != nil {
			t.Errorf("Fill = %+v, want nil", got.Fill)
		}
	} else {
		if got.Fill == nil || got.Fill.ID != s.want.fillID {
			t.Errorf("Fill = %+v, want candle id %d", got.Fill, s.want.fillID)
		}
	}

	if s.want.firstTarget == nil {
		if got.FirstTarget != nil {
			t.Errorf("FirstTarget = %+v, want nil", got.FirstTarget)
		}
	} else {
		if got.FirstTarget == nil {
			t.Errorf("FirstTarget = nil, want index %d candle %d", s.want.firstTarget.index, s.want.firstTarget.id)
		} else if got.FirstTarget.Index != s.want.firstTarget.index || got.FirstTarget.Candle.ID != s.want.firstTarget.id {
			t.Errorf("FirstTarget = %+v, want index %d candle %d", got.FirstTarget, s.want.firstTarget.index, s.want.firstTarget.id)
		}
	}

	if len(got.LaterTargets) != len(s.want.laterTargets) {
		t.Errorf("LaterTargets = %+v, want %+v", got.LaterTargets, s.want.laterTargets)
	} else {
		for i, lt := range got.LaterTargets {
			w := s.want.laterTargets[i]
			if lt.Index != w.index || lt.Candle.ID != w.id {
				t.Errorf("LaterTargets[%d] = %+v, want index %d candle %d", i, lt, w.index, w.id)
			}
		}
	}

	if s.want.stopID == 0 {
		if got.Stop != nil {
			t.Errorf("Stop = %+v, want nil", got.Stop)
		}
	} else {
		if got.Stop == nil || got.Stop.ID != s.want.stopID {
			t.Errorf("Stop = %+v, want candle id %d", got.Stop, s.want.stopID)
		}
	}

	if got.AmbiguousResolvedAsStop != s.want.ambiguousResolvedAsStop {
		t.Errorf("AmbiguousResolvedAsStop = %v, want %v", got.AmbiguousResolvedAsStop, s.want.ambiguousResolvedAsStop)
	}

	switch s.want.rAtFirstTarget {
	case nil:
		if got.RAtFirstTarget != nil {
			t.Errorf("RAtFirstTarget = %v, want nil", got.RAtFirstTarget)
		}
	default:
		if got.RAtFirstTarget == nil || got.RAtFirstTarget.Cmp(s.want.rAtFirstTarget) != 0 {
			t.Errorf("RAtFirstTarget = %v, want %v", got.RAtFirstTarget, s.want.rAtFirstTarget)
		}
	}
}

// Entry 10000, stop 9900 (100 away), targets at 10050 (R 1/2), 10100 (R 1),
// 10200 (R 2), tick 1 -- the shared geometry nearly every case below uses.
const (
	entry = 10000
	stop  = 9900
)

var targetLvls = []int64{10050, 10100, 10200}

func rr(n, d int64) *big.Rat { return big.NewRat(n, d) }

func TestEvaluate_Fill(t *testing.T) {
	t.Run("touch without trade through: no fill", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10010, 10015, 10000, 10008, 1), // low touches entry exactly, no tick through
			},
			want: want{status: StatusNeverFilled},
		})
	})

	t.Run("trade through by exactly one tick: fill", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10010, 10015, 9999, 10008, 1), // low == entry-tick exactly
			},
			want: want{status: StatusOpen, fillID: 1},
		})
	})

	t.Run("gap through the entry: fill at the entry price, not the open", func(t *testing.T) {
		gapping := scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10006, 9950, 9960, 1), // opens above entry, gaps straight through
			},
			want: want{status: StatusOpen, fillID: 1},
		}
		run(t, gapping)

		// A non-gapping fill at the same tick level must produce the
		// identical Status/R: the fill is priced at entry, never at
		// whatever the candle's own open happened to be.
		nonGapping := scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10001, 10006, 9999, 10000, 1), // low == entry-tick exactly, no gap
			},
			want: want{status: StatusOpen, fillID: 1},
		}
		run(t, nonGapping)
	})

	t.Run("invalidated before the fill candle: never_filled_invalidated", func(t *testing.T) {
		invalidatedAt := at(0) // closes before the fill candle below
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(1, 10010, 10015, 9999, 10008, 1), // would otherwise fill
			},
			invalidatedAt: &invalidatedAt,
			want:          want{status: StatusNeverFilledInvalidated},
		})
	})

	t.Run("never filled at all, never invalidated: never_filled", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10010, 10015, 10005, 10008, 1),
				candle(1, 10008, 10012, 10003, 10006, 1),
			},
			want: want{status: StatusNeverFilled},
		})
	})
}

func TestEvaluate_AfterFill(t *testing.T) {
	t.Run("fill candle also reaches the stop: stopped", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10006, 9890, 9895, 1), // same candle: fills, then closes through stop
			},
			want: want{status: StatusStopped, fillID: 1, stopID: 1},
		})
	})

	t.Run("fill candle also reaches a target but not the stop: target not credited, outcome continues", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10100, 9999, 10010, 1),  // fills AND reaches target 1's level, same candle
				candle(1, 10010, 10015, 10005, 10012, 1), // no further touches
			},
			// Not credited on the fill candle itself: no fill was reached
			// post-fill, so the outcome is still open, not a win.
			want: want{status: StatusOpen, fillID: 1},
		})
	})

	t.Run("later candle reaches stop only: stopped, no ambiguity", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10010, 9999, 10005, 1), // fill
				candle(1, 10000, 10005, 9890, 9895, 1),  // stop only
			},
			want: want{status: StatusStopped, fillID: 1, stopID: 2},
		})
	})

	t.Run("later candle reaches a target only: target_hit", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10010, 9999, 10005, 1),  // fill
				candle(1, 10010, 10050, 10008, 10045, 1), // target 1 only
			},
			want: want{
				status:         StatusTargetHit,
				fillID:         1,
				firstTarget:    &wantTarget{index: 0, id: 2},
				rAtFirstTarget: rr(1, 2),
			},
		})
	})

	t.Run("later candle reaches both stop and a target: ambiguous, stop taken first", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10010, 9999, 10005, 1), // fill
				candle(1, 10000, 10060, 9890, 9895, 1),  // same candle: target 1 level AND stop
			},
			want: want{
				status:                  StatusStopped,
				fillID:                  1,
				stopID:                  2,
				ambiguousResolvedAsStop: true,
			},
		})
	})

	t.Run("first target then a later target before the stop: both recorded, win stays a win", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10010, 9999, 10005, 1),  // fill
				candle(1, 10010, 10050, 10008, 10045, 1), // target 1
				candle(2, 10045, 10100, 10040, 10090, 1), // target 2
			},
			want: want{
				status:         StatusTargetHit,
				fillID:         1,
				firstTarget:    &wantTarget{index: 0, id: 2},
				laterTargets:   []wantTarget{{index: 1, id: 3}},
				rAtFirstTarget: rr(1, 2),
			},
		})
	})

	t.Run("first target then the stop: win stays a win, later targets not credited after the stop", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10010, 9999, 10005, 1),  // fill
				candle(1, 10010, 10050, 10008, 10045, 1), // target 1: win locked in
				candle(2, 10045, 10050, 9890, 9895, 1),   // stop touched afterward -- moot
			},
			want: want{
				status:         StatusTargetHit,
				fillID:         1,
				firstTarget:    &wantTarget{index: 0, id: 2},
				rAtFirstTarget: rr(1, 2),
				// Stop is not reported once the win is already locked in:
				// nothing left to resolve.
				stopID: 0,
			},
		})
	})

	t.Run("never reaches anything: open", func(t *testing.T) {
		run(t, scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10010, 9999, 10005, 1), // fill
				candle(1, 10005, 10020, 9995, 10010, 1), // drifts, touches nothing
			},
			want: want{status: StatusOpen, fillID: 1},
		})
	})
}

func TestEvaluate_PointInTime(t *testing.T) {
	t.Run("a candle still forming is ignored", func(t *testing.T) {
		s := scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10010, 9999, 10005, 1), // fill
				forming(1, 10005, 10100, 9890, 10050),   // would stop AND hit targets, but is not closed
			},
			want: want{status: StatusOpen, fillID: 1},
		}
		run(t, s)
	})

	t.Run("a candle whose close_time is after asOf is ignored", func(t *testing.T) {
		s := scenario{
			entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
			candles: []analysis.CandleInput{
				candle(0, 10005, 10010, 9999, 10005, 1), // fill, closes at minute 1
				candle(1, 10005, 10100, 9890, 10050, 1), // would stop AND hit targets, but closes after asOf
			},
			asOf: at(1), // exactly the fill candle's own close_time; candle 1 (minute 1-2) closes after this
			want: want{status: StatusOpen, fillID: 1},
		}
		run(t, s)
	})
}

func TestEvaluate_CandleRevision(t *testing.T) {
	// ADR-011: a correction changes a candle's own OHLC under the same
	// (instrument, open_time) and bumps its revision. Evaluate has no
	// storage of its own -- it is a pure recompute -- so "append only,
	// old one not overwritten" is the caller's responsibility (the
	// backtest runner, or a future paper-trading store): calling Evaluate
	// again with the corrected candle must simply produce a fresh,
	// correctly different Outcome, carrying that candle's new revision,
	// without mutating or needing to know about whatever Outcome the
	// pre-correction call produced.
	before := scenario{
		entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
		candles: []analysis.CandleInput{
			candle(0, 10005, 10010, 9999, 10005, 1), // fill, revision 1
			candle(1, 10005, 10020, 9995, 10010, 1), // drifts, touches nothing, revision 1
		},
		want: want{status: StatusOpen, fillID: 1},
	}
	run(t, before)

	// A correction to candle 1 (same open_time, revision bumped to 2)
	// that now reaches the stop.
	after := scenario{
		entry: entry, stop: stop, targetLvls: targetLvls, tick: 1,
		candles: []analysis.CandleInput{
			candle(0, 10005, 10010, 9999, 10005, 1),
			candle(1, 10005, 10020, 9890, 9895, 2), // corrected: now reaches the stop, revision 2
		},
		want: want{status: StatusStopped, fillID: 1, stopID: 2},
	}
	run(t, after)

	// The corrected Outcome's decisive candle carries the new revision,
	// not the old one.
	plan := after.plan(analysis.DirectionBullish, false)
	got, err := Evaluate(plan, after.inputCandles(false), nil, quant.Price(1), at(1000))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got.Stop == nil || got.Stop.Revision != 2 {
		t.Fatalf("Stop.Revision = %+v, want revision 2", got.Stop)
	}
}

func TestEvaluate_Determinism(t *testing.T) {
	plan := analysis.TradePlan{
		Status:    analysis.PlanTriggered,
		Direction: analysis.DirectionBullish,
		Entry:     quant.Price(entry),
		Stop:      quant.Price(stop),
		Targets: []analysis.Target{
			target(targetLvls[0], entry, stop),
			target(targetLvls[1], entry, stop),
		},
	}
	candles := []analysis.CandleInput{
		candle(0, 10005, 10010, 9999, 10005, 1),
		candle(1, 10010, 10050, 10008, 10045, 1),
		candle(2, 10045, 10100, 10040, 10090, 1),
	}
	asOf := at(1000)

	first, err := Evaluate(plan, candles, nil, quant.Price(1), asOf)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	second, err := Evaluate(plan, candles, nil, quant.Price(1), asOf)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if first.Status != second.Status ||
		first.RulesVersion != second.RulesVersion ||
		!sameCandleRef(first.Fill, second.Fill) ||
		!sameCandleRef(first.Stop, second.Stop) ||
		first.AmbiguousResolvedAsStop != second.AmbiguousResolvedAsStop ||
		len(first.LaterTargets) != len(second.LaterTargets) {
		t.Fatalf("Evaluate is not deterministic: first=%+v second=%+v", first, second)
	}
	if (first.RAtFirstTarget == nil) != (second.RAtFirstTarget == nil) {
		t.Fatalf("RAtFirstTarget determinism mismatch: first=%v second=%v", first.RAtFirstTarget, second.RAtFirstTarget)
	}
	if first.RAtFirstTarget != nil && first.RAtFirstTarget.Cmp(second.RAtFirstTarget) != 0 {
		t.Fatalf("RAtFirstTarget mismatch: first=%v second=%v", first.RAtFirstTarget, second.RAtFirstTarget)
	}
}

func sameCandleRef(a, b *CandleRef) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return *a == *b
}

func TestEvaluate_InvalidPlan(t *testing.T) {
	t.Run("PlanNone is rejected", func(t *testing.T) {
		_, err := Evaluate(analysis.TradePlan{Status: analysis.PlanNone}, nil, nil, quant.Price(1), at(0))
		if err == nil {
			t.Fatal("Evaluate: want error for PlanNone, got nil")
		}
	})

	t.Run("undetermined direction is rejected", func(t *testing.T) {
		plan := analysis.TradePlan{Status: analysis.PlanTriggered, Direction: analysis.DirectionUndetermined}
		_, err := Evaluate(plan, nil, nil, quant.Price(1), at(0))
		if err == nil {
			t.Fatal("Evaluate: want error for undetermined direction, got nil")
		}
	})
}
