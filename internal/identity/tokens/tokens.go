// Package tokens implements single-use email tokens (verification now,
// password reset in Part 2) -- ADR-015 addendum row 12.
package tokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/store"
)

// Purpose is the email_tokens.purpose CHECK constraint's own two
// values.
type Purpose string

const (
	// PurposeVerifyEmail marks a token issued for email verification.
	PurposeVerifyEmail Purpose = "verify_email"
	// PurposeResetPassword marks a token issued for a password reset
	// (Part 2).
	PurposeResetPassword Purpose = "reset_password"
)

// Token is the domain view of an email_tokens row.
type Token struct {
	ID        int64
	UserID    int64
	Purpose   Purpose
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// ErrInvalid covers "does not exist", "already used" and "expired"
// alike -- Consume cannot and should not distinguish them for the
// caller (an attacker probing which applies would learn something;
// the legitimate caller never needs to know, just that the token did
// not work).
var ErrInvalid = errors.New("tokens: invalid, already used, or expired")

// Store is email_tokens' own data-access layer plus the token
// lifecycle (generation, hashing) around it.
type Store struct {
	q   *store.Queries
	clk clock.Clock
}

// NewStore builds a Store.
func NewStore(db store.DBTX, clk clock.Clock) *Store {
	return &Store{q: store.New(db), clk: clk}
}

// Issue creates a new single-use token for userID and purpose,
// expiring after ttl, and returns the raw token (never stored --
// token_hash is).
func (s *Store) Issue(ctx context.Context, userID int64, purpose Purpose, ttl time.Duration) (rawToken string, tok Token, err error) {
	rawToken, err = generateToken()
	if err != nil {
		return "", Token{}, err
	}
	now := s.clk.Now()
	row, err := s.q.InsertEmailToken(ctx, store.InsertEmailTokenParams{
		UserID: userID, Purpose: string(purpose), TokenHash: hashToken(rawToken),
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	})
	if err != nil {
		return "", Token{}, fmt.Errorf("tokens: issue: %w", err)
	}
	return rawToken, fromRow(row), nil
}

// Consume atomically marks rawToken used for purpose and returns the
// row, only if it exists, has not been used yet, and has not expired
// (one UPDATE ... RETURNING, not a separate check-then-mark pair --
// see ConsumeEmailToken's own comment in db/queries for why this
// matters). ErrInvalid for every failure case; see its own doc comment
// for why none are distinguished.
func (s *Store) Consume(ctx context.Context, rawToken string, purpose Purpose) (Token, error) {
	now := s.clk.Now()
	row, err := s.q.ConsumeEmailToken(ctx, store.ConsumeEmailTokenParams{
		TokenHash: hashToken(rawToken), Purpose: string(purpose),
		ExpiresAt: now, UsedAt: sql.NullTime{Time: now, Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Token{}, ErrInvalid
		}
		return Token{}, fmt.Errorf("tokens: consume: %w", err)
	}
	return fromRow(row), nil
}

func fromRow(row store.EmailToken) Token {
	t := Token{
		ID:        row.ID,
		UserID:    row.UserID,
		Purpose:   Purpose(row.Purpose),
		CreatedAt: row.CreatedAt,
		ExpiresAt: row.ExpiresAt,
	}
	if row.UsedAt.Valid {
		used := row.UsedAt.Time
		t.UsedAt = &used
	}
	return t
}

// generateToken returns 32 random bytes, base64url encoded (the same
// shape session tokens take, ADR-015 addendum rows 5 and 12 both
// specify it) -- crypto/rand only.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("tokens: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is the SHA-256 of the token exactly as issued (the
// base64url string) -- only this hash is ever stored.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
