//go:build integration

package httpapi

import (
	"bytes"
	"context"
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
	"github.com/chibuike-kt/argus/internal/identity/tokens"
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
// pass -- Register/ResendVerification now send mail in the background
// (architect review item 1: the send must not affect response time),
// so a caller can no longer assume a message has arrived the instant
// the HTTP call returns.
func waitForBody(t *testing.T, mail *recordingMailer, email string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if body, ok := mail.bodyFor(email); ok {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("no verification email recorded for %s after 2s", email)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var tokenMarkerRe = regexp.MustCompile(`verify-email: (\S+)`)

func extractVerificationToken(t *testing.T, body string) string {
	t.Helper()
	m := tokenMarkerRe.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("could not find a verification token in body: %q", body)
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
	hasher := password.NewHasher(password.Params{
		MemoryKiB: policy.ArgonMemoryKiB, Iterations: policy.ArgonIterations,
		Parallelism: policy.ArgonParallelism, SaltLen: policy.ArgonSaltLen, KeyLen: policy.ArgonKeyLen,
	}, policy.ArgonMaxConcurrent)
	limiter := ratelimit.NewLimiter(rdb, logger, clk)
	mail := &recordingMailer{}
	svc := service.New(db, users, sessions, tokenStore, hasher, limiter, mail, clk, policy, logger)

	handler := NewRouter(Deps{
		Logger: logger, DB: db, Redis: rdb,
		Auth: svc, Sessions: sessions, Users: users,
		AuthPolicy: policy, IsProduction: false, Clock: clk,
	})
	router, ok := handler.(chi.Router)
	if !ok {
		t.Fatalf("NewRouter returned %T, want chi.Router (for route walking)", handler)
	}

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &authTestServer{Server: srv, mail: mail, policy: policy, router: router}
}

// testAuthPolicy mirrors internal/config.AuthPolicy's own envDefault
// values except for cheap Argon2 (this brief's own "cheaper Argon2
// params in tests through config only") and a CSRF allow-list that
// matches this test's own request Origin.
func testAuthPolicy() config.AuthPolicy {
	return config.AuthPolicy{
		ArgonMemoryKiB: 8, ArgonIterations: 1, ArgonParallelism: 1, ArgonSaltLen: 16, ArgonKeyLen: 32, ArgonMaxConcurrent: 4,
		PasswordMinLength: 12, PasswordMaxLength: 128,
		SessionIdleDuration: 7 * 24 * time.Hour, SessionAbsoluteDuration: 30 * 24 * time.Hour,
		SessionTouchThrottle: time.Minute,
		EmailVerificationTTL: 24 * time.Hour, PasswordResetTTL: time.Hour,
		LoginFailuresPerAccountPer15Min: 5, LoginAttemptsPerIPPer15Min: 1000,
		RegisterPerIPPerHour: 1000, ResendVerificationPerAccountPerHour: 1000, ResendVerificationPerIPPerHour: 1000,
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

func registerAndVerify(t *testing.T, srv *authTestServer, email, pw string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/register", RegisterRequest{
		Email: openapi_types.Email(email), Password: pw,
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("register status = %d, want 202", resp.StatusCode)
	}

	body := waitForBody(t, srv.mail, email)
	token := extractVerificationToken(t, body)

	resp, err = http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/verify-email", VerifyEmailRequest{Token: token}))
	if err != nil {
		t.Fatalf("verify-email: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify-email status = %d, want 200", resp.StatusCode)
	}
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

func TestIntegration_FullAuthFlow_RegisterVerifyLoginMeLogout(t *testing.T) {
	srv := newAuthTestServer(t, testAuthPolicy())
	email := testAuthEmail(t)
	const pw = "a-perfectly-fine-password-1"

	registerAndVerify(t, srv, email, pw)
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

func TestIntegration_Login_WrongPassword_UnknownEmail_Unverified_AllIdentical(t *testing.T) {
	srv := newAuthTestServer(t, testAuthPolicy())
	verifiedEmail := testAuthEmail(t)
	const pw = "a-perfectly-fine-password-1"
	registerAndVerify(t, srv, verifiedEmail, pw)

	unverifiedEmail := testAuthEmail(t)
	resp, err := http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/register", RegisterRequest{
		Email: openapi_types.Email(unverifiedEmail), Password: pw,
	}))
	if err != nil {
		t.Fatalf("register unverified: %v", err)
	}
	_ = resp.Body.Close()

	cases := []struct {
		name  string
		email string
		pw    string
	}{
		{"unknown email", testAuthEmail(t), pw},
		{"wrong password", verifiedEmail, "a-wrong-password-entirely"},
		{"unverified", unverifiedEmail, pw},
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
	policy.RegisterPerIPPerHour = 1
	srv := newAuthTestServer(t, policy)

	req := func() *http.Response {
		resp, err := http.DefaultClient.Do(authJSONRequest(t, http.MethodPost, srv.URL+"/api/v1/auth/register", RegisterRequest{
			Email: openapi_types.Email(testAuthEmail(t)), Password: "a-perfectly-fine-password-1",
		}))
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		return resp
	}

	first := req()
	_ = first.Body.Close()
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first register status = %d, want 202", first.StatusCode)
	}

	second := req()
	_ = second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second register (over RegisterPerIPPerHour=1) status = %d, want 429", second.StatusCode)
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
	registerAndVerify(t, srv, email, pw)

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
