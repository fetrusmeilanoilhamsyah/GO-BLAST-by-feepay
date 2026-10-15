package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"goblast/internal/auth"
	"goblast/internal/testutil"
)

// setupWebH5aTest mengubah direktori kerja ke root proyek agar template dapat dimuat.
func setupWebH5aTest(t *testing.T) {
	t.Helper()
	root := testutil.RootDir(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
}

// TestFlaw_K3_FrozenAccountLeakage membuktikan bahwa akun dibekukan membocorkan
// status pembekuannya meskipun penyerang memasukkan kata sandi yang salah.
func TestFlaw_K3_FrozenAccountLeakage(t *testing.T) {
	setupWebH5aTest(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	nonce := time.Now().UnixNano()
	username := fmt.Sprintf("frz_%d", nonce)
	correctPass := "Password123!"
	wrongPass := "WrongPassword999!"

	hash, err := auth.HashPassword(correctPass)
	if err != nil {
		t.Fatalf("hash failed: %v", err)
	}

	var userID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role, display_name, referral_code, status, freeze_reason)
		VALUES ($1, $2, 'worker', 'Frozen Worker', $3, 'frozen', 'Melanggar aturan')
		RETURNING id
	`, username, hash, fmt.Sprintf("REF_%d", nonce)).Scan(&userID)
	if err != nil {
		t.Fatalf("insert frozen user failed: %v", err)
	}
	defer func() {
		testutil.CleanupTestData(pool, []int64{userID}, nil)
	}()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Coba login dengan username akun beku TAPI kata sandi SALAH
	form := url.Values{}
	form.Set("identifier", username)
	form.Set("password", wrongPass)

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	srv.handleLogin(w, req)

	respBody := w.Body.String()
	// Seharusnya responnya IDENTIK dengan akun salah kata sandi biasa ("Username/Email atau kata sandi salah").
	// Tetapi di kode saat ini, ia menampilkan "Akun Anda sedang dibekukan oleh administrator" sebelum cek password!
	if strings.Contains(respBody, "dibekukan") {
		t.Errorf("BUG TERBUKTI: status pembekuan akun bocor meskipun kata sandi salah! Respon mengandung: %q", respBody)
	} else {
		t.Logf("Sukses: status pembekuan tidak bocor saat password salah")
	}
}

// TestFlaw_K4_AuditLogPasswordLeakage membuktikan bahwa kegagalan login mencatat
// identifier mentah apa pun ke audit_logs.entity_id (bocor jika user salah ketik password di field identifier).
func TestFlaw_K4_AuditLogPasswordLeakage(t *testing.T) {
	setupWebH5aTest(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Penyerang atau user salah mengetikkan password sensitif ke kolom identifier (berisi spasi atau karakter khusus)
	leakPass := "MySecretPass 123!@#"

	form := url.Values{}
	form.Set("identifier", leakPass)
	form.Set("password", "dummy_wrong_pass")

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	srv.handleLogin(w, req)

	var loggedEntityID string
	err = pool.QueryRow(ctx, `
		SELECT entity_id FROM audit_logs 
		WHERE action = 'login_failed' 
		ORDER BY id DESC LIMIT 1
	`).Scan(&loggedEntityID)
	if err != nil {
		t.Fatalf("baca audit log gagal: %v", err)
	}

	if loggedEntityID == leakPass {
		t.Errorf("BUG TERBUKTI: audit_logs.entity_id mencatat teks kata sandi mentah (%q) alih-alih '[tidak valid]'!", loggedEntityID)
	}
}

// TestFlaw_K7_ClientIPValidation membuktikan fungsi getIP belum mendukung
// CF-Connecting-IP dari proxy tepercaya dan tidak memvalidasi IP sampah.
func TestFlaw_K7_ClientIPValidation(t *testing.T) {
	// 1. Uji header CF-Connecting-IP dari RemoteAddr 127.0.0.1
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "127.0.0.1:54321"
	req1.Header.Set("CF-Connecting-IP", "203.0.113.195")

	ip1 := getIP(req1)
	if ip1 != "203.0.113.195" {
		t.Errorf("BUG TERBUKTI: getIP mengabaikan CF-Connecting-IP dari proxy lokal, hasil: %q", ip1)
	}

	// 2. Uji nilai IP sampah / serangan injeksi header
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "127.0.0.1:54321"
	req2.Header.Set("X-Real-IP", "<script>alert(1)</script>")

	ip2 := getIP(req2)
	if ip2 == "<script>alert(1)</script>" {
		t.Errorf("BUG TERBUKTI: getIP menerima string bukan IP valid mentah-mentah: %q", ip2)
	}
}

// TestFlaw_K5_MissingSecurityHeaders membuktikan server belum mengirimkan
// header keamanan standar (CSP, X-Frame-Options, Permissions-Policy, nosniff).
func TestFlaw_K5_MissingSecurityHeaders(t *testing.T) {
	setupWebH5aTest(t)
	database := testutil.TestDBInstance(t)
	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Errorf("BUG TERBUKTI: Content-Security-Policy tidak ada di header respons")
	}

	xfo := w.Header().Get("X-Frame-Options")
	if xfo == "" {
		t.Errorf("BUG TERBUKTI: X-Frame-Options tidak ada di header respons")
	}

	nosniff := w.Header().Get("X-Content-Type-Options")
	if nosniff == "" {
		t.Errorf("BUG TERBUKTI: X-Content-Type-Options tidak ada di header respons")
	}
}
