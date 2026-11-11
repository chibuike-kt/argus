// Package service orchestrates signup, login and logout (ADR-015
// "Signup and onboarding"): the one place internal/identity's
// password, session, tokens, signupcode, ratelimit and mailer packages
// are composed, so each of those stays small and independently
// testable while this package owns the actual business rules (generic
// responses, rate limiting, rehashing, auditing).
package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	"github.com/chibuike-kt/argus/internal/identity/signupcode"
	"github.com/chibuike-kt/argus/internal/identity/tokens"
	"github.com/chibuike-kt/argus/internal/store"
)

// mailSendTimeout bounds a backgrounded mail send (architect review
// item 1). An implementation limit on how long a fire-and-forget
// goroutine may run, not an ADR-015 addendum policy value, so it lives
// here next to its only use rather than in config.AuthPolicy.
const mailSendTimeout = 10 * time.Second

// ErrInvalidCredentials is Login's one error for "unknown email",
// "wrong password" and "unverified email" alike (ADR-015: "the
// response must not reveal which").
var ErrInvalidCredentials = errors.New("service: invalid credentials")

// ErrTermsNotAccepted is SignupStart's own rejection when
// accepted_terms is not true (ADR-015 "Signup and onboarding":
// "Signup start requires accepted_terms: true"). The HTTP layer maps
// this to 400, not a generic response -- unlike an unknown/known
// email, whether terms were accepted discloses nothing about any
// account, so there is nothing to protect by making this generic.
var ErrTermsNotAccepted = errors.New("service: accepted_terms must be true")

// RateLimitedError is returned by any method that hit a rate limit
// (ADR-015 addendum row 12). The HTTP layer turns this into 429 with
// Retry-After.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("service: rate limited, retry after %s", e.RetryAfter)
}

// watchlistCounter is the slice of *internal/watchlist.Store
// CompleteOnboarding actually needs -- a consumer-defined interface
// (this brief's own rule 7) so this package does not import
// internal/watchlist just for a type name, and so a test can satisfy
// it with a hand-written fake instead of a real Postgres connection.
type watchlistCounter interface {
	Count(ctx context.Context, userID int64) (int, error)
}

// Service is the signup/login/logout orchestrator.
type Service struct {
	db        *sql.DB
	users     *identity.Store
	sessions  *session.Store
	tokens    *tokens.Store
	signup    *signupcode.Store
	watchlist watchlistCounter
	hasher    *password.Hasher
	limiter   *ratelimit.Limiter
	mail      mailer.Mailer
	events    *store.Queries
	clk       clock.Clock
	policy    config.AuthPolicy
	logger    *slog.Logger
}

// New builds a Service from its already-constructed collaborators --
// this package does not build any of them itself, so each stays
// independently testable and cmd/argus-api/main.go stays the one
// place they are all wired together. tokens is kept for the password
// reset flow (internal/identity/tokens' own doc comment) even though
// nothing in this package calls it yet; db is kept (not just wrapped
// into store.Queries) for the same future transaction-spanning need
// VerifyEmail used to have (architect review item 9).
func New(
	db *sql.DB,
	users *identity.Store,
	sessions *session.Store,
	tokens *tokens.Store,
	signup *signupcode.Store,
	watchlist watchlistCounter,
	hasher *password.Hasher,
	limiter *ratelimit.Limiter,
	mail mailer.Mailer,
	clk clock.Clock,
	policy config.AuthPolicy,
	logger *slog.Logger,
) *Service {
	return &Service{
		db: db, users: users, sessions: sessions, tokens: tokens,
		signup: signup, watchlist: watchlist, hasher: hasher,
		limiter: limiter, mail: mail, events: store.New(db),
		clk: clk, policy: policy, logger: logger,
	}
}

// SignupStart begins or restarts a signup: sends a 6-digit emailed
// code to a new email, or a notice to an existing account's email
// (ADR-015 "Signup and onboarding"). Always the same generic success
// from the caller's point of view, with equal timing -- neither branch
// does the kind of expensive work (Argon2 hashing) Register used to
// need VerifyDummy to match, so there is no separate dummy-cost call
// here; both branches do one user lookup plus one rate-limit check
// plus one async send, the same shape either way.
func (s *Service) SignupStart(ctx context.Context, rawEmail string, acceptedTerms bool, ip, userAgent string) error {
	if !acceptedTerms {
		return ErrTermsNotAccepted
	}
	email := strings.TrimSpace(rawEmail)

	ipResult, err := s.limiter.Allow(ctx, "signup:start:ip:"+ip, s.policy.SignupStartsPerIPPerHour, time.Hour)
	if err != nil {
		return fmt.Errorf("service: signup start: ip rate limit: %w", err)
	}
	if !ipResult.Allowed {
		return &RateLimitedError{RetryAfter: ipResult.RetryAfter}
	}

	// Per-email send limit (ADR-015: "At most 5 codes per email per
	// hour"), shared by both branches below. Checked once, before
	// either branch, and always folded into the same generic success
	// on exceed -- never surfaced as 429 for the new-email branch
	// either (not addendum-specified in so many words for that
	// branch; applied symmetrically here, flagged in the report):
	// a 429 that only ever happened on one branch would itself
	// disclose, after enough probes, whether a guessed email already
	// has an account -- exactly the oracle this whole ADR closes
	// everywhere else.
	sendAllowed := s.emailSendAllowed(ctx, email)

	existing, err := s.users.GetByEmail(ctx, email)
	if err == nil {
		if sendAllowed {
			s.sendAsync(ctx, mailer.Message{
				To:      existing.Email,
				Subject: "Someone tried to sign up with your email",
				Body:    "Someone tried to create an ARGUS account with your email address. If this was not you, no action is needed -- your existing account is unaffected.",
			})
		}
		s.recordEvent(ctx, &existing.ID, "signup_start", ip, userAgent, map[string]any{"outcome": "existing_email"})
		return nil
	}
	if !errors.Is(err, identity.ErrNotFound) {
		return fmt.Errorf("service: signup start: look up existing user: %w", err)
	}

	if !sendAllowed {
		s.recordEvent(ctx, nil, "signup_start", ip, userAgent, map[string]any{"outcome": "code_send_limited"})
		return nil
	}

	now := s.clk.Now()
	code, err := s.signup.IssueCode(ctx, email, s.policy.SignupCodeResendCooldown, s.policy.SignupCodeTTL, now, s.policy.TermsVersion)
	if err != nil {
		var tooSoon *signupcode.TooSoonError
		if errors.As(err, &tooSoon) {
			// Architect review (of Part 2) item 1: this WAS mapped to
			// 429, on the reasoning that the cooldown only ever fires
			// for an email with a signup already in progress, and an
			// existing account's email never reaches IssueCode at all.
			// That reasoning was wrong -- it is exactly the asymmetry
			// that makes it an oracle: call start twice within 60s and
			// a new email 429s on the second call while an existing
			// one always 202s, so the two are distinguishable after
			// exactly two requests. No new code is issued or sent; the
			// response is the same silent generic success the per-
			// email send limit above already uses.
			s.recordEvent(ctx, nil, "signup_start", ip, userAgent, map[string]any{"outcome": "resend_cooldown"})
			return nil
		}
		return fmt.Errorf("service: signup start: issue code: %w", err)
	}

	s.sendAsync(ctx, mailer.Message{
		To:      email,
		Subject: "Your ARGUS signup code",
		Body:    fmt.Sprintf("Your ARGUS signup code is %s. It expires in %s.", code, s.policy.SignupCodeTTL),
	})
	s.recordEvent(ctx, nil, "signup_start", ip, userAgent, map[string]any{"outcome": "code_sent"})
	return nil
}

// emailSendAllowed is SignupStart's own per-email send limit (ADR-015:
// "5 codes per email per hour"), keyed on the SHA-256 of the
// normalized email -- never the raw address in a Redis key/log, the
// same convention loginFailureKey already uses. Fails open on a
// Limiter error, the same defensive (not a real escape hatch, see
// ratelimit.Limiter's own fallback) posture the old accountSendAllowed
// used.
func (s *Service) emailSendAllowed(ctx context.Context, email string) bool {
	result, err := s.limiter.Allow(ctx, "signup:send:email:"+emailHashKey(email), s.policy.SignupCodesPerEmailPerHour, time.Hour)
	if err != nil {
		s.logger.Warn("service: signup send rate limit", "error", err)
		return true
	}
	return result.Allowed
}

// sendAsync sends msg in the background (architect review item 1):
// SignupStart's own response time must not depend on how long the
// mail provider takes to accept a message, or a slow provider
// reintroduces the timing oracle this method otherwise closes.
// context.WithoutCancel keeps whatever request-scoped values ctx
// carries (e.g. a trace id) while detaching from its cancellation, so
// the HTTP response returning (and so the request's own context being
// cancelled) does not abort the send; mailSendTimeout bounds it
// instead so a hung provider cannot leak goroutines forever.
func (s *Service) sendAsync(ctx context.Context, msg mailer.Message) {
	bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mailSendTimeout)
	go func() {
		defer cancel()
		if err := s.mail.Send(bgCtx, msg); err != nil {
			s.logger.Error("service: send mail async", "to", msg.To, "subject", msg.Subject, "error", err)
		}
	}()
}

// SignupVerify checks a 6-digit code and, on success, returns a
// single-use signup ticket (ADR-015). signupcode.ErrInvalidCode
// propagates unchanged -- the HTTP layer maps it to the one generic
// invalid_code response the ADR requires, never distinguishing why.
func (s *Service) SignupVerify(ctx context.Context, rawEmail, code, ip, userAgent string) (ticket string, err error) {
	email := strings.TrimSpace(rawEmail)
	ticket, err = s.signup.VerifyCode(ctx, email, code, s.policy.SignupCodeMaxWrongGuesses, s.policy.SignupTicketTTL)
	if err != nil {
		if errors.Is(err, signupcode.ErrInvalidCode) {
			s.recordEvent(ctx, nil, "signup_verify_failure", ip, userAgent, nil)
			return "", err
		}
		return "", fmt.Errorf("service: signup verify: %w", err)
	}
	s.recordEvent(ctx, nil, "signup_verify_success", ip, userAgent, nil)
	return ticket, nil
}

// SignupComplete consumes a signup ticket and creates the account,
// already verified (ADR-015: "no half accounts" -- this is the one
// and only place a users row is created for this flow). On success,
// logs the new account in, the same as Login.
func (s *Service) SignupComplete(ctx context.Context, rawTicket, rawPassword, ip, userAgent string) (rawSessionToken string, user identity.User, err error) {
	// Architect review (of Part 2) item 2: this used to consume the
	// ticket first, then validate the password -- a weak password (or
	// any other failure past this point) left the ticket already
	// spent, forcing the user back to a brand new emailed code even
	// though nothing about the code/ticket itself was wrong. Read-only
	// lookup first, so a rejected password never burns the ticket.
	verification, err := s.signup.GetByTicket(ctx, rawTicket)
	if err != nil {
		if errors.Is(err, signupcode.ErrInvalidTicket) {
			return "", identity.User{}, err
		}
		return "", identity.User{}, fmt.Errorf("service: signup complete: get ticket: %w", err)
	}

	if err := password.ValidatePolicy(rawPassword, identity.LocalPart(verification.Email)); err != nil {
		return "", identity.User{}, err
	}

	// Hashed before the transaction below opens: Argon2's own cost must
	// not hold a database transaction open for as long as the hash
	// takes (architect review item 2).
	hash, err := s.hasher.Hash(ctx, rawPassword)
	if err != nil {
		return "", identity.User{}, fmt.Errorf("service: signup complete: hash password: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", identity.User{}, fmt.Errorf("service: signup complete: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Consuming the ticket and creating the user share one transaction
	// (architect review item 2): a failed insert now rolls the
	// ticket's consumption back too, instead of permanently burning it
	// for a failure that had nothing to do with the code/ticket.
	consumed, err := s.signup.WithTx(tx).ConsumeTicket(ctx, rawTicket)
	if err != nil {
		if errors.Is(err, signupcode.ErrInvalidTicket) {
			return "", identity.User{}, err
		}
		return "", identity.User{}, fmt.Errorf("service: signup complete: consume ticket: %w", err)
	}

	now := s.clk.Now()
	user, err = s.users.WithTx(tx).InsertVerified(ctx, consumed.Email, hash, now, consumed.TermsAcceptedAt, consumed.TermsVersion)
	if err != nil {
		if errors.Is(err, identity.ErrAlreadyExists) {
			// Architect review item 5: a user can run start/verify/
			// complete in one tab while a second tab does the same for
			// the same email with its own, separately issued ticket --
			// each ticket is bound to its own signup_verifications row
			// (ADR-015), so both can reach here, and only one Insert
			// wins the email_normalized unique constraint. The loser's
			// ticket is still consumed (rolled back below along with
			// the rest of this transaction) -- mapped to the same
			// generic invalid_ticket the HTTP layer already uses for
			// every other ticket failure, never a 500, and never
			// disclosing that the email now belongs to an account.
			return "", identity.User{}, signupcode.ErrInvalidTicket
		}
		return "", identity.User{}, fmt.Errorf("service: signup complete: insert user: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", identity.User{}, fmt.Errorf("service: signup complete: commit: %w", err)
	}

	token, _, err := s.sessions.Create(ctx, user.ID, userAgent, ip)
	if err != nil {
		return "", identity.User{}, fmt.Errorf("service: signup complete: create session: %w", err)
	}

	s.recordEvent(ctx, &user.ID, "signup_complete", ip, userAgent, nil)
	return token, user, nil
}

// UpdateProfile sets userID's display name (ADR-015 "Signup and
// onboarding"), after validating it.
func (s *Service) UpdateProfile(ctx context.Context, userID int64, rawDisplayName, ip, userAgent string) (identity.User, error) {
	name, err := identity.ValidateDisplayName(rawDisplayName, s.policy.DisplayNameMaxLength)
	if err != nil {
		return identity.User{}, err
	}
	user, err := s.users.SetDisplayName(ctx, userID, name, s.clk.Now())
	if err != nil {
		return identity.User{}, fmt.Errorf("service: update profile: %w", err)
	}
	s.recordEvent(ctx, &userID, "profile_update", ip, userAgent, nil)
	return user, nil
}

// ErrOnboardingIncomplete is CompleteOnboarding's own rejection when
// userID is missing a display name or has no watchlist item yet
// (ADR-015: "Onboarding is complete only with a display name and at
// least one watchlist item").
var ErrOnboardingIncomplete = errors.New("service: onboarding requires a display name and at least one watchlist item")

// CompleteOnboarding records onboarding_completed_at, but only once
// userID already has a display name and at least one watchlist item
// (ADR-015) -- checked here, in one visible place, rather than
// duplicated as a SQL WHERE clause (internal/identity.Store's own
// MarkOnboardingCompleted comment).
func (s *Service) CompleteOnboarding(ctx context.Context, userID int64, ip, userAgent string) (identity.User, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return identity.User{}, fmt.Errorf("service: complete onboarding: look up user: %w", err)
	}
	if user.DisplayName == nil {
		return identity.User{}, ErrOnboardingIncomplete
	}
	count, err := s.watchlist.Count(ctx, userID)
	if err != nil {
		return identity.User{}, fmt.Errorf("service: complete onboarding: count watchlist: %w", err)
	}
	if count == 0 {
		return identity.User{}, ErrOnboardingIncomplete
	}

	user, err = s.users.MarkOnboardingCompleted(ctx, userID, s.clk.Now())
	if err != nil {
		return identity.User{}, fmt.Errorf("service: complete onboarding: %w", err)
	}
	s.recordEvent(ctx, &userID, "onboarding_complete", ip, userAgent, nil)
	return user, nil
}

// emailHashKey is the SHA-256 hex digest of the normalized email --
// the same "never a raw email in a Redis key" convention
// loginFailureKey uses, factored out since SignupStart needs it too.
func emailHashKey(email string) string {
	sum := sha256.Sum256([]byte(identity.NormalizeEmail(email)))
	return hex.EncodeToString(sum[:])
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

	// Architect review item 2: keyed and applied on the normalized
	// email's own hash, before the user lookup, so an unknown email
	// accrues the same counter and the same progressive delay a known
	// one would -- the delay itself was measured as an oracle
	// (unknown emails never slowed down, known ones did after 5
	// failures). Reset on success below, same as before.
	failureKey := loginFailureKey(email)
	if count, cerr := s.limiter.IncrementAndCount(ctx, failureKey, 15*time.Minute); cerr != nil {
		s.logger.Warn("service: login: delay counter", "error", cerr)
	} else if count > s.policy.LoginFailuresPerAccountPer15Min {
		// Formula (LoginDelayStep/LoginDelayMax) is this brief's own
		// choice, not the addendum's (ADR-015 addendum row 12: "no hard
		// lockout" is all it specifies); see internal/config/auth.go's
		// own doc comment.
		delay := min(time.Duration(count-s.policy.LoginFailuresPerAccountPer15Min)*s.policy.LoginDelayStep, s.policy.LoginDelayMax)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", identity.User{}, ctx.Err()
		}
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

	// Architect review item 4: an oversize password must not reach
	// Argon2 at all (a cheap way to burn CPU with a multi-megabyte
	// password otherwise) -- VerifyDummy still pays the usual cost so
	// this does not become its own, faster-rejection oracle.
	if len([]rune(rawPassword)) > s.policy.PasswordMaxLength {
		if verr := s.hasher.VerifyDummy(ctx, rawPassword); verr != nil {
			s.logger.Warn("service: login: dummy verify failed", "error", verr)
		}
		s.recordEvent(ctx, &u.ID, "login_failure", ip, userAgent, map[string]any{"reason": "password_too_long"})
		return "", identity.User{}, ErrInvalidCredentials
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
	// Architect review item 3: disabled_at existed but nothing checked
	// it -- a disabled account could still log in, and an existing
	// session kept working (checked separately in the auth
	// middleware). Checked after the password check so a disabled
	// account's login takes exactly as long as an enabled one's wrong-
	// password case, same generic error either way.
	if u.Disabled() {
		s.recordEvent(ctx, &u.ID, "login_failure", ip, userAgent, map[string]any{"reason": "account_disabled"})
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

// loginFailureKey is the progressive-login-delay counter's own key
// (architect review item 2): the SHA-256 hex digest of the normalized
// email, never the user id -- computed and incremented before the
// user lookup even runs, so an unknown email accrues exactly the same
// counter, under exactly the same key shape, as a known one. Hashing
// (rather than using the normalized email directly) keeps a raw email
// address out of Redis keys/logs, consistent with this package's own
// "never log a secret" posture even though an email is not itself a
// password or token.
func loginFailureKey(normalizedEmail string) string {
	sum := sha256.Sum256([]byte(identity.NormalizeEmail(normalizedEmail)))
	return "login:account:" + hex.EncodeToString(sum[:])
}

func toNullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}
