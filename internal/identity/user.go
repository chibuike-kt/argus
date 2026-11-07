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
	ID              int64
	Email           string
	EmailNormalized string
	PasswordHash    string
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DisabledAt      *time.Time
}

// EmailVerified reports whether the account has completed email
// verification.
func (u User) EmailVerified() bool { return u.EmailVerifiedAt != nil }

// Disabled reports whether the account is disabled.
func (u User) Disabled() bool { return u.DisabledAt != nil }

// NormalizeEmail is the one place "what counts as the same email
// address" is decided: lowercased and trimmed. Used both to compute
// email_normalized on registration and to look up an existing account,
// so the two can never disagree.
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

// Insert creates a new, unverified account. ErrAlreadyExists on a
// duplicate email_normalized -- the unique index is the actual
// guarantee; this translates its violation into a sentinel the caller
// can branch on without parsing a driver error string.
func (s *Store) Insert(ctx context.Context, email, passwordHash string) (User, error) {
	row, err := s.q.InsertUser(ctx, store.InsertUserParams{
		Email: email, EmailNormalized: NormalizeEmail(email), PasswordHash: passwordHash,
	})
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == uniqueViolation {
			return User{}, ErrAlreadyExists
		}
		return User{}, fmt.Errorf("identity: insert user: %w", err)
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
	return u
}
