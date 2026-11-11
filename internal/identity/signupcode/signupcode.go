// Package signupcode implements the signup-by-emailed-code flow's own
// intermediate state (ADR-015 "Signup and onboarding"): a 6-digit code,
// HMAC-SHA256'd before storage since it is too low entropy for a plain
// hash to resist an offline brute force of a database breach, and the
// single-use signup ticket a correct code unlocks. No user row exists
// yet at any point this package touches -- that is internal/identity's
// own job, once internal/identity/service consumes a ticket this
// package issued.
package signupcode

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/store"
)

// ErrInvalidCode covers "no signup in progress for this email",
// "wrong code", "expired", "already verified" and "exhausted" alike --
// one generic failure (ADR-015: "one generic invalid_code for a
// wrong, expired, unknown or exhausted code"), never distinguished for
// the caller.
var ErrInvalidCode = errors.New("signupcode: invalid, expired, or exhausted code")

// ErrInvalidTicket covers "does not exist", "already used" and
// "expired" alike, the same reason tokens.ErrInvalid does not
// distinguish those cases either.
var ErrInvalidTicket = errors.New("signupcode: invalid, expired, or already used ticket")

// TooSoonError is IssueCode's own result when the previous code for
// this email was sent less than the configured cooldown ago (ADR-015:
// "A new code can be requested after 60 seconds"). Shaped like
// service.RateLimitedError deliberately, so the HTTP layer can treat
// the two identically (429 with Retry-After) without this package
// importing that one.
type TooSoonError struct {
	RetryAfter time.Duration
}

func (e *TooSoonError) Error() string {
	return fmt.Sprintf("signupcode: resend too soon, retry after %s", e.RetryAfter)
}

// Verification is the domain view of a signup_verifications row --
// only what a caller outside this package ever needs, never a hash or
// the HMAC key.
type Verification struct {
	Email           string
	EmailNormalized string
	TermsAcceptedAt time.Time
	TermsVersion    string
}

// Store is signup_verifications' own data-access layer plus the code/
// ticket lifecycle (generation, hashing, comparison) around it.
type Store struct {
	q       *store.Queries
	clk     clock.Clock
	hmacKey []byte
}

// NewStore builds a Store. hmacKey is ADR-015's own "server key" the
// emailed code is HMAC-SHA256'd against; config.Validate rejects the
// local-dev placeholder outside local/test, so a real deployment
// cannot reach here with it unreplaced.
func NewStore(db store.DBTX, clk clock.Clock, hmacKey string) *Store {
	return &Store{q: store.New(db), clk: clk, hmacKey: []byte(hmacKey)}
}

// WithTx returns a Store bound to tx -- SignupComplete's own atomic
// consume-ticket-then-insert-user transaction (architect review of
// Part 2, item 2) needs ConsumeTicket to run against the same tx as
// identity.Store's InsertVerified, not its own separate connection.
func (s *Store) WithTx(tx *sql.Tx) *Store {
	return &Store{q: s.q.WithTx(tx), clk: s.clk, hmacKey: s.hmacKey}
}

// IssueCode starts or restarts a signup for email: a fresh 6-digit
// code, replacing whatever was live before, unless the previous code
// was sent less than cooldown ago (*TooSoonError). termsAcceptedAt/
// termsVersion are the signup-start request's own "accepted_terms:
// true" moment and the current terms version -- carried through to the
// users row internal/identity/service creates at signup complete,
// since that row does not exist yet.
func (s *Store) IssueCode(ctx context.Context, email string, cooldown, ttl time.Duration, termsAcceptedAt time.Time, termsVersion string) (rawCode string, err error) {
	emailNormalized := normalizeEmail(email)
	now := s.clk.Now()

	if existing, err := s.q.GetSignupVerificationByEmail(ctx, emailNormalized); err == nil {
		if elapsed := now.Sub(existing.CodeSentAt); elapsed < cooldown {
			return "", &TooSoonError{RetryAfter: cooldown - elapsed}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("signupcode: issue code: look up existing: %w", err)
	}

	rawCode, err = generateCode()
	if err != nil {
		return "", err
	}
	_, err = s.q.UpsertSignupCode(ctx, store.UpsertSignupCodeParams{
		Email:           strings.TrimSpace(email),
		EmailNormalized: emailNormalized,
		CodeHash:        s.hashCode(rawCode),
		CodeSentAt:      now,
		CodeExpiresAt:   now.Add(ttl),
		TermsAcceptedAt: termsAcceptedAt,
		TermsVersion:    termsVersion,
	})
	if err != nil {
		return "", fmt.Errorf("signupcode: issue code: upsert: %w", err)
	}
	return rawCode, nil
}

// VerifyCode checks rawCode against whatever is currently live for
// email and, on a match, returns a fresh single-use ticket valid for
// ticketTTL. ErrInvalidCode for every failure (see its own doc
// comment).
//
// Architect review (of Part 2) item 3: this used to read
// wrong_guesses, compare, and only then increment in a separate
// statement -- N concurrent requests all read the same pre-increment
// count and all got a guess, so the limit did not actually limit
// anything under concurrency. Every attempt now claims its guess
// atomically first (IncrementSignupWrongGuessesIfEligible), and the
// code is compared only if that claim succeeds; a correct guess is
// not given back, so wrong_guesses also counts the one successful
// guess, if any, once the row is verified -- a column named
// "wrong_guesses" that also counts a single right one is a minor
// semantic imprecision, accepted here rather than renaming the column
// (out of scope for this review; the 5-guess limit itself is
// unchanged either way).
func (s *Store) VerifyCode(ctx context.Context, email, rawCode string, maxWrongGuesses int, ticketTTL time.Duration) (rawTicket string, err error) {
	emailNormalized := normalizeEmail(email)
	now := s.clk.Now()

	row, err := s.q.IncrementSignupWrongGuessesIfEligible(ctx, store.IncrementSignupWrongGuessesIfEligibleParams{
		EmailNormalized: emailNormalized,
		CodeExpiresAt:   now,
		WrongGuesses:    int32(maxWrongGuesses), //nolint:gosec // a small config policy value (ADR-015 default: 5), never user input
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Not eligible even before comparing the code: unknown
			// email, already verified, expired, or already exhausted
			// -- the same generic failure, never distinguished.
			return "", ErrInvalidCode
		}
		return "", fmt.Errorf("signupcode: verify code: claim attempt: %w", err)
	}

	if !hmac.Equal([]byte(s.hashCode(rawCode)), []byte(row.CodeHash)) {
		return "", ErrInvalidCode
	}

	rawTicket, err = generateTicket()
	if err != nil {
		return "", err
	}
	_, err = s.q.MarkSignupVerified(ctx, store.MarkSignupVerifiedParams{
		EmailNormalized: emailNormalized,
		VerifiedAt:      sql.NullTime{Time: now, Valid: true},
		TicketHash:      sql.NullString{String: hashTicket(rawTicket), Valid: true},
		TicketExpiresAt: sql.NullTime{Time: now.Add(ticketTTL), Valid: true},
		CodeExpiresAt:   now,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Row state changed between the claim above and this
			// atomic transition (a concurrent verify already won, or
			// the code expired in between) -- the same generic
			// failure, not a different one (ADR-015).
			return "", ErrInvalidCode
		}
		return "", fmt.Errorf("signupcode: verify code: mark verified: %w", err)
	}
	return rawTicket, nil
}

// GetByTicket returns the signup rawTicket belongs to, without
// consuming it (architect review of Part 2, item 2): SignupComplete
// needs this to validate the password policy against the right email
// before it commits to consuming the ticket, so a rejected password
// never burns it. ErrInvalidTicket for "does not exist", "already
// used" and "expired" alike, the same as ConsumeTicket.
func (s *Store) GetByTicket(ctx context.Context, rawTicket string) (Verification, error) {
	row, err := s.q.GetSignupVerificationByTicket(ctx, sql.NullString{String: hashTicket(rawTicket), Valid: true})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Verification{}, ErrInvalidTicket
		}
		return Verification{}, fmt.Errorf("signupcode: get by ticket: %w", err)
	}
	if row.TicketUsedAt.Valid || !row.TicketExpiresAt.Valid || s.clk.Now().After(row.TicketExpiresAt.Time) {
		return Verification{}, ErrInvalidTicket
	}
	return Verification{
		Email:           row.Email,
		EmailNormalized: row.EmailNormalized,
		TermsAcceptedAt: row.TermsAcceptedAt,
		TermsVersion:    row.TermsVersion,
	}, nil
}

// ConsumeTicket marks rawTicket used and returns the signup it
// descends from, atomically, only if it exists, has not been used
// yet, and has not expired.
func (s *Store) ConsumeTicket(ctx context.Context, rawTicket string) (Verification, error) {
	now := s.clk.Now()
	row, err := s.q.ConsumeSignupTicket(ctx, store.ConsumeSignupTicketParams{
		TicketHash:      sql.NullString{String: hashTicket(rawTicket), Valid: true},
		TicketUsedAt:    sql.NullTime{Time: now, Valid: true},
		TicketExpiresAt: sql.NullTime{Time: now, Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Verification{}, ErrInvalidTicket
		}
		return Verification{}, fmt.Errorf("signupcode: consume ticket: %w", err)
	}
	return Verification{
		Email:           row.Email,
		EmailNormalized: row.EmailNormalized,
		TermsAcceptedAt: row.TermsAcceptedAt,
		TermsVersion:    row.TermsVersion,
	}, nil
}

// hashCode is the HMAC-SHA256 (this Store's own server key) of the raw
// 6-digit code, hex encoded -- never the code itself.
func (s *Store) hashCode(code string) string {
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// hashTicket is the plain SHA-256 of the raw ticket -- high entropy
// (32 random bytes), unlike the code, so no HMAC key is needed to
// resist an offline brute force of a breach.
func hashTicket(ticket string) string {
	sum := sha256.Sum256([]byte(ticket))
	return hex.EncodeToString(sum[:])
}

// generateCode returns a 6-digit numeric code, crypto/rand only, zero
// padded (e.g. "004217") -- every value from "000000" to "999999" is
// equally likely, not just those without a leading zero.
func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("signupcode: generate code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// generateTicket returns 32 random bytes, base64url encoded -- the
// same shape tokens.generateToken and session tokens already take.
func generateTicket() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("signupcode: generate ticket: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// normalizeEmail matches identity.NormalizeEmail exactly (lowercased,
// trimmed) without importing that package just for one function --
// this package knows nothing about users, only about email strings as
// signup_verifications' own lookup key.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
