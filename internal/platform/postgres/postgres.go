// Package postgres opens and pings the PostgreSQL connection pool.
//
// The skeleton uses database/sql with the lib/pq driver, which sqlc targets
// via the database/sql output. Swapping to pgx later is a change to sqlc.yaml
// and this file only; callers use *sql.DB.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// lib/pq registers the "postgres" database/sql driver via its init.
	_ "github.com/lib/pq"
)

// Open returns a configured *sql.DB and verifies connectivity with a ping.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}
