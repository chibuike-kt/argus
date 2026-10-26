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
	r.Use(middleware.RealIP)
	r.Use(requestLogger(d.Logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	h := &healthHandler{db: d.DB, redis: d.Redis}
	r.Get("/healthz", h.live)
	r.Get("/readyz", h.ready)

	r.Route("/api/v1", func(r chi.Router) {
		// Feature routes are mounted here as modules land.
		// Example: r.Mount("/instruments", instruments.Routes(...))
		r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"message": "pong"})
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
