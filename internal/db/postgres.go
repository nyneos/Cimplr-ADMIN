package db

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationSQL is baked into the binary at compile time.
// No file-system access needed at runtime — works on Render, Docker, etc.
//
//go:embed 001_init.sql
var migrationSQL string

// NewPool creates a pgxpool connection pool using the provided DSN.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	cfg.MaxConns = 5                          // Supabase free tier: max 15 total, keep headroom
	cfg.MinConns = 0                          // no persistent idle conns — PgBouncer kills them anyway
	cfg.MaxConnLifetime = 5 * time.Minute     // recycle proactively before PgBouncer cuts us off
	cfg.MaxConnIdleTime = 20 * time.Second    // drop idle before PgBouncer kills at ~30s
	cfg.HealthCheckPeriod = 30 * time.Second  // don't hammer PgBouncer with keep-alive pings

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// Migrate runs the embedded SQL migration against the database.
// The SQL uses CREATE TABLE IF NOT EXISTS throughout, so it is safe to re-run.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("db: acquire conn for migration: %w", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, migrationSQL)
	if err != nil {
		return fmt.Errorf("db: run migration: %w", err)
	}
	return nil
}

