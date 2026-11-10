package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/password"
	"github.com/chibuike-kt/argus/internal/identity/service"
	"github.com/chibuike-kt/argus/internal/identity/session"
	"github.com/chibuike-kt/argus/internal/identity/signupcode"
)

// authService is the boundary apiHandler needs from
// internal/identity/service.Service -- the same consumer-defined-
// interface pattern as setupsStore/instrumentsStore/candlesStore.
type authService interface {
	SignupStart(ctx context.Context, email string, acceptedTerms bool, ip, userAgent string) error
	SignupVerify(ctx context.Context, email, code, ip, userAgent string) (ticket string, err error)
	SignupComplete(ctx context.Context, ticket, pw, ip, userAgent string) (string, identity.User, error)
	Login(ctx context.Context, email, pw, ip, userAgent string) (string, identity.User, error)
	Logout(ctx context.Context, sessionID, userID int64, ip, userAgent string) error
	LogoutAll(ctx context.Context, userID int64, ip, userAgent string) error
	ListSessions(ctx context.Context, userID int64) ([]session.Session, error)
	RevokeSession(ctx context.Context, sessionID, userID int64, ip, userAgent string) error
	UpdateProfile(ctx context.Context, userID int64, displayName, ip, userAgent string) (identity.User, error)
	CompleteOnboarding(ctx context.Context, userID int64, ip, userAgent string) (identity.User, error)
}

var _ authService = (*service.Service)(nil)

func (h *apiHandler) AuthSignupStart(ctx context.Context, request AuthSignupStartRequestObject) (AuthSignupStartResponseObject, error) {
	if request.Body == nil {
		return AuthSignupStart400JSONResponse(apiError("invalid_argument", "request body is required")), nil
	}
	ip, userAgent := requestMeta(ctx)
	err := h.auth.SignupStart(ctx, string(request.Body.Email), request.Body.AcceptedTerms, ip, userAgent)
	if err == nil {
		return AuthSignupStart202JSONResponse{Status: "accepted"}, nil
	}
	if errors.Is(err, service.ErrTermsNotAccepted) {
		return AuthSignupStart400JSONResponse(apiError("invalid_argument", err.Error())), nil
	}
	var rl *service.RateLimitedError
	if errors.As(err, &rl) {
		return AuthSignupStart429JSONResponse(apiError("rate_limited", rl.Error())), nil
	}
	h.logger.Error("httpapi: signup start", "error", err)
	return nil, fmt.Errorf("signup start: %w", err)
}

func (h *apiHandler) AuthSignupVerify(ctx context.Context, request AuthSignupVerifyRequestObject) (AuthSignupVerifyResponseObject, error) {
	if request.Body == nil {
		return AuthSignupVerify400JSONResponse(apiError("invalid_argument", "request body is required")), nil
	}
	ip, userAgent := requestMeta(ctx)
	ticket, err := h.auth.SignupVerify(ctx, string(request.Body.Email), request.Body.Code, ip, userAgent)
	if err == nil {
		return AuthSignupVerify200JSONResponse{Ticket: ticket}, nil
	}
	if errors.Is(err, signupcode.ErrInvalidCode) {
		return AuthSignupVerify400JSONResponse(apiError("invalid_code", "the code is invalid, expired, unknown, or exhausted")), nil
	}
	h.logger.Error("httpapi: signup verify", "error", err)
	return nil, fmt.Errorf("signup verify: %w", err)
}

func (h *apiHandler) AuthSignupComplete(ctx context.Context, request AuthSignupCompleteRequestObject) (AuthSignupCompleteResponseObject, error) {
	if request.Body == nil {
		return AuthSignupComplete400JSONResponse(apiError("invalid_argument", "request body is required")), nil
	}
	ip, userAgent := requestMeta(ctx)
	rawToken, user, err := h.auth.SignupComplete(ctx, request.Body.Ticket, request.Body.Password, ip, userAgent)
	if err == nil {
		return authSignupCompleteResponse{user: toAuthUser(user), cookie: h.newSessionCookie(rawToken)}, nil
	}
	if errors.Is(err, signupcode.ErrInvalidTicket) {
		return AuthSignupComplete400JSONResponse(apiError("invalid_ticket", "the ticket is invalid, expired, or already used")), nil
	}
	switch {
	case errors.Is(err, password.ErrTooShort), errors.Is(err, password.ErrTooLong),
		errors.Is(err, password.ErrCommon), errors.Is(err, password.ErrContainsEmail):
		return AuthSignupComplete400JSONResponse(apiError("invalid_argument", err.Error())), nil
	}
	h.logger.Error("httpapi: signup complete", "error", err)
	return nil, fmt.Errorf("signup complete: %w", err)
}

func (h *apiHandler) AuthLogin(ctx context.Context, request AuthLoginRequestObject) (AuthLoginResponseObject, error) {
	if request.Body == nil {
		return AuthLogin401JSONResponse(apiError("invalid_credentials", "invalid email or password")), nil
	}
	ip, userAgent := requestMeta(ctx)
	rawToken, user, err := h.auth.Login(ctx, string(request.Body.Email), request.Body.Password, ip, userAgent)
	if err == nil {
		return authLoginResponse{
			user:   toAuthUser(user),
			cookie: h.newSessionCookie(rawToken),
		}, nil
	}
	if errors.Is(err, service.ErrInvalidCredentials) {
		return AuthLogin401JSONResponse(apiError("invalid_credentials", "invalid email or password")), nil
	}
	var rl *service.RateLimitedError
	if errors.As(err, &rl) {
		return AuthLogin429JSONResponse(apiError("rate_limited", rl.Error())), nil
	}
	h.logger.Error("httpapi: login", "error", err)
	return nil, fmt.Errorf("login: %w", err)
}

func (h *apiHandler) AuthLogout(ctx context.Context, _ AuthLogoutRequestObject) (AuthLogoutResponseObject, error) {
	ip, userAgent := requestMeta(ctx)
	user, hasUser := identity.UserFromContext(ctx)
	sessionID, hasSession := identity.SessionIDFromContext(ctx)
	if hasUser && hasSession {
		if err := h.auth.Logout(ctx, sessionID, user.ID, ip, userAgent); err != nil {
			h.logger.Error("httpapi: logout", "error", err)
			return nil, fmt.Errorf("logout: %w", err)
		}
	}
	return authLogoutResponse{cookie: h.clearedSessionCookie()}, nil
}

func (h *apiHandler) AuthLogoutAll(ctx context.Context, _ AuthLogoutAllRequestObject) (AuthLogoutAllResponseObject, error) {
	ip, userAgent := requestMeta(ctx)
	user, _ := identity.UserFromContext(ctx)
	if err := h.auth.LogoutAll(ctx, user.ID, ip, userAgent); err != nil {
		h.logger.Error("httpapi: logout all", "user_id", user.ID, "error", err)
		return nil, fmt.Errorf("logout all: %w", err)
	}
	return authLogoutAllResponse{cookie: h.clearedSessionCookie()}, nil
}

func (h *apiHandler) AuthMe(ctx context.Context, _ AuthMeRequestObject) (AuthMeResponseObject, error) {
	user, _ := identity.UserFromContext(ctx)
	return AuthMe200JSONResponse(toAuthUser(user)), nil
}

func (h *apiHandler) AuthListSessions(ctx context.Context, _ AuthListSessionsRequestObject) (AuthListSessionsResponseObject, error) {
	user, _ := identity.UserFromContext(ctx)
	currentID, _ := identity.SessionIDFromContext(ctx)
	rows, err := h.auth.ListSessions(ctx, user.ID)
	if err != nil {
		h.logger.Error("httpapi: list sessions", "user_id", user.ID, "error", err)
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	out := make(AuthListSessions200JSONResponse, 0, len(rows))
	for _, s := range rows {
		out = append(out, toAuthSession(s, s.ID == currentID))
	}
	return out, nil
}

func (h *apiHandler) AuthRevokeSession(ctx context.Context, request AuthRevokeSessionRequestObject) (AuthRevokeSessionResponseObject, error) {
	ip, userAgent := requestMeta(ctx)
	user, _ := identity.UserFromContext(ctx)
	err := h.auth.RevokeSession(ctx, request.Id, user.ID, ip, userAgent)
	if err == nil {
		return AuthRevokeSession204Response{}, nil
	}
	if errors.Is(err, session.ErrNotFound) {
		return AuthRevokeSession404JSONResponse(apiError("not_found", "session not found")), nil
	}
	h.logger.Error("httpapi: revoke session", "user_id", user.ID, "session_id", request.Id, "error", err)
	return nil, fmt.Errorf("revoke session: %w", err)
}

func toAuthUser(u identity.User) AuthUser {
	return AuthUser{
		Id:                  u.ID,
		Email:               u.Email,
		EmailVerified:       u.EmailVerified(),
		CreatedAt:           u.CreatedAt,
		DisplayName:         u.DisplayName,
		OnboardingCompleted: u.OnboardingCompleted(),
	}
}

func toAuthSession(s session.Session, current bool) AuthSession {
	out := AuthSession{
		Id:         s.ID,
		CreatedAt:  s.CreatedAt,
		LastSeenAt: s.LastSeenAt,
		Current:    current,
	}
	if s.UserAgent != "" {
		ua := s.UserAgent
		out.UserAgent = &ua
	}
	if s.IP != "" {
		ip := s.IP
		out.Ip = &ip
	}
	return out
}

// requestMeta reads the ip/userAgent this request's own auth
// middleware (or, for public routes with no middleware-populated
// context, this handler itself) should record. Public routes
// (register, login, ...) call h.auth's methods directly with values
// derived here; protected routes additionally have a user/session in
// context already.
func requestMeta(ctx context.Context) (ip, userAgent string) {
	if v, ok := ctx.Value(requestIPContextKey{}).(string); ok {
		ip = v
	}
	if v, ok := ctx.Value(requestUserAgentContextKey{}).(string); ok {
		userAgent = v
	}
	return ip, userAgent
}

type requestIPContextKey struct{}
type requestUserAgentContextKey struct{}

// withRequestMeta is how NewRouter's own middleware (not the session
// middleware -- this runs on every request, public or not) makes
// ip/userAgent available to a handler via requestMeta, without every
// handler needing *http.Request directly (the strict-server pattern
// only hands handlers a context.Context).
func withRequestMeta(ctx context.Context, ip, userAgent string) context.Context {
	ctx = context.WithValue(ctx, requestIPContextKey{}, ip)
	ctx = context.WithValue(ctx, requestUserAgentContextKey{}, userAgent)
	return ctx
}

// newSessionCookie builds the Set-Cookie for a freshly created
// session. MaxAge matches SessionAbsoluteDuration (ADR-015 addendum
// row 5 does not specify an explicit Max-Age/Expires -- only Path,
// HttpOnly, Secure, SameSite and no Domain -- so this is this brief's
// own choice, flagged in the report): the browser keeps offering the
// cookie for as long as the session could possibly still be valid,
// while the server's own idle/absolute checks are what actually decide
// validity on every request regardless of what the browser thinks.
func (h *apiHandler) newSessionCookie(rawToken string) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is h.authPolicy.SecureCookies, always true outside local http (ADR-015 addendum row 5); gosec cannot see through the call
		Name:     h.authPolicy.CookieName(h.isProduction),
		Value:    rawToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.authPolicy.SecureCookies(h.isProduction),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.authPolicy.SessionAbsoluteDuration.Seconds()),
	}
}

// clearedSessionCookie expires the cookie immediately -- logout/
// logout-all's own Set-Cookie.
func (h *apiHandler) clearedSessionCookie() *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is h.authPolicy.SecureCookies, always true outside local http (ADR-015 addendum row 5); gosec cannot see through the call
		Name:     h.authPolicy.CookieName(h.isProduction),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.authPolicy.SecureCookies(h.isProduction),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// authLoginResponse/authLogoutResponse/authLogoutAllResponse implement
// their own Visit...Response methods directly (rather than using the
// generated Auth...JSONResponse type aliases) because oapi-codegen's
// generated Visit methods have no way to also write a Set-Cookie
// header -- this is the one, deliberate place that generated contract
// is bypassed, and only to add a header, never to change the response
// body/status the spec itself documents.
type authLoginResponse struct {
	user   AuthUser
	cookie *http.Cookie
}

func (r authLoginResponse) VisitAuthLoginResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	return AuthLogin200JSONResponse(r.user).VisitAuthLoginResponse(w)
}

// authSignupCompleteResponse is the signup-complete equivalent of
// authLoginResponse -- same reason, same pattern: it logs the new
// account in (ADR-015), so it needs the same Set-Cookie header the
// generated Auth...JSONResponse type alias has no way to add.
type authSignupCompleteResponse struct {
	user   AuthUser
	cookie *http.Cookie
}

func (r authSignupCompleteResponse) VisitAuthSignupCompleteResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	return AuthSignupComplete200JSONResponse(r.user).VisitAuthSignupCompleteResponse(w)
}

type authLogoutResponse struct {
	cookie *http.Cookie
}

func (r authLogoutResponse) VisitAuthLogoutResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	return AuthLogout204Response{}.VisitAuthLogoutResponse(w)
}

type authLogoutAllResponse struct {
	cookie *http.Cookie
}

func (r authLogoutAllResponse) VisitAuthLogoutAllResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	return AuthLogoutAll204Response{}.VisitAuthLogoutAllResponse(w)
}
