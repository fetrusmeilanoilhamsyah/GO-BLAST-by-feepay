package db_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"

	"goblast/internal/testutil"
)

// TestMigrationsUpAndDown memverifikasi aturan nomor 3:
// "Setiap migrasi punya bagian Down, dan harus lulus naik, turun, naik lagi di database tes."
func TestMigrationsUpAndDown(t *testing.T) {
	root := testutil.RootDir(t)
	_ = godotenv.Load(filepath.Join(root, ".env"))
	testDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if testDSN == "" {
		t.Fatal("TEST_DATABASE_URL kosong")
	}

	sqlDB, err := sql.Open("pgx", testDSN)
	if err != nil {
		t.Fatalf("gagal koneksi sql: %v", err)
	}
	defer sqlDB.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("gagal set dialect: %v", err)
	}

	migrationsDir := filepath.Join(root, "db", "migrations")

	// 1. Pastikan Up sampai migrasi terbaru
	if err := goose.Up(sqlDB, migrationsDir); err != nil {
		t.Fatalf("goose.Up awal gagal: %v", err)
	}

	currentVer, err := goose.GetDBVersion(sqlDB)
	if err != nil {
		t.Fatalf("gagal ambil DB version: %v", err)
	}
	t.Logf("Versi saat ini setelah Up: %d", currentVer)
	if currentVer < 24 {
		t.Fatalf("diharapkan versi >= 24 setelah up, didapat %d", currentVer)
	}

	// Putuskan koneksi lain yang mungkin menggantung sebelum DDL
	_, _ = sqlDB.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()`)

	// 2. Down sampai ke migrasi 18 (menguji Down 0024, 0023, 0022, 0021, 0020, 0019)
	t.Log("Menjalankan goose.DownTo versi 18...")
	if err := goose.DownTo(sqlDB, migrationsDir, 18); err != nil {
		t.Fatalf("goose.DownTo(18) gagal: %v", err)
	}

	downVer, err := goose.GetDBVersion(sqlDB)
	if err != nil {
		t.Fatalf("gagal ambil DB version setelah down: %v", err)
	}
	t.Logf("Versi setelah DownTo: %d", downVer)
	if downVer != 18 {
		t.Fatalf("diharapkan versi 18 setelah DownTo, didapat %d", downVer)
	}

	// 3. Up lagi ke versi terbaru (naik lagi)
	t.Log("Menjalankan goose.Up kembali ke versi terbaru...")
	if err := goose.Up(sqlDB, migrationsDir); err != nil {
		t.Fatalf("goose.Up (naik lagi) gagal: %v", err)
	}

	upAgainVer, err := goose.GetDBVersion(sqlDB)
	if err != nil {
		t.Fatalf("gagal ambil DB version setelah up lagi: %v", err)
	}
	t.Logf("Versi setelah Up lagi: %d", upAgainVer)
	if upAgainVer < 24 {
		t.Fatalf("diharapkan versi >= 24 setelah up lagi, didapat %d", upAgainVer)
	}
}
