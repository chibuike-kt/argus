// Package clock provides the injected-clock abstraction and the distinct
// time-semantic types ARGUS uses so that exchange event time, ingestion
// time, processing time and evaluation time cannot be passed in each
// other's place by accident (guide sections 13 and 16; ADR-009, ADR-019).
//
// Business logic must never call time.Now() directly. It takes a Clock (or
// an already-captured, correctly-typed value from this package) so that
// tests, replay and point-in-time evaluation do not depend on wall-clock
// state.
package clock

import (
	"sync"
	"time"
)

// Clock returns the current time. Production code uses Real; tests use
// Fixed or Mutable so time does not depend on wall-clock state (guide 7, 10,
// 13).
type Clock interface {
	Now() time.Time
}

// Real is the production Clock. It always returns UTC.
type Real struct{}

// Now returns the current wall-clock time in UTC.
func (Real) Now() time.Time { return time.Now().UTC() }

// Fixed is a Clock that always returns the same instant. It is the default
// choice for deterministic tests and point-in-time replay fixtures.
type Fixed struct {
	t time.Time
}

// NewFixed returns a Fixed clock at t, normalized to UTC.
func NewFixed(t time.Time) Fixed { return Fixed{t: t.UTC()} }

// Now returns the fixed instant.
func (f Fixed) Now() time.Time { return f.t }

// Mutable is a Clock whose value a test can advance explicitly, for
// scenarios that need time to move during the test (e.g. simulating a
// reconnect delay). It is safe for concurrent use.
type Mutable struct {
	mu sync.Mutex
	t  time.Time
}

// NewMutable returns a Mutable clock starting at t, normalized to UTC.
func NewMutable(t time.Time) *Mutable {
	return &Mutable{t: t.UTC()}
}

// Now returns the current value of the mutable clock.
func (m *Mutable) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t
}

// Set assigns the clock's current value, normalized to UTC.
func (m *Mutable) Set(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t = t.UTC()
}

// Advance moves the clock forward (or backward, for a negative d) by d.
func (m *Mutable) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t = m.t.Add(d)
}
