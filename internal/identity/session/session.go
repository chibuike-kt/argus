// Package session implements opaque server-side sessions (ADR-015
// addendum row 5): creation, resolution (with validity checks),
// sliding idle expiry, and revocation. Postgres is the sole source of
// truth -- nothing here ever consults Redis to decide whether a
// session is valid.
package session

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

// Session is the domain view of a sessions row.
type Session struct {
	ID                int64
	UserID            int64
	CreatedAt         time.Time
	LastSeenAt        time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
	RevokedAt         *time.Time
	UserAgent         string
	IP                string
	LastPasswordAt    time.Time
}

// Revoked reports whether the session has been revoked.
func (s Session) Revoked() bool { return s.RevokedAt != nil }

var (
	// ErrNotFound covers both "no such token" and "no such id for this
	// user" -- the latter deliberately indistinguishable from the
	// former at this layer (see GetForUser's own comment).
	ErrNotFound = errors.New("session: not found")
	// ErrRevoked means the session was found but has been revoked.
	ErrRevoked = errors.New("session: revoked")
	// ErrExpired means the session was found but its idle or absolute
	// expiry has passed.
	ErrExpired = errors.New("session: expired")
	// ErrUnavailable means the database could not be reached at all --
	// distinct from ErrNotFound (a clean "no such row" answer) so the
	// caller returns 503, never treating an unreachable database as
	// either "authenticated" or "not authenticated" (ADR-015: "If the
	// database is unavailable during session lookup, return 503").
	ErrUnavailable = errors.New("session: database unavailable")
)

// Store is sessions' own data-access layer plus the token lifecycle
// (generation, hashing) around it.
type Store struct {
	q                *store.Queries
	clk              clock.Clock
	idleDuration     time.Duration
	absoluteDuration time.Duration
}

// NewStore builds a Store. idleDuration/absoluteDuration come from
// internal/config.AuthPolicy (ADR-015 addendum row 5's own [R]
// defaults, 7 and 30 days) -- never hardcoded here, so a test can use
// shorter durations.
func NewStore(db store.DBTX, clk clock.Clock, idleDuration, absoluteDuration time.Duration) *Store {
	return &Store{q: store.New(db), clk: clk, idleDuration: idleDuration, absoluteDuration: absoluteDuration}
}

// Create makes a new session for userID and returns the raw token
// (base64url of 32 random bytes) alongside the created row. The raw
// token exists only in this return value and in whatever Set-Cookie
// the caller sends -- it is never itself stored; TokenHash
// (sessions.token_hash) is what the table holds.
func (s *Store) Create(ctx context.Context, userID int64, userAgent, ip string) (rawToken string, sess Session, err error) {
	rawToken, err = generateToken()
	if err != nil {
		return "", Session{}, err
	}
	now := s.clk.Now()
	row, err := s.q.InsertSession(ctx, store.InsertSessionParams{
		UserID:            userID,
		TokenHash:         hashToken(rawToken),
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(s.idleDuration),
		AbsoluteExpiresAt: now.Add(s.absoluteDuration),
		UserAgent:         toNullString(userAgent),
		Ip:                toNullString(ip),
		LastPasswordAt:    now,
	})
	if err != nil {
		return "", Session{}, fmt.Errorf("session: create: %w", err)
	}
	return rawToken, fromRow(row), nil
}

// Resolve looks up rawToken and validates it: not found, revoked and
// expired are three distinct, deliberately different errors (the auth
// middleware does not need the distinction to respond -- all three are
// a 401 -- but a test or a log line benefits from knowing which).
// ErrUnavailable means the lookup itself could not run at all.
func (s *Store) Resolve(ctx context.Context, rawToken string) (Session, error) {
	row, err := s.q.GetSessionByTokenHash(ctx, hashToken(rawToken))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	sess := fromRow(row)
	if sess.Revoked() {
		return Session{}, ErrRevoked
	}
	now := s.clk.Now()
	if !now.Before(sess.IdleExpiresAt) || !now.Before(sess.AbsoluteExpiresAt) {
		return Session{}, ErrExpired
	}
	return sess, nil
}

// Touch slides the idle expiry and records last_seen_at. The caller
// (the auth middleware) is responsible for throttling how often this
// actually runs per session (ADR-015: "at most once a minute"), not
// this method.
func (s *Store) Touch(ctx context.Context, sessionID int64) error {
	now := s.clk.Now()
	if err := s.q.TouchSession(ctx, store.TouchSessionParams{
		ID: sessionID, LastSeenAt: now, IdleExpiresAt: now.Add(s.idleDuration),
	}); err != nil {
		return fmt.Errorf("session: touch: %w", err)
	}
	return nil
}

// Revoke revokes sessionID if it belongs to userID. Idempotent:
// revoking an already-revoked session affects zero rows and returns no
// error -- logout's own required idempotence (and DELETE /auth/
// sessions/{id} shares it, having already confirmed ownership via
// GetForUser before calling this).
func (s *Store) Revoke(ctx context.Context, sessionID, userID int64) error {
	if _, err := s.q.RevokeSessionForUser(ctx, store.RevokeSessionForUserParams{
		ID: sessionID, UserID: userID, RevokedAt: sql.NullTime{Time: s.clk.Now(), Valid: true},
	}); err != nil {
		return fmt.Errorf("session: revoke: %w", err)
	}
	return nil
}

// RevokeAllForUser revokes every session belonging to userID
// (logout-all), including the one that made the request -- the brief's
// own "revokes every session of the user," no exception.
func (s *Store) RevokeAllForUser(ctx context.Context, userID int64) error {
	if err := s.q.RevokeAllSessionsForUser(ctx, store.RevokeAllSessionsForUserParams{
		UserID: userID, RevokedAt: sql.NullTime{Time: s.clk.Now(), Valid: true},
	}); err != nil {
		return fmt.Errorf("session: revoke all: %w", err)
	}
	return nil
}

// GetForUser is DELETE /auth/sessions/{id}'s own existence+ownership
// check: ErrNotFound whether sessionID does not exist at all or
// belongs to a different user -- both must produce an identical 404,
// never confirming that someone else's session id is real (the
// brief's own "another user's id is 404, not 403").
func (s *Store) GetForUser(ctx context.Context, sessionID, userID int64) (Session, error) {
	row, err := s.q.GetSessionByIDForUser(ctx, store.GetSessionByIDForUserParams{ID: sessionID, UserID: userID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("session: get for user: %w", err)
	}
	return fromRow(row), nil
}

// ListActiveForUser is GET /auth/sessions' own read path: neither
// revoked nor expired, as of now.
func (s *Store) ListActiveForUser(ctx context.Context, userID int64) ([]Session, error) {
	rows, err := s.q.ListActiveSessionsForUser(ctx, store.ListActiveSessionsForUserParams{
		UserID: userID, IdleExpiresAt: s.clk.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("session: list active for user: %w", err)
	}
	out := make([]Session, len(rows))
	for i, row := range rows {
		out[i] = fromRow(row)
	}
	return out, nil
}

func fromRow(row store.Session) Session {
	s := Session{
		ID:                row.ID,
		UserID:            row.UserID,
		CreatedAt:         row.CreatedAt,
		LastSeenAt:        row.LastSeenAt,
		IdleExpiresAt:     row.IdleExpiresAt,
		AbsoluteExpiresAt: row.AbsoluteExpiresAt,
		UserAgent:         row.UserAgent.String,
		IP:                row.Ip.String,
		LastPasswordAt:    row.LastPasswordAt,
	}
	if row.RevokedAt.Valid {
		t := row.RevokedAt.Time
		s.RevokedAt = &t
	}
	return s
}

func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// generateToken returns 32 random bytes, base64url encoded (ADR-015
// addendum row 5) -- crypto/rand only (this brief's own rule 5).
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is the SHA-256 of the token exactly as issued (the
// base64url string, not the raw bytes decoded back out) -- what the
// client's cookie holds is this same string, so hashing it directly on
// every request needs no decode step. Only this hash is ever stored
// (sessions.token_hash); the raw token is never persisted anywhere.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
