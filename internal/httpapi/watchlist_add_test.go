package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/identity"
	"github.com/chibuike-kt/argus/internal/tracking"
	"github.com/chibuike-kt/argus/internal/watchlist"
)

// fakeWatchlistStore records what the handler asked for and returns a
// scripted error.
type fakeWatchlistStore struct {
	items      []watchlist.Item
	addErr     error
	reorderErr error
	gotUser    int64
	gotOpts    watchlist.AddOptions
}

func (f *fakeWatchlistStore) List(context.Context, int64) ([]watchlist.Item, error) {
	return f.items, nil
}
func (f *fakeWatchlistStore) Replace(context.Context, int64, []int64, int) ([]watchlist.Item, error) {
	return f.items, nil
}
func (f *fakeWatchlistStore) Remove(context.Context, int64, int64) (bool, error) { return true, nil }
func (f *fakeWatchlistStore) Add(_ context.Context, userID, _ int64, opt watchlist.AddOptions) (watchlist.AddResult, error) {
	f.gotUser, f.gotOpts = userID, opt
	return watchlist.AddResult{Items: f.items}, f.addErr
}
func (f *fakeWatchlistStore) Reorder(_ context.Context, userID int64, _ []int64) ([]watchlist.Item, error) {
	f.gotUser = userID
	return f.items, f.reorderErr
}

type fakeTrackingStore struct {
	tracked []tracking.Tracked
	states  map[int64]tracking.Status
}

func (f fakeTrackingStore) Tracked(context.Context, []string) ([]tracking.Tracked, error) {
	return f.tracked, nil
}
func (f fakeTrackingStore) States(context.Context, []int64) (map[int64]tracking.Status, error) {
	return f.states, nil
}

func watchlistHandler(wl *fakeWatchlistStore, production bool) *apiHandler {
	return &apiHandler{
		watchlist: wl, isProduction: production, environment: "local", trackedCap: 8,
		trackingFloor: []string{"BTC"}, authPolicy: config.AuthPolicy{WatchlistMaxItems: 50}, logger: slog.Default(),
	}
}

func asUser(id int64) context.Context {
	return identity.WithUser(context.Background(), identity.User{ID: id})
}

func TestAddWatchlistItem_UserFromTheSessionAndEnablingOnlyOutsideProduction(t *testing.T) {
	for _, production := range []bool{false, true} {
		wl := &fakeWatchlistStore{items: []watchlist.Item{{InstrumentID: 7}}}
		h := watchlistHandler(wl, production)
		resp, err := h.AddWatchlistItem(asUser(42), AddWatchlistItemRequestObject{Body: &AddWatchlistItemJSONRequestBody{InstrumentId: 7}})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := resp.(AddWatchlistItem200JSONResponse); !ok {
			t.Fatalf("production=%v: got %T, want 200", production, resp)
		}
		if wl.gotUser != 42 {
			t.Fatalf("store asked for user %d, want the session's 42", wl.gotUser)
		}
		if wl.gotOpts.AllowEnable == production {
			t.Fatalf("production=%v: AllowEnable=%v, want enabling only outside production (D15.1)", production, wl.gotOpts.AllowEnable)
		}
		if wl.gotOpts.MaxItems != 50 || wl.gotOpts.TrackedCap != 8 || wl.gotOpts.Environment != "local" || len(wl.gotOpts.Floor) != 1 {
			t.Fatalf("options %+v", wl.gotOpts)
		}
	}
}

func TestAddWatchlistItem_ErrorsMapToTheirCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
		want any
	}{
		{watchlist.ErrInstrumentNotFound, "not_found", AddWatchlistItem404JSONResponse{}},
		{watchlist.ErrInstrumentDisabled, "instrument_not_enabled", AddWatchlistItem400JSONResponse{}},
		{watchlist.ErrTooManyItems, "watchlist_full", AddWatchlistItem409JSONResponse{}},
		{watchlist.ErrTrackedCapReached, "tracked_cap_reached", AddWatchlistItem409JSONResponse{}},
	}
	for _, c := range cases {
		h := watchlistHandler(&fakeWatchlistStore{addErr: c.err}, false)
		resp, err := h.AddWatchlistItem(asUser(1), AddWatchlistItemRequestObject{Body: &AddWatchlistItemJSONRequestBody{InstrumentId: 7}})
		if err != nil {
			t.Fatal(err)
		}
		var code string
		switch r := resp.(type) {
		case AddWatchlistItem404JSONResponse:
			code = r.Error.Code
		case AddWatchlistItem400JSONResponse:
			code = r.Error.Code
		case AddWatchlistItem409JSONResponse:
			code = r.Error.Code
		default:
			t.Fatalf("%v: got %T", c.err, resp)
		}
		if code != c.code {
			t.Errorf("%v: code %q, want %q", c.err, code, c.code)
		}
	}
	// A bad id never reaches the store; an unexpected error is a 500.
	wl := &fakeWatchlistStore{}
	h := watchlistHandler(wl, false)
	if resp, _ := h.AddWatchlistItem(asUser(1), AddWatchlistItemRequestObject{Body: &AddWatchlistItemJSONRequestBody{InstrumentId: 0}}); resp == nil {
		t.Fatal("nil response")
	} else if _, ok := resp.(AddWatchlistItem400JSONResponse); !ok || wl.gotUser != 0 {
		t.Fatalf("id 0: got %T, store called=%v", resp, wl.gotUser != 0)
	}
	h = watchlistHandler(&fakeWatchlistStore{addErr: errors.New("db down")}, false)
	if _, err := h.AddWatchlistItem(asUser(1), AddWatchlistItemRequestObject{Body: &AddWatchlistItemJSONRequestBody{InstrumentId: 7}}); err == nil {
		t.Fatal("unexpected store error was not returned as a 500")
	}
}

func TestReorderWatchlist_StaleOrderIs409(t *testing.T) {
	h := watchlistHandler(&fakeWatchlistStore{reorderErr: watchlist.ErrOrderMismatch}, false)
	resp, err := h.ReorderWatchlist(asUser(1), ReorderWatchlistRequestObject{Body: &ReorderWatchlistJSONRequestBody{InstrumentIds: []int64{1, 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := resp.(ReorderWatchlist409JSONResponse); !ok || r.Error.Code != "watchlist_changed" {
		t.Fatalf("got %T %+v, want 409 watchlist_changed", resp, resp)
	}
	many := make([]int64, 51)
	if resp, _ := h.ReorderWatchlist(asUser(1), ReorderWatchlistRequestObject{Body: &ReorderWatchlistJSONRequestBody{InstrumentIds: many}}); resp == nil {
		t.Fatal("nil")
	} else if _, ok := resp.(ReorderWatchlist400JSONResponse); !ok {
		t.Fatalf("51 ids: got %T, want 400", resp)
	}
}

func TestGetWatchlistTracking_StatesAndTheCap(t *testing.T) {
	at := time.Date(2026, 11, 22, 4, 0, 0, 0, time.FixedZone("WAT", 3600))
	wl := &fakeWatchlistStore{items: []watchlist.Item{{InstrumentID: 1}, {InstrumentID: 9}, {InstrumentID: 12}}}
	h := watchlistHandler(wl, false)
	h.trackedCap = 2
	h.tracking = fakeTrackingStore{
		tracked: []tracking.Tracked{{ID: 1, Symbol: "BTC", IsFloor: true}, {ID: 9, Symbol: "AVAX"}, {ID: 12, Symbol: "LINK"}},
		states: map[int64]tracking.Status{
			9:  {InstrumentID: 9, State: tracking.LoadingHistory, UpdatedAt: at},
			12: {InstrumentID: 12, State: tracking.Waiting, Reason: "over the system cap of 2 tracked symbols", UpdatedAt: at},
		},
	}
	resp, err := h.GetWatchlistTracking(asUser(1), GetWatchlistTrackingRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.(GetWatchlistTracking200JSONResponse)
	if !got.CanEnable {
		t.Fatal("can_enable false outside production")
	}
	if got.Cap != 2 || len(got.TrackedInstrumentIds) != 2 || got.TrackedInstrumentIds[1] != 9 {
		t.Fatalf("cap %d tracked %v, want 2 and [1 9] (the cap's first two)", got.Cap, got.TrackedInstrumentIds)
	}
	if got.Items[0].State != nil || !got.Items[0].Tracked {
		t.Fatalf("BTC: %+v, want tracked with no state yet", got.Items[0])
	}
	if *got.Items[1].State != "loading_history" || got.Items[1].UpdatedAt.Location() != time.UTC {
		t.Fatalf("AVAX: %+v, want loading_history at a UTC time", got.Items[1])
	}
	if got.Items[2].Tracked || *got.Items[2].State != "waiting" {
		t.Fatalf("LINK: %+v, want untracked (over the cap) and waiting", got.Items[2])
	}
}
