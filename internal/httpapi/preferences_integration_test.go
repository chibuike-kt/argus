//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/identity/session"
)

const prefsTestOrigin = "http://localhost:5173"

// prefsServer is the production router and middleware (session, CSRF,
// body limit, timeout) served by the production NewServer.
func prefsServer(t *testing.T) (base string, users *identity.Store, sessions *session.Store) {
	t.Helper()
	db := testDB(t)
	deps, users, sessions := testAuthDeps(db)
	deps.Logger, deps.DB = slog.Default(), db
	deps.AuthPolicy.AllowedOrigins = []string{prefsTestOrigin}
	return startRealServer(t, NewRouter(deps), 15*time.Second, 30*time.Second), users, sessions
}

// newUser creates a verified user and returns its id.
func newUser(t *testing.T, users *identity.Store) int64 {
	t.Helper()
	now := time.Now()
	u, err := users.InsertVerified(context.Background(), fmt.Sprintf("prefs-%d@example.com", time.Now().UnixNano()),
		"$argon2id$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA", now, now, "test")
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// deviceFor is one browser for userID: its own session and cookie jar --
// a second call for the same user is "logging in on another device".
func deviceFor(t *testing.T, base string, sessions *session.Store, userID int64) *http.Client {
	t.Helper()
	raw, _, err := sessions.Create(context.Background(), userID, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(base)
	jar.SetCookies(u, []*http.Cookie{{Name: "argus_session", Value: raw}})
	return &http.Client{Jar: jar}
}

type prefsResp struct {
	Data    json.RawMessage `json:"data"`
	Version int32           `json:"version"`
}

func doJSON(t *testing.T, c *http.Client, method, u string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, u, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", prefsTestOrigin)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func putPrefs(t *testing.T, c *http.Client, base string, data string, version int32) (int, prefsResp) {
	t.Helper()
	code, body := doJSON(t, c, http.MethodPut, base+"/api/v1/me/preferences", map[string]any{"data": json.RawMessage(data), "version": version})
	var p prefsResp
	_ = json.Unmarshal(body, &p)
	return code, p
}

func getPrefs(t *testing.T, c *http.Client, base string) prefsResp {
	t.Helper()
	code, body := doJSON(t, c, http.MethodGet, base+"/api/v1/me/preferences", nil)
	if code != http.StatusOK {
		t.Fatalf("GET preferences = %d: %s", code, body)
	}
	var p prefsResp
	_ = json.Unmarshal(body, &p)
	return p
}

func TestIntegration_Preferences_PersistAcrossDevicesWithVersioning(t *testing.T) {
	base, users, sessions := prefsServer(t)
	uid := newUser(t, users)
	laptop := deviceFor(t, base, sessions, uid)

	// Nothing stored yet: {} at version 0.
	if p := getPrefs(t, laptop, base); string(p.Data) != "{}" || p.Version != 0 {
		t.Fatalf("initial = %s v%d", p.Data, p.Version)
	}
	code, p := putPrefs(t, laptop, base, `{"timeframe":"4h","right_panel_tab":"watchlist"}`, 0)
	if code != http.StatusOK || p.Version != 1 {
		t.Fatalf("first PUT = %d v%d", code, p.Version)
	}

	// A login on another device (new session, empty browser) sees it.
	phone := deviceFor(t, base, sessions, uid)
	got := getPrefs(t, phone, base)
	if got.Version != 1 || !strings.Contains(string(got.Data), `"timeframe":"4h"`) {
		t.Fatalf("second device sees %s v%d", got.Data, got.Version)
	}

	// Two tabs: the phone writes v1 -> v2; the laptop's write based on v1
	// is now stale -> 409, nothing overwritten.
	if code, _ := putPrefs(t, phone, base, `{"timeframe":"1h","right_panel_tab":"watchlist"}`, 1); code != http.StatusOK {
		t.Fatalf("phone PUT = %d", code)
	}
	if code, _ := putPrefs(t, laptop, base, `{"timeframe":"5m","right_panel_tab":"watchlist"}`, 1); code != http.StatusConflict {
		t.Fatalf("stale laptop PUT = %d, want 409", code)
	}
	if p := getPrefs(t, laptop, base); p.Version != 2 || !strings.Contains(string(p.Data), `"1h"`) {
		t.Fatalf("after conflict = %s v%d, want the phone's v2 untouched", p.Data, p.Version)
	}
	// A first write claiming version 0 when one exists is also stale.
	if code, _ := putPrefs(t, laptop, base, `{}`, 0); code != http.StatusConflict {
		t.Fatalf("version-0 overwrite = %d, want 409", code)
	}
}

func TestIntegration_Preferences_OwnershipValidationAndLimits(t *testing.T) {
	base, users, sessions := prefsServer(t)
	alice := deviceFor(t, base, sessions, newUser(t, users))
	bob := deviceFor(t, base, sessions, newUser(t, users))

	if code, _ := putPrefs(t, alice, base, `{"timeframe":"1d"}`, 0); code != http.StatusOK {
		t.Fatalf("alice PUT = %d", code)
	}
	// Bob never sees Alice's document: ownership is the session user.
	if p := getPrefs(t, bob, base); string(p.Data) != "{}" || p.Version != 0 {
		t.Fatalf("bob sees %s v%d", p.Data, p.Version)
	}

	// Unauthenticated: 401.
	if code, _ := doJSON(t, &http.Client{}, http.MethodGet, base+"/api/v1/me/preferences", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401", code)
	}
	// Unknown key: 400.
	if code, body := putPrefs(t, bob, base, `{"theme":"dark"}`, 0); code != http.StatusBadRequest {
		t.Fatalf("unknown key = %d %v, want 400", code, body)
	}
	// 70 KB: 413 (the router's own body limit or the schema cap).
	big := `{"watchlist_groups_collapsed":{"` + strings.Repeat("a", 70*1024) + `":true}}`
	if code, _ := putPrefs(t, bob, base, big, 0); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("70 KB = %d, want 413", code)
	}
}

func TestIntegration_Drawings_PerChartVersionedAndCapped(t *testing.T) {
	base, users, sessions := prefsServer(t)
	db := testDB(t)
	uid := newUser(t, users)
	c := deviceFor(t, base, sessions, uid)

	inst := testInstrument(t, db)
	enableInstrument(t, db, inst)
	disabled := testInstrument(t, db)
	u := func(id int64, tf string) string {
		return fmt.Sprintf("%s/api/v1/me/drawings?instrument_id=%d&timeframe=%s", base, id, tf)
	}

	code, body := doJSON(t, c, http.MethodGet, u(inst, "1m"), nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"data":[]`) {
		t.Fatalf("empty GET = %d %s", code, body)
	}
	drawing := json.RawMessage(`[{"id":"d1","kind":"trendline","p1":{"time":1,"price":2}}]`)
	if code, body := doJSON(t, c, http.MethodPut, u(inst, "1m"), map[string]any{"data": drawing, "version": 0}); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	// Scoped per timeframe: 5m is still empty.
	if _, body := doJSON(t, c, http.MethodGet, u(inst, "5m"), nil); !strings.Contains(string(body), `"data":[]`) {
		t.Fatalf("5m leaked 1m drawings: %s", body)
	}
	// Stale version: 409.
	if code, _ := doJSON(t, c, http.MethodPut, u(inst, "1m"), map[string]any{"data": drawing, "version": 0}); code != http.StatusConflict {
		t.Fatalf("stale drawings PUT = %d, want 409", code)
	}
	// 200 KB fits the 256 KB drawings cap (above the API's 64 KB default).
	mid := json.RawMessage(`[{"id":"t","kind":"text","text":"` + strings.Repeat("x", 200*1024) + `"}]`)
	if code, body := doJSON(t, c, http.MethodPut, u(inst, "1h"), map[string]any{"data": mid, "version": 0}); code != http.StatusOK {
		t.Fatalf("200 KB drawings = %d %s", code, body)
	}
	// 300 KB: 413.
	huge := json.RawMessage(`[{"id":"t","kind":"text","text":"` + strings.Repeat("x", 300*1024) + `"}]`)
	if code, _ := doJSON(t, c, http.MethodPut, u(inst, "4h"), map[string]any{"data": huge, "version": 0}); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("300 KB drawings = %d, want 413", code)
	}
	// Disabled or unknown instrument: 404, same answer.
	for _, id := range []int64{disabled, 999_999_999} {
		if code, _ := doJSON(t, c, http.MethodGet, u(id, "1m"), nil); code != http.StatusNotFound {
			t.Fatalf("instrument %d = %d, want 404", id, code)
		}
	}
	// Bad timeframe: 400.
	if code, _ := doJSON(t, c, http.MethodGet, u(inst, "2h"), nil); code != http.StatusBadRequest {
		t.Fatalf("bad timeframe = %d, want 400", code)
	}
}

// ADR-016: the rows go with the account.
func TestIntegration_Preferences_DeletedWithTheAccount(t *testing.T) {
	base, users, sessions := prefsServer(t)
	db := testDB(t)
	uid := newUser(t, users)
	c := deviceFor(t, base, sessions, uid)
	inst := testInstrument(t, db)
	enableInstrument(t, db, inst)

	if code, _ := putPrefs(t, c, base, `{"timeframe":"1d"}`, 0); code != http.StatusOK {
		t.Fatal("PUT preferences")
	}
	drawing := json.RawMessage(`[{"id":"d1","kind":"hline"}]`)
	if code, _ := doJSON(t, c, http.MethodPut, fmt.Sprintf("%s/api/v1/me/drawings?instrument_id=%d&timeframe=1m", base, inst), map[string]any{"data": drawing, "version": 0}); code != http.StatusOK {
		t.Fatal("PUT drawings")
	}

	ctx := context.Background()
	// Delete the account the way a deletion would: the user row (and its
	// sessions first, which reference it).
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, uid); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM user_preferences WHERE user_id = $1) + (SELECT count(*) FROM user_drawings WHERE user_id = $1)`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d preference/drawing rows survived account deletion", n)
	}
}
