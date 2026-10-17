//go:build load

package loadtest

import (
	"context"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa/watest"
	"goblast/internal/web"
)

// TestL7_Shutdown menjalankan skenario L7: Penghentian graceful shutdown saat beban L1 berjalan.
// Kriteria lulus:
// - Proses selesai dalam 25 detik
// - Tidak ada upah hilang (setiap nomor sukses 'terkirim' memiliki entri 'upah' di ledger_entries)
// - Tidak ada nomor 'dikirim' yang menjadi yatim
func TestL7_Shutdown(t *testing.T) {
	RequireSafety(t)
	ChangeToProjectRoot(t)
	t.Logf("=== Menjalankan Skenario L7: Graceful Shutdown (SIGTERM under Load) ===")

	pool := GetTestPool(t, 25, nil)
	ctx := context.Background()

	// 1. Setup Data
	workerID, err := CreateLoadWorker(ctx, pool, "l7")
	if err != nil {
		t.Fatalf("Gagal membuat worker: %v", err)
	}

	campaignID, batchID, err := CreateLoadCampaign(ctx, pool, workerID, "Campaign L7 Shutdown")
	if err != nil {
		t.Fatalf("Gagal membuat campaign: %v", err)
	}
	defer testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})

	const numberCount = 10000
	if err := PopulateFictitiousNumbers(ctx, pool, campaignID, batchID, numberCount, 1); err != nil {
		t.Fatalf("Gagal populasi nomor: %v", err)
	}

	const accountCount = 50
	accIDs, err := CreateLoadAccounts(ctx, pool, workerID, accountCount, "fast", "l7")
	if err != nil {
		t.Fatalf("Gagal membuat akun: %v", err)
	}

	const rateWage = int64(50)
	if err := SetupTestSettings(ctx, pool, rateWage, 10); err != nil {
		t.Fatalf("Gagal setup settings: %v", err)
	}

	// FakeSender dengan delay pengiriman realistis agar selalu ada in-flight
	sender := &watest.FakeSender{
		SendDelay: 40 * time.Millisecond,
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, sender, nil, nil)

	srv, err := web.NewServer(database, "test", nil, eng)
	if err != nil {
		t.Fatalf("Gagal membuat server web: %v", err)
	}

	// 2. Mulai semua 50 akun
	for _, id := range accIDs {
		eng.StartAccount(id, workerID)
	}

	// Biarkan beban berjalan aktif selama 2 detik
	t.Logf("Menjalankan beban aktif 50 akun selama 2 detik...")
	time.Sleep(2 * time.Second)

	// 3. TRIGGER SIGTERM / SHUTDOWN:
	// Memanggil protokol shutdown terstruktur:
	// a. srv.SetDraining(true)
	// b. eng.StopAll() (drain timeout maks 20 detik)
	t.Logf("Memicu graceful shutdown (SetDraining + eng.StopAll)...")
	shutdownStart := time.Now()

	srv.SetDraining(true)
	eng.StopAll()

	shutdownDuration := time.Since(shutdownStart)
	t.Logf("Shutdown selesai dalam %v", shutdownDuration)

	// 4. Verifikasi Kriteria Lulus

	// Kriteria 1: Proses selesai dalam 25 detik
	if shutdownDuration > 25*time.Second {
		t.Errorf("GAGAL Kriteria 1: Durasi shutdown %v melebihi batas 25 detik!", shutdownDuration)
	} else {
		t.Logf("LULUS Kriteria 1: Shutdown selesai dalam %v (< 25 detik).", shutdownDuration)
	}

	// Kriteria 2: Tidak ada nomor 'dikirim' yang menjadi yatim
	var hungCount int64
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_numbers
		WHERE campaign_id = $1 AND status = 'dikirim'
	`, campaignID).Scan(&hungCount)
	if err != nil {
		t.Fatalf("Gagal query nomor dikirim: %v", err)
	}

	if hungCount != 0 {
		t.Errorf("GAGAL Kriteria 2: Ditemukan %d nomor menggantung di status 'dikirim' setelah shutdown!", hungCount)
	} else {
		t.Logf("LULUS Kriteria 2: 0 nomor menggantung di status 'dikirim'.")
	}

	// Kriteria 3: Tidak ada upah hilang
	var successCount int64
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_numbers
		WHERE campaign_id = $1 AND status = 'terkirim'
	`, campaignID).Scan(&successCount)
	if err != nil {
		t.Fatalf("Gagal query nomor terkirim: %v", err)
	}

	var wageCount int64
	var totalWage int64
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'upah'
	`, workerID).Scan(&wageCount, &totalWage)
	if err != nil {
		t.Fatalf("Gagal query ledger upah: %v", err)
	}

	t.Logf("Audit Upah: Terkirim=%d, Baris Upah=%d, Total Nilai=Rp %d", successCount, wageCount, totalWage)

	if wageCount != successCount {
		t.Errorf("GAGAL Kriteria 3: Inkonsistensi data: %d nomor terkirim != %d upah buku besar!",
			successCount, wageCount)
	} else if totalWage != successCount*rateWage {
		t.Errorf("GAGAL Kriteria 3: Total upah Rp %d tidak sesuai dengan perhitungan (%d x Rp %d)!",
			totalWage, successCount, rateWage)
	} else {
		t.Logf("LULUS Kriteria 3: 0 upah hilang. Seluruh %d nomor terkirim memiliki buku besar yang valid.", successCount)
	}
}
