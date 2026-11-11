//go:build integration

package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
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
	"github.com/chibuike-kt/argus/internal/identity/signupcode"
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
// is appended, under a mutex, so a test can assert what SignupStart
// actually sent without a real mail provider.
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
// for email or 2s pass -- SignupStart sends mail in the background
// (architect review item 1: the send must not affect response time),
// so a caller can no longer assume a message has arrived the instant
// SignupStart returns.
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

// fakeWatchlistCounter is a hand-written fake implementing
// watchlistCounter -- CompleteOnboarding's own test does not need a
// real watchlist store, only a count it can set directly.
type fakeWatchlistCounter struct {
	counts map[int64]int
}

func (f *fakeWatchlistCounter) Count(_ context.Context, userID int64) (int, error) {
	if f.counts == nil {
		return 0, nil
	}
	return f.counts[userID], nil
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
		PasswordResetTTL:                time.Hour,
		LoginFailuresPerAccountPer15Min: 5, LoginAttemptsPerIPPer15Min: 1000,
		SignupCodeTTL: 10 * time.Minute, SignupCodeMaxWrongGuesses: 5, SignupCodeResendCooldown: time.Millisecond,
		SignupCodesPerEmailPerHour: 1000, SignupStartsPerIPPerHour: 1000, SignupTicketTTL: 30 * time.Minute,
		SignupCodeHMACKey:    "test-only-signup-code-hmac-key-not-for-production-use",
		DisplayNameMaxLength: 60, WatchlistMaxItems: 50, TermsVersion: "1",
		LoginDelayStep: time.Millisecond, LoginDelayMax: 10 * time.Millisecond,
	}
}

type testService struct {
	*Service
	mail      *recordingMailer
	clk       *clock.Mutable
	db        *sql.DB
	sess      *session.Store
	hasher    *password.Hasher
	watchlist *fakeWatchlistCounter
	logBuf    *bytes.Buffer
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
	signupStore := signupcode.NewStore(db, clk, policy.SignupCodeHMACKey)
	watchlist := &fakeWatchlistCounter{}
	hasher := password.NewHasher(password.Params{
		MemoryKiB: policy.ArgonMemoryKiB, Iterations: policy.ArgonIterations,
		Parallelism: policy.ArgonParallelism, SaltLen: policy.ArgonSaltLen, KeyLen: policy.ArgonKeyLen,
	}, policy.ArgonMaxConcurrent)
	limiter := ratelimit.NewLimiter(rdb, logger, clk)
	mail := &recordingMailer{}

	svc := New(db, users, sessions, tokenStore, signupStore, watchlist, hasher, limiter, mail, clk, policy, logger)
	return &testService{
		Service: svc, mail: mail, clk: clk, db: db, sess: sessions,
		hasher: hasher, watchlist: watchlist, logBuf: &buf,
	}
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

// signupNewUser runs the full SignupStart/SignupVerify/SignupComplete
// sequence against ts and returns the resulting identity.User --
// every test below that just needs a real, verified account uses this
// rather than repeating the three calls inline.
func signupNewUser(t *testing.T, ts *testService, email, pw string) identity.User {
	t.Helper()
	ctx := context.Background()
	if err := ts.SignupStart(ctx, email, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("SignupStart: %v", err)
	}
	code := extractCode(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
	ticket, err := ts.SignupVerify(ctx, email, code, "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("SignupVerify: %v", err)
	}
	_, user, err := ts.SignupComplete(ctx, ticket, pw, "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("SignupComplete: %v", err)
	}
	return user
}

func TestIntegration_SignupStart_NewEmail_SendsCode(t *testing.T) {
	ts := newTestService(t)
	email := uniqueEmail(t)
	ctx := context.Background()

	if err := ts.SignupStart(ctx, email, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("SignupStart: %v", err)
	}

	if _, err := identity.NewStore(ts.db).GetByEmail(ctx, email); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("GetByEmail after SignupStart = %v, want identity.ErrNotFound (no half accounts)", err)
	}

	msgs := waitForMessages(t, ts.mail, email, 1)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages to %s, want 1", len(msgs), email)
	}
	if !strings.Contains(msgs[0].Body, "signup code") {
		t.Fatalf("signup start email body = %q, want it to mention the signup code", msgs[0].Body)
	}
}

func TestIntegration_SignupStart_ExistingEmail_SendsNoticeNotCode(t *testing.T) {
	ts := newTestService(t)
	email := uniqueEmail(t)
	ctx := context.Background()
	signupNewUser(t, ts, email, testPassword)

	if err := ts.SignupStart(ctx, email, true, "203.0.113.2", "test-agent"); err != nil {
		t.Fatalf("second SignupStart (existing email): %v", err)
	}

	msgs := waitForMessages(t, ts.mail, email, 2)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (original signup code + notice)", len(msgs))
	}
	if strings.Contains(msgs[1].Body, "signup code") {
		t.Fatalf("second message to an existing account contains a code: %q", msgs[1].Body)
	}
}

func TestIntegration_SignupStart_TermsNotAccepted_Rejected(t *testing.T) {
	ts := newTestService(t)
	if err := ts.SignupStart(context.Background(), uniqueEmail(t), false, "203.0.113.1", "test-agent"); !errors.Is(err, ErrTermsNotAccepted) {
		t.Fatalf("SignupStart(accepted_terms=false) = %v, want ErrTermsNotAccepted", err)
	}
}

func TestIntegration_SignupVerifyAndComplete_HappyPathThenTicketSingleUse(t *testing.T) {
	ts := newTestService(t)
	email := uniqueEmail(t)
	ctx := context.Background()

	if err := ts.SignupStart(ctx, email, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("SignupStart: %v", err)
	}
	code := extractCode(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
	ticket, err := ts.SignupVerify(ctx, email, code, "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("SignupVerify: %v", err)
	}

	// A second verify of the same (still otherwise-valid) code must
	// fail generically: the row is already verified.
	if _, err := ts.SignupVerify(ctx, email, code, "203.0.113.1", "test-agent"); !errors.Is(err, signupcode.ErrInvalidCode) {
		t.Fatalf("second SignupVerify (already verified) = %v, want signupcode.ErrInvalidCode", err)
	}

	_, user, err := ts.SignupComplete(ctx, ticket, testPassword, "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("SignupComplete: %v", err)
	}
	if !user.EmailVerified() {
		t.Fatal("user created by SignupComplete is not marked verified")
	}
	if user.Email != email {
		t.Fatalf("user.Email = %q, want %q", user.Email, email)
	}

	// The ticket is single-use: a second complete with the same ticket
	// must fail.
	if _, _, err := ts.SignupComplete(ctx, ticket, "a-different-password-2", "203.0.113.1", "test-agent"); !errors.Is(err, signupcode.ErrInvalidTicket) {
		t.Fatalf("second SignupComplete (ticket reuse) = %v, want signupcode.ErrInvalidTicket", err)
	}
}

// TestIntegration_SignupComplete_WeakPassword_DoesNotBurnTicket is
// architect review (of Part 2) item 2's own test: a password that
// fails policy must not consume the ticket -- the same ticket then
// succeeds once a valid password is given, with no new emailed code
// needed.
func TestIntegration_SignupComplete_WeakPassword_DoesNotBurnTicket(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := ts.SignupStart(ctx, email, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("SignupStart: %v", err)
	}
	code := extractCode(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
	ticket, err := ts.SignupVerify(ctx, email, code, "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("SignupVerify: %v", err)
	}

	if _, _, err := ts.SignupComplete(ctx, ticket, "too-short", "203.0.113.1", "test-agent"); !errors.Is(err, password.ErrTooShort) {
		t.Fatalf("SignupComplete(weak password) = %v, want password.ErrTooShort", err)
	}

	_, user, err := ts.SignupComplete(ctx, ticket, testPassword, "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("SignupComplete(same ticket, good password) = %v, want nil (the weak-password attempt must not have burned the ticket)", err)
	}
	if user.Email != email {
		t.Fatalf("user.Email = %q, want %q", user.Email, email)
	}
}

// TestIntegration_SignupComplete_FailedInsert_LeavesTicketUsable is
// architect review (of Part 2) item 2's own second test: forcing
// InsertVerified to fail (a pre-existing row with the same
// email_normalized, simulating the "insert fails for some other
// reason" case) must roll back the ticket consumption too -- the
// ticket is still usable once the obstruction is removed.
func TestIntegration_SignupComplete_FailedInsert_LeavesTicketUsable(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := ts.SignupStart(ctx, email, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("SignupStart: %v", err)
	}
	code := extractCode(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
	ticket, err := ts.SignupVerify(ctx, email, code, "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("SignupVerify: %v", err)
	}

	// Obstruct the insert: a row already occupies this email_normalized.
	normalized := identity.NormalizeEmail(email)
	if _, err := ts.db.ExecContext(ctx, `
		INSERT INTO users (email, email_normalized, password_hash, email_verified_at)
		VALUES ($1, $1, 'x', now())`, normalized); err != nil {
		t.Fatalf("seed obstructing user: %v", err)
	}

	if _, _, err := ts.SignupComplete(ctx, ticket, testPassword, "203.0.113.1", "test-agent"); !errors.Is(err, signupcode.ErrInvalidTicket) {
		t.Fatalf("SignupComplete with an obstructed insert = %v, want signupcode.ErrInvalidTicket", err)
	}

	// Remove the obstruction and retry with the same ticket: it must
	// still be usable, proving the failed insert rolled the ticket's
	// consumption back rather than leaving it permanently burned.
	if _, err := ts.db.ExecContext(ctx, `DELETE FROM users WHERE email_normalized = $1`, normalized); err != nil {
		t.Fatalf("remove obstructing user: %v", err)
	}
	_, user, err := ts.SignupComplete(ctx, ticket, testPassword, "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("SignupComplete(same ticket, after removing the obstruction) = %v, want nil", err)
	}
	if user.Email != email {
		t.Fatalf("user.Email = %q, want %q", user.Email, email)
	}
}

func TestIntegration_SignupVerify_WrongCode_ExhaustsAfterMaxGuesses(t *testing.T) {
	ts := newTestService(t)
	email := uniqueEmail(t)
	ctx := context.Background()

	if err := ts.SignupStart(ctx, email, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("SignupStart: %v", err)
	}
	code := extractCode(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
	wrongCode := "000000"
	if wrongCode == code {
		wrongCode = "111111"
	}

	for i := range ts.policy.SignupCodeMaxWrongGuesses {
		if _, err := ts.SignupVerify(ctx, email, wrongCode, "203.0.113.1", "test-agent"); !errors.Is(err, signupcode.ErrInvalidCode) {
			t.Fatalf("SignupVerify(wrong code) attempt %d = %v, want signupcode.ErrInvalidCode", i, err)
		}
	}

	// The real code no longer works either: wrong_guesses is exhausted.
	if _, err := ts.SignupVerify(ctx, email, code, "203.0.113.1", "test-agent"); !errors.Is(err, signupcode.ErrInvalidCode) {
		t.Fatalf("SignupVerify(correct code, after exhausting wrong guesses) = %v, want signupcode.ErrInvalidCode", err)
	}
}

// TestIntegration_SignupVerify_ConcurrentWrongGuesses_NeverExceedsMax
// is architect review (of Part 2) item 3's own test: firing many
// concurrent wrong guesses against one live code must still cap
// wrong_guesses at the configured maximum -- a read-then-increment
// race previously let every one of them "get a guess" regardless of
// the limit.
func TestIntegration_SignupVerify_ConcurrentWrongGuesses_NeverExceedsMax(t *testing.T) {
	ts := newTestService(t)
	email := uniqueEmail(t)
	ctx := context.Background()

	if err := ts.SignupStart(ctx, email, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("SignupStart: %v", err)
	}
	code := extractCode(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
	wrongCode := "000000"
	if wrongCode == code {
		wrongCode = "111111"
	}

	// Every one of these returns the same generic ErrInvalidCode
	// whether this particular call actually claimed a guess (and
	// compared it, wrongly) or was rejected outright for already being
	// over the limit -- that genericness is deliberate (ADR-015), so
	// the race this test is actually proving a fix for is checked
	// directly on wrong_guesses below, not by distinguishing responses.
	const concurrency = 50
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for range concurrency {
		go func() {
			defer wg.Done()
			if _, err := ts.SignupVerify(ctx, email, wrongCode, "203.0.113.1", "test-agent"); !errors.Is(err, signupcode.ErrInvalidCode) {
				t.Errorf("concurrent SignupVerify(wrong code) = %v, want signupcode.ErrInvalidCode", err)
			}
		}()
	}
	wg.Wait()

	var wrongGuesses int
	if err := ts.db.QueryRow(`SELECT wrong_guesses FROM signup_verifications WHERE email_normalized = $1`, identity.NormalizeEmail(email)).Scan(&wrongGuesses); err != nil {
		t.Fatalf("query wrong_guesses: %v", err)
	}
	if wrongGuesses > ts.policy.SignupCodeMaxWrongGuesses {
		t.Fatalf("wrong_guesses = %d after %d concurrent wrong guesses, want at most %d (the max) -- the attempt limit is not actually limiting anything under concurrency", wrongGuesses, concurrency, ts.policy.SignupCodeMaxWrongGuesses)
	}

	// The correct code now fails too: the limit is exhausted.
	if _, err := ts.SignupVerify(ctx, email, code, "203.0.113.1", "test-agent"); !errors.Is(err, signupcode.ErrInvalidCode) {
		t.Fatalf("SignupVerify(correct code, after concurrent wrong guesses exhausted the limit) = %v, want signupcode.ErrInvalidCode", err)
	}
}

// TestIntegration_SignupStart_ResendCooldown_NeverReturns429ForEitherEmail
// is architect review (of Part 2) item 1's own test: a 60s cooldown
// that 429s for a brand-new email but always 202s for an existing one
// (which never reaches IssueCode at all) is itself an existence
// oracle after exactly two calls. Both must return the identical
// generic success.
func TestIntegration_SignupStart_ResendCooldown_NeverReturns429ForEitherEmail(t *testing.T) {
	ts := newTestService(t)
	ts.policy.SignupCodeResendCooldown = time.Hour
	ctx := context.Background()

	newEmail := uniqueEmail(t)
	if err := ts.SignupStart(ctx, newEmail, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("first SignupStart(new): %v", err)
	}
	waitForMessages(t, ts.mail, newEmail, 1)

	existingEmail := uniqueEmail(t)
	signupNewUser(t, ts, existingEmail, testPassword)

	if err := ts.SignupStart(ctx, newEmail, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("second SignupStart(new, within cooldown) = %v, want nil (same generic success as an existing email)", err)
	}
	if err := ts.SignupStart(ctx, existingEmail, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("second SignupStart(existing) = %v, want nil", err)
	}

	// No second code was sent for the new email -- the cooldown means
	// nothing new went out, not just that the response looked the same.
	if msgs := ts.mail.messagesTo(newEmail); len(msgs) != 1 {
		t.Fatalf("got %d messages to the new email after the cooldown call, want still 1 (no new code issued)", len(msgs))
	}
}

func TestIntegration_Login_IdenticalErrorForUnknownWrongPasswordAndNeverCompleted(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()

	// Unknown email.
	_, _, err := ts.Login(ctx, uniqueEmail(t), testPassword, "203.0.113.9", "test-agent")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login(unknown email) = %v, want ErrInvalidCredentials", err)
	}

	// Signup started but never verified/completed: no user row exists
	// (ADR-015 "no half accounts").
	neverCompletedEmail := uniqueEmail(t)
	if err := ts.SignupStart(ctx, neverCompletedEmail, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("SignupStart: %v", err)
	}
	_, _, err = ts.Login(ctx, neverCompletedEmail, testPassword, "203.0.113.9", "test-agent")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login(never completed signup, right password) = %v, want ErrInvalidCredentials", err)
	}

	// Verified, wrong password.
	verifiedEmail := uniqueEmail(t)
	signupNewUser(t, ts, verifiedEmail, testPassword)
	_, _, err = ts.Login(ctx, verifiedEmail, "wrong-password-entirely", "203.0.113.9", "test-agent")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login(verified, wrong password) = %v, want ErrInvalidCredentials", err)
	}
}

func TestIntegration_Login_Success_CreatesResolvableSession(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	signupNewUser(t, ts, email, testPassword)

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
	signupNewUser(t, ts, email, testPassword)

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
	signupNewUser(t, ts, ownerEmail, testPassword)
	signupNewUser(t, ts, otherEmail, testPassword)

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
// explicit requirement: capture every log line SignupStart/SignupVerify/
// SignupComplete/Login produce for a real password, a real signup
// code and a real signup ticket, and prove none of them ever appears
// in those logs (CLAUDE.md rule 3 / this brief's own rule 3 --
// redact at the logging boundary, never log a password, code or
// token).
func TestIntegration_SecretsNeverAppearInLogs(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	const secretPassword = "a-very-specific-secret-password-99"

	if err := ts.SignupStart(ctx, email, true, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("SignupStart: %v", err)
	}
	code := extractCode(t, waitForMessages(t, ts.mail, email, 1)[0].Body)
	ticket, err := ts.SignupVerify(ctx, email, code, "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("SignupVerify: %v", err)
	}
	if _, _, err := ts.SignupComplete(ctx, ticket, secretPassword, "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("SignupComplete: %v", err)
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
	if strings.Contains(logs, code) {
		t.Fatalf("log output contains the raw signup code:\n%s", logs)
	}
	if strings.Contains(logs, ticket) {
		t.Fatalf("log output contains the raw signup ticket:\n%s", logs)
	}
}

func TestIntegration_Login_ProgressiveDelay_SameCounterForUnknownAndKnownEmail(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()

	knownEmail := uniqueEmail(t)
	signupNewUser(t, ts, knownEmail, testPassword)
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

func TestIntegration_Login_DisabledAccount_SameGenericError(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	user := signupNewUser(t, ts, email, testPassword)

	if _, err := ts.db.ExecContext(ctx, `UPDATE users SET disabled_at = now() WHERE id = $1`, user.ID); err != nil {
		t.Fatalf("disable user: %v", err)
	}

	if _, _, err := ts.Login(ctx, email, testPassword, "203.0.113.9", "test-agent"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login(disabled account, correct password) = %v, want ErrInvalidCredentials", err)
	}
}

// TestIntegration_SignupStart_PerEmailSendLimit_NeverReturns429ForEitherBranch
// is this package's own equivalent of architect review item 6's test:
// exceeding the per-email send limit (ADR-015: "5 codes per email per
// hour") is folded into the same generic success for BOTH the new-
// email (code) and existing-email (notice) branches -- symmetrically,
// so neither branch can be distinguished from the other by whether it
// ever 429s (service.go's own SignupStart comment on why).
func TestIntegration_SignupStart_PerEmailSendLimit_NeverReturns429ForEitherBranch(t *testing.T) {
	ts := newTestService(t)
	ts.policy.SignupCodesPerEmailPerHour = 5
	ts.policy.SignupCodeResendCooldown = 0
	ctx := context.Background()

	existingEmail := uniqueEmail(t)
	signupNewUser(t, ts, existingEmail, testPassword)
	newEmail := uniqueEmail(t)

	for i := range 6 {
		if err := ts.SignupStart(ctx, existingEmail, true, "203.0.113.1", "test-agent"); err != nil {
			t.Fatalf("SignupStart(existing) call %d = %v, want nil (never 429, send limit is silent)", i, err)
		}
		if err := ts.SignupStart(ctx, newEmail, true, "203.0.113.1", "test-agent"); err != nil {
			t.Fatalf("SignupStart(new) call %d = %v, want nil (never 429, send limit is silent)", i, err)
		}
	}
}

func TestIntegration_UpdateProfile_TrimsAndValidates(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	user := signupNewUser(t, ts, uniqueEmail(t), testPassword)

	updated, err := ts.UpdateProfile(ctx, user.ID, "  Ada Lovelace  ", "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if updated.DisplayName == nil || *updated.DisplayName != "Ada Lovelace" {
		t.Fatalf("DisplayName = %v, want trimmed \"Ada Lovelace\"", updated.DisplayName)
	}

	if _, err := ts.UpdateProfile(ctx, user.ID, "   ", "203.0.113.1", "test-agent"); !errors.Is(err, identity.ErrDisplayNameEmpty) {
		t.Fatalf("UpdateProfile(blank) = %v, want identity.ErrDisplayNameEmpty", err)
	}
	if _, err := ts.UpdateProfile(ctx, user.ID, strings.Repeat("a", 61), "203.0.113.1", "test-agent"); !errors.Is(err, identity.ErrDisplayNameTooLong) {
		t.Fatalf("UpdateProfile(61 chars) = %v, want identity.ErrDisplayNameTooLong", err)
	}
}

func TestIntegration_CompleteOnboarding_RequiresDisplayNameAndWatchlistItem(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	user := signupNewUser(t, ts, uniqueEmail(t), testPassword)

	if _, err := ts.CompleteOnboarding(ctx, user.ID, "203.0.113.1", "test-agent"); !errors.Is(err, ErrOnboardingIncomplete) {
		t.Fatalf("CompleteOnboarding(no display name, no watchlist) = %v, want ErrOnboardingIncomplete", err)
	}

	if _, err := ts.UpdateProfile(ctx, user.ID, "Ada", "203.0.113.1", "test-agent"); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if _, err := ts.CompleteOnboarding(ctx, user.ID, "203.0.113.1", "test-agent"); !errors.Is(err, ErrOnboardingIncomplete) {
		t.Fatalf("CompleteOnboarding(display name set, no watchlist) = %v, want ErrOnboardingIncomplete", err)
	}

	ts.watchlist.counts = map[int64]int{user.ID: 1}
	completed, err := ts.CompleteOnboarding(ctx, user.ID, "203.0.113.1", "test-agent")
	if err != nil {
		t.Fatalf("CompleteOnboarding(display name + watchlist item) = %v, want nil", err)
	}
	if !completed.OnboardingCompleted() {
		t.Fatal("CompleteOnboarding did not set onboarding_completed_at")
	}
	if countAuthEvents(t, ts.db, user.ID, "onboarding_complete") != 1 {
		t.Fatal("expected one onboarding_complete auth_events row")
	}
}

// codeMarkerRe matches SignupStart's own emailed-code body.
var codeMarkerRe = regexp.MustCompile(`Your ARGUS signup code is (\d{6})`)

func extractCode(t *testing.T, body string) string {
	t.Helper()
	m := codeMarkerRe.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("could not find a signup code in body: %q", body)
	}
	return m[1]
}
