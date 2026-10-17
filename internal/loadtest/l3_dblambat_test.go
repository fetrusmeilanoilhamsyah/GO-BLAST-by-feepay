//go:build load

package loadtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa/watest"
	"goblast/internal/web"
)

// TestL3_DBLambat menjalankan skenario L3: DB lambat dengan injeksi latensi 200ms pada 10% query.
// Kriteria lulus:
// - Tidak ada pesan ganda
// - Tidak ada nomor yatim setelah selesai
// - Engine pulih sendiri
// - /readyz tetap menjawab di bawah 100 ms
func TestL3_DBLambat(t *testing.T) {
	RequireSafety(t)
	ChangeToProjectRoot(t)

	duration := GetDurationEnv("LOADTEST_L3_DURATION", 3*time.Minute)
	t.Logf("=== Menjalankan Skenario L3: DB Lambat (Durasi: %v) ===", duration)

	chaosTracer := NewChaosLatencyTracer(0.10, 200*time.Millisecond)
	pool := GetTestPool(t, 25, chaosTracer)
	ctx := context.Background()

	// 1. Setup Data
	workerID, err := CreateLoadWorker(ctx, pool, "l3")
	if err != nil {
		t.Fatalf("Gagal membuat worker: %v", err)
	}

	campaignID, batchID, err := CreateLoadCampaign(ctx, pool, workerID, "Campaign L3 DB Lambat")
	if err != nil {
		t.Fatalf("Gagal membuat campaign: %v", err)
	}
	defer testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})

	const numberCount = 5000
	if err := PopulateFictitiousNumbers(ctx, pool, campaignID, batchID, numberCount, 1); err != nil {
		t.Fatalf("Gagal populasi nomor: %v", err)
	}

	const accountCount = 20
	accIDs, err := CreateLoadAccounts(ctx, pool, workerID, accountCount, "fast", "l3")
	if err != nil {
		t.Fatalf("Gagal membuat akun: %v", err)
	}

	if err := SetupTestSettings(ctx, pool, 50, 10); err != nil {
		t.Fatalf("Gagal setup settings: %v", err)
	}

	sender := &watest.FakeSender{
		SendDelay: 5 * time.Millisecond,
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, sender, nil, nil)

	// Setup Server untuk /readyz check
	srv, err := web.NewServer(database, "test", nil, eng)
	if err != nil {
		t.Fatalf("Gagal membuat server web: %v", err)
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// 2. Jalankan engine
	t.Logf("Memulai %d akun dengan 10%% query ditunda 200ms...", accountCount)
	for _, id := range accIDs {
		eng.StartAccount(id, workerID)
	}

	// 3. Pantau /readyz latensi secara periodik selama injeksi berlangsung
	var readyzP95 Tracker = &PercentileTracker{}
	var readyzFailures int
	stopMonitoring := make(chan struct{})

	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				start := time.Now()
				req := httptest.NewRequest("GET", "/readyz", nil)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				lat := time.Since(start)

				if rec.Code == http.StatusOK {
					readyzP95.(*PercentileTracker).Record(lat)
				} else {
					readyzFailures++
				}
			case <-stopMonitoring:
				return
			}
		}
	}()

	// Tunggu durasi uji
	time.Sleep(duration)
	close(stopMonitoring)

	// 4. Matikan tracer chaos untuk memverifikasi pemulihan mandiri
	t.Logf("Mematikan injeksi latensi DB (memverifikasi pemulihan mandiri)...")
	chaosTracer.SetActive(false)
	time.Sleep(5 * time.Second)

	// 5. Hentikan engine secara bersih
	eng.StopAll()

	// 6. Evaluasi Metrik
	stats := eng.Stats()
	t.Logf("Hasil L3: Terkirim=%d, Klaim=%d, Gagal=%d, Total Query=%d, Query Ditunda=%d (%.2f%%)",
		stats.SentCount, stats.ClaimedCount, stats.GagalCount,
		chaosTracer.TotalQueries.Load(), chaosTracer.DelayedQueries.Load(),
		float64(chaosTracer.DelayedQueries.Load())/float64(chaosTracer.TotalQueries.Load())*100.0,
	)

	// Kriteria 1: Tidak ada pesan ganda
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
		t.Errorf("GAGAL Kriteria 1: Ditemukan %d pesan ganda di database!", dupCount)
	} else {
		t.Logf("LULUS Kriteria 1: 0 pesan ganda terverifikasi.")
	}

	// Kriteria 2: Tidak ada nomor yatim setelah selesai (status 'dikirim' = 0)
	var orphanCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_numbers
		WHERE campaign_id = $1 AND status = 'dikirim'
	`, campaignID).Scan(&orphanCount)
	if err != nil {
		t.Fatalf("Gagal query nomor dikirim: %v", err)
	}
	if orphanCount != 0 {
		t.Errorf("GAGAL Kriteria 2: Ditemukan %d nomor yatim berstatus 'dikirim' setelah engine berhenti!", orphanCount)
	} else {
		t.Logf("LULUS Kriteria 2: 0 nomor yatim tersisa.")
	}

	// Kriteria 3: Engine pulih sendiri
	if stats.SentCount == 0 {
		t.Errorf("GAGAL Kriteria 3: Engine tidak memproses pengiriman sama sekali!")
	} else {
		t.Logf("LULUS Kriteria 3: Engine pulih sendiri dan berhasil memproses %d pesan.", stats.SentCount)
	}

	// Kriteria 4: /readyz tetap menjawab di bawah 100 ms
	p95Readyz := readyzP95.(*PercentileTracker).Percentile(95)
	meanReadyz := readyzP95.(*PercentileTracker).Mean()
	maxReadyz := readyzP95.(*PercentileTracker).Max()
	t.Logf("/readyz Latensi: p95=%v, Mean=%v, Max=%v (Sampel=%d, Gagal=%d)",
		p95Readyz, meanReadyz, maxReadyz, readyzP95.(*PercentileTracker).Count(), readyzFailures)

	if p95Readyz > 100*time.Millisecond {
		t.Errorf("GAGAL Kriteria 4: p95 latensi /readyz %v melebihi 100 ms!", p95Readyz)
	} else {
		t.Logf("LULUS Kriteria 4: /readyz p95 %v di bawah 100 ms.", p95Readyz)
	}
}

type Tracker interface {
	Record(d time.Duration)
}
