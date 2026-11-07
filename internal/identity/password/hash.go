// Package password implements Argon2id hashing/verification (ADR-015
// addendum row 2, approved parameters) and the password policy. The
// hashing parameters live in internal/config.AuthPolicy, tagged [R]
// there -- this package only knows how to apply whatever Params it is
// given, never a hardcoded default of its own, so a test can use
// cheaper parameters without this package changing at all.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"sync/atomic"

	"golang.org/x/crypto/argon2"
)

// Params are the Argon2id tunables. Every field is set from
// internal/config.AuthPolicy's own [R]-tagged defaults; nothing in this
// package invents a value.
type Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
}

const (
	phcID      = "argon2id"
	phcVersion = argon2.Version
)

// Hash computes an Argon2id hash of password and encodes it, together
// with its own parameters and salt, as a PHC string (the standard,
// self-describing format: $argon2id$v=19$m=...,t=...,p=...$salt$hash).
// Self-describing means a later parameter change is detectable per
// hash (NeedsRehash) without any schema change -- every stored hash
// already carries the parameters it was computed with.
func hash(password string, p Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyLen)
	return encodePHC(p, salt, key), nil
}

func encodePHC(p Params, salt, key []byte) string {
	return fmt.Sprintf(
		"$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		phcID, phcVersion, p.MemoryKiB, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

// verify reports whether password matches encoded, re-deriving a key
// with exactly the parameters and salt encoded carries -- never the
// caller's current Params -- so an old hash made with different
// settings still verifies correctly. needsRehash (below) is what
// detects that it should be replaced, not this.
func verify(password, encoded string) (bool, error) {
	p, salt, key, err := decodePHC(encoded)
	if err != nil {
		return false, err
	}
	candidate := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, uint32(len(key))) //nolint:gosec // key is a decoded PHC field, always a small, bounded byte count
	return subtle.ConstantTimeCompare(candidate, key) == 1, nil
}

// needsRehash reports whether encoded was hashed with different
// parameters than current (ADR-015 addendum row 2's rehash-on-login).
func needsRehash(encoded string, current Params) (bool, error) {
	p, salt, key, err := decodePHC(encoded)
	if err != nil {
		return false, err
	}
	return p.MemoryKiB != current.MemoryKiB ||
		p.Iterations != current.Iterations ||
		p.Parallelism != current.Parallelism ||
		uint32(len(salt)) != current.SaltLen || //nolint:gosec // decoded PHC field, always small and bounded
		uint32(len(key)) != current.KeyLen, nil //nolint:gosec // decoded PHC field, always small and bounded
}

func decodePHC(encoded string) (Params, []byte, []byte, error) {
	// "$argon2id$v=19$m=...,t=...,p=...$salt$hash" splits on "$" into
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", "salt", "hash"].
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != phcID {
		return Params{}, nil, nil, fmt.Errorf("password: not an argon2id PHC string")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, fmt.Errorf("password: malformed version: %w", err)
	}
	if version != phcVersion {
		return Params{}, nil, nil, fmt.Errorf("password: unsupported argon2 version %d", version)
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Iterations, &p.Parallelism); err != nil {
		return Params{}, nil, nil, fmt.Errorf("password: malformed parameters: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("password: malformed salt: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("password: malformed key: %w", err)
	}
	p.SaltLen = uint32(len(salt)) //nolint:gosec // base64-decoded PHC field, always small and bounded
	p.KeyLen = uint32(len(key))   //nolint:gosec // base64-decoded PHC field, always small and bounded
	return p, salt, key, nil
}

// dummyPassword/dummySalt are fixed and never secret: dummyHash below
// never protects a real credential (nothing is ever authenticated with
// it) -- it exists purely so login's unknown-email path costs the same
// CPU time as a real Verify (ADR-015: "the response must not reveal
// which [case it is]").
const dummyPassword = "argus-dummy-password-for-constant-time-login"

func dummyHash(p Params) string {
	salt := make([]byte, p.SaltLen) // all-zero; deterministic on purpose
	key := argon2.IDKey([]byte(dummyPassword), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyLen)
	return encodePHC(p, salt, key)
}

// Limiter bounds concurrent Argon2 operations across the whole process
// (ADR-015 addendum row 2: "at most 4 concurrent hash operations per
// process"). Argon2id at these parameters is deliberately memory- and
// CPU-heavy; with no cap, a burst of login/register requests could
// try to allocate many times MemoryKiB simultaneously. A buffered
// channel used as a semaphore -- the standard library has no semaphore
// type, and golang.org/x/sync is not an approved dependency for this
// brief, so this is the boring, standard pattern instead of a new import.
type limiter struct {
	sem chan struct{}
}

func newLimiter(maxConcurrent int) *limiter {
	return &limiter{sem: make(chan struct{}, maxConcurrent)}
}

func (l *limiter) acquire(ctx context.Context) error {
	select {
	case l.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *limiter) release() {
	<-l.sem
}

// Hasher is the package's one exported entry point: Params plus the
// concurrency limiter plus a precomputed dummy hash, bundled so every
// caller goes through the same limiter instance (a process has exactly
// one Hasher, built once at startup).
type Hasher struct {
	params    Params
	limiter   *limiter
	dummyHash string
	ops       atomic.Int64
}

// NewHasher builds a Hasher. maxConcurrent is ADR-015 addendum row 2's
// own "at most 4 concurrent hash operations per process" (config, not
// hardcoded here either).
func NewHasher(params Params, maxConcurrent int) *Hasher {
	return &Hasher{
		params:    params,
		limiter:   newLimiter(maxConcurrent),
		dummyHash: dummyHash(params),
	}
}

// Hash computes a new PHC-encoded Argon2id hash of password, through
// the concurrency limiter.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.limiter.acquire(ctx); err != nil {
		return "", err
	}
	defer h.limiter.release()
	h.ops.Add(1)
	return hash(password, h.params)
}

// Verify reports whether password matches encoded, through the
// concurrency limiter.
func (h *Hasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	if err := h.limiter.acquire(ctx); err != nil {
		return false, err
	}
	defer h.limiter.release()
	h.ops.Add(1)
	return verify(password, encoded)
}

// Ops returns the number of real Argon2 operations (Hash, or Verify/
// VerifyDummy, which calls Verify) this Hasher has performed so far.
// It exists for tests that must prove a timing-sensitive code path
// pays the same Argon2 cost on every branch (architect review item 1)
// without asserting on wall-clock time, which is inherently flaky --
// counting real operations is the deterministic alternative.
func (h *Hasher) Ops() int64 { return h.ops.Load() }

// VerifyDummy runs exactly the same work as Verify, against a fixed,
// never-secret hash -- login's own unknown-email path calls this
// instead of skipping Verify entirely, so the CPU cost (and therefore
// the response time) does not disclose whether the email exists.
func (h *Hasher) VerifyDummy(ctx context.Context, password string) error {
	_, err := h.Verify(ctx, password, h.dummyHash)
	return err
}

// NeedsRehash reports whether encoded was hashed with parameters other
// than this Hasher's current ones.
func (h *Hasher) NeedsRehash(encoded string) (bool, error) {
	return needsRehash(encoded, h.params)
}
