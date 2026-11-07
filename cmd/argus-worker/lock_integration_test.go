//go:build integration

package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chibuike-kt/argus/internal/ingestion/fixture"
)

// TestIntegration_InstrumentLock_SecondAcquireFailsWhileFirstHeld is
// the advisory lock's own basic contract, independent of
// processInstrumentLocked: pg_try_advisory_lock is keyed purely on the
// two integers given it, with no foreign-key relationship to a real
// instruments row, so this exercises acquireInstrumentLock directly
// against an arbitrary id.
func TestIntegration_InstrumentLock_SecondAcquireFailsWhileFirstHeld(t *testing.T) {
	db := testWorkerDB(t)
	ctx := context.Background()
	const instrumentID = int64(9_000_001)

	first, ok, err := acquireInstrumentLock(ctx, db, instrumentID, "1m")
	if err != nil {
		t.Fatalf("acquireInstrumentLock (first): %v", err)
	}
	if !ok {
		t.Fatal("acquireInstrumentLock (first): ok = false, want true (nothing else holds it yet)")
	}

	_, ok, err = acquireInstrumentLock(ctx, db, instrumentID, "1m")
	if err != nil {
		t.Fatalf("acquireInstrumentLock (second, same key): %v", err)
	}
	if ok {
		t.Fatal("acquireInstrumentLock (second, same key): ok = true, want false -- the first lock is still held")
	}

	if err := first.release(ctx); err != nil {
		t.Fatalf("release (first): %v", err)
	}

	// Now that the first lock is released, the same key is acquirable
	// again -- proof release actually freed it, not just reported
	// success.
	second, ok, err := acquireInstrumentLock(ctx, db, instrumentID, "1m")
	if err != nil {
		t.Fatalf("acquireInstrumentLock (after release): %v", err)
	}
	if !ok {
		t.Fatal("acquireInstrumentLock (after release): ok = false, want true")
	}
	if err := second.release(ctx); err != nil {
		t.Fatalf("release (second): %v", err)
	}
}

// TestIntegration_InstrumentLock_ReleaseSurvivesCancelledContext is the
// web-wiring-polish brief's own required proof for item 4: release must
// not leak the session-level lock when given an already-cancelled
// context (processInstrumentLocked's own defer still calls it even
// when the processing run's ctx was cancelled mid-flight) -- release
// runs the unlock on context.WithoutCancel(ctx) with its own short
// timeout specifically so this works.
func TestIntegration_InstrumentLock_ReleaseSurvivesCancelledContext(t *testing.T) {
	db := testWorkerDB(t)
	const instrumentID = int64(9_000_002)

	lock, ok, err := acquireInstrumentLock(context.Background(), db, instrumentID, "1m")
	if err != nil {
		t.Fatalf("acquireInstrumentLock: %v", err)
	}
	if !ok {
		t.Fatal("acquireInstrumentLock: ok = false, want true (nothing else holds it yet)")
	}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := lock.release(cancelledCtx); err != nil {
		t.Fatalf("release (cancelled ctx): %v", err)
	}

	// The real proof: another acquirer succeeds afterward. A worker
	// that never retries this check would otherwise skip this
	// instrument forever, exactly the bug this item fixes.
	second, ok, err := acquireInstrumentLock(context.Background(), db, instrumentID, "1m")
	if err != nil {
		t.Fatalf("acquireInstrumentLock (after cancelled-context release): %v", err)
	}
	if !ok {
		t.Fatal("acquireInstrumentLock (after cancelled-context release): ok = false -- the lock was leaked")
	}
	if err := second.release(context.Background()); err != nil {
		t.Fatalf("release (second): %v", err)
	}
}

// TestIntegration_ProcessInstrumentLocked_ReleasedAfterNormalCompletion
// proves the lock is free again immediately after a successful
// processInstrumentLocked call, not just eventually (e.g. only once the
// connection is garbage collected).
func TestIntegration_ProcessInstrumentLocked_ReleasedAfterNormalCompletion(t *testing.T) {
	db := testWorkerDB(t)
	logger := quietLogger()
	ctx := context.Background()

	loaded, err := fixture.DevDataset()
	if err != nil {
		t.Fatalf("DevDataset: %v", err)
	}
	base := loaded.Candles["BTC-PERP"][:20]
	instID, _ := seedAndIngest(t, db, logger, base, "release-normal")
	proc, _ := newWorkerProcessor(db, logger)
	inst := mustGetInstrument(t, db, instID)

	_, skipped, err := proc.processInstrumentLocked(ctx, inst)
	if err != nil {
		t.Fatalf("processInstrumentLocked: %v", err)
	}
	if skipped {
		t.Fatal("processInstrumentLocked: skipped = true on a completely free lock, want false")
	}

	lock, ok, err := acquireInstrumentLock(ctx, db, instID, "1m")
	if err != nil {
		t.Fatalf("acquireInstrumentLock (after normal completion): %v", err)
	}
	if !ok {
		t.Fatal("acquireInstrumentLock (after normal completion): ok = false -- the lock was not released")
	}
	_ = lock.release(ctx)
}

// TestIntegration_ProcessInstrumentLocked_ReleasedAfterErrorReturn
// proves the same thing on the error path, using the existing
// testFailBeforeReconcile test hook to force processInstrument to fail
// partway through -- the defer in processInstrumentLocked must still
// run.
func TestIntegration_ProcessInstrumentLocked_ReleasedAfterErrorReturn(t *testing.T) {
	db := testWorkerDB(t)
	logger := quietLogger()
	ctx := context.Background()

	loaded, err := fixture.DevDataset()
	if err != nil {
		t.Fatalf("DevDataset: %v", err)
	}
	base := loaded.Candles["BTC-PERP"][:20]
	instID, _ := seedAndIngest(t, db, logger, base, "release-error")
	proc, _ := newWorkerProcessor(db, logger)
	proc.testFailBeforeReconcile = func(time.Time) bool { return true }
	inst := mustGetInstrument(t, db, instID)

	_, skipped, err := proc.processInstrumentLocked(ctx, inst)
	if err == nil {
		t.Fatal("processInstrumentLocked: err = nil, want the simulated crash error")
	}
	if skipped {
		t.Fatal("processInstrumentLocked: skipped = true, want false -- the lock was free, processing itself failed")
	}

	lock, ok, err := acquireInstrumentLock(ctx, db, instID, "1m")
	if err != nil {
		t.Fatalf("acquireInstrumentLock (after error return): %v", err)
	}
	if !ok {
		t.Fatal("acquireInstrumentLock (after error return): ok = false -- the lock was not released on the error path")
	}
	_ = lock.release(ctx)
}

// TestIntegration_ProcessInstrumentLocked_ConcurrentWorkers_ExactlyOneProcesses
// is the brief's own required proof: two "worker processes" (two
// *processor values sharing one *sql.DB, each call getting its own
// dedicated advisory-lock connection, which is the part under test)
// calling processInstrumentLocked concurrently for the SAME instrument
// must not both actually process it. Exactly one must; the other must
// report skipped=true. The one that does process must reach the exact
// same final setups/setup_events a single, uncontended run reaches, and
// neither call may return a database constraint error.
func TestIntegration_ProcessInstrumentLocked_ConcurrentWorkers_ExactlyOneProcesses(t *testing.T) {
	db := testWorkerDB(t)
	logger := quietLogger()
	ctx := context.Background()

	loaded, err := fixture.DevDataset()
	if err != nil {
		t.Fatalf("DevDataset: %v", err)
	}
	base := loaded.Candles["BTC-PERP"][:200]

	// Reference: a single, uncontended run, for comparison.
	refInstID, _ := seedAndIngest(t, db, logger, base, "lock-ref")
	refProc, refStore := newWorkerProcessor(db, logger)
	refInst := mustGetInstrument(t, db, refInstID)
	if _, err := refProc.processInstrument(ctx, refInst); err != nil {
		t.Fatalf("reference run: processInstrument: %v", err)
	}
	refFingerprint := setupFingerprints(t, refStore, refInstID)
	if len(refFingerprint) == 0 {
		t.Fatal("reference run produced no setups -- nothing to prove the locked run matches")
	}

	// The contended run: two independent *processor values (two
	// "workers"), same underlying database, racing on the same
	// instrument.
	contInstID, _ := seedAndIngest(t, db, logger, base, "lock-contended")
	contInst := mustGetInstrument(t, db, contInstID)
	workerA, contStore := newWorkerProcessor(db, logger)
	workerB, _ := newWorkerProcessor(db, logger)

	type outcome struct {
		skipped bool
		err     error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, w := range []*processor{workerA, workerB} {
		wg.Add(1)
		go func(p *processor) {
			defer wg.Done()
			<-start
			_, skipped, err := p.processInstrumentLocked(ctx, contInst)
			results <- outcome{skipped: skipped, err: err}
		}(w)
	}
	close(start)
	wg.Wait()
	close(results)

	var processedCount, skippedCount int
	for r := range results {
		if r.err != nil {
			t.Fatalf("processInstrumentLocked (concurrent): unexpected error: %v", r.err)
		}
		if r.skipped {
			skippedCount++
		} else {
			processedCount++
		}
	}
	if processedCount != 1 || skippedCount != 1 {
		t.Fatalf("got processed=%d skipped=%d, want exactly one of each", processedCount, skippedCount)
	}

	contFingerprint := setupFingerprints(t, contStore, contInstID)
	if len(contFingerprint) != len(refFingerprint) {
		t.Fatalf("contended run produced %d setups, want %d (the reference run's own count)",
			len(contFingerprint), len(refFingerprint))
	}
	for i, want := range refFingerprint {
		if contFingerprint[i] != want {
			t.Fatalf("contFingerprint[%d] = %q, want %q", i, contFingerprint[i], want)
		}
	}
}
