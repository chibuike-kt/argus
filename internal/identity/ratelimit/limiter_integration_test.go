//go:build integration

package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/argus/internal/clock"
)

// testRedis connects to logical database 15 (the standard Redis
// instance already has 16 by default), kept distinct from database 0
// that the rest of ARGUS uses, so these tests never share keys with a
// real run against the same Redis. Set ARGUS_TEST_REDIS_URL to
// override. Run with:
//
//	go test -tags=integration ./internal/identity/ratelimit/...
func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("ARGUS_TEST_REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379/15"
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := redis.NewClient(opt)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("integration test redis unreachable at %s: %v", url, err)
	}
	return client
}

func uniqueKey(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s:%s:%d", prefix, t.Name(), time.Now().UnixNano())
}

func TestIntegration_Allow_RealRedis_EnforcesLimit(t *testing.T) {
	rdb := testRedis(t)
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	l := NewLimiter(rdb, slog.Default(), clk)
	ctx := context.Background()
	key := uniqueKey(t, "allow")
	t.Cleanup(func() { _ = rdb.Del(context.Background(), key).Err() })

	for i := 0; i < 2; i++ {
		res, err := l.Allow(ctx, key, 2, time.Minute)
		if err != nil || !res.Allowed {
			t.Fatalf("Allow #%d = %v, %v, want allowed", i, res, err)
		}
	}
	res, err := l.Allow(ctx, key, 2, time.Minute)
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

// TestIntegration_IncrWithExpiry_SelfHealsAMissingTTL is architect
// review item 5's own test: a key that already exists with no TTL
// (simulating the old two-command INCR-then-EXPIRE losing the second
// command, or the process dying between them) gets one on its very
// next hit through this package, rather than being left to grow
// forever -- which for login:ip:* was a permanent ban, not "a drip of
// Redis memory" as the old code's own comment called it.
func TestIntegration_IncrWithExpiry_SelfHealsAMissingTTL(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	key := uniqueKey(t, "no-ttl")
	t.Cleanup(func() { _ = rdb.Del(context.Background(), key).Err() })

	// Simulate the pre-existing, TTL-less key directly -- no NewLimiter
	// call needed to get into this state, which is exactly the point:
	// today's incrWithExpiry must repair it regardless of how it arose.
	if err := rdb.Set(ctx, key, 5, 0).Err(); err != nil {
		t.Fatalf("seed key with no TTL: %v", err)
	}
	if ttl, err := rdb.TTL(ctx, key).Result(); err != nil || ttl > 0 {
		t.Fatalf("seeded key TTL = %v, %v, want no TTL (-1)", ttl, err)
	}

	if _, err := incrWithExpiry(ctx, rdb, key, time.Minute); err != nil {
		t.Fatalf("incrWithExpiry: %v", err)
	}

	ttl, err := rdb.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("TTL after incrWithExpiry = %v, want a positive TTL (self-healed)", ttl)
	}
}

// TestIntegration_Allow_StopsLimitingAfterWindowExpires confirms the
// atomic script's own TTL still actually expires the key (not just
// "gets set once and never checked again"): once the window passes, a
// previously-limited key allows again.
func TestIntegration_Allow_StopsLimitingAfterWindowExpires(t *testing.T) {
	rdb := testRedis(t)
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	l := NewLimiter(rdb, slog.Default(), clk)
	ctx := context.Background()
	key := uniqueKey(t, "window")
	t.Cleanup(func() { _ = rdb.Del(context.Background(), key).Err() })

	const window = 2 * time.Second
	if res, err := l.Allow(ctx, key, 1, window); err != nil || !res.Allowed {
		t.Fatalf("Allow #1 = %v, %v, want allowed", res, err)
	}
	if res, err := l.Allow(ctx, key, 1, window); err != nil || res.Allowed {
		t.Fatalf("Allow #2 (over limit) = %v, %v, want denied", res, err)
	}

	time.Sleep(window + 500*time.Millisecond)

	if res, err := l.Allow(ctx, key, 1, window); err != nil || !res.Allowed {
		t.Fatalf("Allow after window expired = %v, %v, want allowed again", res, err)
	}
}

func TestIntegration_Reset_ClearsRealRedisCounter(t *testing.T) {
	rdb := testRedis(t)
	clk := clock.NewFixed(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	l := NewLimiter(rdb, slog.Default(), clk)
	ctx := context.Background()
	key := uniqueKey(t, "reset")
	t.Cleanup(func() { _ = rdb.Del(context.Background(), key).Err() })

	if _, err := l.IncrementAndCount(ctx, key, time.Minute); err != nil {
		t.Fatalf("IncrementAndCount: %v", err)
	}
	if err := l.Reset(ctx, key); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	got, err := l.IncrementAndCount(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("IncrementAndCount after Reset: %v", err)
	}
	if got != 1 {
		t.Fatalf("count after Reset = %d, want 1", got)
	}
}
