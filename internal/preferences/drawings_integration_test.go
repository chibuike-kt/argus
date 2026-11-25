//go:build integration

package preferences

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/instruments"
	"github.com/chibuike-kt/argus/internal/quant"
)

func newTestInstrument(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	ps, qs := quant.Scale(2), quant.Scale(4)
	inst, err := instruments.NewStore(db).Insert(context.Background(), instruments.NewInstrumentParams{
		Provider: fmt.Sprintf("drawings-test-%d", time.Now().UnixNano()), NativeSymbol: "DRAW", AssetClass: "crypto_perp",
		PriceScale: &ps, QtyScale: &qs, TickSize: 1, QtyStepSize: 1, SupportedIntervals: []string{"1m"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return inst.ID
}

// seedTimeframeDoc stores a drawings document the way the pre D17 release
// did: one row per (user, instrument, timeframe) in user_drawings.
func seedTimeframeDoc(t *testing.T, db *sql.DB, user, inst int64, tf, data string, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO user_drawings (user_id, instrument_id, timeframe, data, version, updated_at) VALUES ($1, $2, $3, $4, 3, $5)`,
		user, inst, tf, data, at); err != nil {
		t.Fatal(err)
	}
}

func sortedIDs(t *testing.T, raw []byte) []string {
	t.Helper()
	var ds []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &ds); err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	sort.Strings(out)
	return out
}

// D17: drawings stored on three timeframes of one instrument (one id on
// two of them, one drawing only on one) all appear in the instrument
// document, none lost; the old rows are left untouched; concurrent first
// reads give one document; a first write cannot hide the merge.
func TestIntegration_Drawings_MergeTimeframesOnFirstRead(t *testing.T) {
	for _, zone := range []string{"Etc/UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			db := dbInZone(t, zone)
			ctx := context.Background()
			at := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
			s := NewStore(db, clock.NewFixed(at))
			uid := newTestUser(t, db)
			other := newTestUser(t, db)
			inst := newTestInstrument(t, db)

			t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
			seedTimeframeDoc(t, db, uid, inst, "1m", `[{"id":"a","kind":"trendline","label":"a old","updatedAt":100},{"id":"d","kind":"text"}]`, t0)
			seedTimeframeDoc(t, db, uid, inst, "1h", `[{"id":"b","kind":"rectangle","updatedAt":200},{"id":"a","kind":"trendline","label":"a new","updatedAt":300}]`, t0.Add(time.Hour))
			seedTimeframeDoc(t, db, uid, inst, "1d", `[{"id":"c","kind":"hline","updatedAt":5}]`, t0.Add(2*time.Hour))
			// Another user's drawings on the same instrument stay theirs.
			seedTimeframeDoc(t, db, other, inst, "1m", `[{"id":"z","kind":"hline"}]`, t0)

			// Concurrent first reads (two tabs): one merge, one document.
			var wg sync.WaitGroup
			docs := make([]Doc, 4)
			errs := make([]error, 4)
			for i := range docs {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					docs[i], errs[i] = s.GetDrawings(ctx, uid, inst)
				}(i)
			}
			wg.Wait()
			for i := range docs {
				if errs[i] != nil {
					t.Fatal(errs[i])
				}
				if docs[i].Version != 1 || fmt.Sprint(sortedIDs(t, docs[i].Data)) != "[a b c d]" {
					t.Fatalf("read %d = version %d ids %v, want version 1 with a b c d", i, docs[i].Version, sortedIDs(t, docs[i].Data))
				}
			}
			var rows int
			if err := db.QueryRow(`SELECT count(*) FROM user_instrument_drawings WHERE user_id = $1 AND instrument_id = $2`, uid, inst).Scan(&rows); err != nil || rows != 1 {
				t.Fatalf("instrument documents = %d %v, want 1", rows, err)
			}
			var merged []struct {
				ID    string `json:"id"`
				Label string `json:"label"`
			}
			_ = json.Unmarshal(docs[0].Data, &merged)
			for _, d := range merged {
				if d.ID == "a" && d.Label != "a new" {
					t.Fatalf("duplicate a kept %q, want its most recently changed copy", d.Label)
				}
			}
			if docs[0].UpdatedAt == nil || !docs[0].UpdatedAt.Equal(at) || docs[0].UpdatedAt.Location() != time.UTC {
				t.Fatalf("updated_at = %v, want %v in UTC", docs[0].UpdatedAt, at)
			}

			// The old rows are not changed or deleted in this release.
			var old int
			var oldData string
			if err := db.QueryRow(`SELECT count(*), max(data::text) FILTER (WHERE timeframe = '1d') FROM user_drawings WHERE user_id = $1 AND instrument_id = $2`, uid, inst).Scan(&old, &oldData); err != nil || old != 3 || oldData != `[{"id": "c", "kind": "hline", "updatedAt": 5}]` {
				t.Fatalf("old rows = %d %q %v, want the 3 rows unchanged", old, oldData, err)
			}

			// The version rule still holds on the instrument document.
			if _, err := s.PutDrawings(ctx, uid, inst, []byte(`[]`), 0); !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("a version 0 write over the merged document = %v, want ErrVersionConflict", err)
			}
			next, err := s.PutDrawings(ctx, uid, inst, []byte(`[{"id":"a","kind":"trendline"}]`), 1)
			if err != nil || next.Version != 2 {
				t.Fatalf("write at version 1 = %+v %v", next, err)
			}

			// The other user's drawings merge into their own document only.
			theirs, err := s.GetDrawings(ctx, other, inst)
			if err != nil || fmt.Sprint(sortedIDs(t, theirs.Data)) != "[z]" {
				t.Fatalf("other user's drawings = %v %v, want only z", sortedIDs(t, theirs.Data), err)
			}
		})
	}
}

// A first write on an instrument that still has only per timeframe
// documents cannot create a document that hides them: it merges first and
// is refused as stale, and the next read returns every drawing.
func TestIntegration_Drawings_FirstWriteNeverHidesTheMerge(t *testing.T) {
	db := dbInZone(t, "America/New_York")
	ctx := context.Background()
	s := NewStore(db, clock.Real{})
	uid := newTestUser(t, db)
	inst := newTestInstrument(t, db)
	seedTimeframeDoc(t, db, uid, inst, "4h", `[{"id":"old","kind":"hline"}]`, time.Now().Add(-time.Hour))

	if _, err := s.PutDrawings(ctx, uid, inst, []byte(`[{"id":"new","kind":"vline"}]`), 0); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("first write = %v, want ErrVersionConflict (the merge exists now)", err)
	}
	doc, err := s.GetDrawings(ctx, uid, inst)
	if err != nil || doc.Version != 1 || fmt.Sprint(sortedIDs(t, doc.Data)) != "[old]" {
		t.Fatalf("read after the refused write = %+v %v, want the merged old drawing at version 1", doc, err)
	}
	// Nothing stored at all: a first write simply creates the document.
	fresh := newTestInstrument(t, db)
	if d, err := s.PutDrawings(ctx, uid, fresh, []byte(`[{"id":"n","kind":"vline"}]`), 0); err != nil || d.Version != 1 {
		t.Fatalf("first write with nothing stored = %+v %v", d, err)
	}
}
