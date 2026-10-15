package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"goblast/internal/auth"
	"goblast/internal/db"
	"goblast/internal/testutil"
)

type statsMockWorkController struct {
	runningCount int
}

func (m *statsMockWorkController) StartAccount(accountID, workerID int64) {}
func (m *statsMockWorkController) StopAccount(accountID int64)            {}
func (m *statsMockWorkController) IsRunning(accountID int64) bool         { return false }
func (m *statsMockWorkController) RunningCount() int                      { return m.runningCount }

// TestWorkerAPIStats_CacheAndRole menguji spesifikasi Tahap S2:
// - Endpoint GET /worker/api/stats (hanya pekerja)
// - Nilai: database dapat dikirim, akun bekerja global (Engine.RunningCount()), pendapatan hari ini Asia/Jakarta
// - Cache memori 2 detik untuk angka global
func TestWorkerAPIStats_CacheAndRole(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	pool := testutil.TestDB(t)
	ctx := context.Background()

	// 1. Setup user worker
	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Stats S2', $2)
		 RETURNING id`,
		fmt.Sprintf("worker_s2_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REFS2_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	// Buat token sesi login
	token, tokenHash, err := auth.GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate token gagal: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		 VALUES ($1, $2, now() + interval '1 day', '127.0.0.1', 'test-agent')`,
		workerID, tokenHash,
	)
	if err != nil {
		t.Fatalf("setup web session gagal: %v", err)
	}

	// 2. Setup campaign berjalan dengan 5 nomor tersedia
	var cID, bID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, started_at)
		 VALUES ('Camp Stats S2', 'berjalan', 'kartu_link', 'Halo', '{}', $1, now())
		 RETURNING id`,
		workerID,
	).Scan(&cID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'b.csv', $2, 5, 5) RETURNING id`,
		cID, workerID,
	).Scan(&bID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		SELECT $1, $2, '628994' || LPAD(s::text, 6, '0'), '62', 'tersedia', 0
		FROM generate_series(1, 5) AS s
	`, cID, bID)
	if err != nil {
		t.Fatalf("setup numbers gagal: %v", err)
	}

	// 3. Tambahkan pendapatan hari ini (Asia/Jakarta)
	_, err = pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, amount, currency, rate_snapshot)
		VALUES ($1, 'upah', 25000, 'IDR', 250)
	`, workerID)
	if err != nil {
		t.Fatalf("setup ledger gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{cID})
	})

	mockCtrl := &statsMockWorkController{runningCount: 7}
	srv, err := NewServer(&db.DB{Pool: pool}, "test", nil, mockCtrl)
	if err != nil {
		t.Fatalf("NewServer gagal: %v", err)
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// A. Uji akses tanpa login (harus ditolak / redirect)
	{
		req := httptest.NewRequest("GET", "/worker/api/stats", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusSeeOther {
			t.Fatalf("diharapkan 401 atau redirect 303 untuk unauthenticated, didapat: %d", rec.Code)
		}
	}

	// B. Uji akses dengan pekerja terautentikasi
	var firstResp struct {
		DatabaseTersedia  int    `json:"database_tersedia"`
		AkunBekerjaGlobal int    `json:"akun_bekerja_global"`
		PendapatanHariIni int64  `json:"pendapatan_hari_ini"`
		PendapatanFormatted string `json:"pendapatan_hari_ini_formatted"`
	}
	{
		req := httptest.NewRequest("GET", "/worker/api/stats", nil)
		req.AddCookie(&http.Cookie{Name: "gb_session", Value: token})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("diharapkan status 200, didapat: %d body: %s", rec.Code, rec.Body.String())
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &firstResp); err != nil {
			t.Fatalf("gagal parse response JSON: %v", err)
		}

		if firstResp.AkunBekerjaGlobal != 7 {
			t.Fatalf("diharapkan akun_bekerja_global = 7, didapat: %d", firstResp.AkunBekerjaGlobal)
		}
		if firstResp.PendapatanHariIni != 25000 {
			t.Fatalf("diharapkan pendapatan_hari_ini = 25000, didapat: %d", firstResp.PendapatanHariIni)
		}
	}

	// C. Uji cache memori 2 detik:
	// Tambah 3 nomor lagi di database. Karena cache 2 detik aktif, request langsung berikutnya
	// harus tetap mengembalikan angka database_tersedia yang sama dari cache memori.
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		SELECT $1, $2, '628995' || LPAD(s::text, 6, '0'), '62', 'tersedia', 0
		FROM generate_series(1, 3) AS s
	`, cID, bID)
	if err != nil {
		t.Fatalf("insert nomor tambahan gagal: %v", err)
	}

	// Request segera (< 2 detik): harus CACHE HIT (angka database_tersedia sama dengan firstResp)
	{
		req := httptest.NewRequest("GET", "/worker/api/stats", nil)
		req.AddCookie(&http.Cookie{Name: "gb_session", Value: token})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		var secondResp struct {
			DatabaseTersedia  int `json:"database_tersedia"`
			AkunBekerjaGlobal int `json:"akun_bekerja_global"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &secondResp)

		if secondResp.DatabaseTersedia != firstResp.DatabaseTersedia {
			t.Fatalf("CACHE TIDAK BEKERJA: diharapkan tetap %d dari cache, didapat: %d",
				firstResp.DatabaseTersedia, secondResp.DatabaseTersedia)
		}
	}

	// Tunggu cache kedaluwarsa (> 2 detik)
	time.Sleep(2500 * time.Millisecond)

	// Request setelah > 2 detik: harus CACHE MISS (angka database_tersedia terbarui +3)
	{
		req := httptest.NewRequest("GET", "/worker/api/stats", nil)
		req.AddCookie(&http.Cookie{Name: "gb_session", Value: token})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		var thirdResp struct {
			DatabaseTersedia  int `json:"database_tersedia"`
			AkunBekerjaGlobal int `json:"akun_bekerja_global"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &thirdResp)

		if thirdResp.DatabaseTersedia != firstResp.DatabaseTersedia+3 {
			t.Fatalf("diharapkan angka terbarui setelah cache expired %d, didapat: %d",
				firstResp.DatabaseTersedia+3, thirdResp.DatabaseTersedia)
		}
	}
}
