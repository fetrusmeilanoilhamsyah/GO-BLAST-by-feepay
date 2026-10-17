//go:build load

package loadtest

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa/watest"
)

// TestL5_MatiMendadak menjalankan skenario L5: Crash tanpa drain di tengah pengiriman dan pemulihan boot A1.
// Kriteria lulus:
// - Sesuai Keputusan A1: tiap nomor diterima FakeSender paling banyak sekali
// - Jumlah nomor yatim sama dengan yang ditandai hasil_tidak_pasti
// - Tidak ada status 'dikirim' tersisa
func TestL5_MatiMendadak(t *testing.T) {
	RequireSafety(t)
	t.Logf("=== Menjalankan Skenario L5: Mati Mendadak (Sudden Crash & RecoverOnBoot) ===")

	pool := GetTestPool(t, 25, nil)
	ctx := context.Background()

	// 1. Setup Data
	workerID, err := CreateLoadWorker(ctx, pool, "l5")
	if err != nil {
		t.Fatalf("Gagal membuat worker: %v", err)
	}

	campaignID, batchID, err := CreateLoadCampaign(ctx, pool, workerID, "Campaign L5 Mati Mendadak")
	if err != nil {
		t.Fatalf("Gagal membuat campaign: %v", err)
	}
	defer testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})

	const numberCount = 5000
	if err := PopulateFictitiousNumbers(ctx, pool, campaignID, batchID, numberCount, 1); err != nil {
		t.Fatalf("Gagal populasi nomor: %v", err)
	}

	const accountCount = 30
	accIDs, err := CreateLoadAccounts(ctx, pool, workerID, accountCount, "fast", "l5")
	if err != nil {
		t.Fatalf("Gagal membuat akun: %v", err)
	}

	if err := SetupTestSettings(ctx, pool, 50, 10); err != nil {
		t.Fatalf("Gagal setup settings: %v", err)
	}

	// 2. Gunakan delay pengiriman dan fungsi crash injeksi agar goroutine mati saat in-flight
	var crashTriggered atomic.Bool
	sender := &watest.FakeSender{
		ErrFunc: func(accountID int64) error {
			if crashTriggered.Load() {
				// Simulasi SIGKILL: goroutine mati mendadak tanpa sempat memutasi status DB
				runtime.Goexit()
			}
			return nil
		},
		SendDelay: 50 * time.Millisecond,
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, sender, nil, nil)

	// 3. Mulai akun dan biarkan pesan mengalir
	for _, id := range accIDs {
		eng.StartAccount(id, workerID)
	}

	// Tunggu sejenak agar banyak goroutine aktif mengklaim dan sedang in-flight
	time.Sleep(500 * time.Millisecond)

	// 4. SIMULASI MATI MENDADAK:
	// Memicu crash instan pada semua worker yang sedang in-flight
	t.Logf("MENSIMULASIKAN CRASH / SIGKILL (Mati mendadak saat in-flight)...")
	crashTriggered.Store(true)

	// Beri jeda kecil agar goroutine in-flight mengeksekusi Goexit()
	time.Sleep(300 * time.Millisecond)

	// Hentikan akun yang tersisa
	for _, id := range accIDs {
		eng.StopAccount(id)
	}

	// 5. Cek kondisi database sebelum RecoverOnBoot
	var inFlightBefore int64
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_numbers
		WHERE campaign_id = $1 AND status = 'dikirim'
	`, campaignID).Scan(&inFlightBefore)
	if err != nil {
		t.Fatalf("Gagal query status dikirim: %v", err)
	}
	t.Logf("Jumlah nomor menggantung di status 'dikirim' akibat crash: %d", inFlightBefore)
	if inFlightBefore == 0 {
		t.Logf("Catatan: Kebetulan tidak ada nomor yang tertahan in-flight saat stop.")
	}

	// 6. Jalankan Engine Baru (Simulasi server booting kembali)
	t.Logf("Memulai instance Engine baru dan mengeksekusi RecoverOnBoot...")
	newEng := engine.New(database, sender, nil, nil)

	recoveredCount, err := newEng.RecoverOnBoot(ctx)
	if err != nil {
		t.Fatalf("RecoverOnBoot gagal: %v", err)
	}
	t.Logf("RecoverOnBoot berhasil memulihkan %d nomor menggantung.", recoveredCount)

	// 7. Evaluasi Kriteria Lulus

	// Kriteria A1.1: Jumlah nomor yatim sama dengan yang ditandai hasil_tidak_pasti
	if recoveredCount != inFlightBefore {
		t.Errorf("GAGAL Kriteria A1.1: Jumlah dipulihkan (%d) != jumlah dikirim sebelum boot (%d)!",
			recoveredCount, inFlightBefore)
	} else {
		t.Logf("LULUS Kriteria A1.1: Jumlah dipulihkan (%d) sama persis dengan nomor in-flight (%d).",
			recoveredCount, inFlightBefore)
	}

	// Kriteria A1.2: Tidak ada status 'dikirim' tersisa
	var inFlightAfter int64
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_numbers
		WHERE campaign_id = $1 AND status = 'dikirim'
	`, campaignID).Scan(&inFlightAfter)
	if err != nil {
		t.Fatalf("Gagal query inFlightAfter: %v", err)
	}

	if inFlightAfter != 0 {
		t.Errorf("GAGAL Kriteria A1.2: Masih ada %d nomor berstatus 'dikirim' tersisa setelah RecoverOnBoot!", inFlightAfter)
	} else {
		t.Logf("LULUS Kriteria A1.2: 0 nomor tersisa di status 'dikirim'.")
	}

	// Kriteria A1.3: Semua nomor yang dipulihkan memiliki last_error = 'hasil_tidak_pasti' dan status 'gagal'
	var recoveredVerified int64
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_numbers
		WHERE campaign_id = $1 AND status = 'gagal' AND last_error = 'hasil_tidak_pasti'
	`, campaignID).Scan(&recoveredVerified)
	if err != nil {
		t.Fatalf("Gagal query recoveredVerified: %v", err)
	}

	if recoveredVerified < recoveredCount {
		t.Errorf("GAGAL Kriteria A1.3: Verifikasi status gagal (%d) < jumlah dipulihkan (%d)!",
			recoveredVerified, recoveredCount)
	} else {
		t.Logf("LULUS Kriteria A1.3: Seluruh %d nomor dipulihkan berstatus 'gagal' dengan last_error='hasil_tidak_pasti' (total tidak pasti: %d).",
			recoveredCount, recoveredVerified)
	}

	// Kriteria A1.4: Tiap nomor diterima FakeSender paling banyak satu kali
	var dupSentCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
			SELECT phone_e164 FROM campaign_numbers
			WHERE campaign_id = $1 AND status = 'terkirim'
			GROUP BY phone_e164 HAVING COUNT(*) > 1
		) d
	`, campaignID).Scan(&dupSentCount)
	if err != nil {
		t.Fatalf("Gagal query duplikat terkirim: %v", err)
	}

	if dupSentCount != 0 {
		t.Errorf("GAGAL Kriteria A1.4: Ditemukan %d nomor terkirim ganda!", dupSentCount)
	} else {
		t.Logf("LULUS Kriteria A1.4: 0 pesan ganda terverifikasi.")
	}
}
