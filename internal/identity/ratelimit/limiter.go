// Package ratelimit implements the counters behind ADR-015 addendum
// row 12's thresholds: Redis-backed fixed-window counters, with an
// in-process fallback so a Redis outage slows limiting down to a
// single process's own memory rather than removing it (the addendum's
// own "limiting is never disabled").
package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/argus/internal/clock"
)

// Result is what Allow reports: whether the action may proceed, and,
// when it may not, how long until it can be retried.
type Result struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Limiter checks and increments a fixed-window counter for a given
// key. One key per (action, dimension, identifier) -- e.g.
// "login:account:42" or "register:ip:203.0.113.5" -- chosen by the
// caller (internal/identity/service), not this package: a limiter has
// no opinion on what is being limited, only on counting.
type Limiter struct {
	redis  *redis.Client
	logger *slog.Logger
	clk    clock.Clock

	mu       sync.Mutex
	fallback map[string]*fallbackEntry
}

type fallbackEntry struct {
	count   int
	resetAt time.Time
}

// NewLimiter builds a Limiter backed by redis, falling back to an
// in-process counter when redis errors.
func NewLimiter(redis *redis.Client, logger *slog.Logger, clk clock.Clock) *Limiter {
	return &Limiter{redis: redis, logger: logger, clk: clk, fallback: make(map[string]*fallbackEntry)}
}

// incrWithExpiryScript makes "increment, and make sure the key has a
// TTL" one atomic Redis operation instead of two separate commands
// (architect review item 5). Two commands meant that if the process
// died, or the EXPIRE call itself failed, between them, the key was
// left with no TTL and nothing but a successful call to Reset would
// ever clear it again -- for "login:ip:*" that is not "a drip of
// Redis memory" (the old comment's words), it is a permanent ban of
// that IP. The script re-applies EXPIRE whenever the key has no TTL
// (TTL < 0), not only when count == 1, so it also self-heals a key
// that already lost its TTL some other way, on its very next hit.
var incrWithExpiryScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
	redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return count
`)

func incrWithExpiry(ctx context.Context, rdb *redis.Client, key string, window time.Duration) (int64, error) {
	return incrWithExpiryScript.Run(ctx, rdb, []string{key}, int64(window.Seconds())).Int64()
}

// Allow increments key's counter for the current window and reports
// whether it is still within limit. A fixed window, not a sliding one
// -- the standard, simplest Redis rate-limit pattern (INCR + EXPIRE),
// at the cost of allowing up to limit requests again right after a
// window boundary; the addendum's own thresholds are generous enough
// that this is an acceptable, well-understood trade-off, not a reason
// for a more complex algorithm.
//
// On a Redis error, this logs at Warn and falls back to an in-process
// counter rather than returning an error that a caller might treat as
// "skip limiting" -- limiting must never be disabled, even if it
// becomes per-process instead of shared across every API instance
// while Redis is down.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	result, err := l.allowRedis(ctx, key, limit, window)
	if err == nil {
		return result, nil
	}
	l.logger.Warn("ratelimit: redis unavailable, using in-process fallback", "key", key, "error", err)
	return l.allowFallback(key, limit, window), nil
}

func (l *Limiter) allowRedis(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	count, err := incrWithExpiry(ctx, l.redis, key, window)
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: incr: %w", err)
	}
	if count > int64(limit) {
		ttl, err := l.redis.TTL(ctx, key).Result()
		if err != nil || ttl < 0 {
			ttl = window
		}
		return Result{Allowed: false, RetryAfter: ttl}, nil
	}
	return Result{Allowed: true}, nil
}

func (l *Limiter) allowFallback(key string, limit int, window time.Duration) Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	l.evictExpiredLocked(now)

	e, ok := l.fallback[key]
	if !ok || !now.Before(e.resetAt) {
		e = &fallbackEntry{count: 0, resetAt: now.Add(window)}
		l.fallback[key] = e
	}
	e.count++
	if e.count > limit {
		return Result{Allowed: false, RetryAfter: e.resetAt.Sub(now)}
	}
	return Result{Allowed: true}
}

// IncrementAndCount increments key's fixed-window counter and returns
// the new count, with no allow/deny decision of its own -- for a
// caller that wants to apply its own graduated response (ADR-015
// addendum row 12's progressive login delay) rather than a hard
// allow/deny at one threshold.
func (l *Limiter) IncrementAndCount(ctx context.Context, key string, window time.Duration) (int, error) {
	count, err := incrWithExpiry(ctx, l.redis, key, window)
	if err == nil {
		return int(count), nil
	}
	l.logger.Warn("ratelimit: redis unavailable, using in-process fallback", "key", key, "error", err)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	l.evictExpiredLocked(now)
	e, ok := l.fallback[key]
	if !ok || !now.Before(e.resetAt) {
		e = &fallbackEntry{count: 0, resetAt: now.Add(window)}
		l.fallback[key] = e
	}
	e.count++
	return e.count, nil
}

// Reset clears key's counter immediately -- a successful login
// resetting its own account's failure streak, in both backends.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if err := l.redis.Del(ctx, key).Err(); err != nil {
		l.logger.Warn("ratelimit: redis unavailable for reset, clearing in-process fallback only", "key", key, "error", err)
	}
	l.mu.Lock()
	delete(l.fallback, key)
	l.mu.Unlock()
	return nil
}

// evictExpiredLocked drops expired entries so a long-running process
// falling back for an extended Redis outage does not accumulate one
// entry per distinct key (IP, account) forever. Called with mu held,
// opportunistically on every fallback hit -- simple, no background
// goroutine needed for Part 1's scope.
func (l *Limiter) evictExpiredLocked(now time.Time) {
	for k, e := range l.fallback {
		if !now.Before(e.resetAt) {
			delete(l.fallback, k)
		}
	}
}
