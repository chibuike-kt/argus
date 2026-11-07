// Package service orchestrates registration, login and logout
// (ADR-015 Part 1): the one place internal/identity's password,
// session, tokens, ratelimit and mailer packages are composed, so each
// of those stays small and independently testable while this package
// owns the actual business rules (generic responses, rate limiting,
// rehashing, auditing).
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/mailer"
	"github.com/chibuike-kt/argus/internal/identity/password"
	"github.com/chibuike-kt/argus/internal/identity/ratelimit"
	"github.com/chibuike-kt/argus/internal/identity/session"
	"github.com/chibuike-kt/argus/internal/identity/tokens"
	"github.com/chibuike-kt/argus/internal/store"
)

// ErrInvalidCredentials is Login's one error for "unknown email",
// "wrong password" and "unverified email" alike (ADR-015: "the
// response must not reveal which").
var ErrInvalidCredentials = errors.New("service: invalid credentials")

// RateLimitedError is returned by any method that hit a rate limit
// (ADR-015 addendum row 12). The HTTP layer turns this into 429 with
// Retry-After.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("service: rate limited, retry after %s", e.RetryAfter)
}

// Service is the registration/login/logout orchestrator.
type Service struct {
	users    *identity.Store
	sessions *session.Store
	tokens   *tokens.Store
	hasher   *password.Hasher
	limiter  *ratelimit.Limiter
	mail     mailer.Mailer
	events   *store.Queries
	clk      clock.Clock
	policy   config.AuthPolicy
	logger   *slog.Logger
}

// New builds a Service from its already-constructed collaborators --
// this package does not build any of them itself, so each stays
// independently testable and cmd/argus-api/main.go stays the one
// place they are all wired together.
func New(
	db store.DBTX,
	users *identity.Store,
	sessions *session.Store,
	tokens *tokens.Store,
	hasher *password.Hasher,
	limiter *ratelimit.Limiter,
	mail mailer.Mailer,
	clk clock.Clock,
	policy config.AuthPolicy,
	logger *slog.Logger,
) *Service {
	return &Service{
		users: users, sessions: sessions, tokens: tokens, hasher: hasher,
		limiter: limiter, mail: mail, events: store.New(db),
		clk: clk, policy: policy, logger: logger,
	}
}

// Register validates the password policy, then creates an unverified
// account and sends a verification email -- unless the email is
// already registered, in which case nothing new is created, but the
// caller still gets the same success result either way (ADR-015:
// "Always responds with the same generic 202 body whether the email
// already existed or not").
func (s *Service) Register(ctx context.Context, rawEmail, rawPassword, ip, userAgent string) error {
	email := strings.TrimSpace(rawEmail)
	if err := password.ValidatePolicy(rawPassword, identity.LocalPart(email)); err != nil {
		return err
	}

	result, err := s.limiter.Allow(ctx, "register:ip:"+ip, s.policy.RegisterPerIPPerHour, time.Hour)
	if err != nil {
		return fmt.Errorf("service: register: rate limit: %w", err)
	}
	if !result.Allowed {
		return &RateLimitedError{RetryAfter: result.RetryAfter}
	}

	existing, err := s.users.GetByEmail(ctx, email)
	if err == nil {
		if existing.EmailVerified() {
			// Do not send a verification link to an already-verified
			// account (ADR-015); a "someone tried to register" notice
			// instead, so the real owner learns about the attempt.
			if sendErr := s.mail.Send(ctx, mailer.Message{
				To:      existing.Email,
				Subject: "Someone tried to register with your email",
				Body:    "Someone tried to create an ARGUS account with your email address. If this was not you, no action is needed -- your existing account is unaffected.",
			}); sendErr != nil {
				s.logger.Error("service: send already-registered notice", "user_id", existing.ID, "error", sendErr)
			}
		} else {
			// Not addendum-specified: resending the verification link
			// for an existing, unverified account on a repeat register
			// attempt (the person most likely forgot they already
			// signed up) -- flagged in the report.
			s.sendVerificationEmail(ctx, existing)
		}
		s.recordEvent(ctx, nil, "register", ip, userAgent, map[string]any{"outcome": "existing_email"})
		return nil
	}
	if !errors.Is(err, identity.ErrNotFound) {
		return fmt.Errorf("service: register: look up existing user: %w", err)
	}

	hash, err := s.hasher.Hash(ctx, rawPassword)
	if err != nil {
		return fmt.Errorf("service: register: hash password: %w", err)
	}
	user, err := s.users.Insert(ctx, email, hash)
	if err != nil {
		if errors.Is(err, identity.ErrAlreadyExists) {
			// A concurrent registration won the race between our own
			// GetByEmail and this Insert -- still a success response,
			// same as the "existing" branch above, and still no
			// verification email (we cannot tell from here whether
			// that concurrent registration already sent one; sending a
			// second could just as easily mean the real owner gets two
			// -- accepted as a rare, harmless edge case rather than a
			// reason to make this read-then-write non-atomic any more
			// complex than it already is).
			s.recordEvent(ctx, nil, "register", ip, userAgent, map[string]any{"outcome": "race_existing_email"})
			return nil
		}
		return fmt.Errorf("service: register: insert user: %w", err)
	}

	s.sendVerificationEmail(ctx, user)
	s.recordEvent(ctx, &user.ID, "register", ip, userAgent, map[string]any{"outcome": "created"})
	return nil
}

func (s *Service) sendVerificationEmail(ctx context.Context, u identity.User) {
	rawToken, _, err := s.tokens.Issue(ctx, u.ID, tokens.PurposeVerifyEmail, s.policy.EmailVerificationTTL)
	if err != nil {
		s.logger.Error("service: issue verification token", "user_id", u.ID, "error", err)
		return
	}
	// No frontend route exists yet to build a clickable link against
	// (out of scope for this brief) -- the token itself is what
	// POST /auth/verify-email takes, so the email states that
	// directly rather than inventing a URL shape nothing serves yet.
	body := fmt.Sprintf(
		"Welcome to ARGUS. To verify your email, submit this token to POST /api/v1/auth/verify-email: %s\n\nThis token expires in %s.",
		rawToken, s.policy.EmailVerificationTTL,
	)
	if err := s.mail.Send(ctx, mailer.Message{To: u.Email, Subject: "Verify your ARGUS email", Body: body}); err != nil {
		s.logger.Error("service: send verification email", "user_id", u.ID, "error", err)
	}
}

// VerifyEmail consumes a single-use verification token and marks the
// account verified. Does not log the user in (ADR-015).
func (s *Service) VerifyEmail(ctx context.Context, rawToken, ip, userAgent string) error {
	tok, err := s.tokens.Consume(ctx, rawToken, tokens.PurposeVerifyEmail)
	if err != nil {
		return err
	}
	if _, err := s.users.MarkEmailVerified(ctx, tok.UserID, s.clk.Now()); err != nil {
		return fmt.Errorf("service: verify email: %w", err)
	}
	s.recordEvent(ctx, &tok.UserID, "verify_email", ip, userAgent, nil)
	return nil
}

// ResendVerification is rate limited per account and per IP, and
// always succeeds from the caller's point of view regardless of
// whether the email exists (ADR-015).
func (s *Service) ResendVerification(ctx context.Context, rawEmail, ip, userAgent string) error {
	email := strings.TrimSpace(rawEmail)

	ipResult, err := s.limiter.Allow(ctx, "resend:ip:"+ip, s.policy.ResendVerificationPerIPPerHour, time.Hour)
	if err != nil {
		return fmt.Errorf("service: resend verification: rate limit: %w", err)
	}
	if !ipResult.Allowed {
		return &RateLimitedError{RetryAfter: ipResult.RetryAfter}
	}

	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("service: resend verification: look up user: %w", err)
	}

	accountResult, err := s.limiter.Allow(ctx, fmt.Sprintf("resend:account:%d", user.ID), s.policy.ResendVerificationPerAccountPerHour, time.Hour)
	if err != nil {
		return fmt.Errorf("service: resend verification: rate limit: %w", err)
	}
	if !accountResult.Allowed {
		return &RateLimitedError{RetryAfter: accountResult.RetryAfter}
	}

	if user.EmailVerified() {
		return nil
	}
	s.sendVerificationEmail(ctx, user)
	s.recordEvent(ctx, &user.ID, "resend_verification", ip, userAgent, nil)
	return nil
}

// Login verifies credentials and, on success, creates a session.
// Unknown email, wrong password and an unverified account all return
// the identical ErrInvalidCredentials (ADR-015: "the response must not
// reveal which"); an unknown email still pays the full Argon2 cost via
// VerifyDummy, so the response time does not reveal it either.
func (s *Service) Login(ctx context.Context, rawEmail, rawPassword, ip, userAgent string) (rawSessionToken string, user identity.User, err error) {
	email := strings.TrimSpace(rawEmail)

	ipResult, err := s.limiter.Allow(ctx, "login:ip:"+ip, s.policy.LoginAttemptsPerIPPer15Min, 15*time.Minute)
	if err != nil {
		return "", identity.User{}, fmt.Errorf("service: login: rate limit: %w", err)
	}
	if !ipResult.Allowed {
		return "", identity.User{}, &RateLimitedError{RetryAfter: ipResult.RetryAfter}
	}

	u, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, identity.ErrNotFound) {
			return "", identity.User{}, fmt.Errorf("service: login: look up user: %w", err)
		}
		if verr := s.hasher.VerifyDummy(ctx, rawPassword); verr != nil {
			s.logger.Warn("service: login: dummy verify failed", "error", verr)
		}
		s.recordEvent(ctx, nil, "login_failure", ip, userAgent, map[string]any{"reason": "unknown_email"})
		return "", identity.User{}, ErrInvalidCredentials
	}

	// Progressive delay past LoginFailuresPerAccountPer15Min
	// (ADR-015 addendum row 12: "no hard lockout") -- the formula
	// itself (LoginDelayStep/LoginDelayMax) is this brief's own
	// choice, not the addendum's; see internal/config/auth.go's own
	// doc comment. Counted and delayed before the password check so a
	// rapid attacker is slowed down on their very next attempt, not
	// only after it fails; reset on success below.
	failureKey := fmt.Sprintf("login:account:%d", u.ID)
	if count, cerr := s.limiter.IncrementAndCount(ctx, failureKey, 15*time.Minute); cerr != nil {
		s.logger.Warn("service: login: delay counter", "error", cerr)
	} else if count > s.policy.LoginFailuresPerAccountPer15Min {
		delay := min(time.Duration(count-s.policy.LoginFailuresPerAccountPer15Min)*s.policy.LoginDelayStep, s.policy.LoginDelayMax)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", identity.User{}, ctx.Err()
		}
	}

	ok, err := s.hasher.Verify(ctx, rawPassword, u.PasswordHash)
	if err != nil {
		return "", identity.User{}, fmt.Errorf("service: login: verify password: %w", err)
	}
	if !ok {
		s.recordEvent(ctx, &u.ID, "login_failure", ip, userAgent, map[string]any{"reason": "wrong_password"})
		return "", identity.User{}, ErrInvalidCredentials
	}
	if !u.EmailVerified() {
		s.recordEvent(ctx, &u.ID, "login_failure", ip, userAgent, map[string]any{"reason": "email_unverified"})
		return "", identity.User{}, ErrInvalidCredentials
	}

	if err := s.limiter.Reset(ctx, failureKey); err != nil {
		s.logger.Warn("service: login: reset delay counter", "error", err)
	}

	if needs, nerr := s.hasher.NeedsRehash(u.PasswordHash); nerr == nil && needs {
		if newHash, herr := s.hasher.Hash(ctx, rawPassword); herr == nil {
			if uerr := s.users.UpdatePasswordHash(ctx, u.ID, newHash, s.clk.Now()); uerr != nil {
				s.logger.Error("service: rehash on login", "user_id", u.ID, "error", uerr)
			}
		} else {
			s.logger.Error("service: rehash on login: hash", "user_id", u.ID, "error", herr)
		}
	}

	token, _, err := s.sessions.Create(ctx, u.ID, userAgent, ip)
	if err != nil {
		return "", identity.User{}, fmt.Errorf("service: login: create session: %w", err)
	}

	s.recordEvent(ctx, &u.ID, "login_success", ip, userAgent, nil)
	return token, u, nil
}

// Logout revokes sessionID if owned by userID. Idempotent.
func (s *Service) Logout(ctx context.Context, sessionID, userID int64, ip, userAgent string) error {
	if err := s.sessions.Revoke(ctx, sessionID, userID); err != nil {
		return fmt.Errorf("service: logout: %w", err)
	}
	s.recordEvent(ctx, &userID, "logout", ip, userAgent, nil)
	return nil
}

// LogoutAll revokes every session belonging to userID.
func (s *Service) LogoutAll(ctx context.Context, userID int64, ip, userAgent string) error {
	if err := s.sessions.RevokeAllForUser(ctx, userID); err != nil {
		return fmt.Errorf("service: logout all: %w", err)
	}
	s.recordEvent(ctx, &userID, "logout_all", ip, userAgent, nil)
	return nil
}

// ListSessions returns userID's active sessions (GET /auth/sessions).
func (s *Service) ListSessions(ctx context.Context, userID int64) ([]session.Session, error) {
	return s.sessions.ListActiveForUser(ctx, userID)
}

// RevokeSession is DELETE /auth/sessions/{id}: session.ErrNotFound
// (surfaced unchanged) covers both "no such session" and "not yours",
// which the HTTP layer maps to an identical 404.
func (s *Service) RevokeSession(ctx context.Context, sessionID, userID int64, ip, userAgent string) error {
	if _, err := s.sessions.GetForUser(ctx, sessionID, userID); err != nil {
		return err
	}
	if err := s.sessions.Revoke(ctx, sessionID, userID); err != nil {
		return fmt.Errorf("service: revoke session: %w", err)
	}
	s.recordEvent(ctx, &userID, "session_revoke", ip, userAgent, map[string]any{"session_id": sessionID})
	return nil
}

// recordEvent writes an auth_events row. Never returns an error to its
// caller -- an audit-log write failure must not block the operation it
// is describing; it is logged instead. detail must never contain a
// password, token, TOTP secret or code (CLAUDE.md rule 3 / this
// brief's own rule 3); every call site above passes only ids and fixed
// reason strings, never raw user input.
func (s *Service) recordEvent(ctx context.Context, userID *int64, kind, ip, userAgent string, detail map[string]any) {
	var detailJSON json.RawMessage
	if detail == nil {
		detailJSON = json.RawMessage(`{}`)
	} else if b, err := json.Marshal(detail); err == nil {
		detailJSON = b
	} else {
		detailJSON = json.RawMessage(`{}`)
	}
	var uid sql.NullInt64
	if userID != nil {
		uid = sql.NullInt64{Int64: *userID, Valid: true}
	}
	if err := s.events.InsertAuthEvent(ctx, store.InsertAuthEventParams{
		At: s.clk.Now(), UserID: uid, Kind: kind,
		Ip: toNullString(ip), UserAgent: toNullString(userAgent), Detail: detailJSON,
	}); err != nil {
		s.logger.Error("service: write auth event", "kind", kind, "error", err)
	}
}

func toNullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}
