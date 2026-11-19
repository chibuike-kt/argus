//go:build integration

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// DELETE /watchlist/items/{instrument_id} through the production router
// and middleware (session, CSRF origin check), against a real Postgres.

func watchlistDo(t *testing.T, srv *authTestServer, cookie *http.Cookie, method, path string, body any) (int, []WatchlistItem) {
	t.Helper()
	req := authJSONRequest(t, method, srv.URL+"/api/v1"+path, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var items []WatchlistItem
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
			t.Fatalf("decode %s %s: %v", method, path, err)
		}
	}
	return resp.StatusCode, items
}

func ids(items []WatchlistItem) []int64 {
	out := make([]int64, len(items))
	for i, it := range items {
		out[i] = it.InstrumentId
	}
	return out
}

func TestIntegration_RemoveWatchlistItem(t *testing.T) {
	srv := newAuthTestServer(t, testAuthPolicy())
	alice := signupNewUser(t, srv, testAuthEmail(t), "a-perfectly-fine-password-1")
	bob := signupNewUser(t, srv, testAuthEmail(t), "a-perfectly-fine-password-2")
	a, b, c := testEnabledInstrument(t, srv.db), testEnabledInstrument(t, srv.db), testEnabledInstrument(t, srv.db)

	if st, _ := watchlistDo(t, srv, alice, http.MethodPut, "/watchlist", WatchlistUpdateRequest{InstrumentIds: []int64{a, b, c}}); st != http.StatusOK {
		t.Fatalf("alice PUT = %d", st)
	}
	if st, _ := watchlistDo(t, srv, bob, http.MethodPut, "/watchlist", WatchlistUpdateRequest{InstrumentIds: []int64{b}}); st != http.StatusOK {
		t.Fatalf("bob PUT = %d", st)
	}

	t.Run("removes one item and returns the rest in order", func(t *testing.T) {
		st, items := watchlistDo(t, srv, alice, http.MethodDelete, fmt.Sprintf("/watchlist/items/%d", b), nil)
		if st != http.StatusOK || fmt.Sprint(ids(items)) != fmt.Sprint([]int64{a, c}) {
			t.Fatalf("DELETE b = %d %v, want 200 [%d %d]", st, ids(items), a, c)
		}
		_, got := watchlistDo(t, srv, alice, http.MethodGet, "/watchlist", nil)
		if fmt.Sprint(ids(got)) != fmt.Sprint([]int64{a, c}) {
			t.Fatalf("GET after delete = %v, want it persisted", ids(got))
		}
	})

	t.Run("idempotent: removing it again is not an error", func(t *testing.T) {
		st, items := watchlistDo(t, srv, alice, http.MethodDelete, fmt.Sprintf("/watchlist/items/%d", b), nil)
		if st != http.StatusOK || len(items) != 2 {
			t.Fatalf("second DELETE b = %d %v, want 200 and the same two items", st, ids(items))
		}
	})

	t.Run("only the caller's own list changes", func(t *testing.T) {
		_, got := watchlistDo(t, srv, bob, http.MethodGet, "/watchlist", nil)
		if fmt.Sprint(ids(got)) != fmt.Sprint([]int64{b}) {
			t.Fatalf("bob's list = %v after alice removed %d, want [%d]", ids(got), b, b)
		}
	})

	t.Run("unauthenticated is 401", func(t *testing.T) {
		if st, _ := watchlistDo(t, srv, nil, http.MethodDelete, fmt.Sprintf("/watchlist/items/%d", a), nil); st != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", st)
		}
	})

	t.Run("a cross-origin delete is refused (CSRF)", func(t *testing.T) {
		req := authJSONRequest(t, http.MethodDelete, srv.URL+fmt.Sprintf("/api/v1/watchlist/items/%d", a), nil)
		req.Header.Set("Origin", "https://evil.example.com")
		req.AddCookie(alice)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
		_, got := watchlistDo(t, srv, alice, http.MethodGet, "/watchlist", nil)
		if len(got) != 2 {
			t.Fatalf("list = %v after a refused delete, want unchanged", ids(got))
		}
	})

	t.Run("a bad id is 400", func(t *testing.T) {
		for _, p := range []string{"/watchlist/items/0", "/watchlist/items/-3", "/watchlist/items/abc"} {
			if st, _ := watchlistDo(t, srv, alice, http.MethodDelete, p, nil); st != http.StatusBadRequest {
				t.Fatalf("DELETE %s = %d, want 400", p, st)
			}
		}
	})
}
