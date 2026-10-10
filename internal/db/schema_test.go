package db_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"
)

func setupTestDB(t *testing.T) (*sql.DB, *pgxpool.Pool) {
	// Try loading .env from project root
	_ = godotenv.Load("../../.env")

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL environment variable is empty. Please set it to a test database.")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("failed to connect to db: %v", err)
	}

	err = goose.SetDialect("postgres")
	if err != nil {
		t.Fatalf("failed to set dialect: %v", err)
	}

	migrationsDir := filepath.Join("..", "..", "db", "migrations")

	// e) migrate down lalu up berhasil tanpa error
	err = goose.DownTo(db, migrationsDir, 0)
	if err != nil && err != goose.ErrNoNextVersion {
		t.Fatalf("failed to migrate down: %v", err)
	}

	err = goose.Up(db, migrationsDir)
	if err != nil {
		t.Fatalf("failed to migrate up: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("failed to create pgxpool: %v", err)
	}

	return db, pool
}

func TestSchemaConstraints(t *testing.T) {
	sqlDB, pool := setupTestDB(t)
	defer sqlDB.Close()
	defer pool.Close()

	ctx := context.Background()

	// Setup basic data
	var adminID, worker1ID int64
	err := pool.QueryRow(ctx, "INSERT INTO users (email, password_hash, role, display_name, referral_code) VALUES ('admin@test.local', 'hash', 'admin', 'Admin', 'A1') RETURNING id").Scan(&adminID)
	if err != nil {
		t.Fatalf("failed to insert admin: %v", err)
	}
	err = pool.QueryRow(ctx, "INSERT INTO users (email, password_hash, role, display_name, referral_code) VALUES ('w1@test.local', 'hash', 'worker', 'W1', 'W1') RETURNING id").Scan(&worker1ID)
	if err != nil {
		t.Fatalf("failed to insert worker: %v", err)
	}

	var campaign1ID, campaign2ID int64
	err = pool.QueryRow(ctx, "INSERT INTO campaigns (name, body, elements, created_by) VALUES ('C1', 'B1', '{}', $1) RETURNING id", adminID).Scan(&campaign1ID)
	if err != nil {
		t.Fatalf("failed to insert campaign 1: %v", err)
	}
	err = pool.QueryRow(ctx, "INSERT INTO campaigns (name, body, elements, created_by) VALUES ('C2', 'B2', '{}', $1) RETURNING id", adminID).Scan(&campaign2ID)
	if err != nil {
		t.Fatalf("failed to insert campaign 2: %v", err)
	}

	var batchID int64
	err = pool.QueryRow(ctx, "INSERT INTO number_batches (campaign_id, filename, uploaded_by) VALUES ($1, 'f1.csv', $2) RETURNING id", campaign1ID, adminID).Scan(&batchID)
	if err != nil {
		t.Fatalf("failed to insert batch: %v", err)
	}
	var batch2ID int64
	err = pool.QueryRow(ctx, "INSERT INTO number_batches (campaign_id, filename, uploaded_by) VALUES ($1, 'f2.csv', $2) RETURNING id", campaign2ID, adminID).Scan(&batch2ID)
	if err != nil {
		t.Fatalf("failed to insert batch2: %v", err)
	}

	t.Run("a) memasukkan nomor yang sama dua kali di campaign yang sama gagal, di campaign berbeda berhasil", func(t *testing.T) {
		_, err := pool.Exec(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, '62811', '62')", campaign1ID, batchID)
		if err != nil {
			t.Fatalf("failed first insert: %v", err)
		}

		_, err = pool.Exec(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, '62811', '62')", campaign1ID, batchID)
		if err == nil {
			t.Fatal("expected error on duplicate phone in same campaign, got nil")
		}

		_, err = pool.Exec(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, '62811', '62')", campaign2ID, batch2ID)
		if err != nil {
			t.Fatalf("expected success on same phone in different campaign, got error: %v", err)
		}
	})

	t.Run("b) dua baris upah untuk campaign_number_id yang sama gagal", func(t *testing.T) {
		var cnID int64
		err := pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, '62822', '62') RETURNING id", campaign1ID, batchID).Scan(&cnID)
		if err != nil {
			t.Fatalf("failed to insert cn: %v", err)
		}

		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id) VALUES ($1, 'upah', 'IDR', 100, $2)", worker1ID, cnID)
		if err != nil {
			t.Fatalf("failed first upah: %v", err)
		}

		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id) VALUES ($1, 'upah', 'IDR', 100, $2)", worker1ID, cnID)
		if err == nil {
			t.Fatal("expected error on duplicate upah for same campaign_number_id, got nil")
		}
	})

	t.Run("c) UPDATE dan DELETE pada ledger_entries gagal", func(t *testing.T) {
		var ledgerID int64
		err := pool.QueryRow(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'koreksi', 'IDR', 50) RETURNING id", worker1ID).Scan(&ledgerID)
		if err != nil {
			t.Fatalf("failed to insert ledger entry: %v", err)
		}

		_, err = pool.Exec(ctx, "UPDATE ledger_entries SET amount = 100 WHERE id = $1", ledgerID)
		if err == nil {
			t.Fatal("expected error on UPDATE ledger_entries, got nil")
		}

		_, err = pool.Exec(ctx, "DELETE FROM ledger_entries WHERE id = $1", ledgerID)
		if err == nil {
			t.Fatal("expected error on DELETE ledger_entries, got nil")
		}
	})

	t.Run("d) penarikan melebihi saldo gagal", func(t *testing.T) {
		// worker1 has 100 + 50 = 150 IDR currently.
		
		// Withdraw 100 should succeed
		_, err := pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'penarikan', 'IDR', -100)", worker1ID)
		if err != nil {
			t.Fatalf("expected success on valid withdrawal, got error: %v", err)
		}

		// Withdraw 100 again should fail because balance is 50
		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'penarikan', 'IDR', -100)", worker1ID)
		if err == nil {
			t.Fatal("expected error on withdrawal exceeding balance, got nil")
		}
	})
}
