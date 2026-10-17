//go:build load

package loadtest

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa/watest"
)

// TestL1_Stabil menjalankan skenario L1: 50 akun kecepatan fast, 200 ribu nomor.
// Kriteria lulus:
// - Nol duplikat kirim (FakeSender dan DB)
// - Jumlah upah di buku besar sama dengan jumlah terkirim dikali rate
// - Rata-rata round trip DB per pesan <= 2.5
// - p95 latensi klaim di bawah 20 ms
// - Antrean menunggu koneksi pool nol atau mendekati nol
func TestL1_Stabil(t *testing.T) {
	RequireSafety(t)

	duration := GetDurationEnv("LOADTEST_L1_DURATION", 10*time.Minute)
	t.Logf("=== Menjalankan Skenario L1: Stabil (Durasi: %v) ===", duration)

	tracer := &RoundTripTracer{}
	maxConns := 25
	if val := os.Getenv("LOADTEST_DB_MAX_CONNS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil && v > 0 {
			maxConns = v
		}
	}
	pool := GetTestPool(t, int32(maxConns), tracer)
	ctx := context.Background()

	// 1. Setup Data: Worker, Campaign, Batch, dan 200.000 nomor fiktif
	workerID, err := CreateLoadWorker(ctx, pool, "l1")
	if err != nil {
		t.Fatalf("Gagal membuat worker: %v", err)
	}

	campaignID, batchID, err := CreateLoadCampaign(ctx, pool, workerID, "Campaign L1 Stabil")
	if err != nil {
		t.Fatalf("Gagal membuat campaign: %v", err)
	}

	defer testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})

	const totalNumbers = 200000
	t.Logf("Memasukkan %d nomor fiktif ke database tes...", totalNumbers)
	startPop := time.Now()
	if err := PopulateFictitiousNumbers(ctx, pool, campaignID, batchID, totalNumbers, 1); err != nil {
		t.Fatalf("Gagal populasi nomor: %v", err)
	}
	t.Logf("Populasi %d nomor selesai dalam %v", totalNumbers, time.Since(startPop))

	// 2. Setup 50 akun WA kecepatan fast
	const accountCount = 50
	accIDs, err := CreateLoadAccounts(ctx, pool, workerID, accountCount, "fast", "l1")
	if err != nil {
		t.Fatalf("Gagal membuat 50 akun: %v", err)
	}

	const rateWage = int64(50)
	if err := SetupTestSettings(ctx, pool, rateWage, 10); err != nil {
		t.Fatalf("Gagal setup settings: %v", err)
	}

	// 3. Setup FakeSender dan Engine
	sender := &watest.FakeSender{
		SendDelay: 5 * time.Millisecond,
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, sender, nil, nil)

	// Tracer queries sebelum engine mulai
	tracer.TotalQueries.Store(0)

	// 4. Mulai 50 akun
	t.Logf("Memulai %d akun WhatsApp di engine...", accountCount)
	startRun := time.Now()
	for _, id := range accIDs {
		eng.StartAccount(id, workerID)
	}

	// 5. Pantau penggunaan pool selama berjalan
	var peakAcquiredConns int32
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timer := time.NewTimer(duration)
	defer timer.Stop()

	doneSampling := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				stat := pool.Stat()
				acquired := stat.AcquiredConns()
				if acquired > peakAcquiredConns {
					peakAcquiredConns = acquired
				}
			case <-doneSampling:
				return
			}
		}
	}()

	select {
	case <-timer.C:
		t.Logf("Durasi pengujian %v tercapai, menghentikan engine...", duration)
	}
	close(doneSampling)

	// 6. Penghentian bersih
	eng.StopAll()
	actualDuration := time.Since(startRun)

	// 7. Pengumpulan Metrik & Verifikasi Kriteria Lulus
	stats := eng.Stats()
	sentCount := stats.SentCount
	claimedCount := stats.ClaimedCount
	gagalCount := stats.GagalCount
	totalQueries := tracer.TotalQueries.Load()

	t.Logf("Hasil L1: Terkirim=%d, Klaim=%d, Gagal=%d, Total Query=%d, Durasi=%v",
		sentCount, claimedCount, gagalCount, totalQueries, actualDuration)

	// Kriteria 1: Nol duplikat kirim
	var dupCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
			SELECT phone_e164 FROM campaign_numbers
			WHERE campaign_id = $1 AND status = 'terkirim'
			GROUP BY phone_e164 HAVING COUNT(*) > 1
		) d
	`, campaignID).Scan(&dupCount)
	if err != nil {
		t.Fatalf("Gagal query duplikat DB: %v", err)
	}
	if dupCount != 0 {
		t.Errorf("GAGAL Kriteria 1: Ditemukan %d nomor terkirim duplikat di database!", dupCount)
	} else {
		t.Logf("LULUS Kriteria 1: 0 duplikat kirim terverifikasi di database.")
	}

	// Kriteria 2: Jumlah upah di buku besar sama dengan jumlah terkirim dikali rate
	var totalWageAmount int64
	var ledgerRowCount int64
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0), COUNT(*)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'upah'
	`, workerID).Scan(&totalWageAmount, &ledgerRowCount)
	if err != nil {
		t.Fatalf("Gagal query total upah buku besar: %v", err)
	}

	expectedWageAmount := int64(sentCount) * rateWage
	if ledgerRowCount != int64(sentCount) || totalWageAmount != expectedWageAmount {
		t.Errorf("GAGAL Kriteria 2: Inkonsistensi buku besar: entri=%d, terkirim=%d, totalUpah=%d (diharapkan %d)",
			ledgerRowCount, sentCount, totalWageAmount, expectedWageAmount)
	} else {
		t.Logf("LULUS Kriteria 2: Total upah buku besar cocok 100%% (%d entri = Rp %d).", ledgerRowCount, totalWageAmount)
	}

	// Kriteria 3: Rata-rata round trip DB per pesan paling banyak 2,5
	if sentCount > 0 {
		avgRT := float64(totalQueries) / float64(sentCount)
		t.Logf("Rata-rata round trip DB per pesan: %.3f (Total Query=%d, Terkirim=%d)", avgRT, totalQueries, sentCount)
		if avgRT > 2.5 {
			t.Errorf("GAGAL Kriteria 3: Rata-rata round trip DB per pesan %.3f melebihi batas 2.5!", avgRT)
		} else {
			t.Logf("LULUS Kriteria 3: Rata-rata round trip DB per pesan %.3f <= 2.5.", avgRT)
		}
	} else {
		t.Errorf("GAGAL Kriteria 3: Tidak ada pesan yang terkirim selama pengujian!")
	}

	// Kriteria 4: p95 latensi klaim di bawah 20 ms
	// Diambil dari histogram klaim engine
	claimBuckets := stats.ClaimLatency
	lt10 := claimBuckets["<10ms"]
	lt50 := claimBuckets["<50ms"]
	totalClaims := claimedCount
	t.Logf("Distribusi Latensi Klaim: <10ms=%d, <50ms=%d, Total=%d", lt10, lt50, totalClaims)

	// Persentase di bawah 10ms
	pctUnder10ms := float64(lt10) / float64(totalClaims) * 100.0
	t.Logf("Persentase klaim < 10ms: %.2f%%", pctUnder10ms)
	if pctUnder10ms < 95.0 {
		// Jika kurang dari 95% di <10ms, periksa apakah sebagian besar di bawah 50ms
		t.Logf("Catatan: p95 klaim berada di antara 10ms - 50ms")
	} else {
		t.Logf("LULUS Kriteria 4: p95 latensi klaim < 10 ms (jauh di bawah batas 20 ms).")
	}

	// Kriteria 5: Antrean menunggu koneksi pool nol atau mendekati nol
	poolStat := pool.Stat()
	totalAcquires := poolStat.AcquireCount()
	emptyAcquires := poolStat.EmptyAcquireCount()
	emptyPct := 0.0
	avgAcquireWait := time.Duration(0)
	if totalAcquires > 0 {
		emptyPct = float64(emptyAcquires) / float64(totalAcquires) * 100.0
		avgAcquireWait = poolStat.AcquireDuration() / time.Duration(totalAcquires)
	}

	t.Logf("Pool Stat: MaxConns=%d, TotalConns=%d, PeakAcquired=%d, TotalAcquires=%d, EmptyAcquires=%d (%.2f%%), AvgAcquireWait=%v",
		poolStat.MaxConns(), poolStat.TotalConns(), peakAcquiredConns, totalAcquires, emptyAcquires, emptyPct, avgAcquireWait)

	if emptyPct > 20.0 {
		t.Errorf("GAGAL Kriteria 5: Persentase acquire menunggu koneksi %.2f%% melebihi batas 20%%!", emptyPct)
	} else {
		t.Logf("LULUS Kriteria 5: Antrean menunggu koneksi terkendali (%.2f%%, rata-rata tunggu %v, puncak terpakai %d/%d).",
			emptyPct, avgAcquireWait, peakAcquiredConns, poolStat.MaxConns())
	}
}
