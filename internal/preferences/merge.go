package preferences

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// TimeframeDoc is one drawings document stored per (user, instrument,
// timeframe) before owner decision D17 (migration 000017's user_drawings).
type TimeframeDoc struct {
	Timeframe string
	Data      []byte
	UpdatedAt time.Time
}

// MergeTimeframeDrawings merges an instrument's per timeframe drawings
// documents into the one instrument document D17 keeps, losing no drawing:
//
//   - every drawing of every document is kept, once per id;
//   - a drawing id stored on several timeframes keeps its most recently
//     changed copy: the larger "updatedAt" the web app stamps on every
//     drawing (milliseconds), else the document's own change time; an
//     exact tie goes to the copy in the later document;
//   - z order (the array order: last is in front): the documents oldest
//     change first, each in its own stored order, so the chart the user
//     worked on last stays on top; a duplicate sits where its kept copy
//     sat.
//
// Each drawing's JSON is kept exactly as stored. docs need not be sorted.
func MergeTimeframeDrawings(docs []TimeframeDoc) ([]byte, error) {
	ordered := append([]TimeframeDoc(nil), docs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].UpdatedAt.Equal(ordered[j].UpdatedAt) {
			return ordered[i].UpdatedAt.Before(ordered[j].UpdatedAt)
		}
		return timeframeRank(ordered[i].Timeframe) < timeframeRank(ordered[j].Timeframe)
	})

	type copyOf struct {
		seq     int
		changed float64
		raw     json.RawMessage
	}
	best := map[string]copyOf{}
	seq := 0
	for _, doc := range ordered {
		var items []json.RawMessage
		if err := json.Unmarshal(doc.Data, &items); err != nil {
			return nil, fmt.Errorf("preferences: merge drawings on %s: %w", doc.Timeframe, err)
		}
		docChanged := float64(doc.UpdatedAt.UnixMilli())
		for _, raw := range items {
			var head struct {
				ID        string   `json:"id"`
				UpdatedAt *float64 `json:"updatedAt"`
			}
			if err := json.Unmarshal(raw, &head); err != nil || head.ID == "" {
				return nil, fmt.Errorf("preferences: merge drawings on %s: a drawing without an id", doc.Timeframe)
			}
			changed := docChanged
			if head.UpdatedAt != nil {
				changed = *head.UpdatedAt
			}
			if prev, ok := best[head.ID]; !ok || changed >= prev.changed {
				best[head.ID] = copyOf{seq: seq, changed: changed, raw: raw}
			}
			seq++
		}
	}
	kept := make([]copyOf, 0, len(best))
	for _, c := range best {
		kept = append(kept, c)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].seq < kept[j].seq })
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, c := range kept {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(c.raw)
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// timeframeRank orders documents changed at the same instant, shortest
// timeframe first, so the merge is deterministic.
func timeframeRank(tf string) int {
	for i, t := range []string{"1m", "5m", "15m", "1h", "4h", "1d"} {
		if t == tf {
			return i
		}
	}
	return 99
}
