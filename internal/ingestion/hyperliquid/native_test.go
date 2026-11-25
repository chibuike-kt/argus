package hyperliquid

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/marketdata"
)

// intervalServer serves candleSnapshot like the real endpoint (L1.0): every
// candle of the requested interval whose window overlaps [startTime,
// endTime], starting with the one CONTAINING startTime, at most pageSize
// per response. repeatLast makes every page after the first also start with
// the previous page's last candle, as a server that repeats could.
type intervalServer struct {
	mu         sync.Mutex
	pageSize   int
	repeatLast bool
	starts     []int64
	intervals  []string
	lastSent   int64
}

func (s *intervalServer) handle(w http.ResponseWriter, r *http.Request) {
	var req candleSnapshotRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	d := map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "4h": 4 * time.Hour}[req.Req.Interval]
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, req.Req.StartTime)
	s.intervals = append(s.intervals, req.Req.Interval)
	page := []wireCandle{}
	start := time.UnixMilli(req.Req.StartTime).UTC().Truncate(d)
	for m := start; m.UnixMilli() <= req.Req.EndTime && len(page) < s.pageSize; m = m.Add(d) {
		page = append(page, wire(m.UnixMilli(), d, req.Req.Interval))
	}
	out := page
	if s.repeatLast && len(s.starts) > 1 {
		// On top of a full page, so the page boundaries stay the same.
		out = append([]wireCandle{wire(s.lastSent, d, req.Req.Interval)}, page...)
	}
	if len(page) > 0 {
		s.lastSent = page[len(page)-1].T
	}
	_ = json.NewEncoder(w).Encode(out)
}

func wire(openMs int64, d time.Duration, interval string) wireCandle {
	return wireCandle{T: openMs, C: openMs + d.Milliseconds() - 1, S: "BTC", I: interval, O: "1.0", H: "1.0", L: "1.0", Cl: "1.0", V: "1", N: 1}
}

// L1.1 decision 4: the pager advances by the interval and de-duplicates by
// open time, across page boundaries.
func TestHistoricalIntervalCandles_PagesByTheIntervalAndDeduplicates(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		name := "real paging"
		if repeat {
			name = "a server that repeats the last candle"
		}
		t.Run(name, func(t *testing.T) {
			srv := &intervalServer{pageSize: 3, repeatLast: repeat}
			p, _ := testProvider(t, srv.handle)
			// 10:30 to 20:30: the 1h candles opening 10:00 (containing the
			// start) through 20:00, eleven in all, four pages of three.
			start := time.Date(2026, 11, 20, 10, 30, 0, 0, time.UTC)
			end := time.Date(2026, 11, 20, 20, 30, 0, 0, time.UTC)
			got, err := p.HistoricalIntervalCandles(t.Context(), "BTC", marketdata.Interval1h, start, end)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 11 {
				t.Fatalf("got %d candles, want 11 (10:00 to 20:00, each once): %+v", len(got), got)
			}
			for i, c := range got {
				want := time.Date(2026, 11, 20, 10+i, 0, 0, 0, time.UTC)
				if !c.OpenTime.Equal(want) || c.IsFinal {
					t.Fatalf("candle %d opens %s (final %v), want %s, not final (closure is the caller's decision)", i, c.OpenTime, c.IsFinal, want)
				}
			}
			// Each page starts one interval after the previous page's last
			// open time: 10:30, then 13:00, 16:00, 19:00.
			wantStarts := []time.Time{start, start.Add(150 * time.Minute), start.Add(330 * time.Minute), start.Add(510 * time.Minute)}
			if len(srv.starts) != len(wantStarts) {
				t.Fatalf("requests = %d, want %d (starts %v)", len(srv.starts), len(wantStarts), srv.starts)
			}
			for i, w := range wantStarts {
				if srv.starts[i] != w.UnixMilli() || srv.intervals[i] != "1h" {
					t.Fatalf("request %d: startTime %s interval %s, want %s 1h", i, time.UnixMilli(srv.starts[i]).UTC(), srv.intervals[i], w)
				}
			}
		})
	}
}

func TestHistoricalIntervalCandles_RefusesAnIntervalNotStored(t *testing.T) {
	p, _ := testProvider(t, func(_ http.ResponseWriter, _ *http.Request) { t.Error("no request expected") })
	if _, err := p.HistoricalIntervalCandles(t.Context(), "BTC", marketdata.Interval("5m"), time.Now(), time.Now()); !errors.Is(err, marketdata.ErrUnknownInterval) {
		t.Fatalf("5m = %v, want ErrUnknownInterval (5m stays derived)", err)
	}
}
