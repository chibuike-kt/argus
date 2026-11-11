// Package httpapi wires the REST router, middleware and HTTP server.
package httpapi

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/setups"
)

// maxRequestBodyBytes bounds every /api/v1 request body (architect
// review item 4): an implementation limit, not an ADR-015 addendum
// policy value, so it lives here next to the middleware that enforces
// it rather than in config.AuthPolicy. Pre-auth endpoints (register,
// login, ...) must not accept an unbounded body before any
// authentication has even happened; applied to every /api/v1 route,
// not only the auth ones, since no route needs more than this.
const maxRequestBodyBytes = 64 * 1024

// limitRequestBody wraps the request body in http.MaxBytesReader so a
// read past maxRequestBodyBytes fails instead of buffering an
// arbitrarily large body into memory. The read failure itself is
// translated to 413 in the strict handler's RequestErrorHandlerFunc
// below, once oapi-codegen's own JSON decode actually hits it.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// Deps are the dependencies the HTTP layer needs.
type Deps struct {
	Logger *slog.Logger
	DB     *sql.DB
	Redis  *redis.Client

	// Auth is internal/identity/service.Service; Sessions is
	// *internal/identity/session.Store; Users is *internal/identity.Store
	// -- all three declared here as this package's own consumer-defined
	// interfaces (authService/sessionResolver/userLookup, this brief's
	// own rule 7) so server.go does not need to import those packages
	// just for a type name, and so a test can satisfy them with a
	// hand-written fake instead of a real Postgres connection.
	Auth         authService
	Watchlist    watchlistStore
	Sessions     sessionResolver
	Users        userLookup
	AuthPolicy   config.AuthPolicy
	IsProduction bool
	Clock        clock.Clock
}

// NewRouter builds the chi router with base middleware and routes.
// REST is versioned under /api/v1 per ADR-004. Health endpoints live at the root.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// middleware.RealIP is deliberately not used: it trusts X-Forwarded-For
	// unconditionally, which is spoofable by any client unless we sit
	// behind a trusted proxy that sets it (guide 12; GO-2026-5777,
	// GO-2026-5775). Re-add it once deployed behind one, with an explicit
	// trusted-proxy configuration rather than chi's unconditional default.
	r.Use(requestLogger(d.Logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	h := &healthHandler{db: d.DB, redis: d.Redis, logger: d.Logger}
	r.Get("/healthz", h.live)
	r.Get("/readyz", h.ready)

	// chi's own default 404 is plain text ("404 page not found"), not
	// this API's shared Error shape -- every other error response goes
	// through writeAPIError (see the RequestErrorHandlerFunc/
	// ResponseErrorHandlerFunc below), so an unmatched route should too,
	// not fall through to a different, undocumented body.
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusNotFound, "not_found", "no such route")
	})

	r.Route("/api/v1", func(r chi.Router) {
		clk := d.Clock
		if clk == nil {
			clk = clock.Real{}
		}
		// Every /api/v1 request gets its body size bounded first (before
		// anything else reads it), then ip/User-Agent in context (public
		// or not -- Register/Login/etc. need it too), then CSRF
		// checking on state-changing methods, then deny-by-default
		// session resolution (requireSession's own public-path
		// allow-list is what actually exempts a route, not this
		// ordering).
		r.Use(limitRequestBody)
		r.Use(requestMetaMiddleware(d.AuthPolicy.TrustProxyForIP))
		r.Use(csrfProtection(d.AuthPolicy.AllowedOrigins))
		r.Use(requireSession(d.Sessions, d.Users, d.AuthPolicy.CookieName(d.IsProduction), d.AuthPolicy.SessionTouchThrottle, clk, d.Logger))

		// OpenAPI-first (ADR-004): every operation in api/openapi.yaml,
		// generated into openapi_gen.go by oapi-codegen (make generate),
		// is served through the one strict handler below -- including
		// /ping, which this replaces (it was hand-rolled before any
		// feature module had a generated route to mount alongside it).
		api := &apiHandler{
			store:        setups.NewStore(d.DB),
			instruments:  instruments.NewStore(d.DB),
			candles:      marketdata.NewStore(d.DB, clock.Real{}),
			auth:         d.Auth,
			watchlist:    d.Watchlist,
			authPolicy:   d.AuthPolicy,
			isProduction: d.IsProduction,
			logger:       d.Logger,
		}
		strict := NewStrictHandlerWithOptions(api, nil, StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
					writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
					return
				}
				writeAPIError(w, http.StatusBadRequest, "invalid_argument", err.Error())
			},
			// err here is whatever a StrictServerInterface method
			// returned as a plain error (never one of its typed
			// *Response values) -- an unexpected failure, not a
			// modeled 4xx. Logged, never echoed into the response body
			// (guide 12, matching health.go's own convention): the
			// handler methods above already logged their own context
			// before returning it, so this is deliberately a second,
			// generic line plus a generic client-facing message.
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				d.Logger.Error("httpapi: unhandled error", "error", err)
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
			},
		})
		HandlerWithOptions(strict, ChiServerOptions{
			BaseRouter: r,
			ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				writeAPIError(w, http.StatusBadRequest, "invalid_argument", err.Error())
			},
		})
	})

	return r
}

// NewServer returns an http.Server configured from timeouts.
func NewServer(addr string, handler http.Handler, readTimeout time.Duration) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
