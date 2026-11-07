//go:build integration

package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/mailer"
	"github.com/chibuike-kt/argus/internal/identity/password"
	"github.com/chibuike-kt/argus/internal/identity/ratelimit"
	"github.com/chibuike-kt/argus/internal/identity/session"
	"github.com/chibuike-kt/argus/internal/identity/tokens"

	_ "github.com/lib/pq"
)

// Run with (needs a real Postgres with all migrations applied, and a
// real Redis):
//
//	go test -tags=integration ./internal/identity/service/...
//
// testDB defaults to a separate argus_test database, never the dev
// argus database (this brief's own rule); testRedis uses logical
// database 15, distinct from the one the rest of ARGUS uses. Both can
// be overridden with ARGUS_TEST_DATABASE_URL / ARGUS_TEST_REDIS_URL.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ARGUS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://argus:argus@localhost:5432/argus_test?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("integration test database unreachable: %v", err)
	}
	return db
}

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("ARGUS_TEST_REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379/15"
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := redis.NewClient(opt)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("integration test redis unreachable: %v", err)
	}
	return client
}

// recordingMailer is a test double for mailer.Mailer: every Send call
// is appended, under a mutex, so a test can assert what Register/
// ResendVerification actually sent without a real mail provider.
type recordingMailer struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (m *recordingMailer) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *recordingMailer) messagesTo(email string) []mailer.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []mailer.Message
	for _, msg := range m.sent {
		if msg.To == email {
			out = append(out, msg)
		}
	}
	return out
}

// waitForMessages polls messagesTo until it has at least n messages
// for email or 2s pass -- Register/ResendVerification now send mail
// in the background (architect review item 1: the send must not
// affect response time), so a caller can no longer assume a message
// has arrived the instant Register/ResendVerification returns.
func waitForMessages(t *testing.T, mail *recordingMailer, email string, n int) []mailer.Message {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		msgs := mail.messagesTo(email)
		if len(msgs) >= n {
			return msgs
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d messages to %s after 2s, want at least %d", len(msgs), email, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// testPolicy returns cheap Argon2 parameters (this brief's own
// "cheaper Argon2 params in tests through config only") and generous
// rate limits, so a test exercises real business logic without paying
// production hashing cost or tripping a limit the test itself did not
// intend to test.
func testPolicy() config.AuthPolicy {
	return config.AuthPolicy{
		ArgonMemoryKiB: 8, ArgonIterations: 1, ArgonParallelism: 1,
		ArgonSaltLen: 16, ArgonKeyLen: 32, ArgonMaxConcurrent: 4,
		PasswordMinLength: 12, PasswordMaxLength: 128,
		SessionIdleDuration: 7 * 24 * time.Hour, SessionAbsoluteDuration: 30 * 24 * time.Hour,
		EmailVerificationTTL: 24 * time.Hour, PasswordResetTTL: time.Hour,
		LoginFailuresPerAccountPer15Min: 5, LoginAttemptsPerIPPer15Min: 1000,
		RegisterPerIPPerHour: 1000, ResendVerificationPerAccountPerHour: 1000, ResendVerificationPerIPPerHour: 1000,
		LoginDelayStep: time.Millisecond, LoginDelayMax: 10 * time.Millisecond,
	}
}

type testService struct {
	*Service
	mail   *recordingMailer
	clk    *clock.Mutable
	db     *sql.DB
	sess   *session.Store
	hasher *password.Hasher
	logBuf *bytes.Buffer
}

func newTestService(t *testing.T) *testService {
	t.Helper()
	db := testDB(t)
	rdb := testRedis(t)
	clk := clock.NewMutable(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	policy := testPolicy()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	users := identity.NewStore(db)
	sessions := session.NewStore(db, clk, policy.SessionIdleDuration, policy.SessionAbsoluteDuration)
	tokenStore := tokens.NewStore(db, clk)
	hasher := password.NewHasher(password.Params{
		MemoryKiB: policy.ArgonMemoryKiB, Iterations: policy.ArgonIterations,
		Parallelism: policy.ArgonParallelism, SaltLen: policy.ArgonSaltLen, KeyLen: policy.ArgonKeyLen,
	}, policy.ArgonMaxConcurrent)
	limiter := ratelimit.NewLimiter(rdb, logger, clk)
	mail := &recordingMailer{}

	svc := New(db, users, sessions, tokenStore, hasher, limiter, mail, clk, policy, "http://localhost:5173", logger)
	return &testService{Service: svc, mail: mail, clk: clk, db: db, sess: sessions, hasher: hasher, logBuf: &buf}
}

// emailCounter guarantees uniqueEmail never collides even when called
// twice in the same test: time.Now().UnixNano() alone is not a
// reliable discriminator on every OS/clock (observed colliding on two
// calls a few nanoseconds apart).
var emailCounter atomic.Int64

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("svc-test-%s-%d-%d@example.com", strings.ToLower(t.Name()), time.Now().UnixNano(), emailCounter.Add(1))
}

func countAuthEvents(t *testing.T, db *sql.DB, userID int64, kind string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM auth_events WHERE user_id = $1 AND kind = $2`, userID, kind).Scan(&n); err != nil {
		t.Fatalf("count auth_events: %v", err)
	}
	return n
}

const testPassword = "a-perfectly-fine-password-1"

func TestIntegration_Register_NewEmail_SendsVerification(t *testing.T) {
	ts := newTestService(t)
	email := uniqueEmail(t)
	ctx := context.Background()

	if err := ts.Register(ctx, email, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	u, err := identity.NewStore(ts.db).GetByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if u.EmailVerified() {
		t.Fatal("newly registered user is already verified, want unverified")
	}

	msgs := waitForMessages(t, ts.mail, email, 1)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages to %s, want 1", len(msgs), email)
	}
	if !strings.Contains(msgs[0].Body, "verify") {
		t.Fatalf("verification email body = %q, want it to mention verification", msgs[0].Body)
	}
	if countAuthEvents(t, ts.db, u.ID, "register") != 1 {
		t.Fatal("expected one register auth_events row")
	}
}

func TestIntegration_Register_ExistingVerifiedEmail_SendsNoticeNotToken(t *testing.T) {
	ts := newTestService(t)
	email := uniqueEmail(t)
	ctx := context.Background()

	if err := ts.Register(ctx, email, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	u, err := identity.NewStore(ts.db).GetByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if _, err := identity.NewStore(ts.db).MarkEmailVerified(ctx, u.ID, ts.clk.Now()); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}

	if err := ts.Register(ctx, email, "a-totally-different-password-2", "203.0.113.2", "test-agent"); err != nil {
		t.Fatalf("second Register (existing, verified): %v", err)
	}

	msgs := waitForMessages(t, ts.mail, email, 2)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (original verification + notice)", len(msgs))
	}
	if strings.Contains(msgs[1].Body, "token") {
		t.Fatalf("second message to an already-verified email contains a token: %q", msgs[1].Body)
	}
}

func TestIntegration_VerifyEmail_HappyPathThenSingleUse(t *testing.T) {
	ts := newTestService(t)
	email := uniqueEmail(t)
	ctx := context.Background()

	if err := ts.Register(ctx, email, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	msgs := waitForMessages(t, ts.mail, email, 1)
	token := extractToken(t, msgs[0].Body)

	if err := ts.VerifyEmail(ctx, token, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	u, err := identity.NewStore(ts.db).GetByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if !u.EmailVerified() {
		t.Fatal("user not marked verified after VerifyEmail")
	}

	if err := ts.VerifyEmail(ctx, token, "203.0.113.1", "test-agent"); !errors.Is(err, tokens.ErrInvalid) {
		t.Fatalf("second VerifyEmail (reuse) = %v, want tokens.ErrInvalid", err)
	}
}

func TestIntegration_Login_IdenticalErrorForUnknownWrongPasswordAndUnverified(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()

	// Unknown email.
	_, _, err := ts.Login(ctx, uniqueEmail(t), testPassword, "203.0.113.9", "test-agent")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login(unknown email) = %v, want ErrInvalidCredentials", err)
	}

	// Registered but unverified, right password.
	unverifiedEmail := uniqueEmail(t)
	if err := ts.Register(ctx, unverifiedEmail, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, _, err = ts.Login(ctx, unverifiedEmail, testPassword, "203.0.113.9", "test-agent")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login(unverified, right password) = %v, want ErrInvalidCredentials", err)
	}

	// Verified, wrong password.
	verifiedEmail := uniqueEmail(t)
	if err := ts.Register(ctx, verifiedEmail, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	msgs := waitForMessages(t, ts.mail, verifiedEmail, 1)
	token := extractToken(t, msgs[0].Body)
	if err := ts.VerifyEmail(ctx, token, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	_, _, err = ts.Login(ctx, verifiedEmail, "wrong-password-entirely", "203.0.113.9", "test-agent")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login(verified, wrong password) = %v, want ErrInvalidCredentials", err)
	}
}

func TestIntegration_Login_Success_CreatesResolvableSession(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := ts.Register(ctx, email, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	msgs := waitForMessages(t, ts.mail, email, 1)
	token := extractToken(t, msgs[0].Body)
	if err := ts.VerifyEmail(ctx, token, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}

	rawSession, u, err := ts.Login(ctx, email, testPassword, "203.0.113.9", "test-agent")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if u.Email != email {
		t.Fatalf("Login returned user %q, want %q", u.Email, email)
	}
	resolved, err := ts.sess.Resolve(ctx, rawSession)
	if err != nil {
		t.Fatalf("Resolve returned session: %v", err)
	}
	if resolved.UserID != u.ID {
		t.Fatalf("resolved session UserID = %d, want %d", resolved.UserID, u.ID)
	}
	if countAuthEvents(t, ts.db, u.ID, "login_success") != 1 {
		t.Fatal("expected one login_success auth_events row")
	}
}

func TestIntegration_Logout_RevokesSession(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := ts.Register(ctx, email, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	token := extractToken(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
	if err := ts.VerifyEmail(ctx, token, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	rawSession, u, err := ts.Login(ctx, email, testPassword, "203.0.113.9", "test-agent")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	sess, err := ts.sess.Resolve(ctx, rawSession)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := ts.Logout(ctx, sess.ID, u.ID, "203.0.113.9", "test-agent"); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := ts.sess.Resolve(ctx, rawSession); !errors.Is(err, session.ErrRevoked) {
		t.Fatalf("Resolve after Logout = %v, want session.ErrRevoked", err)
	}
}

func TestIntegration_RevokeSession_AnotherUsersSessionIsNotFound(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()

	ownerEmail, otherEmail := uniqueEmail(t), uniqueEmail(t)
	for _, email := range []string{ownerEmail, otherEmail} {
		if err := ts.Register(ctx, email, testPassword, "203.0.113.1", "test-agent"); err != nil {
			t.Fatalf("Register(%s): %v", email, err)
		}
		token := extractToken(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
		if err := ts.VerifyEmail(ctx, token, "203.0.113.1", "test-agent"); err != nil {
			t.Fatalf("VerifyEmail(%s): %v", email, err)
		}
	}
	_, owner, err := ts.Login(ctx, ownerEmail, testPassword, "203.0.113.9", "test-agent")
	if err != nil {
		t.Fatalf("Login(owner): %v", err)
	}
	ownerSessionRaw, _, err := ts.Login(ctx, ownerEmail, testPassword, "203.0.113.9", "test-agent")
	if err != nil {
		t.Fatalf("Login(owner) again: %v", err)
	}
	ownerSession, err := ts.sess.Resolve(ctx, ownerSessionRaw)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_, other, err := ts.Login(ctx, otherEmail, testPassword, "203.0.113.9", "test-agent")
	if err != nil {
		t.Fatalf("Login(other): %v", err)
	}

	if err := ts.RevokeSession(ctx, ownerSession.ID, other.ID, "203.0.113.9", "test-agent"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("RevokeSession(owner's session, as other user) = %v, want session.ErrNotFound", err)
	}
	if err := ts.RevokeSession(ctx, ownerSession.ID, owner.ID, "203.0.113.9", "test-agent"); err != nil {
		t.Fatalf("RevokeSession(owner's session, as owner) = %v, want nil", err)
	}
}

// TestIntegration_SecretsNeverAppearInLogs is this brief's own
// explicit requirement: capture every log line Register+Login produce
// for a real password and a real issued verification token, and prove
// neither ever appears in them (CLAUDE.md rule 3 / this brief's own
// rule 3 -- redact at the logging boundary, never log a password or
// token).
func TestIntegration_SecretsNeverAppearInLogs(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	const secretPassword = "a-very-specific-secret-password-99"

	if err := ts.Register(ctx, email, secretPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	token := extractToken(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
	if err := ts.VerifyEmail(ctx, token, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	if _, _, err := ts.Login(ctx, email, secretPassword, "203.0.113.9", "test-agent"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// A wrong-password attempt too -- the most likely place an
	// implementation mistake would log the attempted password "for
	// debugging".
	if _, _, err := ts.Login(ctx, email, "a-wrong-password-entirely", "203.0.113.9", "test-agent"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login(wrong password) = %v, want ErrInvalidCredentials", err)
	}

	logs := ts.logBuf.String()
	if strings.Contains(logs, secretPassword) {
		t.Fatalf("log output contains the real password:\n%s", logs)
	}
	if strings.Contains(logs, "a-wrong-password-entirely") {
		t.Fatalf("log output contains the wrong-password attempt:\n%s", logs)
	}
	if strings.Contains(logs, token) {
		t.Fatalf("log output contains the raw verification token:\n%s", logs)
	}
}

// TestIntegration_Register_ExistingAndNewPaths_SameHasherOpCount is
// architect review item 1's own test: every Register path that does
// not insert a new user (existing verified, existing unverified) must
// still pay exactly one real Argon2 operation, matching the new-user
// path's own Hash call -- counted through Hasher.Ops, never wall-clock
// time (inherently flaky).
func TestIntegration_Register_ExistingAndNewPaths_SameHasherOpCount(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()

	newEmail := uniqueEmail(t)
	before := ts.hasher.Ops()
	if err := ts.Register(ctx, newEmail, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register(new): %v", err)
	}
	if got := ts.hasher.Ops() - before; got != 1 {
		t.Fatalf("new email: hasher performed %d operations, want exactly 1", got)
	}

	verifiedEmail := uniqueEmail(t)
	if err := ts.Register(ctx, verifiedEmail, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register(seed existing-verified): %v", err)
	}
	u, err := identity.NewStore(ts.db).GetByEmail(ctx, verifiedEmail)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if _, err := identity.NewStore(ts.db).MarkEmailVerified(ctx, u.ID, ts.clk.Now()); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}

	before = ts.hasher.Ops()
	if err := ts.Register(ctx, verifiedEmail, "a-different-password-2", "203.0.113.2", "test-agent"); err != nil {
		t.Fatalf("Register(existing verified): %v", err)
	}
	if got := ts.hasher.Ops() - before; got != 1 {
		t.Fatalf("existing verified email: hasher performed %d operations, want exactly 1 (same as the new-email path)", got)
	}

	before = ts.hasher.Ops()
	if err := ts.Register(ctx, newEmail, "another-password-3", "203.0.113.3", "test-agent"); err != nil {
		t.Fatalf("Register(existing unverified): %v", err)
	}
	if got := ts.hasher.Ops() - before; got != 1 {
		t.Fatalf("existing unverified email: hasher performed %d operations, want exactly 1 (same as the new-email path)", got)
	}
}

// TestIntegration_Login_ProgressiveDelay_SameCounterForUnknownAndKnownEmail
// is architect review item 2's own test: the progressive-delay counter
// is keyed on the normalized email's hash and incremented before the
// user lookup, so an unknown email accrues the exact same counter an
// otherwise-identical known one would -- proven directly on the
// counter (loginFailureKey, same package) rather than by comparing
// wall-clock delays, which would make this test flaky under load.
func TestIntegration_Login_ProgressiveDelay_SameCounterForUnknownAndKnownEmail(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()

	knownEmail := uniqueEmail(t)
	if err := ts.Register(ctx, knownEmail, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	token := extractToken(t, waitForMessages(t, ts.mail, knownEmail, 1)[0].Body)
	if err := ts.VerifyEmail(ctx, token, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	unknownEmail := uniqueEmail(t)

	const attempts = 3
	for i := range attempts {
		if _, _, err := ts.Login(ctx, knownEmail, "wrong-password", "203.0.113.9", "test-agent"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Login(known) attempt %d = %v, want ErrInvalidCredentials", i, err)
		}
		if _, _, err := ts.Login(ctx, unknownEmail, "wrong-password", "203.0.113.9", "test-agent"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Login(unknown) attempt %d = %v, want ErrInvalidCredentials", i, err)
		}
	}

	knownCount, err := ts.limiter.IncrementAndCount(ctx, loginFailureKey(knownEmail), 15*time.Minute)
	if err != nil {
		t.Fatalf("IncrementAndCount(known): %v", err)
	}
	unknownCount, err := ts.limiter.IncrementAndCount(ctx, loginFailureKey(unknownEmail), 15*time.Minute)
	if err != nil {
		t.Fatalf("IncrementAndCount(unknown): %v", err)
	}
	if knownCount != attempts+1 || unknownCount != attempts+1 {
		t.Fatalf("counts after %d Login attempts + 1 probe = known:%d unknown:%d, want both %d -- an unknown email must accrue the same failure counter as a known one", attempts, knownCount, unknownCount, attempts+1)
	}
}

// TestIntegration_Login_DisabledAccount_SameGenericError is architect
// review item 3's own service-level test: a disabled account's login
// gets the same ErrInvalidCredentials every other failure case does.
// The middleware's own "an existing session stops working" half of
// item 3 is tested in internal/httpapi (auth_middleware_test.go),
// since it needs the session-resolution middleware, not this package.
func TestIntegration_Login_DisabledAccount_SameGenericError(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := ts.Register(ctx, email, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	token := extractToken(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
	if err := ts.VerifyEmail(ctx, token, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	u, err := identity.NewStore(ts.db).GetByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if _, err := ts.db.ExecContext(ctx, `UPDATE users SET disabled_at = now() WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("disable user: %v", err)
	}

	if _, _, err := ts.Login(ctx, email, testPassword, "203.0.113.9", "test-agent"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login(disabled account, correct password) = %v, want ErrInvalidCredentials", err)
	}
}

// TestIntegration_ResendVerification_AccountLimitNeverReturns429 is
// architect review item 6's own test: the 6th resend within the hour
// (per-account limit is 5, set via testPolicy's own override below)
// returns the identical generic 202-equivalent (nil error) for a
// known, unverified account as for an unknown one -- a 429 only for
// accounts that exist would itself disclose existence.
func TestIntegration_ResendVerification_AccountLimitNeverReturns429(t *testing.T) {
	ts := newTestService(t)
	ts.policy.ResendVerificationPerAccountPerHour = 5
	ctx := context.Background()

	knownEmail := uniqueEmail(t)
	if err := ts.Register(ctx, knownEmail, testPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	unknownEmail := uniqueEmail(t)

	for i := range 6 {
		if err := ts.ResendVerification(ctx, knownEmail, "203.0.113.1", "test-agent"); err != nil {
			t.Fatalf("ResendVerification(known) call %d = %v, want nil (never 429, architect review item 6)", i, err)
		}
		if err := ts.ResendVerification(ctx, unknownEmail, "203.0.113.1", "test-agent"); err != nil {
			t.Fatalf("ResendVerification(unknown) call %d = %v, want nil", i, err)
		}
	}
}

// extractToken pulls the raw token out of sendVerificationEmail's own
// body format ("... POST /api/v1/auth/verify-email: <token>\n\n...").
// extractToken pulls the raw token out of sendVerificationEmail's own
// link format ("... /verify-email?token=<token>\n\n...", web auth UI
// brief Part A0 -- a real link now, not a bare token to POST by hand).
func extractToken(t *testing.T, body string) string {
	t.Helper()
	const marker = "/verify-email?token="
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("could not find a verification link in body: %q", body)
	}
	rest := body[i+len(marker):]
	end := strings.IndexAny(rest, "\n")
	if end < 0 {
		end = len(rest)
	}
	raw, err := url.QueryUnescape(strings.TrimSpace(rest[:end]))
	if err != nil {
		t.Fatalf("unescape token from link: %v", err)
	}
	return raw
}
