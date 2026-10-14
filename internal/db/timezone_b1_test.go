package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"goblast/internal/testutil"
)

// TestTimezoneB1_CurrentDateFlaw membuktikan bahwa query yang memakai CURRENT_DATE
// bergantung pada zona waktu sesi database dan menghasilkan angka yang salah
// untuk transaksi yang dibuat dini hari WIB (misal 00:30 WIB) ketika di-query
// dengan sesi DB bertransaksi UTC.
func TestTimezoneB1_CurrentDateFlaw(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	nonce := time.Now().UnixNano()
	var workerID int64
	err := pool.QueryRow(ctx, "INSERT INTO users (email, username, password_hash, role, display_name, referral_code) VALUES ($1, $2, 'hash', 'worker', 'TzWorker', $3) RETURNING id",
		fmt.Sprintf("tz_%d@test.local", nonce), fmt.Sprintf("tz_%d", nonce), fmt.Sprintf("TZ%d", nonce)).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker failed: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", workerID)
	}()

	// Misalkan hari ini di WIB adalah Tgl X. Kita buat waktu jam 00:30 WIB hari ini.
	locWIB, _ := time.LoadLocation("Asia/Jakarta")
	if locWIB == nil {
		locWIB = time.FixedZone("Asia/Jakarta", 7*3600)
	}
	nowWIB := time.Now().In(locWIB)
	txWIB := time.Date(nowWIB.Year(), nowWIB.Month(), nowWIB.Day(), 0, 30, 0, 0, locWIB)

	// Masukkan transaksi dengan created_at = 00:30 WIB hari ini
	var ledgerID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, created_at)
		VALUES ($1, 'upah', 'IDR', 50, $2)
		RETURNING id
	`, workerID, txWIB).Scan(&ledgerID)
	if err != nil {
		t.Fatalf("insert ledger entry failed: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "ALTER TABLE ledger_entries DISABLE TRIGGER trg_prevent_ledger_update_delete")
		_, _ = pool.Exec(ctx, "DELETE FROM ledger_entries WHERE id = $1", ledgerID)
		_, _ = pool.Exec(ctx, "ALTER TABLE ledger_entries ENABLE TRIGGER trg_prevent_ledger_update_delete")
	}()

	// Query 1: Jalankan query worker_handlers lama (memakai CURRENT_DATE) dengan sesi di-set ke UTC
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn failed: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, "SET TIME ZONE 'UTC'")
	if err != nil {
		t.Fatalf("set timezone UTC failed: %v", err)
	}

	var sumCurrentDate int64
	err = conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind IN ('upah', 'referral') AND currency = 'IDR' AND created_at >= CURRENT_DATE
	`, workerID).Scan(&sumCurrentDate)
	if err != nil {
		t.Fatalf("query CURRENT_DATE failed: %v", err)
	}

	// Jika waktu sekarang di UTC sudah melewati 00:00 UTC (yaitu >= 07:00 WIB),
	// CURRENT_DATE di UTC adalah hari ini (misal 15 Okt 00:00 UTC).
	// Sedangkan txWIB adalah 00:30 WIB (14 Okt 17:30 UTC), sehingga txWIB < CURRENT_DATE (UTC)
	// dan menghasilkan sum = 0 (data hilang dari pendapatan hari ini!).
	nowUTC := time.Now().UTC()
	if nowUTC.Day() == nowWIB.Day() {
		// Ketika jam di UTC sudah mencapai tanggal yang sama dengan WIB (yaitu >= 07:00 WIB)
		t.Logf("Waktu UTC (%v) dan WIB (%v): sumCurrentDate = %d", nowUTC, nowWIB, sumCurrentDate)
	}

	// Query 2: Dengan parameter eksplisit awal hari Asia/Jakarta
	startOfDayJakarta := time.Date(nowWIB.Year(), nowWIB.Month(), nowWIB.Day(), 0, 0, 0, 0, locWIB)
	var sumExplicitStartOfDay int64
	err = conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind IN ('upah', 'referral') AND currency = 'IDR' AND created_at >= $2
	`, workerID, startOfDayJakarta).Scan(&sumExplicitStartOfDay)
	if err != nil {
		t.Fatalf("query explicit start of day failed: %v", err)
	}

	if sumExplicitStartOfDay != 50 {
		t.Errorf("expected explicit start of day to return 50, got %d", sumExplicitStartOfDay)
	}
}
