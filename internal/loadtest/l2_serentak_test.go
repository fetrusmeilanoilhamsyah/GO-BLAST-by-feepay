//go:build load

package loadtest

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa/watest"
)

// TestL2_Serentak menjalankan skenario L2: semua 50 akun dimulai pada detik yang sama.
// Kriteria lulus:
// - Semuanya mengklaim nomor dalam 5 detik
// - Tidak ada galat pool
// - Tidak ada deadlock
func TestL2_Serentak(t *testing.T) {
	RequireSafety(t)
	t.Logf("=== Menjalankan Skenario L2: Serentak (Thundering Herd 50 Akun) ===")

	pool := GetTestPool(t, 25, nil)
	ctx := context.Background()

	// 1. Setup Data
	workerID, err := CreateLoadWorker(ctx, pool, "l2")
	if err != nil {
		t.Fatalf("Gagal membuat worker: %v", err)
	}

	campaignID, batchID, err := CreateLoadCampaign(ctx, pool, workerID, "Campaign L2 Serentak")
	if err != nil {
		t.Fatalf("Gagal membuat campaign: %v", err)
	}
	defer testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})

	// Populasi 1.000 nomor fiktif
	const numberCount = 1000
	if err := PopulateFictitiousNumbers(ctx, pool, campaignID, batchID, numberCount, 1); err != nil {
		t.Fatalf("Gagal populasi nomor: %v", err)
	}

	const accountCount = 50
	accIDs, err := CreateLoadAccounts(ctx, pool, workerID, accountCount, "fast", "l2")
	if err != nil {
		t.Fatalf("Gagal membuat 50 akun: %v", err)
	}

	if err := SetupTestSettings(ctx, pool, 50, 10); err != nil {
		t.Fatalf("Gagal setup settings: %v", err)
	}

	sender := &watest.FakeSender{
		SendDelay: 10 * time.Millisecond,
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, sender, nil, nil)
	defer eng.StopAll()

	// 2. Barrier start: semua 50 goroutine siap dan dilepas bersamaan
	var readyWg sync.WaitGroup
	var releaseGate = make(chan struct{})
	var claimsCount atomic.Int64
	var claimErrors atomic.Int64

	readyWg.Add(accountCount)
	startTimes := make([]time.Time, accountCount)
	claimTimes := make([]time.Duration, accountCount)

	for i := 0; i < accountCount; i++ {
		idx := i
		accID := accIDs[i]
		go func() {
			readyWg.Done()
			<-releaseGate // Tunggu detik pelepasan yang sama

			start := time.Now()
			startTimes[idx] = start

			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				claimed, err := eng.ClaimNextNumber(ctx, accID)
				if err == nil && claimed != nil {
					claimTimes[idx] = time.Since(start)
					claimsCount.Add(1)
					return
				}
				if err != nil && err.Error() != "no rows in result set" {
					claimErrors.Add(1)
					t.Errorf("Akun %d galat non-transient: %v", accID, err)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			claimErrors.Add(1)
			t.Errorf("Akun %d gagal mengklaim dalam 5 detik", accID)
		}()
	}

	// Tunggu semua 50 goroutine siap
	readyWg.Wait()

	// Lepas gate serentak!
	gateReleaseTime := time.Now()
	close(releaseGate)

	// Beri batas waktu maksimal 5 detik
	doneCh := make(chan struct{})
	go func() {
		// Tunggu hingga semua 50 klaim selesai
		for {
			if claimsCount.Load()+claimErrors.Load() >= int64(accountCount) {
				close(doneCh)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	select {
	case <-doneCh:
		totalElapsed := time.Since(gateReleaseTime)
		t.Logf("Semua 50 akun selesai mengklaim dalam %v", totalElapsed)

		// Verifikasi Kriteria 1: Semuanya mengklaim dalam 5 detik
		var maxClaimTime time.Duration
		for i, d := range claimTimes {
			if d > maxClaimTime {
				maxClaimTime = d
			}
			if d > 5*time.Second {
				t.Errorf("GAGAL Kriteria 1: Akun %d membutuhkan %v (> 5 detik) untuk mengklaim!", accIDs[i], d)
			}
		}
		t.Logf("LULUS Kriteria 1: 50/50 akun berhasil mengklaim. Puncak latensi klaim = %v (batas 5s).", maxClaimTime)

	case <-time.After(5 * time.Second):
		t.Fatalf("GAGAL Kriteria 1: Batas waktu 5 detik terlewati! Klaim berhasil=%d, Galat=%d",
			claimsCount.Load(), claimErrors.Load())
	}

	// Verifikasi Kriteria 2 & 3: Tidak ada galat pool dan tidak ada deadlock
	errCount := claimErrors.Load()
	if errCount > 0 {
		t.Errorf("GAGAL Kriteria 2: Ditemukan %d galat klaim/pool saat pelepasan serentak!", errCount)
	} else {
		t.Logf("LULUS Kriteria 2 & 3: 0 galat pool, 0 deadlock terdeteksi.")
	}
}
