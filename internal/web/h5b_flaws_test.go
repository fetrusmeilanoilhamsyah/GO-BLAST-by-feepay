package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"goblast/internal/auth"
	"goblast/internal/testutil"
)

// TestFlaw_Logout_AcceptsGET membuktikan bahwa /logout saat ini menerima metode GET
// sehingga pengguna bisa dikeluarkan paksa via serangan seperti <img src="/logout">.
func TestFlaw_Logout_AcceptsGET(t *testing.T) {
	setupWebH5aTest(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Buat user worker uji
	nonce := time.Now().UnixNano()
	username := fmt.Sprintf("logout_user_%d", nonce)
	var userID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role, display_name, referral_code, status)
		VALUES ($1, 'hash', 'worker', 'Logout Worker', $2, 'active')
		RETURNING id
	`, username, fmt.Sprintf("REF_%d", nonce)).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user failed: %v", err)
	}
	defer func() {
		testutil.CleanupTestData(pool, []int64{userID}, nil)
	}()

	rawToken, tokenHash, _ := auth.GenerateSessionToken()
	_, err = pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, ip, user_agent, expires_at)
		VALUES ($1, $2, '127.0.0.1', 'test-ua', $3)
	`, userID, tokenHash, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("insert session failed: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Kirim GET /logout
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: srv.sessionCookieName(), Value: rawToken})
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	// Jika rute /logout menerima GET, statusnya adalah 303 See Other (berhasil logout).
	// Seharusnya GET /logout ditolak (misalnya 405 Method Not Allowed).
	if w.Code == http.StatusSeeOther {
		t.Errorf("BUG TERBUKTI: GET /logout diizinkan dan mengeluarkan pengguna! Seharusnya hanya POST.")
	} else {
		t.Logf("Sukses: GET /logout ditolak dengan status %d", w.Code)
	}
}

// TestFlaw_K6_EndpointRateLimiting_Ticket membuktikan bahwa saat ini belum ada rate limiting
// pada endpoint POST /worker/ticket sehingga bisa dispam lebih dari 3 kali per 10 menit.
func TestFlaw_K6_EndpointRateLimiting_Ticket(t *testing.T) {
	setupWebH5aTest(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	nonce := time.Now().UnixNano()
	username := fmt.Sprintf("tkt_user_%d", nonce)
	var userID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role, display_name, referral_code, status)
		VALUES ($1, 'hash', 'worker', 'Ticket Worker', $2, 'active')
		RETURNING id
	`, username, fmt.Sprintf("REF_%d", nonce)).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user failed: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM support_tickets WHERE user_id = $1", userID)
		testutil.CleanupTestData(pool, []int64{userID}, nil)
	}()

	rawToken, tokenHash, _ := auth.GenerateSessionToken()
	_, err = pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, ip, user_agent, expires_at)
		VALUES ($1, $2, '127.0.0.1', 'test-ua', $3)
	`, userID, tokenHash, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("insert session failed: %v", err)
	}

	csrfToken := auth.SignCSRFToken(rawToken, srv.SessionSecret)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Kirim 4 tiket berturut-turut (batas adalah 3 per 10 menit)
	var got429 bool
	for i := 1; i <= 4; i++ {
		form := url.Values{}
		form.Set("csrf_token", csrfToken)
		form.Set("category", "kendala_akun")
		form.Set("message", fmt.Sprintf("Pesan tiket bantuan ke-%d untuk pengujian batas", i))

		req := httptest.NewRequest(http.MethodPost, "/worker/ticket", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: srv.sessionCookieName(), Value: rawToken})
		req.AddCookie(&http.Cookie{Name: srv.csrfCookieName(), Value: csrfToken})

		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code == http.StatusTooManyRequests || strings.Contains(w.Header().Get("Location"), "err=Terlalu+banyak") || strings.Contains(w.Header().Get("Location"), "error=Terlalu+banyak") {
			got429 = true
			t.Logf("Permintaan tiket ke-%d berhasil ditolak rate limit", i)
			break
		}
	}

	if !got429 {
		t.Errorf("BUG TERBUKTI: POST /worker/ticket dapat dikirim 4 kali berturut-turut tanpa pembatasan laju!")
	}
}

// TestFlaw_K9_InternalErrorLeakage membuktikan bahwa kegagalan kueri internal
// membocorkan pesan error mentah (seperti sintaks DB / constraint) ke respons.
func TestFlaw_K9_InternalErrorLeakage(t *testing.T) {
	setupWebH5aTest(t)
	database := testutil.TestDBInstance(t)

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Kirim form registrasi yang memicu error duplikasi atau error database
	// Saat ini kode auth_handlers.go:453 berisi: s.renderRegister(w, r, "Gagal mendaftar: "+insertErr.Error())
	// Kita simulasikan dengan memanggil handler langsung pada query yang gagal
	form := url.Values{}
	form.Set("username", "test_leak_user")
	form.Set("password", "Password123!")
	form.Set("confirm_password", "Password123!")
	form.Set("agree_terms", "true")
	form.Set("form_timing", auth.SignFormTimingToken(time.Now().Add(-5*time.Second), srv.SessionSecret))

	// Tutup pool DB sementara untuk mensimulasikan kegagalan database total
	srv.DB.Pool.Close()

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	srv.handleRegister(w, req)

	respBody := w.Body.String()
	// Jika bocor pesan internal seperti "closed pool" atau "connection refused"
	if strings.Contains(strings.ToLower(respBody), "closed pool") || strings.Contains(respBody, "Gagal mendaftar: ") {
		t.Errorf("BUG TERBUKTI: Error internal database bocor ke halaman web! Respon: %q", respBody)
	} else {
		t.Logf("Sukses: Error internal tidak bocor ke respons pengguna")
	}
}
