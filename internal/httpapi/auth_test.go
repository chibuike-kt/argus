package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/service"
	"github.com/chibuike-kt/argus/internal/identity/session"
	"github.com/chibuike-kt/argus/internal/identity/signupcode"
)

// fakeAuthService is a hand-written fake implementing authService --
// the same pattern setups_test.go's fakeSetupsStore uses -- so
// apiHandler's own error-mapping and cookie-setting logic can be unit
// tested with no database or Redis at all.
type fakeAuthService struct {
	signupStartErr error

	signupVerifyTicket string
	signupVerifyErr    error

	signupCompleteToken string
	signupCompleteUser  identity.User
	signupCompleteErr   error

	loginToken string
	loginUser  identity.User
	loginErr   error

	logoutErr     error
	logoutAllErr  error
	listSessions  []session.Session
	listErr       error
	revokeErr     error
	gotRevokeID   int64
	gotRevokeUser int64

	updateProfileUser identity.User
	updateProfileErr  error

	completeOnboardingUser identity.User
	completeOnboardingErr  error
}

func (f *fakeAuthService) SignupStart(context.Context, string, bool, string, string) error {
	return f.signupStartErr
}
func (f *fakeAuthService) SignupVerify(context.Context, string, string, string, string) (string, error) {
	return f.signupVerifyTicket, f.signupVerifyErr
}
func (f *fakeAuthService) SignupComplete(context.Context, string, string, string, string) (string, identity.User, error) {
	return f.signupCompleteToken, f.signupCompleteUser, f.signupCompleteErr
}
func (f *fakeAuthService) Login(context.Context, string, string, string, string) (string, identity.User, error) {
	return f.loginToken, f.loginUser, f.loginErr
}
func (f *fakeAuthService) Logout(context.Context, int64, int64, string, string) error {
	return f.logoutErr
}
func (f *fakeAuthService) LogoutAll(context.Context, int64, string, string) error {
	return f.logoutAllErr
}
func (f *fakeAuthService) ListSessions(context.Context, int64) ([]session.Session, error) {
	return f.listSessions, f.listErr
}
func (f *fakeAuthService) RevokeSession(_ context.Context, sessionID, userID int64, _, _ string) error {
	f.gotRevokeID, f.gotRevokeUser = sessionID, userID
	return f.revokeErr
}
func (f *fakeAuthService) UpdateProfile(context.Context, int64, string, string, string) (identity.User, error) {
	return f.updateProfileUser, f.updateProfileErr
}
func (f *fakeAuthService) CompleteOnboarding(context.Context, int64, string, string) (identity.User, error) {
	return f.completeOnboardingUser, f.completeOnboardingErr
}

func newTestHandler(auth authService) *apiHandler {
	return &apiHandler{
		auth: auth,
		authPolicy: config.AuthPolicy{
			CookieNameProd: "__Host-argus_session", CookieNameLocal: "argus_session",
			SessionAbsoluteDuration: 30 * 24 * time.Hour,
		},
		isProduction: false,
		logger:       slog.Default(),
	}
}

func TestAuthLogin_Success_SetsCookieAndReturnsUser(t *testing.T) {
	user := identity.User{ID: 42, Email: "a@example.com", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	h := newTestHandler(&fakeAuthService{loginToken: "raw-token-123", loginUser: user})

	resp, err := h.AuthLogin(context.Background(), AuthLoginRequestObject{
		Body: &AuthLoginJSONRequestBody{Email: openapi_types.Email("a@example.com"), Password: "whatever"},
	})
	if err != nil {
		t.Fatalf("AuthLogin: %v", err)
	}
	loginResp, ok := resp.(authLoginResponse)
	if !ok {
		t.Fatalf("AuthLogin returned %T, want authLoginResponse", resp)
	}
	if loginResp.user.Id != 42 {
		t.Fatalf("user.Id = %d, want 42", loginResp.user.Id)
	}
	if loginResp.cookie.Value != "raw-token-123" {
		t.Fatalf("cookie.Value = %q, want the raw session token", loginResp.cookie.Value)
	}
	if loginResp.cookie.Name != "argus_session" {
		t.Fatalf("cookie.Name = %q, want argus_session (not production)", loginResp.cookie.Name)
	}
	if !loginResp.cookie.HttpOnly {
		t.Fatal("cookie is not HttpOnly")
	}
	if loginResp.cookie.Secure {
		t.Fatal("cookie is Secure on a non-production response, want false (local http)")
	}
	if loginResp.cookie.SameSite != 2 /* http.SameSiteLaxMode */ {
		t.Fatalf("cookie.SameSite = %v, want SameSiteLaxMode", loginResp.cookie.SameSite)
	}
}

func TestAuthLogin_InvalidCredentials_Maps401(t *testing.T) {
	h := newTestHandler(&fakeAuthService{loginErr: service.ErrInvalidCredentials})

	resp, err := h.AuthLogin(context.Background(), AuthLoginRequestObject{
		Body: &AuthLoginJSONRequestBody{Email: openapi_types.Email("a@example.com"), Password: "whatever"},
	})
	if err != nil {
		t.Fatalf("AuthLogin: %v", err)
	}
	if _, ok := resp.(AuthLogin401JSONResponse); !ok {
		t.Fatalf("AuthLogin returned %T, want AuthLogin401JSONResponse", resp)
	}
}

func TestAuthLogin_RateLimited_Maps429(t *testing.T) {
	h := newTestHandler(&fakeAuthService{loginErr: &service.RateLimitedError{RetryAfter: 30 * time.Second}})

	resp, err := h.AuthLogin(context.Background(), AuthLoginRequestObject{
		Body: &AuthLoginJSONRequestBody{Email: openapi_types.Email("a@example.com"), Password: "whatever"},
	})
	if err != nil {
		t.Fatalf("AuthLogin: %v", err)
	}
	if _, ok := resp.(AuthLogin429JSONResponse); !ok {
		t.Fatalf("AuthLogin returned %T, want AuthLogin429JSONResponse", resp)
	}
}

func TestAuthLogout_ClearsCookie(t *testing.T) {
	h := newTestHandler(&fakeAuthService{})
	ctx := identity.WithUser(context.Background(), identity.User{ID: 1})
	ctx = identity.WithSessionID(ctx, 7)

	resp, err := h.AuthLogout(ctx, AuthLogoutRequestObject{})
	if err != nil {
		t.Fatalf("AuthLogout: %v", err)
	}
	logoutResp, ok := resp.(authLogoutResponse)
	if !ok {
		t.Fatalf("AuthLogout returned %T, want authLogoutResponse", resp)
	}
	if logoutResp.cookie.MaxAge >= 0 {
		t.Fatalf("cleared cookie MaxAge = %d, want negative (immediate expiry)", logoutResp.cookie.MaxAge)
	}
	if logoutResp.cookie.Value != "" {
		t.Fatalf("cleared cookie Value = %q, want empty", logoutResp.cookie.Value)
	}
}

func TestAuthRevokeSession_NotFoundMaps404(t *testing.T) {
	h := newTestHandler(&fakeAuthService{revokeErr: session.ErrNotFound})
	ctx := identity.WithUser(context.Background(), identity.User{ID: 1})

	resp, err := h.AuthRevokeSession(ctx, AuthRevokeSessionRequestObject{Id: 99})
	if err != nil {
		t.Fatalf("AuthRevokeSession: %v", err)
	}
	if _, ok := resp.(AuthRevokeSession404JSONResponse); !ok {
		t.Fatalf("AuthRevokeSession returned %T, want AuthRevokeSession404JSONResponse", resp)
	}
}

func TestAuthRevokeSession_Success(t *testing.T) {
	fake := &fakeAuthService{}
	h := newTestHandler(fake)
	ctx := identity.WithUser(context.Background(), identity.User{ID: 1})

	resp, err := h.AuthRevokeSession(ctx, AuthRevokeSessionRequestObject{Id: 99})
	if err != nil {
		t.Fatalf("AuthRevokeSession: %v", err)
	}
	if _, ok := resp.(AuthRevokeSession204Response); !ok {
		t.Fatalf("AuthRevokeSession returned %T, want AuthRevokeSession204Response", resp)
	}
	if fake.gotRevokeID != 99 || fake.gotRevokeUser != 1 {
		t.Fatalf("RevokeSession called with (%d, %d), want (99, 1)", fake.gotRevokeID, fake.gotRevokeUser)
	}
}

func TestAuthListSessions_MarksCurrentSession(t *testing.T) {
	fake := &fakeAuthService{listSessions: []session.Session{{ID: 1}, {ID: 2}}}
	h := newTestHandler(fake)
	ctx := identity.WithUser(context.Background(), identity.User{ID: 1})
	ctx = identity.WithSessionID(ctx, 2)

	resp, err := h.AuthListSessions(ctx, AuthListSessionsRequestObject{})
	if err != nil {
		t.Fatalf("AuthListSessions: %v", err)
	}
	list, ok := resp.(AuthListSessions200JSONResponse)
	if !ok {
		t.Fatalf("AuthListSessions returned %T", resp)
	}
	for _, s := range list {
		want := s.Id == 2
		if s.Current != want {
			t.Fatalf("session %d Current = %v, want %v", s.Id, s.Current, want)
		}
	}
}

func TestAuthMe_ReturnsContextUser(t *testing.T) {
	h := newTestHandler(&fakeAuthService{})
	ctx := identity.WithUser(context.Background(), identity.User{ID: 5, Email: "me@example.com"})

	resp, err := h.AuthMe(ctx, AuthMeRequestObject{})
	if err != nil {
		t.Fatalf("AuthMe: %v", err)
	}
	me, ok := resp.(AuthMe200JSONResponse)
	if !ok {
		t.Fatalf("AuthMe returned %T", resp)
	}
	if me.Id != 5 || me.Email != "me@example.com" {
		t.Fatalf("AuthMe = %+v", me)
	}
}

func TestAuthSignupStart_Success_Maps202(t *testing.T) {
	h := newTestHandler(&fakeAuthService{})

	resp, err := h.AuthSignupStart(context.Background(), AuthSignupStartRequestObject{
		Body: &AuthSignupStartJSONRequestBody{Email: openapi_types.Email("a@example.com"), AcceptedTerms: true},
	})
	if err != nil {
		t.Fatalf("AuthSignupStart: %v", err)
	}
	if _, ok := resp.(AuthSignupStart202JSONResponse); !ok {
		t.Fatalf("AuthSignupStart returned %T, want AuthSignupStart202JSONResponse", resp)
	}
}

func TestAuthSignupStart_TermsNotAccepted_Maps400(t *testing.T) {
	h := newTestHandler(&fakeAuthService{signupStartErr: service.ErrTermsNotAccepted})

	resp, err := h.AuthSignupStart(context.Background(), AuthSignupStartRequestObject{
		Body: &AuthSignupStartJSONRequestBody{Email: openapi_types.Email("a@example.com"), AcceptedTerms: false},
	})
	if err != nil {
		t.Fatalf("AuthSignupStart: %v", err)
	}
	if _, ok := resp.(AuthSignupStart400JSONResponse); !ok {
		t.Fatalf("AuthSignupStart returned %T, want AuthSignupStart400JSONResponse", resp)
	}
}

func TestAuthSignupStart_RateLimited_Maps429(t *testing.T) {
	h := newTestHandler(&fakeAuthService{signupStartErr: &service.RateLimitedError{RetryAfter: time.Minute}})

	resp, err := h.AuthSignupStart(context.Background(), AuthSignupStartRequestObject{
		Body: &AuthSignupStartJSONRequestBody{Email: openapi_types.Email("a@example.com"), AcceptedTerms: true},
	})
	if err != nil {
		t.Fatalf("AuthSignupStart: %v", err)
	}
	if _, ok := resp.(AuthSignupStart429JSONResponse); !ok {
		t.Fatalf("AuthSignupStart returned %T, want AuthSignupStart429JSONResponse", resp)
	}
}

func TestAuthSignupStart_UnexpectedError_IsNotSwallowed(t *testing.T) {
	h := newTestHandler(&fakeAuthService{signupStartErr: errors.New("boom")})

	_, err := h.AuthSignupStart(context.Background(), AuthSignupStartRequestObject{
		Body: &AuthSignupStartJSONRequestBody{Email: openapi_types.Email("a@example.com"), AcceptedTerms: true},
	})
	if err == nil {
		t.Fatal("AuthSignupStart swallowed an unexpected error, want it returned so ResponseErrorHandlerFunc maps it to 500")
	}
}

func TestAuthSignupVerify_Success_ReturnsTicket(t *testing.T) {
	h := newTestHandler(&fakeAuthService{signupVerifyTicket: "the-ticket"})

	resp, err := h.AuthSignupVerify(context.Background(), AuthSignupVerifyRequestObject{
		Body: &AuthSignupVerifyJSONRequestBody{Email: openapi_types.Email("a@example.com"), Code: "123456"},
	})
	if err != nil {
		t.Fatalf("AuthSignupVerify: %v", err)
	}
	verifyResp, ok := resp.(AuthSignupVerify200JSONResponse)
	if !ok {
		t.Fatalf("AuthSignupVerify returned %T, want AuthSignupVerify200JSONResponse", resp)
	}
	if verifyResp.Ticket != "the-ticket" {
		t.Fatalf("Ticket = %q, want %q", verifyResp.Ticket, "the-ticket")
	}
}

func TestAuthSignupVerify_InvalidCode_MapsGeneric400(t *testing.T) {
	h := newTestHandler(&fakeAuthService{signupVerifyErr: signupcode.ErrInvalidCode})

	resp, err := h.AuthSignupVerify(context.Background(), AuthSignupVerifyRequestObject{
		Body: &AuthSignupVerifyJSONRequestBody{Email: openapi_types.Email("a@example.com"), Code: "000000"},
	})
	if err != nil {
		t.Fatalf("AuthSignupVerify: %v", err)
	}
	errResp, ok := resp.(AuthSignupVerify400JSONResponse)
	if !ok {
		t.Fatalf("AuthSignupVerify returned %T, want AuthSignupVerify400JSONResponse", resp)
	}
	if errResp.Error.Code != "invalid_code" {
		t.Fatalf("error code = %q, want invalid_code", errResp.Error.Code)
	}
}

func TestAuthSignupComplete_Success_SetsCookieAndReturnsUser(t *testing.T) {
	user := identity.User{ID: 7, Email: "a@example.com", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	h := newTestHandler(&fakeAuthService{signupCompleteToken: "session-token", signupCompleteUser: user})

	resp, err := h.AuthSignupComplete(context.Background(), AuthSignupCompleteRequestObject{
		Body: &AuthSignupCompleteJSONRequestBody{Ticket: "the-ticket", Password: "whatever-12345"},
	})
	if err != nil {
		t.Fatalf("AuthSignupComplete: %v", err)
	}
	completeResp, ok := resp.(authSignupCompleteResponse)
	if !ok {
		t.Fatalf("AuthSignupComplete returned %T, want authSignupCompleteResponse", resp)
	}
	if completeResp.user.Id != 7 {
		t.Fatalf("user.Id = %d, want 7", completeResp.user.Id)
	}
	if completeResp.cookie.Value != "session-token" {
		t.Fatalf("cookie.Value = %q, want the raw session token", completeResp.cookie.Value)
	}
}

func TestAuthSignupComplete_InvalidTicket_MapsGeneric400(t *testing.T) {
	h := newTestHandler(&fakeAuthService{signupCompleteErr: signupcode.ErrInvalidTicket})

	resp, err := h.AuthSignupComplete(context.Background(), AuthSignupCompleteRequestObject{
		Body: &AuthSignupCompleteJSONRequestBody{Ticket: "bad-ticket", Password: "whatever-12345"},
	})
	if err != nil {
		t.Fatalf("AuthSignupComplete: %v", err)
	}
	errResp, ok := resp.(AuthSignupComplete400JSONResponse)
	if !ok {
		t.Fatalf("AuthSignupComplete returned %T, want AuthSignupComplete400JSONResponse", resp)
	}
	if errResp.Error.Code != "invalid_ticket" {
		t.Fatalf("error code = %q, want invalid_ticket", errResp.Error.Code)
	}
}

func TestAuthSignupComplete_UnexpectedError_IsNotSwallowed(t *testing.T) {
	h := newTestHandler(&fakeAuthService{signupCompleteErr: errors.New("boom")})

	_, err := h.AuthSignupComplete(context.Background(), AuthSignupCompleteRequestObject{
		Body: &AuthSignupCompleteJSONRequestBody{Ticket: "t", Password: "whatever-12345"},
	})
	if err == nil {
		t.Fatal("AuthSignupComplete swallowed an unexpected error, want it returned so ResponseErrorHandlerFunc maps it to 500")
	}
}
