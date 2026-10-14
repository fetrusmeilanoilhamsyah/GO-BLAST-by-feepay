package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"

	"goblast/internal/db"
)

var (
	migrateOnce sync.Once
	migrateErr  error
)

// RootDir mencari direktori root proyek (lokasi file go.mod).
func RootDir(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("testutil: gagal mendapatkan direktori kerja: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("testutil: root proyek (go.mod) tidak ditemukan")
	return ""
}

// TestDBURL memuat .env dan mengembalikan TEST_DATABASE_URL yang valid.
func TestDBURL(t testing.TB) string {
	t.Helper()
	root := RootDir(t)
	_ = godotenv.Load(filepath.Join(root, ".env"))
	testDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if testDSN == "" {
		t.Fatal("TEST_DATABASE_URL kosong. Harap atur TEST_DATABASE_URL dengan nama database berakhiran '_test'.")
	}
	return testDSN
}

// TestDB memuat .env dari root, membaca TEST_DATABASE_URL, memvalidasi nama database
// berakhiran "_test" dan tidak sama dengan DATABASE_URL, menjalankan migrasi goose bila belum,
// serta mengembalikan pgxpool.Pool yang otomatis ditutup saat tes selesai.
func TestDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	root := RootDir(t)

	// Memuat .env dari root proyek
	_ = godotenv.Load(filepath.Join(root, ".env"))

	testDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if testDSN == "" {
		t.Fatal("TEST_DATABASE_URL kosong. Harap atur TEST_DATABASE_URL dengan nama database berakhiran '_test'.")
	}

	testCfg, err := pgxpool.ParseConfig(testDSN)
	if err != nil {
		t.Fatalf("testutil.TestDB: format TEST_DATABASE_URL tidak valid: %v", err)
	}

	dbName := testCfg.ConnConfig.Database
	if !strings.HasSuffix(dbName, "_test") {
		t.Fatalf("testutil.TestDB: nama database %q pada TEST_DATABASE_URL harus berakhiran '_test'", dbName)
	}

	mainDSN := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if mainDSN != "" {
		if testDSN == mainDSN {
			t.Fatal("testutil.TestDB: TEST_DATABASE_URL tidak boleh sama dengan DATABASE_URL")
		}
		mainCfg, err := pgxpool.ParseConfig(mainDSN)
		if err == nil {
			if testCfg.ConnConfig.Host == mainCfg.ConnConfig.Host &&
				testCfg.ConnConfig.Port == mainCfg.ConnConfig.Port &&
				testCfg.ConnConfig.Database == mainCfg.ConnConfig.Database {
				t.Fatalf("testutil.TestDB: TEST_DATABASE_URL mengarah ke database yang sama (%q) dengan DATABASE_URL", dbName)
			}
		}
	}

	// Menjalankan migrasi bila belum
	migrateOnce.Do(func() {
		sqlDB, err := sql.Open("pgx", testDSN)
		if err != nil {
			migrateErr = fmt.Errorf("gagal membuka koneksi sql untuk migrasi: %w", err)
			return
		}
		defer sqlDB.Close()

		if err := goose.SetDialect("postgres"); err != nil {
			migrateErr = fmt.Errorf("gagal mengatur goose dialect: %w", err)
			return
		}

		migrationsDir := filepath.Join(root, "db", "migrations")
		if err := goose.Up(sqlDB, migrationsDir); err != nil {
			migrateErr = fmt.Errorf("migrasi goose gagal: %w", err)
			return
		}
	})

	if migrateErr != nil {
		t.Fatalf("testutil.TestDB: %v", migrateErr)
	}

	pool, err := pgxpool.New(context.Background(), testDSN)
	if err != nil {
		t.Fatalf("testutil.TestDB: gagal menghubungkan pgxpool: %v", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("testutil.TestDB: ping database gagal: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}

// TestDBInstance mengembalikan wrapper *db.DB dari TestDB.
func TestDBInstance(t testing.TB) *db.DB {
	t.Helper()
	return &db.DB{Pool: TestDB(t)}
}

// TestEncryptionKey mengembalikan kunci 32-byte tetap untuk pengujian.
func TestEncryptionKey() []byte {
	return []byte("goblast-test-key-32-bytes-aes!!!")
}

// CleanupTestData menghapus data yang dibuat saat tes secara terisolasi hanya dengan WHERE pada id yang dibuat.
func CleanupTestData(pool *pgxpool.Pool, userIDs []int64, campaignIDs []int64) {
	ctx := context.Background()
	_, _ = pool.Exec(ctx, "ALTER TABLE ledger_entries DISABLE TRIGGER trg_prevent_ledger_update_delete")
	if len(campaignIDs) > 0 {
		_, _ = pool.Exec(ctx, "DELETE FROM ledger_entries WHERE campaign_number_id IN (SELECT id FROM campaign_numbers WHERE campaign_id = ANY($1))", campaignIDs)
	}
	if len(userIDs) > 0 {
		_, _ = pool.Exec(ctx, "DELETE FROM ledger_entries WHERE user_id = ANY($1)", userIDs)
	}
	_, _ = pool.Exec(ctx, "ALTER TABLE ledger_entries ENABLE TRIGGER trg_prevent_ledger_update_delete")

	if len(campaignIDs) > 0 {
		_, _ = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE campaign_id = ANY($1)", campaignIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM number_batches WHERE campaign_id = ANY($1)", campaignIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM campaigns WHERE id = ANY($1)", campaignIDs)
	}
	if len(userIDs) > 0 {
		_, _ = pool.Exec(ctx, "DELETE FROM admin_recovery_codes WHERE user_id = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM backups WHERE created_by = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM web_sessions WHERE user_id = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM audit_logs WHERE actor_id = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM notifications WHERE user_id = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM support_tickets WHERE user_id = ANY($1) OR replied_by = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM broadcasts WHERE created_by = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM wallet_entries WHERE created_by = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM currency_conversions WHERE user_id = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM wa_accounts WHERE worker_id = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM withdrawals WHERE user_id = ANY($1) OR processed_by = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "UPDATE settings SET updated_by = NULL WHERE updated_by = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "UPDATE users SET referred_by = NULL WHERE referred_by = ANY($1)", userIDs)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1)", userIDs)
	}
}
