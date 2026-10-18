package engine_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa"
	"goblast/internal/wa/watest"
)

type mockChecker struct {
	mu sync.Mutex
	fn func(ctx context.Context, accountID int64, phone string) wa.CheckResult
}

func (m *mockChecker) IsOnWhatsApp(ctx context.Context, accountID int64, phone string) wa.CheckResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fn != nil {
		return m.fn(ctx, accountID, phone)
	}
	return wa.CheckResultTerdaftar
}

// TestH13_EngineNumberChecking memverifikasi integrasi engine dengan pemeriksaan pendaftaran WhatsApp:
// 1. Nomor terdaftar (CheckResultTerdaftar) lanjut dikirim via Sender dan sukses terkirim.
// 2. Nomor tidak terdaftar sah (CheckResultTidakTerdaftar) langsung difinalisasi status 'tidak_terdaftar' tanpa upah.
// 3. Nomor tidak pasti (CheckResultTidakPasti) saat attempts < 3 dikembalikan ke 'tersedia' dengan last_error 'cek_nomor_gagal'.
// 4. Nomor tidak pasti (CheckResultTidakPasti) saat attempts >= 3 difinalisasi 'gagal' dengan last_error 'cek_nomor_gagal'.
// 5. Metrik pemeriksaan CheckCounts dan ConsecutiveTidakTerdaftar diperbarui secara akurat.
func TestH13_EngineNumberChecking(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	// Bersihkan tabel untuk pengujian terisolasi
	_, err := pool.Exec(ctx, `TRUNCATE TABLE ledger_entries, campaign_numbers, campaigns, wa_accounts CASCADE`)
	if err != nil {
		t.Fatalf("failed to truncate tables: %v", err)
	}

	// Buat user worker dan admin
	nowNano := time.Now().UnixNano()
	var workerID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role, display_name, referral_code)
		VALUES ($1, 'hash', 'worker', 'Worker H13', $2)
		RETURNING id
	`, fmt.Sprintf("worker_h13_%d", nowNano), fmt.Sprintf("REF_W_%d", nowNano)).Scan(&workerID)
	if err != nil {
		t.Fatalf("failed to insert worker: %v", err)
	}

	var adminID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role, display_name, referral_code)
		VALUES ($1, 'hash', 'admin', 'Admin H13', $2)
		RETURNING id
	`, fmt.Sprintf("admin_h13_%d", nowNano), fmt.Sprintf("REF_A_%d", nowNano)).Scan(&adminID)
	if err != nil {
		t.Fatalf("failed to insert admin: %v", err)
	}

	// Buat campaign aktif
	var campID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Camp H13 Engine', 'berjalan', 'kartu_link', 'Halo {nama|Kak}', '{}'::jsonb, $1)
		RETURNING id
	`, adminID).Scan(&campID)
	if err != nil {
		t.Fatalf("failed to insert campaign: %v", err)
	}

	var batchID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO number_batches (campaign_id, filename, uploaded_by)
		VALUES ($1, 'batch_h13.csv', $2)
		RETURNING id
	`, campID, adminID).Scan(&batchID)
	if err != nil {
		t.Fatalf("failed to insert batch: %v", err)
	}

	database := &db.DB{Pool: pool}
	settings := newTestSettings()
	settings.SetInt("rate_wage", 100)
	settings.SetInt("rate_referral", 20)
	fakeSender := &watest.FakeSender{}
	checker := &mockChecker{}

	eng := engine.New(database, fakeSender, checker, settings)

	createConnectedAccount := func(label string) int64 {
		var aid int64
		err := pool.QueryRow(ctx, `
			INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
			VALUES ($1, $2, $3, 'terhubung', 'fast')
			RETURNING id
		`, workerID, label, fmt.Sprintf("hash_%s_%d", label, time.Now().UnixNano())).Scan(&aid)
		if err != nil {
			t.Fatalf("insert wa_account error: %v", err)
		}
		return aid
	}

	t.Run("1. Nomor terdaftar (CheckResultTerdaftar) sukses dikirim dan mendapat upah", func(t *testing.T) {
		accID := createConnectedAccount("WA H13 Acc 1")

		var numID int64
		err := pool.QueryRow(ctx, `
			INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
			VALUES ($1, $2, '628130000001', '62', 'tersedia', 0)
			RETURNING id
		`, campID, batchID).Scan(&numID)
		if err != nil {
			t.Fatalf("insert number error: %v", err)
		}

		checker.fn = func(ctx context.Context, accountID int64, phone string) wa.CheckResult {
			return wa.CheckResultTerdaftar
		}

		eng.StartAccount(accID, workerID)

		deadline := time.Now().Add(4 * time.Second)
		var st string
		for time.Now().Before(deadline) {
			_ = pool.QueryRow(ctx, `SELECT status FROM campaign_numbers WHERE id = $1`, numID).Scan(&st)
			if st == "terkirim" {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		eng.StopAccount(accID)

		if st != "terkirim" {
			t.Fatalf("expected status 'terkirim', got %q", st)
		}

		// Verifikasi upah masuk ke ledger
		var wageCount int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'upah'`, workerID).Scan(&wageCount)
		if wageCount != 1 {
			t.Fatalf("expected 1 wage ledger entry, got %d", wageCount)
		}

		stats := eng.Stats()
		if stats.CheckCounts["terdaftar"] != 1 {
			t.Fatalf("expected check_counts.terdaftar == 1, got %d", stats.CheckCounts["terdaftar"])
		}
		if stats.ConsecutiveTidakTerdaftarCount != 0 {
			t.Fatalf("expected consecutive_tidak_terdaftar_count == 0, got %d", stats.ConsecutiveTidakTerdaftarCount)
		}
	})

	t.Run("2. Nomor pasti tidak terdaftar (CheckResultTidakTerdaftar) difinalisasi tidak_terdaftar tanpa upah", func(t *testing.T) {
		accID := createConnectedAccount("WA H13 Acc 2")

		var numID int64
		err := pool.QueryRow(ctx, `
			INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
			VALUES ($1, $2, '628130000002', '62', 'tersedia', 0)
			RETURNING id
		`, campID, batchID).Scan(&numID)
		if err != nil {
			t.Fatalf("insert number error: %v", err)
		}

		checker.fn = func(ctx context.Context, accountID int64, phone string) wa.CheckResult {
			return wa.CheckResultTidakTerdaftar
		}

		eng.StartAccount(accID, workerID)

		deadline := time.Now().Add(4 * time.Second)
		var st string
		for time.Now().Before(deadline) {
			_ = pool.QueryRow(ctx, `SELECT status FROM campaign_numbers WHERE id = $1`, numID).Scan(&st)
			if st == "tidak_terdaftar" {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		eng.StopAccount(accID)

		if st != "tidak_terdaftar" {
			t.Fatalf("expected status 'tidak_terdaftar', got %q", st)
		}

		// Upah tidak boleh bertambah (tetap 1 dari subtest 1)
		var wageCount int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'upah'`, workerID).Scan(&wageCount)
		if wageCount != 1 {
			t.Fatalf("expected still 1 wage ledger entry, got %d", wageCount)
		}

		stats := eng.Stats()
		if stats.CheckCounts["tidak_terdaftar"] != 1 {
			t.Fatalf("expected check_counts.tidak_terdaftar == 1, got %d", stats.CheckCounts["tidak_terdaftar"])
		}
		if stats.ConsecutiveTidakTerdaftarCount != 1 {
			t.Fatalf("expected consecutive_tidak_terdaftar_count == 1, got %d", stats.ConsecutiveTidakTerdaftarCount)
		}
	})

	t.Run("3. Nomor tidak pasti (CheckResultTidakPasti) saat attempts < 3 kembali tersedia", func(t *testing.T) {
		accID := createConnectedAccount("WA H13 Acc 3")

		var numID int64
		err := pool.QueryRow(ctx, `
			INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
			VALUES ($1, $2, '628130000003', '62', 'tersedia', 0)
			RETURNING id
		`, campID, batchID).Scan(&numID)
		if err != nil {
			t.Fatalf("insert number error: %v", err)
		}

		checker.fn = func(ctx context.Context, accountID int64, phone string) wa.CheckResult {
			return wa.CheckResultTidakPasti
		}

		eng.StartAccount(accID, workerID)

		deadline := time.Now().Add(4 * time.Second)
		var st, lastErr string
		var attempts int
		for time.Now().Before(deadline) {
			_ = pool.QueryRow(ctx, `SELECT status, COALESCE(last_error, ''), attempts FROM campaign_numbers WHERE id = $1`, numID).Scan(&st, &lastErr, &attempts)
			// Harus kembali 'tersedia', bukan 'tidak_terdaftar', dengan attempts >= 1 dan last_error 'cek_nomor_gagal'
			if st == "tersedia" && lastErr == "cek_nomor_gagal" && attempts >= 1 {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		eng.StopAccount(accID)

		if st != "tersedia" || lastErr != "cek_nomor_gagal" {
			t.Fatalf("expected status 'tersedia' and last_error 'cek_nomor_gagal', got st=%q, lastErr=%q", st, lastErr)
		}

		// Bersihkan nomor subtest 3 agar tidak merebut klaim pada subtest berikutnya
		_, _ = pool.Exec(ctx, `DELETE FROM campaign_numbers WHERE id = $1`, numID)

		stats := eng.Stats()
		if stats.CheckCounts["tidak_pasti"] < 1 {
			t.Fatalf("expected check_counts.tidak_pasti >= 1, got %d", stats.CheckCounts["tidak_pasti"])
		}
	})

	t.Run("4. Nomor tidak pasti (CheckResultTidakPasti) saat attempts >= 3 difinalisasi gagal cek_nomor_gagal", func(t *testing.T) {
		accID := createConnectedAccount("WA H13 Acc 4")

		// Pasang attempts awal = 2, sehingga saat ClaimNextNumber attempts menjadi 3 (mencapai batas)
		var numID int64
		err := pool.QueryRow(ctx, `
			INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
			VALUES ($1, $2, '628130000004', '62', 'tersedia', 2)
			RETURNING id
		`, campID, batchID).Scan(&numID)
		if err != nil {
			t.Fatalf("insert number error: %v", err)
		}

		checker.fn = func(ctx context.Context, accountID int64, phone string) wa.CheckResult {
			return wa.CheckResultTidakPasti
		}

		eng.StartAccount(accID, workerID)

		deadline := time.Now().Add(4 * time.Second)
		var st, lastErr string
		var attempts int
		for time.Now().Before(deadline) {
			_ = pool.QueryRow(ctx, `SELECT status, COALESCE(last_error, ''), attempts FROM campaign_numbers WHERE id = $1`, numID).Scan(&st, &lastErr, &attempts)
			if st == "gagal" && lastErr == "cek_nomor_gagal" && attempts >= 3 {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		eng.StopAccount(accID)

		if st != "gagal" || lastErr != "cek_nomor_gagal" {
			t.Fatalf("expected status 'gagal' and last_error 'cek_nomor_gagal', got st=%q, lastErr=%q, attempts=%d", st, lastErr, attempts)
		}
	})

	t.Run("5. Pelacakan beruntun ConsecutiveTidakTerdaftar ter-reset saat ada nomor terdaftar", func(t *testing.T) {
		// Simulasikan 3 tidak_terdaftar beruntun
		eng.Metrics().IncCheckResult(wa.CheckResultTidakTerdaftar)
		eng.Metrics().IncCheckResult(wa.CheckResultTidakTerdaftar)
		eng.Metrics().IncCheckResult(wa.CheckResultTidakTerdaftar)

		if eng.ConsecutiveTidakTerdaftar() < 3 {
			t.Fatalf("expected consecutive >= 3, got %d", eng.ConsecutiveTidakTerdaftar())
		}

		// Terdaftar 1 nomor -> harus reset ke 0
		eng.Metrics().IncCheckResult(wa.CheckResultTerdaftar)
		if eng.ConsecutiveTidakTerdaftar() != 0 {
			t.Fatalf("expected consecutive reset to 0, got %d", eng.ConsecutiveTidakTerdaftar())
		}
	})
}
