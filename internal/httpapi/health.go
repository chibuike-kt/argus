package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

type healthHandler struct {
	db    *sql.DB
	redis *redis.Client
}

// live reports process liveness. It does not touch dependencies.
func (h *healthHandler) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ready reports dependency readiness. Per ADR-006 and ADR-018, data freshness
// is a separate concern and is not asserted here; this only checks that the
// datastores are reachable.
func (h *healthHandler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]string{"postgres": "ok", "redis": "ok"}
	status := http.StatusOK

	if h.db != nil {
		if err := h.db.PingContext(ctx); err != nil {
			checks["postgres"] = "error: " + err.Error()
			status = http.StatusServiceUnavailable
		}
	} else {
		checks["postgres"] = "not configured"
	}

	if h.redis != nil {
		if err := h.redis.Ping(ctx).Err(); err != nil {
			checks["redis"] = "error: " + err.Error()
			status = http.StatusServiceUnavailable
		}
	} else {
		checks["redis"] = "not configured"
	}

	writeJSON(w, status, checks)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
