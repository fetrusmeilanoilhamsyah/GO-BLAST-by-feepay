package db

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"goblast/internal/config"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

// QueryRow mengeksekusi query baris tunggal pada database pool.
func (d *DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return d.Pool.QueryRow(ctx, sql, args...)
}

// Stat mengembalikan statistik pool database untuk pemantauan/metrik.
func (d *DB) Stat() *pgxpool.Stat {
	if d.Pool == nil {
		return nil
	}
	return d.Pool.Stat()
}

func Connect(ctx context.Context, databaseURL string) (*DB, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is empty")
	}

	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// 1. Baca DB_MAX_CONNS & DB_MIN_CONNS dari environment (fallback ke konstanta di limits.go)
	maxConns := config.DefaultDBMaxConns
	if valStr := os.Getenv("DB_MAX_CONNS"); valStr != "" {
		if val, err := strconv.Atoi(valStr); err == nil && val > 0 {
			maxConns = val
		}
	}

	minConns := config.DefaultDBMinConns
	if valStr := os.Getenv("DB_MIN_CONNS"); valStr != "" {
		if val, err := strconv.Atoi(valStr); err == nil && val >= 0 {
			minConns = val
		}
	}

	poolCfg.MaxConns = int32(maxConns)
	poolCfg.MinConns = int32(minConns)
	poolCfg.MaxConnLifetime = config.DefaultDBMaxConnLifetime
	poolCfg.MaxConnIdleTime = config.DefaultDBMaxConnIdleTime
	poolCfg.HealthCheckPeriod = config.DefaultDBHealthCheckPeriod
	poolCfg.ConnConfig.ConnectTimeout = config.DefaultDBConnectTimeout

	// 2. Set runtime params: statement_timeout 30s, lock_timeout 5s, idle_in_transaction_session_timeout 30s
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = config.DefaultDBStatementTimeout
	poolCfg.ConnConfig.RuntimeParams["lock_timeout"] = config.DefaultDBLockTimeout
	poolCfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = config.DefaultDBIdleInTransactionTimeout
	poolCfg.ConnConfig.RuntimeParams["application_name"] = "goblast"

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{Pool: pool}, nil
}

