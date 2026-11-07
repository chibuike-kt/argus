package password

import (
	"context"
	"strings"
	"testing"
)

// testParams are deliberately much cheaper than the real ADR-015
// addendum defaults (64 MiB/t=3) -- fast unit tests, never used to
// hash anything real. internal/config.AuthPolicy is the one place the
// real defaults live; this file never imports them.
func testParams() Params {
	return Params{MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}
}

func TestHasher_HashAndVerify_RoundTrip(t *testing.T) {
	h := NewHasher(testParams(), 4)
	ctx := context.Background()

	encoded, err := h.Hash(ctx, "a correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("encoded = %q, want a PHC string with these exact parameters", encoded)
	}

	ok, err := h.Verify(ctx, "a correct horse battery staple", encoded)
	if err != nil {
		t.Fatalf("Verify (correct password): %v", err)
	}
	if !ok {
		t.Fatal("Verify (correct password) = false, want true")
	}

	ok, err = h.Verify(ctx, "the wrong password entirely", encoded)
	if err != nil {
		t.Fatalf("Verify (wrong password): %v", err)
	}
	if ok {
		t.Fatal("Verify (wrong password) = true, want false")
	}
}

// storedPHCFixture is a PHC string computed independently of this
// package's own Hash() (fixed salt, testParams(), password "a correct
// horse battery staple") and checked in as a literal -- unlike the
// round-trip tests above, this catches a future accidental change to
// encodePHC/decodePHC's own format (field order, base64 variant,
// version string) that both encoded and decoded consistently with
// itself, which a self-generated round trip can never detect.
const storedPHCFixture = "$argon2id$v=19$m=8192,t=1,p=1$MDEyMzQ1Njc4OWFiY2RlZg$lWsZgFRAwlKohUXvif2ETwWT21Rb5EDKCwg3pRdAmX8"

func TestVerify_StoredPHCFixture(t *testing.T) {
	h := NewHasher(testParams(), 4)
	ctx := context.Background()

	ok, err := h.Verify(ctx, "a correct horse battery staple", storedPHCFixture)
	if err != nil {
		t.Fatalf("Verify(stored fixture, correct password): %v", err)
	}
	if !ok {
		t.Fatal("Verify(stored fixture, correct password) = false, want true")
	}

	ok, err = h.Verify(ctx, "the wrong password entirely", storedPHCFixture)
	if err != nil {
		t.Fatalf("Verify(stored fixture, wrong password): %v", err)
	}
	if ok {
		t.Fatal("Verify(stored fixture, wrong password) = true, want false")
	}

	needs, err := h.NeedsRehash(storedPHCFixture)
	if err != nil {
		t.Fatalf("NeedsRehash(stored fixture): %v", err)
	}
	if needs {
		t.Fatal("NeedsRehash(stored fixture, same params as testParams()) = true, want false")
	}
}

func TestHasher_NeedsRehash(t *testing.T) {
	h := NewHasher(testParams(), 4)
	ctx := context.Background()
	encoded, err := h.Hash(ctx, "a correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	same := NewHasher(testParams(), 4)
	needs, err := same.NeedsRehash(encoded)
	if err != nil {
		t.Fatalf("NeedsRehash (same params): %v", err)
	}
	if needs {
		t.Fatal("NeedsRehash (same params) = true, want false")
	}

	changed := testParams()
	changed.Iterations = 2
	different := NewHasher(changed, 4)
	needs, err = different.NeedsRehash(encoded)
	if err != nil {
		t.Fatalf("NeedsRehash (changed params): %v", err)
	}
	if !needs {
		t.Fatal("NeedsRehash (changed params) = false, want true -- iterations changed")
	}
}

func TestHasher_VerifyDummy_NeverMatchesAndNeverErrors(t *testing.T) {
	h := NewHasher(testParams(), 4)
	ctx := context.Background()
	// VerifyDummy's only contract: it costs the same as a real Verify
	// and never returns an error a caller would need to branch on --
	// login's own unknown-email path just calls it and discards the
	// result, exactly like it would ignore "wrong password".
	if err := h.VerifyDummy(ctx, "whatever the caller typed"); err != nil {
		t.Fatalf("VerifyDummy: %v", err)
	}
}

func TestHasher_ConcurrencyLimiter_BoundsInFlightHashes(t *testing.T) {
	h := NewHasher(testParams(), 2)
	ctx := context.Background()

	inFlight := make(chan struct{}, 10)
	release := make(chan struct{})
	done := make(chan struct{}, 10)
	for range 4 {
		go func() {
			_ = h.limiter.acquire(ctx)
			inFlight <- struct{}{}
			<-release
			h.limiter.release()
			done <- struct{}{}
		}()
	}

	// Only 2 (the limit) should have acquired; the other 2 are blocked
	// in limiter.acquire until release closes.
	if len(drain(inFlight, 2)) != 2 {
		t.Fatal("more than the configured 2 concurrent slots were acquired")
	}
	select {
	case <-inFlight:
		t.Fatal("a 3rd goroutine acquired the limiter while only 2 slots exist")
	default:
	}

	close(release)
	for range 4 {
		<-done
	}
}

func drain(ch chan struct{}, n int) []struct{} {
	out := make([]struct{}, 0, n)
	for range n {
		out = append(out, <-ch)
	}
	return out
}
