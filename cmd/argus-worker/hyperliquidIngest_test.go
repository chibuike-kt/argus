package main

import (
	"testing"
	"time"
)

func TestReconnectBackoff_ExponentialWithFullJitterAndCap(t *testing.T) {
	base, capDuration := time.Second, 30*time.Second

	// attempt 0: uniformly in [0, base]. Deterministic bounds, not the
	// random value itself -- same reasoning the live-chart brief's own
	// client-side backoff tests use.
	for i := 0; i < 50; i++ {
		d := reconnectBackoff(0, base, capDuration)
		if d < 0 || d > base {
			t.Fatalf("attempt 0: delay %v not in [0, %v]", d, base)
		}
	}

	// attempt 5: base*2^5 = 32s, already above the 30s cap -- so this
	// must be bounded by cap, not by the uncapped exponential.
	for i := 0; i < 50; i++ {
		d := reconnectBackoff(5, base, capDuration)
		if d < 0 || d > capDuration {
			t.Fatalf("attempt 5: delay %v not in [0, %v] (the cap)", d, capDuration)
		}
	}

	// A very large attempt count must not overflow into a negative or
	// nonsensical duration -- min(attempt, 32) in the shift exponent
	// exists specifically to guard this.
	d := reconnectBackoff(1_000_000, base, capDuration)
	if d < 0 || d > capDuration {
		t.Fatalf("attempt 1_000_000: delay %v not in [0, %v]", d, capDuration)
	}
}

func TestReconnectBackoff_AttemptsAreNotIdentical(t *testing.T) {
	// Not a statistical test of the jitter distribution -- just a
	// sanity check that attempt 0 and attempt 3 actually produce
	// different RANGES, proving the exponent is applied at all (a
	// regression that hardcoded attempt=0's own range for every call
	// would still pass TestReconnectBackoff_ExponentialWithFullJitterAndCap
	// above by accident, since attempt 0's range is a subset of every
	// later one's).
	base, capDuration := time.Second, 30*time.Second
	var sawAboveBase bool
	for i := 0; i < 200; i++ {
		if reconnectBackoff(3, base, capDuration) > base {
			sawAboveBase = true
			break
		}
	}
	if !sawAboveBase {
		t.Error("200 draws at attempt 3 (range up to base*2^3=8s) never exceeded base (1s) -- the exponent does not appear to be applied")
	}
}
