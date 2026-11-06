package main

import (
	"context"
	"database/sql"
	"fmt"
)

// timeframeLockKey maps a timeframe string to a small, stable int32 --
// the second component of the per-(instrument, timeframe) Postgres
// advisory lock key acquireInstrumentLock takes (project-status audit,
// section E: "nothing prevents two argus-worker processes processing
// the same instrument"). Only "1m" is ever evaluable today (this
// package's own timeframe constant), but keyed by value, not position
// in some list, so adding a second timeframe later can never silently
// collide with an existing key. Add to this map, never reuse a value
// already in it, if a second timeframe is ever added.
var timeframeLockKey = map[string]int32{
	"1m": 1,
}

// instrumentLock is a held Postgres session-level advisory lock for one
// (instrument, timeframe), bound to the single *sql.Conn it was
// acquired on.
//
// Session-level advisory locks (pg_try_advisory_lock/pg_advisory_unlock,
// as opposed to the _xact_ variants) are tied to the physical
// connection/backend that took them, not to a session's query text or
// to *sql.DB's own pooled notion of "a connection" -- database/sql can
// otherwise multiplex unrelated queries from the same *sql.DB onto any
// idle physical connection between two calls. That is exactly why this
// uses a dedicated *sql.Conn (database/sql's own escape hatch for "one
// physical connection for its whole lifetime, until Close"), obtained
// once in acquireInstrumentLock and held for every statement this type
// issues, rather than pooled queries through *sql.DB directly: a lock
// taken on one physical connection and "released" through a different
// one would silently no-op, not actually unlock anything.
type instrumentLock struct {
	conn *sql.Conn
	key1 int32
	key2 int32
}

// acquireInstrumentLock attempts to take the advisory lock for
// (instrumentID, timeframe) on a dedicated connection, via the
// non-blocking pg_try_advisory_lock -- a worker that cannot get the
// lock skips this instrument THIS TICK rather than waiting; the next
// tick (processing.go's own run loop, which already retries a failed
// instrument for free) tries again. ok is false when another session
// already holds it; the dedicated connection has already been released
// back to the pool in that case, and lock is nil.
//
// key1 truncates instrumentID to int32 (Postgres advisory locks take
// two int4 keys, or one int8 -- the brief's own instruction is the
// two-int form). instrument_id is BIGSERIAL and, in practice, an
// ordinary auto-incrementing registry of instruments this project adds
// by hand; it is not expected to approach 2^31 in any foreseeable
// future, so this truncation is accepted, not overlooked -- documented
// here rather than worked around with a wider, non-standard lock key.
func acquireInstrumentLock(ctx context.Context, db *sql.DB, instrumentID int64, timeframe string) (lock *instrumentLock, ok bool, err error) {
	key2, known := timeframeLockKey[timeframe]
	if !known {
		return nil, false, fmt.Errorf("processing: no advisory lock key registered for timeframe %q", timeframe)
	}
	key1 := int32(instrumentID) //nolint:gosec // intentional, documented truncation -- see this function's own doc comment

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire instrument lock: get connection: %w", err)
	}

	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1, $2)", key1, key2).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("acquire instrument lock: pg_try_advisory_lock: %w", err)
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	return &instrumentLock{conn: conn, key1: key1, key2: key2}, true, nil
}

// release unlocks and returns the dedicated connection to the pool.
// Safe to call exactly once (the only way this type is used, via
// processInstrumentLocked's own defer).
//
// A held session-level advisory lock is also released automatically
// when its owning backend ends (Postgres's own guarantee) -- so even if
// a caller never reaches this call at all (a panic, or the process
// being killed outright), closing or losing the underlying connection
// still frees the lock. This is what makes the lock's lifetime safe
// across a crash, not just across a normal or error return from
// processInstrument.
func (l *instrumentLock) release(ctx context.Context) error {
	var unlocked bool
	err := l.conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1, $2)", l.key1, l.key2).Scan(&unlocked)
	closeErr := l.conn.Close()
	if err != nil {
		return fmt.Errorf("release instrument lock: pg_advisory_unlock: %w", err)
	}
	if !unlocked {
		return fmt.Errorf("release instrument lock: pg_advisory_unlock reported no lock held for key (%d,%d)", l.key1, l.key2)
	}
	return closeErr
}
