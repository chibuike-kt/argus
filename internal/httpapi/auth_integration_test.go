//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/mailer"
	"github.com/chibuike-kt/argus/internal/identity/password"
	"github.com/chibuike-kt/argus/internal/identity/ratelimit"
	"github.com/chibuike-kt/argus/internal/identity/service"
	"github.com/chibuike-kt/argus/internal/identity/session"
	"github.com/chibuike-kt/argus/internal/identity/signupcode"
	"github.com/chibuike-kt/argus/internal/identity/tokens"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
	"github.com/chibuike-kt/argus/internal/watchlist"
)

// Run with (needs a real Postgres with all migrations applied, and a
// real Redis): go test -tags=integration ./internal/httpapi/...
//
// testDB (database/sql + lib/pq connection) is already defined in
// setups_integration_test.go, same package, same build tag -- reused
// here rather than duplicated. It defaults to the dev database, so
// ARGUS_TEST_DATABASE_URL must be set to a separate database when
// running these (this brief's own rule: never the dev argus
// database).

func testRedisForAuth(t *testing.T) *redis.Client {
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

type recordingMailer struct {
	mu   sync.Mutex
	sent map[string]string // email -> last body
}

func (m *recordingMailer) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sent == nil {
		m.sent = map[string]string{}
	}
	m.sent[msg.To] = msg.Body
	return nil
}

func (m *recordingMailer) bodyFor(email string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.sent[email]
	return b, ok
}

// waitForBody polls bodyFor until a message for email arrives or 2s
// pass -- SignupStart sends mail in the background (architect review
// item 1: the send must not affect response time), so a caller can no
// longer assume a message has arrived the instant the HTTP call
// returns.
func waitForBody(t *testing.T, mail *recordingMailer, email string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if body, ok := mail.bodyFor(email); ok {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("no mail recorded for %s after 2s", email)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// codeMarkerRe matches signupcode's own emailed-code body (ADR-015
// "Signup and onboarding": Service.SignupStart's "Your ARGUS signup
// code is ..." message).
var codeMarkerRe = regexp.MustCompile(`Your ARGUS signup code is (\d{6})`)

func extractSignupCode(t *testing.T, body string) string {
	t.Helper()
	m := codeMarkerRe.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("could not find a signup code in body: %q", body)
	}
	return m[1]
}

var authEmailCounter atomic.Int64

func testAuthEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("httpapi-auth-test-%d-%d@example.com", time.Now().UnixNano(), authEmailCounter.Add(1))
}

// authTestServer bundles everything one auth integration test needs:
// a running httptest.Server over the real router/middleware stack
// (deny-by-default, CSRF, session resolution) with a real Postgres
// and Redis behind it, plus a recordingMailer standing in for the
// unchosen real provider.
type authTestServer struct {
	*httptest.Server
	mail   *recordingMailer
	policy config.AuthPolicy
	router chi.Router
	db     *sql.DB
}

func newAuthTestServer(t *testing.T, policy config.AuthPolicy) *authTestServer {
	t.Helper()
	db := testDB(t)
	rdb := testRedisForAuth(t)
	// Every rate-limit key this package's tests touch is scoped only
	// by IP/account, never by test name -- all requests from this
	// process share 127.0.0.1, and this db is shared across every test
	// in the file. Flushing it here, per test, is what makes
	// TestIntegration_RateLimit_Returns429WithRetryAfter's own
	// "first request succeeds" assumption actually hold, run alone or
	// as part of the full suite.
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush test redis db: %v", err)
	}
	clk := clock.Real{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	users := identity.NewStore(db)
	sessions := session.NewStore(db, clk, policy.SessionIdleDuration, policy.SessionAbsoluteDuration)
	tokenStore := tokens.NewStore(db, clk)
	signupStore := signupcode.NewStore(db, clk, policy.SignupCodeHMACKey)
	instrumentsStore := instruments.NewStore(db)
	watchlistStore := watchlist.NewStore(db, instrumentsStore)
	hasher := password.NewHasher(password.Params{
		MemoryKiB: policy.ArgonMemoryKiB, Iterations: policy.ArgonIterations,
		Parallelism: policy.ArgonParallelism, SaltLen: policy.ArgonSaltLen, KeyLen: policy.ArgonKeyLen,
	}, policy.ArgonMaxConcurrent)
	limiter := ratelimit.NewLimiter(rdb, logger, clk)
	mail := &recordingMailer{}
	svc := service.New(db, users, sessions, tokenStore, signupStore, watchlistStore, hasher, limiter, mail, clk, policy, logger)

	handler := NewRouter(Deps{
		Logger: logger, DB: db, Redis: rdb,
		Auth: svc, Watchlist: watchlistStore, Sessions: sessions, Users: users,
		AuthPolicy: policy, IsProduction: false, Clock: clk,
	})
	router, ok := handler.(chi.Router)
	if !ok {
		t.Fatalf("NewRouter returned %T, want chi.Router (for route walking)", handler)
	}

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &authTestServer{Server: srv, mail: mail, policy: policy, router: router, db: db}
}

// testAuthPolicy mirrors internal/config.AuthPolicy's own envDefault
// values except for cheap Argon2 (this brief's own "cheaper Argon2
// params in tests through config only"), generous signup limits (high
// enough that a single test's own handful of requests never trips
// them), and a CSRF allow-list that matches this test's own request
// Origin.
func testAuthPolicy() config.AuthPolicy {
	return config.AuthPolicy{
		ArgonMemoryKiB: 8, ArgonIterations: 1, ArgonParallelism: 1, ArgonSaltLen: 16, ArgonKeyLen: 32, ArgonMaxConcurrent: 4,
		PasswordMinLength: 12, PasswordMaxLength: 128,
		SessionIdleDuration: 7 * 24 * time.Hour, SessionAbsoluteDuration: 30 * 24 * time.Hour,
		SessionTouchThrottle:            time.Minute,
		PasswordResetTTL:                time.Hour,
		LoginFailuresPerAccountPer15Min: 5, LoginAttemptsPerIPPer15Min: 1000,
		SignupCodeTTL: 10 * time.Minute, SignupCodeMaxWrongGuesses: 5, SignupCodeResendCooldown: time.Millisecond,
		SignupCodesPerEmailPerHour: 1000, SignupStartsPerIPPerHour: 1000, SignupTicketTTL: 30 * time.Minute,
		SignupCodeHMACKey:    "test-only-signup-code-hmac-key-not-for-production-use",
		DisplayNameMaxLength: 60, WatchlistMaxItems: 50, TermsVersion: "1",
		LoginDelayStep: time.Millisecond, LoginDelayMax: 5 * time.Millisecond,
		CookieNameProd: "__Host-argus_session", CookieNameLocal: "argus_session",
		AllowedOrigins: []string{"http://localhost:5173"},
	}
}

func authJSONRequest(t *testing.T, method, url string, body any) *http.Request {
	t.Helper()
	var r *http.Request
	var err error
	if body != nil {
		b, merr := json.Marshal(body)
		if merr != nil {
			t.Fatalf("marshal body: %v", merr)
		}
		r, err = http.NewRequest(method, url, bytes.NewReader(b))
	} else {
		r, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://localhost:5173")
	return r
}

// signupStart posts /auth/signup/start and returns the 6-digit code
// mailed for email -- the shared first step every signup test below
// needs, whether or not it goes on to verify/complete.
func signupStart(t *testing.T, srv *authTestServer, email string) string {
	t.Helper()
	resp, err := http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/signup/start", SignupStartRequest{
		Email: openapi_types.Email(email), AcceptedTerms: true,
	}))
	if err != nil {
		t.Fatalf("signup start: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("signup start status = %d, want 202", resp.StatusCode)
	}
	return extractSignupCode(t, waitForBody(t, srv.mail, email))
}

// signupNewUser runs the full start/verify/complete sequence (ADR-015
// "Signup and onboarding") and returns the session cookie
// signup/complete itself sets -- it logs the new account in, the same
// as Login.
func signupNewUser(t *testing.T, srv *authTestServer, email, pw string) *http.Cookie {
	t.Helper()
	code := signupStart(t, srv, email)

	verifyResp, err := http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/signup/verify", SignupVerifyRequest{
		Email: openapi_types.Email(email), Code: code,
	}))
	if err != nil {
		t.Fatalf("signup verify: %v", err)
	}
	defer func() { _ = verifyResp.Body.Close() }()
	if verifyResp.StatusCode != http.StatusOK {
		t.Fatalf("signup verify status = %d, want 200", verifyResp.StatusCode)
	}
	var verified SignupVerifyResponse
	if err := json.NewDecoder(verifyResp.Body).Decode(&verified); err != nil {
		t.Fatalf("decode signup verify response: %v", err)
	}

	completeResp, err := http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/signup/complete", SignupCompleteRequest{
		Ticket: verified.Ticket, Password: pw,
	}))
	if err != nil {
		t.Fatalf("signup complete: %v", err)
	}
	defer func() { _ = completeResp.Body.Close() }()
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("signup complete status = %d, want 200", completeResp.StatusCode)
	}
	for _, c := range completeResp.Cookies() {
		if c.Name == "argus_session" {
			return c
		}
	}
	t.Fatal("signup complete response set no argus_session cookie")
	return nil
}

func loginAndGetCookie(t *testing.T, srv *authTestServer, email, pw string) *http.Cookie {
	t.Helper()
	resp, err := http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/login", LoginRequest{
		Email: openapi_types.Email(email), Password: pw,
	}))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "argus_session" {
			return c
		}
	}
	t.Fatal("login response set no argus_session cookie")
	return nil
}

// testEnabledInstrument creates an instrument and flips it enabled by
// a direct SQL update -- internal/instruments.Store deliberately has
// no code path to do that itself while ADR-007 (provider selection and
// commercial data rights) is open, so a watchlist integration test that
// needs one goes around the store, test-only, the same way
// setups_integration_test.go's own testInstrument creates a disabled
// one for everything that does not need it enabled.
func testEnabledInstrument(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	priceScale := quant.Scale(0)
	qtyScale := quant.Scale(0)
	symbol := fmt.Sprintf("HTTPAPI-WATCHLIST-%s-%d", t.Name(), time.Now().UnixNano())
	inst, err := instruments.NewStore(db).Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: "test-provider", NativeSymbol: symbol, AssetClass: "crypto_perp",
		PriceScale: &priceScale, QtyScale: &qtyScale, TickSize: 1, QtyStepSize: 1,
		SupportedIntervals: []string{"1m"},
	})
	if err != nil {
		t.Fatalf("create test instrument: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE instruments SET enabled = true WHERE id = $1`, inst.ID); err != nil {
		t.Fatalf("enable test instrument: %v", err)
	}
	return inst.ID
}

func TestIntegration_FullAuthFlow_SignupLoginMeLogout(t *testing.T) {
	srv := newAuthTestServer(t, testAuthPolicy())
	email := testAuthEmail(t)
	const pw = "a-perfectly-fine-password-1"

	signupCookie := signupNewUser(t, srv, email, pw)
	if signupCookie.Value == "" {
		t.Fatal("signup complete set an empty session cookie")
	}

	cookie := loginAndGetCookie(t, srv, email, pw)

	// Cookie shape (ADR-015 addendum row 5): HttpOnly, Secure except
	// local http (this server has no TLS), SameSite=Lax, Path=/, no
	// Domain, local name "argus_session".
	if !cookie.HttpOnly {
		t.Fatal("session cookie is not HttpOnly")
	}
	if cookie.Secure {
		t.Fatal("session cookie is Secure on a non-TLS local server, want false")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie.SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Fatalf("cookie.Path = %q, want /", cookie.Path)
	}
	if cookie.Domain != "" {
		t.Fatalf("cookie.Domain = %q, want empty (no Domain attribute)", cookie.Domain)
	}

	meReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/auth/me", nil)
	meReq.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(meReq)
	if err != nil {
		t.Fatalf("GET /auth/me: %v", err)
	}
	var me AuthUser
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode /auth/me: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /auth/me status = %d, want 200", resp.StatusCode)
	}
	if me.Email != email || !me.EmailVerified {
		t.Fatalf("/auth/me = %+v", me)
	}
	if me.OnboardingCompleted {
		t.Fatal("/auth/me OnboardingCompleted = true right after signup, want false (no display name/watchlist yet)")
	}

	logoutReq := authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/logout", nil)
	logoutReq.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", resp.StatusCode)
	}

	meReq2, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/auth/me", nil)
	meReq2.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(meReq2)
	if err != nil {
		t.Fatalf("GET /auth/me after logout: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /auth/me after logout status = %d, want 401 (session revoked)", resp.StatusCode)
	}
}

// TestIntegration_Onboarding_ProfileAndWatchlistCompleteIt covers the
// "critical journey" ADR-019 names explicitly: signup, then setting a
// display name and a watchlist, then completing onboarding -- and the
// negative case, completing before either exists.
func TestIntegration_Onboarding_ProfileAndWatchlistCompleteIt(t *testing.T) {
	srv := newAuthTestServer(t, testAuthPolicy())
	email := testAuthEmail(t)
	const pw = "a-perfectly-fine-password-1"
	cookie := signupNewUser(t, srv, email, pw)
	instrumentID := testEnabledInstrument(t, srv.db)

	// Too early: no display name, no watchlist item yet.
	tooEarly := authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/me/onboarding/complete", nil)
	tooEarly.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(tooEarly)
	if err != nil {
		t.Fatalf("complete onboarding too early: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("complete onboarding with nothing set status = %d, want 409", resp.StatusCode)
	}

	profileReq := authJSONRequest(t, http.MethodPut, srv.URL+"/api/v1/me/profile", ProfileUpdateRequest{DisplayName: "Ada"})
	profileReq.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(profileReq)
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update profile status = %d, want 200", resp.StatusCode)
	}

	watchlistReq := authJSONRequest(t, http.MethodPut, srv.URL+"/api/v1/watchlist", WatchlistUpdateRequest{InstrumentIds: []int64{instrumentID}})
	watchlistReq.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(watchlistReq)
	if err != nil {
		t.Fatalf("update watchlist: %v", err)
	}
	var items []WatchlistItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode watchlist response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update watchlist status = %d, want 200", resp.StatusCode)
	}
	if len(items) != 1 || items[0].InstrumentId != instrumentID {
		t.Fatalf("watchlist = %+v, want one item with instrument_id %d", items, instrumentID)
	}

	getWatchlistReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/watchlist", nil)
	getWatchlistReq.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(getWatchlistReq)
	if err != nil {
		t.Fatalf("get watchlist: %v", err)
	}
	var got []WatchlistItem
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode get watchlist response: %v", err)
	}
	_ = resp.Body.Close()
	if len(got) != 1 || got[0].InstrumentId != instrumentID {
		t.Fatalf("GET watchlist = %+v, want one item with instrument_id %d", got, instrumentID)
	}

	completeReq := authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/me/onboarding/complete", nil)
	completeReq.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(completeReq)
	if err != nil {
		t.Fatalf("complete onboarding: %v", err)
	}
	var completed AuthUser
	if err := json.NewDecoder(resp.Body).Decode(&completed); err != nil {
		t.Fatalf("decode complete onboarding response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete onboarding status = %d, want 200", resp.StatusCode)
	}
	if !completed.OnboardingCompleted {
		t.Fatal("complete onboarding response OnboardingCompleted = false, want true")
	}
	if completed.DisplayName == nil || *completed.DisplayName != "Ada" {
		t.Fatalf("complete onboarding response DisplayName = %v, want \"Ada\"", completed.DisplayName)
	}
}

// TestIntegration_UpdateWatchlist_DuplicateAndOversizeRejected is
// architect review (of Part 2) item 4's own HTTP-level test: a
// duplicate instrument id returns 400 and leaves the previous list
// untouched, and a list over WatchlistMaxItems (set to 2 here) also
// returns 400 -- both confirmed at the handler, not just the store.
func TestIntegration_UpdateWatchlist_DuplicateAndOversizeRejected(t *testing.T) {
	policy := testAuthPolicy()
	policy.WatchlistMaxItems = 2
	srv := newAuthTestServer(t, policy)
	email := testAuthEmail(t)
	cookie := signupNewUser(t, srv, email, "a-perfectly-fine-password-1")
	a := testEnabledInstrument(t, srv.db)
	b := testEnabledInstrument(t, srv.db)
	c := testEnabledInstrument(t, srv.db)

	baseline := authJSONRequest(t, http.MethodPut, srv.URL+"/api/v1/watchlist", WatchlistUpdateRequest{InstrumentIds: []int64{a}})
	baseline.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(baseline)
	if err != nil {
		t.Fatalf("baseline update watchlist: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("baseline update watchlist status = %d, want 200", resp.StatusCode)
	}

	dup := authJSONRequest(t, http.MethodPut, srv.URL+"/api/v1/watchlist", WatchlistUpdateRequest{InstrumentIds: []int64{b, b}})
	dup.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(dup)
	if err != nil {
		t.Fatalf("duplicate update watchlist: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update watchlist with a duplicate id status = %d, want 400", resp.StatusCode)
	}

	oversize := authJSONRequest(t, http.MethodPut, srv.URL+"/api/v1/watchlist", WatchlistUpdateRequest{InstrumentIds: []int64{a, b, c}})
	oversize.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(oversize)
	if err != nil {
		t.Fatalf("oversize update watchlist: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update watchlist over the cap (2) status = %d, want 400", resp.StatusCode)
	}

	getReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/watchlist", nil)
	getReq.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("get watchlist: %v", err)
	}
	var items []WatchlistItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("decode get watchlist response: %v", err)
	}
	_ = resp.Body.Close()
	if len(items) != 1 || items[0].InstrumentId != a {
		t.Fatalf("watchlist after two rejected updates = %+v, want the baseline [{%d 0}] unchanged", items, a)
	}
}

func TestIntegration_Login_WrongPassword_UnknownEmail_NeverCompleted_AllIdentical(t *testing.T) {
	srv := newAuthTestServer(t, testAuthPolicy())
	verifiedEmail := testAuthEmail(t)
	const pw = "a-perfectly-fine-password-1"
	signupNewUser(t, srv, verifiedEmail, pw)

	// A signup that was started but never verified/completed: no user
	// row exists for this email at all (ADR-015 "no half accounts"),
	// structurally identical to "unknown email" from Login's own point
	// of view.
	neverCompletedEmail := testAuthEmail(t)
	signupStart(t, srv, neverCompletedEmail)

	cases := []struct {
		name  string
		email string
		pw    string
	}{
		{"unknown email", testAuthEmail(t), pw},
		{"wrong password", verifiedEmail, "a-wrong-password-entirely"},
		{"signup started but never completed", neverCompletedEmail, pw},
	}
	var bodies []string
	var statuses []int
	for _, c := range cases {
		resp, err := http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/login", LoginRequest{
			Email: openapi_types.Email(c.email), Password: c.pw,
		}))
		if err != nil {
			t.Fatalf("%s: login: %v", c.name, err)
		}
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		_ = resp.Body.Close()
		statuses = append(statuses, resp.StatusCode)
		bodies = append(bodies, buf.String())
	}
	for i := 1; i < len(cases); i++ {
		if statuses[i] != statuses[0] {
			t.Fatalf("%s status = %d, %s status = %d, want identical", cases[0].name, statuses[0], cases[i].name, statuses[i])
		}
		if bodies[i] != bodies[0] {
			t.Fatalf("%s body = %q, %s body = %q, want identical", cases[0].name, bodies[0], cases[i].name, bodies[i])
		}
	}
	if statuses[0] != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", statuses[0])
	}
}

func TestIntegration_CSRF_RejectsMismatchedOriginAndMissingContentType(t *testing.T) {
	srv := newAuthTestServer(t, testAuthPolicy())

	badOrigin, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/login", bytes.NewReader([]byte(`{}`)))
	badOrigin.Header.Set("Content-Type", "application/json")
	badOrigin.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(badOrigin)
	if err != nil {
		t.Fatalf("bad origin request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("mismatched Origin status = %d, want 403", resp.StatusCode)
	}

	noContentType, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/login", bytes.NewReader([]byte(`{}`)))
	noContentType.Header.Set("Origin", "http://localhost:5173")
	resp, err = http.DefaultClient.Do(noContentType)
	if err != nil {
		t.Fatalf("missing content-type request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing Content-Type status = %d, want 403", resp.StatusCode)
	}
}

func TestIntegration_RateLimit_Returns429WithRetryAfter(t *testing.T) {
	policy := testAuthPolicy()
	policy.SignupStartsPerIPPerHour = 1
	srv := newAuthTestServer(t, policy)

	req := func() *http.Response {
		resp, err := http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/signup/start", SignupStartRequest{
			Email: openapi_types.Email(testAuthEmail(t)), AcceptedTerms: true,
		}))
		if err != nil {
			t.Fatalf("signup start: %v", err)
		}
		return resp
	}

	first := req()
	_ = first.Body.Close()
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first signup start status = %d, want 202", first.StatusCode)
	}

	second := req()
	_ = second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second signup start (over SignupStartsPerIPPerHour=1) status = %d, want 429", second.StatusCode)
	}
}

// TestIntegration_OversizeBody_Returns413 is architect review item 4's
// own test: a request body over maxRequestBodyBytes (64 KiB) is
// rejected with 413 in the shared error shape, before ever reaching
// the strict handler's JSON decode (login was measured accepting an
// 8 MB body).
func TestIntegration_OversizeBody_Returns413(t *testing.T) {
	srv := newAuthTestServer(t, testAuthPolicy())

	// Must be syntactically valid JSON, not just large: an invalid byte
	// (this was originally 8MB of plain 'a' bytes) makes json.Decoder
	// fail on a syntax error after only the first few bytes, long
	// before MaxBytesReader's own limit is ever reached -- the oversize
	// password is itself inside a valid, if very long, JSON string.
	oversize := []byte(`{"email":"a@example.com","password":"` + strings.Repeat("a", 8*1024*1024) + `"}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/login", bytes.NewReader(oversize))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:5173")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("oversize login request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body status = %d, want 413", resp.StatusCode)
	}
	var got Error
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if got.Error.Code != "request_too_large" {
		t.Fatalf("error.code = %q, want request_too_large", got.Error.Code)
	}
}

// TestIntegration_Login_OversizePassword_SameGenericError is architect
// review item 4's own second test: a password longer than the policy
// maximum (128) must never reach Argon2 -- it is rejected as invalid
// credentials, same status and (modulo the password itself, never
// echoed) same body as any other login failure.
func TestIntegration_Login_OversizePassword_SameGenericError(t *testing.T) {
	srv := newAuthTestServer(t, testAuthPolicy())
	email := testAuthEmail(t)
	const pw = "a-perfectly-fine-password-1"
	signupNewUser(t, srv, email, pw)

	longPassword := strings.Repeat("a", 129)
	resp, err := http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/login", LoginRequest{
		Email: openapi_types.Email(email), Password: longPassword,
	}))
	if err != nil {
		t.Fatalf("login with oversize password: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("oversize password login status = %d, want 401", resp.StatusCode)
	}
	var got Error
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if got.Error.Code != "invalid_credentials" {
		t.Fatalf("error.code = %q, want invalid_credentials (same as any other login failure)", got.Error.Code)
	}
}

// TestIntegration_DenyByDefault_EveryNonPublicRouteRequiresASession
// walks every registered /api/v1 route and, for each one not on
// publicAPIPaths' own allow-list, sends a request with no session
// cookie (but valid CSRF headers on state-changing methods, so the
// 401 this asserts is actually the session check, not CSRF rejecting
// first) and requires 401. This is the brief's own required proof
// that a new route added without thinking is protected by
// construction.
func TestIntegration_DenyByDefault_EveryNonPublicRouteRequiresASession(t *testing.T) {
	srv := newAuthTestServer(t, testAuthPolicy())

	idPattern := regexp.MustCompile(`\{[a-zA-Z]+\}`)
	var checked int
	err := chi.Walk(srv.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/v1") {
			return nil
		}
		if publicAPIPaths[route] {
			return nil
		}
		checked++
		url := srv.URL + idPattern.ReplaceAllString(route, "1")
		req, err := http.NewRequest(method, url, bytes.NewReader([]byte(`{}`)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost:5173")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, route, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with no session cookie = %d, want 401", method, route, resp.StatusCode)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	if checked == 0 {
		t.Fatal("walked zero non-public /api/v1 routes -- the walk itself is broken, this proves nothing")
	}
}
