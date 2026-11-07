package ratelimit

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/argus/internal/clock"
)

// brokenRedis returns a client pointed at an address nothing listens
// on, with a short dial timeout -- every call fails fast, so Allow/
// IncrementAndCount/Reset must all fall back to the in-process path
// (ADR-015 addendum: "limiting is never disabled"). This needs no real
// Redis, so it runs as part of the ordinary unit suite, not gated
// behind -tags=integration.
func brokenRedis() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 50 * time.Millisecond,
		ReadTimeout: 50 * time.Millisecond,
	})
}

func TestAllow_FallsBackAndStillEnforcesLimit(t *testing.T) {
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	l := NewLimiter(brokenRedis(), slog.Default(), clk)
	ctx := context.Background()

	for i := range 3 {
		res, err := l.Allow(ctx, "test:key", 3, time.Minute)
		if err != nil {
			t.Fatalf("Allow #%d: %v", i, err)
		}
		if !res.Allowed {
			t.Fatalf("Allow #%d = denied, want allowed (within limit)", i)
		}
	}

	res, err := l.Allow(ctx, "test:key", 3, time.Minute)
	if err != nil {
		t.Fatalf("Allow over limit: %v", err)
	}
	if res.Allowed {
		t.Fatal("Allow over limit = allowed, want denied")
	}
	if res.RetryAfter <= 0 {
		t.Fatalf("RetryAfter = %v, want > 0", res.RetryAfter)
	}
}

func TestAllow_FallbackWindowResets(t *testing.T) {
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	l := NewLimiter(brokenRedis(), slog.Default(), clk)
	ctx := context.Background()

	for i := range 2 {
		if res, err := l.Allow(ctx, "test:window", 2, time.Minute); err != nil || !res.Allowed {
			t.Fatalf("Allow #%d = %v, %v", i, res, err)
		}
	}
	if res, _ := l.Allow(ctx, "test:window", 2, time.Minute); res.Allowed {
		t.Fatal("3rd Allow within window = allowed, want denied")
	}

	clk.Advance(61 * time.Second)
	if res, err := l.Allow(ctx, "test:window", 2, time.Minute); err != nil || !res.Allowed {
		t.Fatalf("Allow after window reset = %v, %v, want allowed", res, err)
	}
}

func TestIncrementAndCount_FallsBack(t *testing.T) {
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	l := NewLimiter(brokenRedis(), slog.Default(), clk)
	ctx := context.Background()

	for want := 1; want <= 3; want++ {
		got, err := l.IncrementAndCount(ctx, "test:count", time.Minute)
		if err != nil {
			t.Fatalf("IncrementAndCount: %v", err)
		}
		if got != want {
			t.Fatalf("IncrementAndCount = %d, want %d", got, want)
		}
	}
}

func TestReset_ClearsFallbackCounter(t *testing.T) {
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	l := NewLimiter(brokenRedis(), slog.Default(), clk)
	ctx := context.Background()

	if _, err := l.IncrementAndCount(ctx, "test:reset", time.Minute); err != nil {
		t.Fatalf("IncrementAndCount: %v", err)
	}
	if err := l.Reset(ctx, "test:reset"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	got, err := l.IncrementAndCount(ctx, "test:reset", time.Minute)
	if err != nil {
		t.Fatalf("IncrementAndCount after Reset: %v", err)
	}
	if got != 1 {
		t.Fatalf("count after Reset = %d, want 1 (counter should have been cleared)", got)
	}
}
