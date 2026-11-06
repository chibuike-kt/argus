// Package httpapi wires the REST router, middleware and HTTP server.
package httpapi

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/marketdata"
	"github.com/chibuike-kt/argus/internal/setups"
)

// Deps are the dependencies the HTTP layer needs.
type Deps struct {
	Logger *slog.Logger
	DB     *sql.DB
	Redis  *redis.Client
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

	r.Route("/api/v1", func(r chi.Router) {
		// OpenAPI-first (ADR-004): every operation in api/openapi.yaml,
		// generated into openapi_gen.go by oapi-codegen (make generate),
		// is served through the one strict handler below -- including
		// /ping, which this replaces (it was hand-rolled before any
		// feature module had a generated route to mount alongside it).
		api := &apiHandler{
			store:       setups.NewStore(d.DB),
			instruments: instruments.NewStore(d.DB),
			candles:     marketdata.NewStore(d.DB, clock.Real{}),
			logger:      d.Logger,
		}
		strict := NewStrictHandlerWithOptions(api, nil, StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
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
