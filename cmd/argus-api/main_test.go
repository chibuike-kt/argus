package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chibuike-kt/argus/internal/httpapi"
)

// main.go had zero test coverage (project-status audit, section H).
// httpapi.NewRouter is already exported and takes the exact Deps
// (Logger, DB, Redis) main's own setup builds -- no buildServer
// extraction is needed here; this test calls NewRouter the same way
// main does, just with DB and Redis left nil.
//
// healthHandler.ready (internal/httpapi/health.go) is already nil-safe
// -- a nil DB/Redis reports "not_configured" rather than panicking --
// so that is this test's own "existing test double" for /readyz, per
// the brief's own instruction; /healthz and /api/v1/ping touch neither
// dependency at all.
func testRouter(t *testing.T) http.Handler {
	t.Helper()
	return httpapi.NewRouter(httpapi.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func TestMain_Healthz_OK(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: status = %d, want 200", rec.Code)
	}
}

func TestMain_Readyz_NilDepsNotConfigured(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /readyz with nil deps: status = %d, want 200 (not_configured, not an error)", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /readyz body: %v", err)
	}
	if body["postgres"] != "not_configured" || body["redis"] != "not_configured" {
		t.Fatalf("GET /readyz body = %+v, want both checks \"not_configured\"", body)
	}
}

func TestMain_Ping_OK(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/ping: status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}

func TestMain_UnknownRoute_SharedErrorShape(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/no-such-route", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /no-such-route: status = %d, want 404", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode unknown-route body as the shared Error shape: %v (body: %s)", err, rec.Body.String())
	}
	if body.Error.Code == "" || body.Error.Message == "" {
		t.Fatalf("unknown-route body = %+v, want a non-empty error.code and error.message", body)
	}
}
