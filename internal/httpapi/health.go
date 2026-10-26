package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

type healthHandler struct {
	db     *sql.DB
	redis  *redis.Client
	logger *slog.Logger
}

// live reports process liveness. It does not touch dependencies (guide 15:
// liveness is distinct from readiness).
func (h *healthHandler) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ready reports dependency readiness. Per ADR-006 and ADR-018, data freshness
// is a separate concern and is not asserted here; this only checks that the
// datastores are reachable. Error detail is logged, not returned, so internal
// information does not leak to callers (guide 12).
func (h *healthHandler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]string{"postgres": "ok", "redis": "ok"}
	status := http.StatusOK

	if h.db == nil {
		checks["postgres"] = "not_configured"
	} else if err := h.db.PingContext(ctx); err != nil {
		if h.logger != nil {
			h.logger.Error("readiness: postgres ping failed", "error", err)
		}
		checks["postgres"] = "error"
		status = http.StatusServiceUnavailable
	}

	if h.redis == nil {
		checks["redis"] = "not_configured"
	} else if err := h.redis.Ping(ctx).Err(); err != nil {
		if h.logger != nil {
			h.logger.Error("readiness: redis ping failed", "error", err)
		}
		checks["redis"] = "error"
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, checks)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
