// Package identity is a feature module boundary (see ADR-003).
// Authentication and authorization (ADR-015): user accounts here;
// sessions, email tokens, rate limiting, mailing and the orchestrating
// service each live in their own sibling package (internal/identity/
// session, tokens, ratelimit, mailer, service) so each has its own
// small, testable surface.
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/chibuike-kt/argus/internal/store"
)

const uniqueViolation = "23505"

// User is the domain view of a users row.
type User struct {
	ID                    int64
	Email                 string
	EmailNormalized       string
	PasswordHash          string
	EmailVerifiedAt       *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
	DisabledAt            *time.Time
	DisplayName           *string
	TermsAcceptedAt       *time.Time
	TermsVersion          *string
	OnboardingCompletedAt *time.Time
}

// EmailVerified reports whether the account has completed email
// verification.
func (u User) EmailVerified() bool { return u.EmailVerifiedAt != nil }

// Disabled reports whether the account is disabled.
func (u User) Disabled() bool { return u.DisabledAt != nil }

// OnboardingCompleted reports whether the account has a display name
// and at least one watchlist item recorded (ADR-015 "Signup and
// onboarding").
func (u User) OnboardingCompleted() bool { return u.OnboardingCompletedAt != nil }

// NormalizeEmail is the one place "what counts as the same email
// address" is decided: lowercased and trimmed. Used both to compute
// email_normalized on account creation and to look up an existing
// account, so the two can never disagree.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// LocalPart returns the part of an email address before "@", lowercased
// -- used by the password policy's "must not contain your email
// address" check. A malformed address with no "@" returns the whole
// (trimmed, lowercased) string; this is a policy input, not a validity
// check, so it degrades gracefully rather than erroring.
func LocalPart(email string) string {
	e := strings.ToLower(strings.TrimSpace(email))
	local, _, found := strings.Cut(e, "@")
	if !found {
		return e
	}
	return local
}

// ErrNotFound means no user matches the given email or id.
var ErrNotFound = errors.New("identity: user not found")

// ErrAlreadyExists means email_normalized already has a row.
var ErrAlreadyExists = errors.New("identity: email already registered")

// Store is the users table's own data-access layer.
type Store struct {
	q *store.Queries
}

// NewStore builds a Store.
func NewStore(db store.DBTX) *Store {
	return &Store{q: store.New(db)}
}

// WithTx returns a Store bound to tx.
func (s *Store) WithTx(tx *sql.Tx) *Store {
	return &Store{q: s.q.WithTx(tx)}
}

// InsertVerified creates a new account, already verified, with its
// terms acceptance recorded (ADR-015: "no half accounts" -- signup
// complete, the one caller, has already consumed a signup ticket that
// only exists once the email's code was verified). ErrAlreadyExists on
// a duplicate email_normalized -- the unique index is the actual
// guarantee; this translates its violation into a sentinel the caller
// can branch on without parsing a driver error string.
func (s *Store) InsertVerified(ctx context.Context, email, passwordHash string, verifiedAt, termsAcceptedAt time.Time, termsVersion string) (User, error) {
	row, err := s.q.InsertVerifiedUser(ctx, store.InsertVerifiedUserParams{
		Email: email, EmailNormalized: NormalizeEmail(email), PasswordHash: passwordHash,
		EmailVerifiedAt: sql.NullTime{Time: verifiedAt, Valid: true},
		TermsAcceptedAt: sql.NullTime{Time: termsAcceptedAt, Valid: true},
		TermsVersion:    sql.NullString{String: termsVersion, Valid: true},
	})
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == uniqueViolation {
			return User{}, ErrAlreadyExists
		}
		return User{}, fmt.Errorf("identity: insert verified user: %w", err)
	}
	return fromRow(row), nil
}

// SetDisplayName sets name (already validated by the caller) on
// userID's account.
func (s *Store) SetDisplayName(ctx context.Context, userID int64, name string, now time.Time) (User, error) {
	row, err := s.q.SetDisplayName(ctx, store.SetDisplayNameParams{
		ID: userID, DisplayName: sql.NullString{String: name, Valid: true}, UpdatedAt: now,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("identity: set display name: %w", err)
	}
	return fromRow(row), nil
}

// MarkOnboardingCompleted sets onboarding_completed_at to now. The
// caller (internal/identity/service) is responsible for having already
// checked that userID has a display name and at least one watchlist
// item -- this method does not check either (ADR-015: that check
// belongs in one visible place in Go, not duplicated in SQL).
func (s *Store) MarkOnboardingCompleted(ctx context.Context, userID int64, now time.Time) (User, error) {
	row, err := s.q.MarkOnboardingCompleted(ctx, store.MarkOnboardingCompletedParams{ID: userID, OnboardingCompletedAt: sql.NullTime{Time: now, Valid: true}})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("identity: mark onboarding completed: %w", err)
	}
	return fromRow(row), nil
}

// GetByEmail returns the user with the given email (normalized before
// lookup), or ErrNotFound.
func (s *Store) GetByEmail(ctx context.Context, email string) (User, error) {
	row, err := s.q.GetUserByEmailNormalized(ctx, NormalizeEmail(email))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("identity: get user by email: %w", err)
	}
	return fromRow(row), nil
}

// GetByID returns the user with the given id, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id int64) (User, error) {
	row, err := s.q.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("identity: get user by id: %w", err)
	}
	return fromRow(row), nil
}

// MarkEmailVerified sets email_verified_at to now (idempotent: calling
// it on an already-verified account just moves the timestamp forward,
// which is harmless -- verify-email's own token is single-use, so this
// only ever runs once per account in practice).
func (s *Store) MarkEmailVerified(ctx context.Context, userID int64, now time.Time) (User, error) {
	row, err := s.q.MarkEmailVerified(ctx, store.MarkEmailVerifiedParams{ID: userID, EmailVerifiedAt: sql.NullTime{Time: now, Valid: true}})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("identity: mark email verified: %w", err)
	}
	return fromRow(row), nil
}

// UpdatePasswordHash is the rehash-on-login path (ADR-015 addendum row 2).
func (s *Store) UpdatePasswordHash(ctx context.Context, userID int64, passwordHash string, now time.Time) error {
	if err := s.q.UpdatePasswordHash(ctx, store.UpdatePasswordHashParams{ID: userID, PasswordHash: passwordHash, UpdatedAt: now}); err != nil {
		return fmt.Errorf("identity: update password hash: %w", err)
	}
	return nil
}

func fromRow(row store.User) User {
	u := User{
		ID:              row.ID,
		Email:           row.Email,
		EmailNormalized: row.EmailNormalized,
		PasswordHash:    row.PasswordHash,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
	if row.EmailVerifiedAt.Valid {
		t := row.EmailVerifiedAt.Time
		u.EmailVerifiedAt = &t
	}
	if row.DisabledAt.Valid {
		t := row.DisabledAt.Time
		u.DisabledAt = &t
	}
	if row.DisplayName.Valid {
		n := row.DisplayName.String
		u.DisplayName = &n
	}
	if row.TermsAcceptedAt.Valid {
		t := row.TermsAcceptedAt.Time
		u.TermsAcceptedAt = &t
	}
	if row.TermsVersion.Valid {
		v := row.TermsVersion.String
		u.TermsVersion = &v
	}
	if row.OnboardingCompletedAt.Valid {
		t := row.OnboardingCompletedAt.Time
		u.OnboardingCompletedAt = &t
	}
	return u
}
