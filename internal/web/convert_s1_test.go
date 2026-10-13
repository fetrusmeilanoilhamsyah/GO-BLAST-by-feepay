package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/i18n"
	"goblast/internal/money"
	"goblast/internal/testutil"
)

func findRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root containing go.mod")
		}
		dir = parent
	}
}

func TestS1CurrencyConversionRequirements(t *testing.T) {
	root := findRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	pool := testutil.TestDB(t)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(&db.DB{Pool: pool}, "test", nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	srv.EncryptionKey = testutil.TestEncryptionKey()

	// 1. Tes matematis: konversi membulatkan ke bawah (tidak ada float)
	t.Run("konversi membulatkan ke bawah tanpa float", func(t *testing.T) {
		// idr=10000, kurs=15000:
		// (10_000 * 1_000_000) / 15_000 = 666_666 (bukan 666_667 atau float)
		micro, err := money.UsdtMicroFromIDR(10000, 15000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if micro != 666666 {
			t.Fatalf("expected exactly 666666 micro-USDT, got %d", micro)
		}
	})

	// Helper to create test worker
	createWorker := func(initialBalanceIDR int64) (int64, UserSession) {
		now := time.Now().UnixNano()
		var uid int64
		err := pool.QueryRow(context.Background(), `
			INSERT INTO users (email, username, password_hash, role, display_name, language, referral_code)
			VALUES ($1, $2, 'hash_pwd', 'worker', 'Test S1 Worker', 'id', $3)
			RETURNING id
		`, fmt.Sprintf("s1_wkr_%d@test.local", now), fmt.Sprintf("s1_wkr_%d", now), fmt.Sprintf("S1REF%d", now)).Scan(&uid)
		if err != nil {
			t.Fatalf("failed to create worker: %v", err)
		}

		if initialBalanceIDR > 0 {
			_, err = pool.Exec(context.Background(), `
				INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
				VALUES ($1, 'upah', 'IDR', $2, 'Initial Balance')
			`, uid, initialBalanceIDR)
			if err != nil {
				t.Fatalf("failed to credit initial balance: %v", err)
			}
		}

		sess := UserSession{
			ID:          uid,
			Role:        "worker",
			DisplayName: "Test S1 Worker",
			Language:    "id",
		}
		return uid, sess
	}

	setKurs := func(kurs int64, updatedAt time.Time) {
		ctx := context.Background()
		_, err := pool.Exec(ctx, `
			INSERT INTO settings (key, value)
			VALUES ('rate_idr_to_usdt', to_jsonb($1::bigint))
			ON CONFLICT (key) DO UPDATE SET value = to_jsonb($1::bigint)
		`, kurs)
		if err != nil {
			t.Fatalf("failed to set kurs: %v", err)
		}

		timeStr := updatedAt.Format(time.RFC3339)
		_, err = pool.Exec(ctx, `
			INSERT INTO settings (key, value)
			VALUES ('rate_idr_to_usdt_updated_at', to_jsonb($1::text))
			ON CONFLICT (key) DO UPDATE SET value = to_jsonb($1::text)
		`, timeStr)
		if err != nil {
			t.Fatalf("failed to set kurs updated_at: %v", err)
		}
	}

	// 2. Token yang sama dikirim dua kali: hanya satu konversi terjadi, respons kedua menampilkan hasil sebelumnya
	t.Run("token duplikat hanya dieksekusi sekali dan idempoten", func(t *testing.T) {
		workerID, sess := createWorker(50000)
		t.Cleanup(func() { testutil.CleanupTestData(pool, []int64{workerID}, nil) })

		setKurs(15000, time.Now())
		token := fmt.Sprintf("tok_idempoten_%d", time.Now().UnixNano())

		form := url.Values{
			"amount_idr":    {"15000"},
			"request_token": {token},
		}

		// Kiriman pertama
		req1 := httptest.NewRequest("POST", "/worker/convert", strings.NewReader(form.Encode()))
		req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req1 = req1.WithContext(context.WithValue(req1.Context(), userContextKey, sess))
		rr1 := httptest.NewRecorder()
		srv.HandleWorkerConvertPOST(rr1, req1)

		if rr1.Code != http.StatusSeeOther || !strings.Contains(rr1.Header().Get("Location"), "success=") {
			t.Fatalf("first request failed: status %d, loc: %s", rr1.Code, rr1.Header().Get("Location"))
		}

		// Kiriman kedua dengan token yang sama
		req2 := httptest.NewRequest("POST", "/worker/convert", strings.NewReader(form.Encode()))
		req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req2 = req2.WithContext(context.WithValue(req2.Context(), userContextKey, sess))
		rr2 := httptest.NewRecorder()
		srv.HandleWorkerConvertPOST(rr2, req2)

		if rr2.Code != http.StatusSeeOther || !strings.Contains(rr2.Header().Get("Location"), "success=") {
			t.Fatalf("second duplicate request failed: status %d, loc: %s", rr2.Code, rr2.Header().Get("Location"))
		}

		// Pastikan di tabel currency_conversions hanya ada 1 baris untuk token ini
		var convCount int
		err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM currency_conversions WHERE user_id = $1 AND request_token = $2", workerID, token).Scan(&convCount)
		if err != nil || convCount != 1 {
			t.Fatalf("expected exactly 1 currency_conversions row, got count %d, err: %v", convCount, err)
		}

		// Pastikan saldo IDR hanya berkurang sekali (50000 - 15000 = 35000)
		var balIDR int64
		_ = pool.QueryRow(context.Background(), "SELECT user_balance($1, 'IDR')", workerID).Scan(&balIDR)
		if balIDR != 35000 {
			t.Fatalf("expected balance IDR 35000, got %d", balIDR)
		}

		// Pastikan saldo USDT hanya bertambah sekali (1_000_000 micro-USDT)
		var balUSDT int64
		_ = pool.QueryRow(context.Background(), "SELECT user_balance($1, 'USDT')", workerID).Scan(&balUSDT)
		if balUSDT != 1000000 {
			t.Fatalf("expected balance USDT 1000000, got %d", balUSDT)
		}
	})

	// 3. Dua konversi bersamaan dengan saldo hanya cukup untuk satu: satu berhasil, satu ditolak
	t.Run("konversi konkuren saat saldo hanya cukup untuk satu", func(t *testing.T) {
		workerID, sess := createWorker(20000) // Saldo hanya 20.000, kedua request minta konversi 15.000
		t.Cleanup(func() { testutil.CleanupTestData(pool, []int64{workerID}, nil) })

		setKurs(15000, time.Now())

		var wg sync.WaitGroup
		var successCount int32
		var failCount int32

		wg.Add(2)
		for i := 1; i <= 2; i++ {
			go func(idx int) {
				defer wg.Done()
				tok := fmt.Sprintf("tok_concurrent_%d_%d", idx, time.Now().UnixNano())
				form := url.Values{
					"amount_idr":    {"15000"},
					"request_token": {tok},
				}
				req := httptest.NewRequest("POST", "/worker/convert", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req = req.WithContext(context.WithValue(req.Context(), userContextKey, sess))
				rr := httptest.NewRecorder()

				srv.HandleWorkerConvertPOST(rr, req)
				loc := rr.Header().Get("Location")
				if rr.Code == http.StatusSeeOther && strings.Contains(loc, "success=") {
					atomic.AddInt32(&successCount, 1)
				} else if rr.Code == http.StatusSeeOther && strings.Contains(loc, "error=") {
					atomic.AddInt32(&failCount, 1)
				}
			}(i)
		}
		wg.Wait()

		if successCount != 1 || failCount != 1 {
			t.Fatalf("expected exactly 1 success and 1 fail, got success: %d, fail: %d", successCount, failCount)
		}

		// Sisa saldo IDR harus 5000
		var balIDR int64
		_ = pool.QueryRow(context.Background(), "SELECT user_balance($1, 'IDR')", workerID).Scan(&balIDR)
		if balIDR != 5000 {
			t.Fatalf("expected balance IDR 5000, got %d", balIDR)
		}
	})

	// 4. Kurs kosong ditolak
	t.Run("kurs kosong ditolak", func(t *testing.T) {
		workerID, sess := createWorker(50000)
		t.Cleanup(func() { testutil.CleanupTestData(pool, []int64{workerID}, nil) })

		// Hapus setting kurs
		_, _ = pool.Exec(context.Background(), "DELETE FROM settings WHERE key IN ('rate_idr_to_usdt', 'rate_idr_to_usdt_updated_at')")

		form := url.Values{
			"amount_idr":    {"15000"},
			"request_token": {"tok_no_kurs"},
		}
		req := httptest.NewRequest("POST", "/worker/convert", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, sess))
		rr := httptest.NewRecorder()

		srv.HandleWorkerConvertPOST(rr, req)
		if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
			t.Fatalf("expected redirect error on missing kurs, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
		}
	})

	// 5. Kurs lebih tua dari KursMaxAgeHours ditolak
	t.Run("kurs lebih tua dari KursMaxAgeHours ditolak", func(t *testing.T) {
		workerID, sess := createWorker(50000)
		t.Cleanup(func() { testutil.CleanupTestData(pool, []int64{workerID}, nil) })

		// Set kurs diperbarui 25 jam yang lalu (melebihi batas 24 jam)
		staleTime := time.Now().Add(-time.Duration(config.KursMaxAgeHours+1) * time.Hour)
		setKurs(15000, staleTime)

		form := url.Values{
			"amount_idr":    {"15000"},
			"request_token": {"tok_stale_kurs"},
		}
		req := httptest.NewRequest("POST", "/worker/convert", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, sess))
		rr := httptest.NewRecorder()

		srv.HandleWorkerConvertPOST(rr, req)
		if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
			t.Fatalf("expected redirect error on stale kurs (>24h), got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
		}
	})
}
