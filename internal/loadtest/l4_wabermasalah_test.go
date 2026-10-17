//go:build load

package loadtest

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa/watest"

	"go.mau.fi/whatsmeow"
)

// TestL4_WABermasalah menjalankan skenario L4: WhatsApp outage (PastiBelumTerkirim) selama 60 detik lalu normal.
// Kriteria lulus:
// - Nol nomor gagal akibat gangguan (attempts di-rollback, nomor kembali 'tersedia')
// - Akun backoff lalu pulih
func TestL4_WABermasalah(t *testing.T) {
	RequireSafety(t)

	outageDuration := GetDurationEnv("LOADTEST_L4_DURATION", 60*time.Second)
	t.Logf("=== Menjalankan Skenario L4: WhatsApp Bermasalah (Outage: %v) ===", outageDuration)

	pool := GetTestPool(t, 25, nil)
	ctx := context.Background()

	// 1. Setup Data
	workerID, err := CreateLoadWorker(ctx, pool, "l4")
	if err != nil {
		t.Fatalf("Gagal membuat worker: %v", err)
	}

	campaignID, batchID, err := CreateLoadCampaign(ctx, pool, workerID, "Campaign L4 WA Bermasalah")
	if err != nil {
		t.Fatalf("Gagal membuat campaign: %v", err)
	}
	defer testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})

	const numberCount = 1000
	if err := PopulateFictitiousNumbers(ctx, pool, campaignID, batchID, numberCount, 1); err != nil {
		t.Fatalf("Gagal populasi nomor: %v", err)
	}

	const accountCount = 10
	accIDs, err := CreateLoadAccounts(ctx, pool, workerID, accountCount, "fast", "l4")
	if err != nil {
		t.Fatalf("Gagal membuat akun: %v", err)
	}

	if err := SetupTestSettings(ctx, pool, 50, 10); err != nil {
		t.Fatalf("Gagal setup settings: %v", err)
	}

	// 2. Setup FakeSender dengan injeksi PastiBelumTerkirim selama outageDuration
	var inOutage atomic.Bool
	inOutage.Store(true)

	var outageRejections atomic.Int64
	var recoveredSends atomic.Int64

	sender := &watest.FakeSender{
		ErrFunc: func(accountID int64) error {
			if inOutage.Load() {
				outageRejections.Add(1)
				// Simulasi error whatsmeow terputus / client nil (ClassPastiBelumTerkirim)
				return whatsmeow.ErrNotConnected
			}
			recoveredSends.Add(1)
			return nil
		},
		SendDelay: 5 * time.Millisecond,
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, sender, nil, nil)

	// 3. Mulai 10 akun selama outage
	t.Logf("Memulai akun saat WhatsApp sedang mengalami gangguan (ErrNotConnected)...")
	for _, id := range accIDs {
		eng.StartAccount(id, workerID)
	}

	// Tunggu selama durasi outage
	t.Logf("Menunggu fase gangguan selama %v...", outageDuration)
	time.Sleep(outageDuration)

	rejections := outageRejections.Load()
	t.Logf("Fase gangguan selesai. Total penolakan PastiBelumTerkirim: %d", rejections)
	if rejections == 0 {
		t.Errorf("Peringatan: Tidak ada penolakan yang tercatat selama fase gangguan!")
	}

	// 4. Pulihkan WhatsApp (Outage berakhir)
	t.Logf("Memulihkan koneksi WhatsApp (inOutage = false)...")
	inOutage.Store(false)

	// Beri waktu 10-15 detik agar akun bangun dari backoff dan mengirim pesan sukses
	t.Logf("Menunggu fase pemulihan akun...")
	time.Sleep(15 * time.Second)

	// Hentikan engine
	eng.StopAll()

	// 5. Evaluasi Kriteria Lulus
	stats := eng.Stats()
	t.Logf("Hasil L4: Terkirim=%d, Klaim=%d, Gagal=%d, BackoffCount=%d",
		stats.SentCount, stats.ClaimedCount, stats.GagalCount, stats.BackoffCount)

	// Kriteria 1: Nol nomor gagal akibat gangguan
	var gagalCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_numbers
		WHERE campaign_id = $1 AND status = 'gagal'
	`, campaignID).Scan(&gagalCount)
	if err != nil {
		t.Fatalf("Gagal query nomor gagal: %v", err)
	}

	if gagalCount != 0 {
		t.Errorf("GAGAL Kriteria 1: Ditemukan %d nomor gagal permanen akibat gangguan WhatsApp sementara!", gagalCount)
	} else {
		t.Logf("LULUS Kriteria 1: 0 nomor gagal permanen (seluruh kegagalan sementara berhasil di-rollback ke 'tersedia').")
	}

	// Kriteria 2: Akun backoff lalu pulih
	if stats.BackoffCount == 0 {
		t.Errorf("GAGAL Kriteria 2: Engine tidak mencatat backoff selama gangguan!")
	} else if stats.SentCount == 0 {
		t.Errorf("GAGAL Kriteria 2: Engine tidak berhasil memulihkan pengiriman setelah gangguan berakhir!")
	} else {
		t.Logf("LULUS Kriteria 2: Akun berhasil backoff (%d kali) lalu pulih mengirim %d pesan setelah koneksi normal.",
			stats.BackoffCount, stats.SentCount)
	}
}
