// Package preferences stores what each user sets -- preferences and
// chart drawings -- server-side, per user (owner decision D1, approved
// 2026-11-17; brief-argus-product-overhaul.md Phase 1), so a setting
// survives a refresh, a cleared browser and a login on another device.
//
// Documents are validated here, in one visible place: the preferences
// document against a closed schema (any unknown key, at any level, is
// rejected), drawings as an array of objects with an id and a kind.
// Ownership is never this package's input from a client: callers pass
// the session user's id.
package preferences

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// MaxPreferencesBytes is the preferences document cap (brief: "413 above
// 64 KB"). It matches the API's own request-body limit.
const MaxPreferencesBytes = 64 * 1024

// MaxDrawingsBytes is the per-chart drawings document cap. [U] 256 KB
// (brief, Phase 1).
const MaxDrawingsBytes = 256 * 1024

// MaxDrawingsPerChart bounds one drawings document. [U] 1000 -- far above
// any hand-drawn chart; it exists so one document cannot hold unbounded
// small items under the byte cap.
const MaxDrawingsPerChart = 1000

var (
	// ErrInvalid wraps every validation failure (a 400).
	ErrInvalid = errors.New("preferences: invalid document")
	// ErrTooLarge is a document over its byte cap (a 413).
	ErrTooLarge = errors.New("preferences: document too large")
	// ErrVersionConflict is a write based on a stale version (a 409).
	ErrVersionConflict = errors.New("preferences: version conflict")
)

// Timeframes are the chart intervals a preference may name.
var timeframes = map[string]bool{"1m": true, "5m": true, "15m": true, "1h": true, "4h": true, "1d": true}

// chartViewKey is "<instrument_id>:<timeframe>".
var chartViewKey = regexp.MustCompile(`^[1-9][0-9]{0,18}:(1m|5m|15m|1h|4h|1d)$`)

// Document is the complete, closed preferences schema. Every field is
// optional (absent = the app's default). Adding a setting means adding it
// here -- a client cannot store a key this type does not name.
type Document struct {
	// SelectedInstrumentID is the instrument last selected on /analysis.
	// An explicit instrument or setup in a link beats it.
	SelectedInstrumentID *int64 `json:"selected_instrument_id,omitempty"`
	// Timeframe is the chart interval.
	Timeframe *string `json:"timeframe,omitempty"`
	// RightPanelTab is "analysis" or "watchlist".
	RightPanelTab *string `json:"right_panel_tab,omitempty"`
	// RightPanelWidth is the right panel's width in CSS pixels.
	RightPanelWidth *int `json:"right_panel_width,omitempty"`
	// ToolRailCollapsed is the drawing tool rail's collapsed state.
	ToolRailCollapsed *bool `json:"tool_rail_collapsed,omitempty"`
	// OtherSetupsOpen is the "other setups" strip's open state.
	OtherSetupsOpen *bool `json:"other_setups_open,omitempty"`
	// AnalysisLayersVisible shows or hides ARGUS's own chart layers (the
	// selected setup's order block or QMR zone and its plan lines). They
	// are system output, never a user drawing, so they can be hidden but
	// not deleted. Absent means shown.
	AnalysisLayersVisible *bool `json:"analysis_layers_visible,omitempty"`
	// SetupLayers are the chart's setup-layer filters (Phase 6.3), each a
	// closed name in setupLayerKeys: state_<state> shows setups in that
	// state (state_invalidated is "show invalidated", drawn dimmed),
	// tf_<timeframe> shows setups found on that timeframe, evidence draws
	// the selected setup's evidence. Absent means the app's default.
	SetupLayers map[string]bool `json:"setup_layers,omitempty"`
	// WatchlistGroupsCollapsed is per watchlist group name.
	WatchlistGroupsCollapsed map[string]bool `json:"watchlist_groups_collapsed,omitempty"`
	// DrawingTools are the drawing toolbar's toggles.
	DrawingTools *DrawingTools `json:"drawing_tools,omitempty"`
	// ChartViews is the saved visible range per "<instrument_id>:<timeframe>".
	ChartViews map[string]ChartView `json:"chart_views,omitempty"`
}

// DrawingTools are the drawing toolbar's toggles.
type DrawingTools struct {
	Magnet            *bool `json:"magnet,omitempty"`
	StayInDrawingMode *bool `json:"stay_in_drawing_mode,omitempty"`
	Locked            *bool `json:"locked,omitempty"`
	Hidden            *bool `json:"hidden,omitempty"`
}

// ChartView is a saved visible range as TIME plus spacing, not raw bar
// indexes (indexes shift when history grows or is trimmed): the open time
// (unix seconds) of the first and last real bars in view, how many bars
// wide the view was, and -- when the newest bar was in view -- how far
// past it the view reached, so a view "following the latest bar" keeps
// following it. These are screen geometry, not prices: float64 is fine.
type ChartView struct {
	FromTime       int64   `json:"from_time"`
	ToTime         int64   `json:"to_time"`
	BarsVisible    float64 `json:"bars_visible"`
	BarSpacing     float64 `json:"bar_spacing"`
	RightOffset    float64 `json:"right_offset"`
	PinnedToLatest bool    `json:"pinned_to_latest"`
}

// setupLayerKeys are the only names SetupLayers may hold.
var setupLayerKeys = map[string]bool{
	"state_detected": true, "state_actionable": true, "state_triggered": true, "state_invalidated": true,
	"tf_1m": true, "tf_5m": true, "tf_15m": true, "tf_1h": true, "tf_4h": true, "tf_1d": true,
	"evidence": true,
}

// Bounds for the validated fields (match the web app's own clamps).
const (
	minRightPanelWidth = 200
	maxRightPanelWidth = 1200
	maxChartViews      = 500
	maxGroups          = 50
	maxGroupNameLen    = 64
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// ValidatePreferences checks raw against Document and returns it
// normalized (re-encoded from the parsed value, so what is stored is
// exactly what the schema names). Unknown keys at any level, a trailing
// value, a wrong type or an out-of-range value are ErrInvalid; more than
// MaxPreferencesBytes is ErrTooLarge.
func ValidatePreferences(raw []byte) ([]byte, error) {
	if len(raw) > MaxPreferencesBytes {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, len(raw), MaxPreferencesBytes)
	}
	var doc Document
	if err := decodeStrict(raw, &doc); err != nil {
		return nil, err
	}
	if doc.SelectedInstrumentID != nil && *doc.SelectedInstrumentID <= 0 {
		return nil, invalid("selected_instrument_id must be positive")
	}
	if doc.Timeframe != nil && !timeframes[*doc.Timeframe] {
		return nil, invalid("timeframe %q is not one of 1m, 5m, 15m, 1h, 4h, 1d", *doc.Timeframe)
	}
	if doc.RightPanelTab != nil && *doc.RightPanelTab != "analysis" && *doc.RightPanelTab != "watchlist" {
		return nil, invalid("right_panel_tab must be analysis or watchlist")
	}
	if w := doc.RightPanelWidth; w != nil && (*w < minRightPanelWidth || *w > maxRightPanelWidth) {
		return nil, invalid("right_panel_width must be between %d and %d", minRightPanelWidth, maxRightPanelWidth)
	}
	if len(doc.WatchlistGroupsCollapsed) > maxGroups {
		return nil, invalid("at most %d watchlist groups", maxGroups)
	}
	for name := range doc.WatchlistGroupsCollapsed {
		if name == "" || len(name) > maxGroupNameLen {
			return nil, invalid("watchlist group name must be 1-%d bytes", maxGroupNameLen)
		}
	}
	for key := range doc.SetupLayers {
		if !setupLayerKeys[key] {
			return nil, invalid("setup_layers key %q is not a known layer filter", key)
		}
	}
	if len(doc.ChartViews) > maxChartViews {
		return nil, invalid("at most %d saved chart views", maxChartViews)
	}
	for key, v := range doc.ChartViews {
		if !chartViewKey.MatchString(key) {
			return nil, invalid("chart_views key %q must be <instrument_id>:<timeframe>", key)
		}
		if v.ToTime < v.FromTime || v.FromTime <= 0 {
			return nil, invalid("chart_views[%s]: from_time must be positive and not after to_time", key)
		}
		if !(v.BarsVisible > 0 && v.BarsVisible <= 100_000) || !(v.BarSpacing > 0 && v.BarSpacing <= 1_000) || v.RightOffset < -100_000 || v.RightOffset > 100_000 {
			return nil, invalid("chart_views[%s]: bars_visible, bar_spacing or right_offset out of range", key)
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("preferences: re-encode: %w", err)
	}
	return out, nil
}

// ValidateDrawings checks raw is a JSON array of at most
// MaxDrawingsPerChart objects, each with a non-empty string "id" (at most
// 64 bytes, unique) and a non-empty string "kind". The rest of each
// drawing is the web app's own shape and is stored as sent.
func ValidateDrawings(raw []byte) ([]byte, error) {
	if len(raw) > MaxDrawingsBytes {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, len(raw), MaxDrawingsBytes)
	}
	var items []map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&items); err != nil {
		return nil, invalid("drawings must be a JSON array of objects: %v", err)
	}
	if dec.More() {
		return nil, invalid("trailing data after the drawings array")
	}
	if items == nil {
		return nil, invalid("drawings must be an array, not null")
	}
	if len(items) > MaxDrawingsPerChart {
		return nil, invalid("at most %d drawings per chart", MaxDrawingsPerChart)
	}
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		if item == nil {
			return nil, invalid("drawing %d is not an object", i)
		}
		var id, kind string
		if err := json.Unmarshal(item["id"], &id); err != nil || id == "" || len(id) > 64 {
			return nil, invalid("drawing %d needs a string id of 1-64 bytes", i)
		}
		if err := json.Unmarshal(item["kind"], &kind); err != nil || kind == "" {
			return nil, invalid("drawing %d needs a string kind", i)
		}
		if seen[id] {
			return nil, invalid("duplicate drawing id %q", id)
		}
		seen[id] = true
	}
	return raw, nil
}

// decodeStrict decodes exactly one JSON object into v, rejecting unknown
// fields at every level and anything after the value.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return invalid("%v", err)
	}
	if dec.More() {
		return invalid("trailing data after the document")
	}
	return nil
}

// IsTimeframe reports whether tf is a chart timeframe drawings may be
// stored under.
func IsTimeframe(tf string) bool { return timeframes[tf] }
