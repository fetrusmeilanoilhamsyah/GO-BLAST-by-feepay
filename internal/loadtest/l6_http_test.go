//go:build load

package loadtest

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/auth"
	"goblast/internal/db"
	"goblast/internal/safe"
	"goblast/internal/testutil"
	"goblast/internal/web"
)

// TestL6_HTTPSimulation menjalankan skenario L6: 1.000 sesi pekerja polling stats/notifs sambil 200 brute force login/s dari 50 IP.
// Kriteria lulus:
// - /healthz dan /readyz tetap di bawah 100 ms
// - Memori puncak proses naik tidak lebih dari 300 MB dari kondisi awal
// - Permintaan berlebih mendapat 429 atau 503, bukan timeout
// - Tidak ada panic
func TestL6_HTTPSimulation(t *testing.T) {
	RequireSafety(t)
	ChangeToProjectRoot(t)

	duration := GetDurationEnv("LOADTEST_L6_DURATION", 2*time.Minute)
	t.Logf("=== Menjalankan Skenario L6: HTTP Load & Memory Under Attack (Durasi: %v) ===", duration)

	pool := GetTestPool(t, 25, nil)
	ctx := context.Background()

	// 1. Setup Data: Buat 10 akun pekerja nyata di database untuk membagi 1.000 sesi token
	const totalWorkers = 10
	workerIDs := make([]int64, totalWorkers)
	sessionTokens := make([]string, 1000)

	for i := 0; i < totalWorkers; i++ {
		wID, err := CreateLoadWorker(ctx, pool, fmt.Sprintf("l6_%d", i+1))
		if err != nil {
			t.Fatalf("Gagal membuat worker: %v", err)
		}
		workerIDs[i] = wID
	}
	defer testutil.CleanupTestData(pool, workerIDs, nil)

	// Buat 1.000 token sesi di tabel web_sessions
	t.Logf("Membuat 1.000 sesi pekerja di web_sessions...")
	for i := 0; i < 1000; i++ {
		wID := workerIDs[i%totalWorkers]
		token := fmt.Sprintf("tok_l6_%d_%d", i, time.Now().UnixNano())
		sessionTokens[i] = token
		tokenHash := auth.HashSessionToken(token)
		_, err := pool.Exec(ctx,
			`INSERT INTO web_sessions (user_id, token_hash, ip, user_agent, expires_at)
			 VALUES ($1, $2, '127.0.0.1', 'loadtest', now() + interval '7 days')`,
			wID, tokenHash,
		)
		if err != nil {
			t.Fatalf("Gagal insert sesi %d: %v", i, err)
		}
	}

	// 2. Setup Server Web
	database := &db.DB{Pool: pool}
	srv, err := web.NewServer(database, "test", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server web: %v", err)
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// 3. Catat Memori Baseline
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	baseAllocMB := float64(memBefore.HeapAlloc) / 1024 / 1024
	t.Logf("Memori Baseline (HeapAlloc): %.2f MB", baseAllocMB)

	var peakAllocMB atomic.Uint64
	peakAllocMB.Store(uint64(memBefore.HeapAlloc))

	// 4. Pelacak Latensi /healthz dan /readyz
	healthzTracker := &PercentileTracker{}
	readyzTracker := &PercentileTracker{}

	// Status respon login brute force
	var resp200 atomic.Int64
	var resp429 atomic.Int64
	var resp503 atomic.Int64
	var respOther atomic.Int64
	var timeouts atomic.Int64

	stopCh := make(chan struct{})

	// Monitor Memori Puncak
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				cur := m.HeapAlloc
				for {
					old := peakAllocMB.Load()
					if cur <= old || peakAllocMB.CompareAndSwap(old, cur) {
						break
					}
				}
			case <-stopCh:
				return
			}
		}
	}()

	// Monitor /healthz dan /readyz
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Healthz
				hStart := time.Now()
				hReq := httptest.NewRequest("GET", "/healthz", nil)
				hRec := httptest.NewRecorder()
				mux.ServeHTTP(hRec, hReq)
				healthzTracker.Record(time.Since(hStart))

				// Readyz
				rStart := time.Now()
				rReq := httptest.NewRequest("GET", "/readyz", nil)
				rRec := httptest.NewRecorder()
				mux.ServeHTTP(rRec, rReq)
				readyzTracker.Record(time.Since(rStart))

			case <-stopCh:
				return
			}
		}
	}()

	// 5. Simulasi 1.000 Sesi Pekerja (Polling /stats dan /notifications)
	// Kita distribusikan ke dalam 50 goroutine pemanggil yang mewakili 1.000 sesi aktif
	const workerRoutines = 50
	for r := 0; r < workerRoutines; r++ {
		rIdx := r
		go func() {
			sessionsPerRoutine := 1000 / workerRoutines
			subTokens := sessionTokens[rIdx*sessionsPerRoutine : (rIdx+1)*sessionsPerRoutine]

			for {
				select {
				case <-stopCh:
					return
				default:
				}

				// Pilih sesi acak
				tok := subTokens[rand.Intn(len(subTokens))]

				// Polling /worker/api/stats
				reqStats := httptest.NewRequest("GET", "/worker/api/stats", nil)
				reqStats.AddCookie(&http.Cookie{Name: "gb_session", Value: tok})
				recStats := httptest.NewRecorder()
				mux.ServeHTTP(recStats, reqStats)

				// Polling /worker/api/notifications
				reqNotifs := httptest.NewRequest("GET", "/worker/api/notifications", nil)
				reqNotifs.AddCookie(&http.Cookie{Name: "gb_session", Value: tok})
				recNotifs := httptest.NewRecorder()
				mux.ServeHTTP(recNotifs, reqNotifs)

				if recStats.Code == http.StatusTooManyRequests || recNotifs.Code == http.StatusTooManyRequests {
					resp429.Add(1)
				} else if recStats.Code == http.StatusOK && recNotifs.Code == http.StatusOK {
					resp200.Add(1)
				}

				// Jeda antara 50ms - 200ms per goroutine (total throughput polling agregat stabil)
				time.Sleep(time.Duration(50+rand.Intn(150)) * time.Millisecond)
			}
		}()
	}

	// 6. Simulasi Serangan Login Brute Force: 200 req/s dari 50 IP fiktif
	const attackerIPs = 50
	go func() {
		// Interval 5ms untuk mencapai rate ~200 request/detik
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ipIdx := rand.Intn(attackerIPs) + 1
				clientIP := fmt.Sprintf("198.51.100.%d", ipIdx)
				csrfTok, _ := auth.GenerateCSRFToken()

				formData := url.Values{
					"csrf_token": {csrfTok},
					"identifier": {fmt.Sprintf("target_user_%d", rand.Intn(100))},
					"password":   {"wrong_password_12345"},
					"website_hp": {""},
				}

				req := httptest.NewRequest("POST", "/login", strings.NewReader(formData.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("CF-Connecting-IP", clientIP)
				req.Header.Set("X-Forwarded-For", clientIP)
				req.RemoteAddr = "127.0.0.1:1234"
				req.AddCookie(&http.Cookie{Name: "gb_csrf", Value: csrfTok})

				rec := httptest.NewRecorder()

				doneReq := make(chan struct{})
				go func() {
					mux.ServeHTTP(rec, req)
					close(doneReq)
				}()

				select {
				case <-doneReq:
					switch rec.Code {
					case http.StatusOK:
						resp200.Add(1)
					case http.StatusTooManyRequests:
						resp429.Add(1)
					case http.StatusServiceUnavailable:
						resp503.Add(1)
					default:
						respOther.Add(1)
					}
				case <-time.After(3 * time.Second):
					timeouts.Add(1)
				}

			case <-stopCh:
				return
			}
		}
	}()

	// Jalankan uji beban selama durasi
	time.Sleep(duration)
	close(stopCh)

	// Beri jeda kecil agar goroutine latar tenang
	time.Sleep(500 * time.Millisecond)

	// 7. Evaluasi Hasil Pengujian
	p95Healthz := healthzTracker.Percentile(95)
	p95Readyz := readyzTracker.Percentile(95)
	peakAlloc := float64(peakAllocMB.Load()) / 1024 / 1024
	memIncreaseMB := peakAlloc - baseAllocMB

	t.Logf("Hasil L6:")
	t.Logf("- Memori Baseline: %.2f MB, Puncak: %.2f MB (Kenaikan: %.2f MB)", baseAllocMB, peakAlloc, memIncreaseMB)
	t.Logf("- /healthz p95: %v (Sampel: %d)", p95Healthz, healthzTracker.Count())
	t.Logf("- /readyz p95: %v (Sampel: %d)", p95Readyz, readyzTracker.Count())
	t.Logf("- Respon Login: 429=%d, 503=%d, 200=%d, Lainnya=%d, Timeout=%d",
		resp429.Load(), resp503.Load(), resp200.Load(), respOther.Load(), timeouts.Load())
	t.Logf("- Panic Count: %d", safe.PanicCount())

	// Kriteria 1: /healthz dan /readyz tetap di bawah 100 ms
	if p95Healthz > 100*time.Millisecond {
		t.Errorf("GAGAL Kriteria 1: p95 /healthz %v melebihi 100 ms!", p95Healthz)
	} else {
		t.Logf("LULUS Kriteria 1a: /healthz p95 %v di bawah 100 ms.", p95Healthz)
	}
	if p95Readyz > 100*time.Millisecond {
		t.Errorf("GAGAL Kriteria 1: p95 /readyz %v melebihi 100 ms!", p95Readyz)
	} else {
		t.Logf("LULUS Kriteria 1b: /readyz p95 %v di bawah 100 ms.", p95Readyz)
	}

	// Kriteria 2: Memori puncak proses naik tidak lebih dari 300 MB dari kondisi awal
	if memIncreaseMB > 300.0 {
		t.Errorf("GAGAL Kriteria 2: Kenaikan memori %.2f MB melebihi plafon 300 MB!", memIncreaseMB)
	} else {
		t.Logf("LULUS Kriteria 2: Kenaikan memori %.2f MB <= 300 MB.", memIncreaseMB)
	}

	// Kriteria 3: Permintaan berlebih mendapat 429 atau 503, bukan timeout
	if timeouts.Load() > 0 {
		t.Errorf("GAGAL Kriteria 3: Ditemukan %d request yang mengalami timeout!", timeouts.Load())
	} else {
		t.Logf("LULUS Kriteria 3a: 0 timeout tercatat.")
	}
	if resp429.Load()+resp503.Load() == 0 {
		t.Errorf("GAGAL Kriteria 3b: Tidak ada penolakan 429/503 yang tercatat!")
	} else {
		t.Logf("LULUS Kriteria 3b: Permintaan berlebih tertolak dengan aman (429: %d, 503: %d).",
			resp429.Load(), resp503.Load())
	}

	// Kriteria 4: Tidak ada panic
	if safe.PanicCount() != 0 {
		t.Errorf("GAGAL Kriteria 4: Ditemukan panic pada server selama uji beban!")
	} else {
		t.Logf("LULUS Kriteria 4: 0 panic tercatat.")
	}
}
