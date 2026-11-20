//go:build integration

package setups

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/analysis"
)

// Phase 6.1: how a setup was found, and the evidence chain, recorded at
// decision time with the immutable events (D6, owner decisions of
// 2026-11-19).

func withEvidence(p analysis.TradePlan, marker string) analysis.TradePlan {
	p.Evidence = &analysis.PlanEvidence{
		Schema:    analysis.EvidenceSchemaVersion,
		Timeframe: "1m",
		Direction: "bullish",
		Steps:     []analysis.EvidenceStep{{Kind: analysis.StepPOI, Section: "12", Result: analysis.EvidenceMet, Attrs: map[string]string{"marker": marker}}},
	}
	return p
}

func TestIntegration_Evidence_RecordedOnDetectedEventOnly(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	ctx := context.Background()
	candleID := oneCandle(t, db, instrumentID, 1, 85)

	// Created straight into actionable: detected (with evidence) then
	// actionable (none).
	created, _, err := ReconcileWithMode(ctx, store, instrumentID, "1m", withEvidence(bullishPlan(analysis.PlanActionable, candleID, 1), "first"), testVersion, et(1), time.Now(), FoundLive)
	if err != nil {
		t.Fatal(err)
	}
	if created.FoundMode != FoundLive {
		t.Fatalf("found mode = %q, want live", created.FoundMode)
	}
	events, err := store.ListEvents(ctx, created.ID)
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %+v err=%v, want detected and actionable", events, err)
	}
	var got analysis.PlanEvidence
	if err := json.Unmarshal(events[0].Evidence, &got); err != nil {
		t.Fatalf("detected event evidence %q: %v", events[0].Evidence, err)
	}
	if got.Steps[0].Attrs["marker"] != "first" || got.StrategyVersion != testVersion || got.AlgorithmVersion != testVersion {
		t.Fatalf("detected evidence = %+v, want the plan's chain with its versions", got)
	}
	if events[1].Evidence != nil {
		t.Fatalf("actionable event evidence = %s, want none", events[1].Evidence)
	}

	// A later transition records none; the found mode never changes,
	// whatever a later step says.
	updated, _, err := ReconcileWithMode(ctx, store, instrumentID, "1m", withEvidence(bullishPlan(analysis.PlanTriggered, candleID, 1), "later"), testVersion, et(2), time.Now(), FoundReplay)
	if err != nil {
		t.Fatal(err)
	}
	if updated.FoundMode != FoundLive {
		t.Fatalf("found mode after a later step = %q, want still live", updated.FoundMode)
	}
	events, _ = store.ListEvents(ctx, created.ID)
	if len(events) != 3 || events[2].Evidence != nil {
		t.Fatalf("triggered event = %+v, want no evidence", events[len(events)-1])
	}

	// The latest recorded evidence is still the detected event's.
	ev, typ, _, ok, err := store.LatestEvidence(ctx, created.ID)
	if err != nil || !ok || typ != "detected" {
		t.Fatalf("LatestEvidence = %s %s ok=%v err=%v", ev, typ, ok, err)
	}

	// Lifecycle: detected at 1, triggered at 2, never invalidated.
	lc, err := store.LifecycleByID(ctx, []int64{created.ID})
	if err != nil {
		t.Fatal(err)
	}
	l := lc[created.ID]
	if l.DetectedAt == nil || !l.DetectedAt.Equal(et(1)) || l.TriggeredAt == nil || !l.TriggeredAt.Equal(et(2)) || l.InvalidatedAt != nil || !l.EvidenceRecorded {
		t.Fatalf("lifecycle = %+v", l)
	}
}

func TestIntegration_Evidence_AmendmentRecordsTheCorrectedChain(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	ctx := context.Background()
	candleID := oneCandle(t, db, instrumentID, 1, 85)

	created, _, err := ReconcileWithMode(ctx, store, instrumentID, "1m", withEvidence(bullishPlan(analysis.PlanActionable, candleID, 1), "rev1"), testVersion, et(1), time.Now(), FoundReplay)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReconcileWithMode(ctx, store, instrumentID, "1m", withEvidence(bullishPlan(analysis.PlanActionable, candleID, 2), "rev2"), testVersion, et(2), time.Now(), FoundReplay); err != nil {
		t.Fatal(err)
	}
	ev, typ, _, ok, err := store.LatestEvidence(ctx, created.ID)
	if err != nil || !ok || typ != "amendment" {
		t.Fatalf("latest evidence from %s ok=%v err=%v, want the amendment", typ, ok, err)
	}
	var got analysis.PlanEvidence
	_ = json.Unmarshal(ev, &got)
	if got.Steps[0].Attrs["marker"] != "rev2" {
		t.Fatalf("latest evidence = %s, want the corrected chain", ev)
	}
	// The original decision's evidence is preserved (ADR-011).
	events, _ := store.ListEvents(ctx, created.ID)
	var first analysis.PlanEvidence
	_ = json.Unmarshal(events[0].Evidence, &first)
	if first.Steps[0].Attrs["marker"] != "rev1" {
		t.Fatalf("detected evidence = %s, want the original kept", events[0].Evidence)
	}
}

// Reconcile (no mode) records unknown, and a plan with no evidence
// records none: what every setup created before Phase 6.1 looks like.
func TestIntegration_Evidence_NoneRecordedIsNotRecorded(t *testing.T) {
	db := testDB(t)
	instrumentID := testInstrument(t, db)
	store := NewStore(db)
	ctx := context.Background()
	candleID := oneCandle(t, db, instrumentID, 1, 85)

	created, _, err := Reconcile(ctx, store, instrumentID, "1m", bullishPlan(analysis.PlanActionable, candleID, 1), testVersion, et(1), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if created.FoundMode != FoundUnknown {
		t.Fatalf("found mode = %q, want unknown", created.FoundMode)
	}
	if _, _, _, ok, err := store.LatestEvidence(ctx, created.ID); err != nil || ok {
		t.Fatalf("LatestEvidence ok=%v err=%v, want not recorded", ok, err)
	}
	lc, _ := store.LifecycleByID(ctx, []int64{created.ID})
	if lc[created.ID].EvidenceRecorded {
		t.Fatal("evidence_recorded = true with none recorded")
	}
	// Stored as the JSON null, never SQL NULL (the column is NOT NULL).
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT evidence::text FROM setup_events WHERE setup_id = $1 ORDER BY id LIMIT 1`, created.ID).Scan(&raw); err != nil || raw != "null" {
		t.Fatalf("stored evidence = %q err=%v, want null", raw, err)
	}
}
