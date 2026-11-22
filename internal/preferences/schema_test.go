package preferences

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePreferences_AcceptsTheFullSchemaAndNormalizes(t *testing.T) {
	in := `{"selected_instrument_id":4,"timeframe":"4h","right_panel_tab":"watchlist","right_panel_width":340,
	"tool_rail_collapsed":true,"other_setups_open":false,"analysis_layers_visible":false,"setup_layers":{"state_invalidated":true,"tf_5m":false,"evidence":true},"watchlist_groups_collapsed":{"Crypto":true},
	"drawing_tools":{"magnet":true,"stay_in_drawing_mode":false,"locked":false,"hidden":true},
	"chart_views":{"4:1m":{"from_time":1763300000,"to_time":1763307140,"bars_visible":125,"bar_spacing":6.25,"right_offset":5,"pinned_to_latest":true}}}`
	out, err := ValidatePreferences([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"timeframe":"4h"`) || strings.ContainsAny(string(out), "\n\t") {
		t.Fatalf("not normalized: %s", out)
	}
}

func TestValidatePreferences_EmptyDocumentIsValid(t *testing.T) {
	out, err := ValidatePreferences([]byte(`{}`))
	if err != nil || string(out) != "{}" {
		t.Fatalf("out=%s err=%v", out, err)
	}
}

func TestValidatePreferences_Rejects(t *testing.T) {
	cases := map[string]string{
		"unknown top-level key":     `{"theme":"dark"}`,
		"unknown nested key":        `{"drawing_tools":{"magnet":true,"colour":"red"}}`,
		"unknown chart view field":  `{"chart_views":{"4:1m":{"from_time":1,"to_time":2,"bars_visible":10,"bar_spacing":6,"right_offset":0,"pinned_to_latest":false,"x":1}}}`,
		"wrong type":                `{"timeframe":5}`,
		"bad timeframe":             `{"timeframe":"2h"}`,
		"bad tab":                   `{"right_panel_tab":"news"}`,
		"width too small":           `{"right_panel_width":10}`,
		"non-positive instrument":   `{"selected_instrument_id":0}`,
		"bad chart view key":        `{"chart_views":{"BTC:1m":{"from_time":1,"to_time":2,"bars_visible":10,"bar_spacing":6,"right_offset":0,"pinned_to_latest":false}}}`,
		"chart view to before from": `{"chart_views":{"4:1m":{"from_time":5,"to_time":2,"bars_visible":10,"bar_spacing":6,"right_offset":0,"pinned_to_latest":false}}}`,
		"chart view zero spacing":   `{"chart_views":{"4:1m":{"from_time":1,"to_time":2,"bars_visible":10,"bar_spacing":0,"right_offset":0,"pinned_to_latest":false}}}`,
		"unknown setup layer key":   `{"setup_layers":{"state_expired":true}}`,
		"setup layer not a bool":    `{"setup_layers":{"evidence":"yes"}}`,
		"not an object":             `[1,2]`,
		"trailing value":            `{} {}`,
		"corrupt JSON":              `{"timeframe":`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidatePreferences([]byte(in)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidatePreferences_OverSizeIsTooLarge(t *testing.T) {
	// 70 KB (the brief's hostile size): rejected before parsing.
	big := `{"watchlist_groups_collapsed":{"` + strings.Repeat("a", 70*1024) + `":true}}`
	if _, err := ValidatePreferences([]byte(big)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestValidateDrawings(t *testing.T) {
	if _, err := ValidateDrawings([]byte(`[{"id":"a","kind":"trendline","p1":{"time":1,"price":2}}]`)); err != nil {
		t.Fatalf("valid drawings rejected: %v", err)
	}
	if _, err := ValidateDrawings([]byte(`[]`)); err != nil {
		t.Fatalf("empty array rejected: %v", err)
	}
	bad := map[string]string{
		"not an array":    `{"id":"a"}`,
		"null":            `null`,
		"missing id":      `[{"kind":"x"}]`,
		"missing kind":    `[{"id":"a"}]`,
		"duplicate id":    `[{"id":"a","kind":"x"},{"id":"a","kind":"y"}]`,
		"element not obj": `[1]`,
	}
	for name, in := range bad {
		if _, err := ValidateDrawings([]byte(in)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	huge := `[{"id":"a","kind":"text","text":"` + strings.Repeat("x", MaxDrawingsBytes) + `"}]`
	if _, err := ValidateDrawings([]byte(huge)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversize: err = %v, want ErrTooLarge", err)
	}
}
