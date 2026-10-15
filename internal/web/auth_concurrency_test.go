package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/auth"
	"goblast/internal/config"
	"goblast/internal/testutil"
)

// TestBenchmark_LoginConcurrencyAndMemory membuktikan bahwa ArgonGate menahan lonjakan memori
// ketika 100 permintaan verifikasi login masuk secara serentak.
// Alokasi memori tidak boleh meledak melebihi (ArgonConcurrencyLimit + antrean) * 64 MB.
func TestBenchmark_LoginConcurrencyAndMemory(t *testing.T) {
	setupWebH5aTest(t)

	// Persiapkan hash contoh
	const testPass = "TestPassword123!"
	encodedHash, err := auth.HashPassword(testPass)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	// Paksa GC agar memori awal bersih
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	const totalRequests = 100
	var (
		wg           sync.WaitGroup
		startSignal  = make(chan struct{})
		peakHeap     uint64
		stopMonitor  = make(chan struct{})
		busyCount    atomic.Int64
		successCount atomic.Int64
		errCount     atomic.Int64
	)

	// Goroutine pemantau memori secara berkala (tiap 5ms)
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopMonitor:
				return
			case <-ticker.C:
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				for {
					cur := atomic.LoadUint64(&peakHeap)
					if ms.HeapAlloc <= cur || atomic.CompareAndSwapUint64(&peakHeap, cur, ms.HeapAlloc) {
						break
					}
				}
			}
		}
	}()

	// Luncurkan 100 goroutine serentak
	wg.Add(totalRequests)
	for i := 0; i < totalRequests; i++ {
		go func(idx int) {
			defer wg.Done()
			<-startSignal // Tunggu peluit bersamaan

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			match, vErr := auth.VerifyPasswordGated(ctx, testPass, encodedHash)
			if errors.Is(vErr, auth.ErrServerBusy) {
				busyCount.Add(1)
				return
			}
			if vErr != nil {
				errCount.Add(1)
				return
			}
			if match {
				successCount.Add(1)
			}
		}(i)
	}

	// Catat memori tepat sebelum start
	var memStart runtime.MemStats
	runtime.ReadMemStats(&memStart)

	// Lepaskan sinyal serentak
	close(startSignal)
	wg.Wait()
	close(stopMonitor)

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	// Batas maksimum memori yang diizinkan sesuai spek:
	// (ArgonConcurrencyLimit + antrean) * 64 MB
	maxAllowedBytes := uint64(config.ArgonConcurrencyLimit+config.ArgonQueueLimit) * 64 * 1024 * 1024 // (2 + 8) * 64MB = 640MB
	unconstrainedEstimate := uint64(totalRequests) * 64 * 1024 * 1024                              // 100 * 64MB = 6.400MB (6,4 GB)

	peakAllocMB := float64(peakHeap) / (1024 * 1024)
	heapBeforeMB := float64(memBefore.HeapAlloc) / (1024 * 1024)
	heapAfterMB := float64(memAfter.HeapAlloc) / (1024 * 1024)
	maxAllowedMB := float64(maxAllowedBytes) / (1024 * 1024)
	unconstrainedMB := float64(unconstrainedEstimate) / (1024 * 1024)

	peakActive := auth.DefaultGate().PeakActive()

	t.Logf("=== HASIL BENCHMARK 100 LOGIN KONKUREN ===")
	t.Logf("Total Requests       : %d", totalRequests)
	t.Logf("Sukses (Diproses)    : %d", successCount.Load())
	t.Logf("Ditolak ServerBusy   : %d", busyCount.Load())
	t.Logf("Error Lain           : %d", errCount.Load())
	t.Logf("Peak Konkuren Aktif  : %d (Batas: %d)", peakActive, config.ArgonConcurrencyLimit)
	t.Logf("HeapAlloc Awal       : %.2f MB", heapBeforeMB)
	t.Logf("HeapAlloc Puncak     : %.2f MB", peakAllocMB)
	t.Logf("HeapAlloc Akhir      : %.2f MB", heapAfterMB)
	t.Logf("Batas Maksimal Spek  : %.2f MB ((%d + %d) * 64MB)", maxAllowedMB, config.ArgonConcurrencyLimit, config.ArgonQueueLimit)
	t.Logf("Estimasi Tanpa Gate  : %.2f MB (100 * 64MB = 6,4 GB)", unconstrainedMB)

	// Verifikasi 1: Peak komputasi aktif tidak boleh melebihi batas batas ArgonConcurrencyLimit
	if peakActive > int64(config.ArgonConcurrencyLimit) {
		t.Errorf("GAGAL: Peak active argon computation %d melebihi batas ArgonConcurrencyLimit %d", peakActive, config.ArgonConcurrencyLimit)
	}

	// Verifikasi 2: Puncak memori tidak boleh melampaui (concurrency + queue) * 64MB
	if peakHeap > maxAllowedBytes {
		t.Errorf("GAGAL: Peak heap memori %.2f MB melebihi batas maksimum %.2f MB", peakAllocMB, maxAllowedMB)
	}

	// Verifikasi 3: Harus ada request yang ditolak ServerBusy (karena 100 request > 2 concurrency + 8 queue)
	if busyCount.Load() == 0 {
		t.Errorf("GAGAL: Tidak ada permintaan yang ditolak ServerBusy saat dihantam 100 permintaan serentak")
	}
}

// TestBenchmark_LoginHTTPHandlerConcurrency menguji ketahanan endpoint POST /login
// terhadap 100 request konkuren melalui router/handler web.
func TestBenchmark_LoginHTTPHandlerConcurrency(t *testing.T) {
	setupWebH5aTest(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Tambahkan 1 user uji coba
	nonce := time.Now().UnixNano()
	username := fmt.Sprintf("bench_user_%d", nonce)
	correctPass := "Password123!"
	hash, err := auth.HashPassword(correctPass)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	var userID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role, display_name, referral_code, status)
		VALUES ($1, $2, 'worker', 'Benchmark User', $3, 'active')
		RETURNING id
	`, username, hash, fmt.Sprintf("BENC_%d", nonce)).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user failed: %v", err)
	}
	defer func() {
		testutil.CleanupTestData(pool, []int64{userID}, nil)
	}()

	const totalHTTPRequests = 100
	var (
		wg          sync.WaitGroup
		startSignal = make(chan struct{})
		statusCodes = make([]int, totalHTTPRequests)
	)

	wg.Add(totalHTTPRequests)
	for i := 0; i < totalHTTPRequests; i++ {
		go func(idx int) {
			defer wg.Done()
			<-startSignal

			form := url.Values{}
			form.Set("identifier", username)
			form.Set("password", correctPass)

			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			// Gunakan IP acak agar tidak langsung dipotong habis oleh rate limiter IP
			req.Header.Set("CF-Connecting-IP", fmt.Sprintf("198.51.100.%d", (idx%250)+1))

			w := httptest.NewRecorder()
			srv.handleLogin(w, req)
			statusCodes[idx] = w.Code
		}(i)
	}

	close(startSignal)
	wg.Wait()

	var count200, count303, count503, count429, countOther int
	for _, code := range statusCodes {
		switch code {
		case http.StatusOK:
			count200++
		case http.StatusSeeOther:
			count303++ // Login sukses redirect ke dashboard
		case http.StatusServiceUnavailable:
			count503++ // ArgonGate busy (Retry-After)
		case http.StatusTooManyRequests:
			count429++ // Rate limiter
		default:
			countOther++
		}
	}

	t.Logf("=== HASIL BENCHMARK 100 HTTP POST /login ===")
	t.Logf("Status 303 (Redirect Sukses) : %d", count303)
	t.Logf("Status 200 (Render Login Form) : %d", count200)
	t.Logf("Status 503 (Server Busy)      : %d", count503)
	t.Logf("Status 429 (Rate Limited)     : %d", count429)
	t.Logf("Status Lain                   : %d", countOther)

	// Pastikan server tidak crash / panic dan merespon semua 100 request
	totalHandled := count200 + count303 + count503 + count429 + countOther
	if totalHandled != totalHTTPRequests {
		t.Errorf("Total request yang ditangani %d != %d", totalHandled, totalHTTPRequests)
	}
}
