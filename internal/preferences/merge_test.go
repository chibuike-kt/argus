package preferences

import (
	"encoding/json"
	"testing"
	"time"
)

type mergedDrawing struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Label     string   `json:"label"`
	UpdatedAt *float64 `json:"updatedAt"`
}

func decodeMerged(t *testing.T, raw []byte) []mergedDrawing {
	t.Helper()
	var out []mergedDrawing
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("merged document is not a JSON array: %v (%s)", err, raw)
	}
	return out
}

func ids(ds []mergedDrawing) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

// D17: the per timeframe documents become one instrument document; no
// drawing is lost, a duplicate id keeps its most recently changed copy, and
// the z order follows the documents' own change order.
func TestMergeTimeframeDrawings(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	docs := []TimeframeDoc{
		// Given out of order on purpose: the merge sorts by change time.
		{Timeframe: "1d", UpdatedAt: t0.Add(3 * time.Hour), Data: []byte(`[{"id":"c","kind":"hline","label":"only on 1d","updatedAt":5}]`)},
		{Timeframe: "1m", UpdatedAt: t0, Data: []byte(`[{"id":"a","kind":"trendline","label":"a old","updatedAt":100},{"id":"d","kind":"text","label":"d"}]`)},
		{Timeframe: "1h", UpdatedAt: t0.Add(time.Hour), Data: []byte(`[{"id":"b","kind":"rectangle","label":"b","updatedAt":200},{"id":"a","kind":"trendline","label":"a new","updatedAt":300}]`)},
	}
	raw, err := MergeTimeframeDrawings(docs)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeMerged(t, raw)
	// Every id once; 1m's d (no updatedAt: its document's time counts) and
	// 1d's c are kept; a's newer 1h copy wins and sits where it sat in 1h.
	if want := []string{"d", "b", "a", "c"}; len(got) != len(want) || ids(got)[0] != "d" || ids(got)[1] != "b" || ids(got)[2] != "a" || ids(got)[3] != "c" {
		t.Fatalf("merged order = %v, want %v", ids(got), want)
	}
	for _, d := range got {
		if d.ID == "a" && d.Label != "a new" {
			t.Fatalf("duplicate a kept %q, want the most recently changed copy", d.Label)
		}
	}
}

func TestMergeTimeframeDrawings_NewerCopyInAnOlderDocument(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	// The drawing was last edited on 1m, though the 4h document changed
	// later for another drawing: the drawing's own updatedAt decides.
	raw, err := MergeTimeframeDrawings([]TimeframeDoc{
		{Timeframe: "1m", UpdatedAt: t0, Data: []byte(`[{"id":"x","kind":"ray","label":"edited last","updatedAt":900}]`)},
		{Timeframe: "4h", UpdatedAt: t0.Add(time.Hour), Data: []byte(`[{"id":"x","kind":"ray","label":"stale","updatedAt":100},{"id":"y","kind":"vline"}]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeMerged(t, raw)
	if len(got) != 2 || got[0].ID != "x" || got[0].Label != "edited last" || got[1].ID != "y" {
		t.Fatalf("merged = %+v, want x (its edited copy, at its own position) then y", got)
	}
}

func TestMergeTimeframeDrawings_TieGoesToTheLaterDocument(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	raw, err := MergeTimeframeDrawings([]TimeframeDoc{
		{Timeframe: "15m", UpdatedAt: t0, Data: []byte(`[{"id":"x","kind":"ray","label":"first","updatedAt":500}]`)},
		{Timeframe: "1h", UpdatedAt: t0.Add(time.Minute), Data: []byte(`[{"id":"x","kind":"ray","label":"second","updatedAt":500}]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeMerged(t, raw); len(got) != 1 || got[0].Label != "second" {
		t.Fatalf("tie kept %+v, want the copy in the later document", got)
	}
}

func TestMergeTimeframeDrawings_EmptyAndBad(t *testing.T) {
	raw, err := MergeTimeframeDrawings([]TimeframeDoc{{Timeframe: "1m", UpdatedAt: time.Now(), Data: []byte(`[]`)}})
	if err != nil || string(raw) != "[]" {
		t.Fatalf("empty = %s %v, want []", raw, err)
	}
	if _, err := MergeTimeframeDrawings([]TimeframeDoc{{Timeframe: "1m", Data: []byte(`[{"kind":"hline"}]`)}}); err == nil {
		t.Fatal("a drawing without an id merged silently")
	}
}
