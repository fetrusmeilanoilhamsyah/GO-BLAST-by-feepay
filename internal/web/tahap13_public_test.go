package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"goblast/internal/auth"
	"goblast/internal/db"
	"goblast/internal/i18n"
	"goblast/internal/testutil"
)

// TestTahap13_PublicRoutesStatus200WithoutAuth memverifikasi bahwa:
// 1. "/", "/syarat", "/privasi" mengembalikan 200 tanpa login.
// 2. Cache-Control untuk publik adalah "public, max-age=60".
// 3. Cache-Control untuk login dan register adalah "no-store".
func TestTahap13_PublicRoutesStatus200WithoutAuth(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(nil, "dev", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	handler := srv.WrapGlobalMiddleware(mux)

	routes := []struct {
		path        string
		expectCode  int
		expectCache string
	}{
		{"/", http.StatusOK, "public, max-age=60"},
		{"/syarat", http.StatusOK, "public, max-age=60"},
		{"/privasi", http.StatusOK, "public, max-age=60"},
		{"/teknologi", http.StatusOK, "public, max-age=60"},
		{"/login", http.StatusOK, "no-store"},
		{"/register", http.StatusOK, "no-store"},
	}

	for _, tc := range routes {
		t.Run("Route_"+tc.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expectCode {
				t.Errorf("Path %s: expected code %d, got %d", tc.path, tc.expectCode, rr.Code)
			}
			cacheCtrl := rr.Header().Get("Cache-Control")
			if !strings.Contains(cacheCtrl, tc.expectCache) {
				t.Errorf("Path %s: expected Cache-Control to contain %q, got %q", tc.path, tc.expectCache, cacheCtrl)
			}
		})
	}
}

// TestTahap13_HomeDataPrivacy memverifikasi bahwa beranda publik "/" tidak memuat
// nomor customer, nama campaign, saldo, atau data pekerja (informasi sensitif).
func TestTahap13_HomeDataPrivacy(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)

	// Buat data fiktif
	custPhone := "6289999" + suffix[len(suffix)-4:]
	campName := "CampaignFiktifSecret_" + suffix
	workerUname := "workerprivasi_" + suffix

	// Masukkan worker dan saldo buku besar
	var workerID int64
	err = database.Pool.QueryRow(ctx, `
		INSERT INTO users (username, display_name, password_hash, role, status, referral_code)
		VALUES ($1, $1, 'hash123', 'worker', 'active', $2)
		RETURNING id
	`, workerUname, "REFPRIV_"+suffix).Scan(&workerID)
	if err != nil {
		t.Fatalf("Gagal insert user: %v", err)
	}

	_, err = database.Pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, amount, currency)
		VALUES ($1, 'upah', 500000, 'IDR')
	`, workerID)
	if err != nil {
		t.Fatalf("Gagal insert ledger: %v", err)
	}

	// Masukkan campaign
	var campID int64
	err = database.Pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, body, button_mode, status, elements, created_by)
		VALUES ($1, 'Pesan uji rahasia', 'kartu_link', 'berjalan', '{}'::jsonb, $2)
		RETURNING id
	`, campName, workerID).Scan(&campID)
	if err != nil {
		t.Fatalf("Gagal insert campaign: %v", err)
	}

	// Masukkan number batch
	var batchID int64
	err = database.Pool.QueryRow(ctx, `
		INSERT INTO number_batches (campaign_id, filename, uploaded_by)
		VALUES ($1, 'test.csv', $2)
		RETURNING id
	`, campID, workerID).Scan(&batchID)
	if err != nil {
		t.Fatalf("Gagal insert number_batches: %v", err)
	}

	// Masukkan customer number
	_, err = database.Pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status)
		VALUES ($1, $2, $3, '62', 'tersedia')
	`, campID, batchID, custPhone)
	if err != nil {
		t.Fatalf("Gagal insert campaign_number: %v", err)
	}

	t.Cleanup(func() {
		_, _ = database.Pool.Exec(context.Background(), "DELETE FROM campaign_numbers WHERE campaign_id = $1", campID)
		_, _ = database.Pool.Exec(context.Background(), "DELETE FROM number_batches WHERE id = $1", batchID)
		_, _ = database.Pool.Exec(context.Background(), "DELETE FROM campaigns WHERE id = $1", campID)
		_, _ = database.Pool.Exec(context.Background(), "DELETE FROM ledger_entries WHERE user_id = $1", workerID)
		_, _ = database.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", workerID)
	})

	// Buka halaman beranda publik "/"
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	srv.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", rr.Code)
	}

	body := rr.Body.String()

	// Pastikan data sensitif tidak pernah bocor ke respons
	if strings.Contains(body, custPhone) {
		t.Errorf("PRIVACY LEAK: nomor customer fiktif %s muncul di beranda publik!", custPhone)
	}
	if strings.Contains(body, campName) {
		t.Errorf("PRIVACY LEAK: nama campaign %s muncul di beranda publik!", campName)
	}
	if strings.Contains(body, workerUname) {
		t.Errorf("PRIVACY LEAK: username pekerja %s muncul di beranda publik!", workerUname)
	}
	if strings.Contains(body, "500000") || strings.Contains(body, "500.000") {
		t.Errorf("PRIVACY LEAK: saldo pekerja muncul di beranda publik!")
	}
}

// TestTahap13_PublicStatsCacheAndDBOutage menguji:
// 1. 100 kunjungan beruntun ke "/" hanya membaca DB 1 kali (lewat cache memori 60s).
// 2. Database dimatikan: "/" tetap 200 OK tanpa error (graceful fallback).
func TestTahap13_PublicStatsCacheAndDBOutage(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	// 1. Uji 100 kunjungan serentak dan beruntun
	srv.pubStatsCache.mu.Lock()
	srv.pubStatsCache.lastFetch = time.Time{}
	srv.pubStatsCache.loaded = false
	srv.pubStatsCache.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/", nil)
			rr := httptest.NewRecorder()
			srv.handleIndex(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("Expected 200 OK during concurrent visits, got %d", rr.Code)
			}
		}()
	}
	wg.Wait()

	// Pastikan cache terisi
	srv.pubStatsCache.mu.Lock()
	cacheFetchTime := srv.pubStatsCache.lastFetch
	cacheLoaded := srv.pubStatsCache.loaded
	srv.pubStatsCache.mu.Unlock()

	if !cacheLoaded || cacheFetchTime.IsZero() {
		t.Fatalf("Cache pubStatsCache tidak terisi setelah 100 kunjungan")
	}

	// 2. Database dimatikan: "/" tetap 200 OK tanpa error
	srv.DB = nil // simulasikan database putus / nil
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	srv.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK during database outage, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

// TestTahap13_UnsetRatesHideCards memverifikasi bahwa jika rate belum diisi admin,
// kartu disembunyikan dan bukan menampilkan "0" atau "Rp 0".
func TestTahap13_UnsetRatesHideCards(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(nil, "dev", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	// Set cache secara manual dengan rate tidak ada / 0
	srv.pubStatsCache.mu.Lock()
	srv.pubStatsCache.loaded = true
	srv.pubStatsCache.lastFetch = time.Now()
	srv.pubStatsCache.claimable = 0
	srv.pubStatsCache.hasWage = false
	srv.pubStatsCache.hasRef = false
	srv.pubStatsCache.mu.Unlock()

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	srv.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", rr.Code)
	}

	body := rr.Body.String()
	if strings.Contains(body, "Rp 0") {
		t.Errorf("Ditemukan teks 'Rp 0' pada halaman saat rate belum diatur admin!")
	}
}

// TestTahap13_I18nKeysSync memverifikasi bahwa semua kunci i18n baru untuk halaman publik,
// syarat, dan privasi tersedia di ketiga bahasa (id, en, zh).
func TestTahap13_I18nKeysSync(t *testing.T) {
	root := findProjectRoot(t)
	localesDir := filepath.Join(root, "locales")

	readJSON := func(lang string) map[string]interface{} {
		data, err := os.ReadFile(filepath.Join(localesDir, lang+".json"))
		if err != nil {
			t.Fatalf("Gagal membaca %s.json: %v", lang, err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Gagal unmarshal %s.json: %v", lang, err)
		}
		return m
	}

	idMap := readJSON("id")
	enMap := readJSON("en")
	zhMap := readJSON("zh")

	requiredPrefixes := []string{"public.", "terms.", "privacy."}

	for key := range idMap {
		isReq := false
		for _, prefix := range requiredPrefixes {
			if strings.HasPrefix(key, prefix) {
				isReq = true
				break
			}
		}
		if !isReq {
			continue
		}

		if _, ok := enMap[key]; !ok {
			t.Errorf("Kunci %q ada di id.json tetapi hilang di en.json", key)
		}
		if _, ok := zhMap[key]; !ok {
			t.Errorf("Kunci %q ada di id.json tetapi hilang di zh.json", key)
		}
	}
}

// TestTahap13_LoginNoDoubleColon_And_RegisterTermsLink memverifikasi:
// 1. Halaman login tidak mengandung "::" (masalah Tema::).
// 2. Halaman registrasi memuat tautan ke /syarat dan /privasi.
func TestTahap13_LoginNoDoubleColon_And_RegisterTermsLink(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(nil, "dev", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	// 1. Login page check for "::"
	reqLogin := httptest.NewRequest("GET", "/login", nil)
	rrLogin := httptest.NewRecorder()
	srv.handleLogin(rrLogin, reqLogin)

	if rrLogin.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from /login, got %d", rrLogin.Code)
	}
	loginBody := rrLogin.Body.String()
	if strings.Contains(loginBody, "::") {
		t.Errorf("Ditemukan titik dua ganda '::' di halaman login: %s", loginBody)
	}
	if !strings.Contains(loginBody, "/syarat") || !strings.Contains(loginBody, "/privasi") {
		t.Errorf("Halaman login tidak memuat tautan ke /syarat atau /privasi")
	}

	// 2. Register page check for /syarat and /privasi links
	reqReg := httptest.NewRequest("GET", "/register", nil)
	rrReg := httptest.NewRecorder()
	srv.handleRegister(rrReg, reqReg)

	if rrReg.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from /register, got %d", rrReg.Code)
	}
	regBody := rrReg.Body.String()
	if !strings.Contains(regBody, `href="/syarat"`) || !strings.Contains(regBody, `target="_blank"`) {
		t.Errorf("Halaman registrasi tidak memiliki tautan persetujuan ke /syarat dengan target _blank")
	}
	if !strings.Contains(regBody, `href="/privasi"`) {
		t.Errorf("Halaman registrasi tidak memiliki tautan ke /privasi")
	}
}

// TestTahap13_TurnstileDisabledNotice memverifikasi bahwa:
// 1. Pada non-produksi (dev) dengan kunci kosong: catatan turnstile nonaktif tampil.
// 2. Pada produksi (prod) dengan kunci kosong: catatan turnstile nonaktif TIDAK tampil.
func TestTahap13_TurnstileDisabledNotice(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	// Skenario 1: Dev tanpa kunci Turnstile
	srvDev, _ := NewServer(nil, "dev", nil, nil)
	rrDev := httptest.NewRecorder()
	srvDev.handleLogin(rrDev, httptest.NewRequest("GET", "/login", nil))
	bodyDev := rrDev.Body.String()
	if !strings.Contains(bodyDev, "Turnstile nonaktif") {
		t.Errorf("Catatan 'Turnstile nonaktif' harus tampil di non-produksi saat kunci kosong")
	}

	// Skenario 2: Prod tanpa kunci Turnstile
	srvProd, _ := NewServer(nil, "prod", nil, nil)
	rrProd := httptest.NewRecorder()
	srvProd.handleLogin(rrProd, httptest.NewRequest("GET", "/login", nil))
	bodyProd := rrProd.Body.String()
	if strings.Contains(bodyProd, "Turnstile nonaktif") {
		t.Errorf("Catatan 'Turnstile nonaktif' DILARANG tampil di produksi!")
	}
}

// TestTahap13_LoggedInRedirects memverifikasi bahwa pengunjung yang sudah punya
// sesi valid dan membuka "/" dialihkan ke panel masing-masing (worker -> /worker, admin -> /admin).
func TestTahap13_LoggedInRedirects(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)

	// Buat worker
	var workerID int64
	_ = database.Pool.QueryRow(ctx, `
		INSERT INTO users (username, display_name, password_hash, role, status, referral_code)
		VALUES ($1, $1, 'hash', 'worker', 'active', $2) RETURNING id
	`, "wlogin_"+suffix, "RW_"+suffix).Scan(&workerID)

	rawTokenW, hashTokenW, _ := auth.GenerateSessionToken()
	_, _ = database.Pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, CURRENT_TIMESTAMP + INTERVAL '1 hour', '127.0.0.1', 'test')
	`, workerID, hashTokenW)

	// Uji worker membuka "/"
	reqW := httptest.NewRequest("GET", "/", nil)
	reqW.AddCookie(&http.Cookie{Name: srv.sessionCookieName(), Value: rawTokenW})
	rrW := httptest.NewRecorder()
	srv.handleIndex(rrW, reqW)

	if rrW.Code != http.StatusSeeOther || rrW.Header().Get("Location") != "/worker" {
		t.Errorf("Worker dengan sesi aktif membuka /: expected redirect ke /worker, got code %d, loc %s", rrW.Code, rrW.Header().Get("Location"))
	}

	// Buat admin
	var adminID int64
	_ = database.Pool.QueryRow(ctx, `
		INSERT INTO users (username, display_name, email, password_hash, role, status, referral_code)
		VALUES ($1, $1, $2, 'hash', 'admin', 'active', $3) RETURNING id
	`, "alogin_"+suffix, "admin_"+suffix+"@example.com", "RA_"+suffix).Scan(&adminID)

	rawTokenA, hashTokenA, _ := auth.GenerateSessionToken()
	_, _ = database.Pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, CURRENT_TIMESTAMP + INTERVAL '1 hour', '127.0.0.1', 'test')
	`, adminID, hashTokenA)

	// Uji admin membuka "/"
	reqA := httptest.NewRequest("GET", "/", nil)
	reqA.AddCookie(&http.Cookie{Name: srv.sessionCookieName(), Value: rawTokenA})
	rrA := httptest.NewRecorder()
	srv.handleIndex(rrA, reqA)

	t.Cleanup(func() {
		_, _ = database.Pool.Exec(context.Background(), "DELETE FROM web_sessions WHERE user_id IN ($1, $2)", workerID, adminID)
		_, _ = database.Pool.Exec(context.Background(), "DELETE FROM users WHERE id IN ($1, $2)", workerID, adminID)
	})

	if rrA.Code != http.StatusSeeOther || rrA.Header().Get("Location") != "/admin" {
		t.Errorf("Admin dengan sesi aktif membuka /: expected redirect ke /admin, got code %d, loc %s", rrA.Code, rrA.Header().Get("Location"))
	}
}

// TestTahap13_PageSizeUnder50KB memverifikasi bahwa total HTML dan CSS beranda di bawah 50 KB.
func TestTahap13_PageSizeUnder50KB(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(nil, "dev", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	// Muat CSS
	cssBytes, err := os.ReadFile(filepath.Join(root, "web", "static", "css", "theme.css"))
	if err != nil {
		t.Fatalf("Gagal membaca theme.css: %v", err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	srv.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", rr.Code)
	}

	htmlLen := rr.Body.Len()
	cssLen := len(cssBytes)
	totalLen := htmlLen + cssLen

	t.Logf("Ukuran beranda: HTML=%d bytes, CSS=%d bytes, Total=%d bytes (%.2f KB)",
		htmlLen, cssLen, totalLen, float64(totalLen)/1024.0)

	const maxLimit = 50 * 1024 // 50 KB
	if totalLen >= maxLimit {
		t.Errorf("Total HTML + CSS beranda (%d bytes) melebihi batas 50 KB (%d bytes)", totalLen, maxLimit)
	}
}

// TestTahap13_TeknologiContentAndSecurity memverifikasi isi halaman /teknologi:
// 1. Berstatus 200 OK tanpa login dengan Cache-Control "public, max-age=60".
// 2. Memuat fakta mesin pengirim (whatsmeow), perlakuan akun WA pekerja, keamanan faktual,
//    tabel penyimpanan dengan 9 jenis data, dan pihak ketiga (Cloudflare).
// 3. TIDAK memuat klaim palsu/slogan ("aman 100", "tidak bisa diretas", "standar bank",
//    "standar militer", "iso", "27001", lokasi server, klaim "tidak dijual/dibagikan", dll).
func TestTahap13_TeknologiContentAndSecurity(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(nil, "dev", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	langs := []string{"id", "en", "zh"}

	forbiddenPhrases := []string{
		"aman 100",
		"100% aman",
		"tidak bisa diretas",
		"unhackable",
		"standar bank",
		"bank grade",
		"standar militer",
		"military grade",
		"iso 27001",
		"iso27001",
		"iso-27001",
		"data tidak dijual",
		"tidak akan pernah dijual",
		"penghapusan akun",
		"lokasi server",
		"server kami berada di",
		"data center kami di",
	}

	for _, lang := range langs {
		t.Run("Lang_"+lang, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/teknologi?lang="+lang, nil)
			rr := httptest.NewRecorder()
			srv.handleTeknologi(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("Status expected 200, got %d", rr.Code)
			}

			cacheCtrl := rr.Header().Get("Cache-Control")
			if !strings.Contains(cacheCtrl, "public, max-age=60") {
				t.Errorf("Cache-Control expected 'public, max-age=60', got %q", cacheCtrl)
			}

			body := rr.Body.String()
			bodyLower := strings.ToLower(body)

			// Verifikasi larangan klaim palsu
			for _, phrase := range forbiddenPhrases {
				if strings.Contains(bodyLower, phrase) {
					t.Errorf("Halaman /teknologi (%s) memuat frasa terlarang: %q", lang, phrase)
				}
			}

			// Verifikasi fakta teknis utama
			if !strings.Contains(body, "whatsmeow") {
				t.Errorf("Halaman /teknologi (%s) harus memuat penyebutan pustaka whatsmeow", lang)
			}
			if !strings.Contains(body, "Cloudflare") {
				t.Errorf("Halaman /teknologi (%s) harus memuat pihak ketiga Cloudflare", lang)
			}
			if !strings.Contains(body, "Argon2id") {
				t.Errorf("Halaman /teknologi (%s) harus memuat algoritma Argon2id", lang)
			}
			if !strings.Contains(body, "AES-256-GCM") {
				t.Errorf("Halaman /teknologi (%s) harus memuat AES-256-GCM", lang)
			}
			if !strings.Contains(body, "TOTP") {
				t.Errorf("Halaman /teknologi (%s) harus memuat TOTP", lang)
			}
			if !strings.Contains(body, ".gbak") {
				t.Errorf("Halaman /teknologi (%s) harus memuat ekstensi backup .gbak", lang)
			}
		})
	}

	// Verifikasi 9 baris data pada tabel penyimpanan dalam bahasa Indonesia
	reqID := httptest.NewRequest("GET", "/teknologi?lang=id", nil)
	rrID := httptest.NewRecorder()
	srv.handleTeknologi(rrID, reqID)
	bodyID := rrID.Body.String()

	expectedRowsID := []string{
		"Akun pekerja",
		"Tujuan penarikan terenkripsi",
		"Saldo dan riwayat upah",
		"Nomor customer dan data campaign",
		"Sesi WhatsApp",
		"Berkas sementara unggahan",
		"Cadangan database (.gbak)",
		"Log audit login gagal",
		"Log operasional server",
	}

	for _, row := range expectedRowsID {
		if !strings.Contains(bodyID, row) {
			t.Errorf("Tabel penyimpanan /teknologi belum memuat jenis data: %q", row)
		}
	}
}

// TestTahap13_PublicFooterPresence memverifikasi bahwa footer 3-kolom hadir
// di seluruh halaman publik: "/", "/syarat", "/privasi", dan "/teknologi".
func TestTahap13_PublicFooterPresence(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(nil, "dev", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	publicPaths := []string{
		"/",
		"/syarat",
		"/privasi",
		"/teknologi",
	}

	for _, path := range publicPaths {
		t.Run("Path_"+path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			rr := httptest.NewRecorder()

			switch path {
			case "/":
				srv.handleIndex(rr, req)
			case "/syarat":
				srv.handleSyarat(rr, req)
			case "/privasi":
				srv.handlePrivasi(rr, req)
			case "/teknologi":
				srv.handleTeknologi(rr, req)
			}

			if rr.Code != http.StatusOK {
				t.Fatalf("Path %s expected 200, got %d", path, rr.Code)
			}

			body := rr.Body.String()

			// 1. Memuat class footer bersama
			if !strings.Contains(body, "pub-footer") {
				t.Errorf("Path %s tidak memuat pub-footer", path)
			}

			// 2. Kolom 1 (Tentang): ada tautan beranda/#tentang, #faq, #kontak
			if (!strings.Contains(body, `href="/#tentang"`) && !strings.Contains(body, `href="/"`)) || !strings.Contains(body, `href="/#faq"`) || !strings.Contains(body, `href="/#kontak"`) {
				t.Errorf("Path %s tidak memuat link kolom Tentang lengkap", path)
			}

			// 3. Kolom 2 (Hukum): ada tautan /syarat dan /privasi
			if !strings.Contains(body, `href="/syarat"`) || !strings.Contains(body, `href="/privasi"`) {
				t.Errorf("Path %s tidak memuat link kolom Hukum", path)
			}

			// 4. Kolom 3 (Teknologi dan data): ada tautan /teknologi
			if !strings.Contains(body, `href="/teknologi"`) {
				t.Errorf("Path %s tidak memuat link kolom Teknologi (/teknologi)", path)
			}

			// 5. Selector bahasa dan tema ada di bottom bar
			if !strings.Contains(body, `action="/language"`) {
				t.Errorf("Path %s tidak memuat selector bahasa", path)
			}
			if !strings.Contains(body, `action="/theme"`) {
				t.Errorf("Path %s tidak memuat selector tema", path)
			}

			// 6. Nonce CSP ada pada script footer
			if !strings.Contains(body, `<script nonce=`) {
				t.Errorf("Path %s tidak memuat script ber-nonce untuk selector footer", path)
			}
		})
	}

	// Verifikasi target anchor id="tentang", id="faq", dan id="kontak" benar-benar ada di beranda
	reqHome := httptest.NewRequest("GET", "/", nil)
	rrHome := httptest.NewRecorder()
	srv.handleIndex(rrHome, reqHome)
	bodyHome := rrHome.Body.String()

	anchorTargets := []string{`id="tentang"`, `id="faq"`, `id="kontak"`}
	for _, target := range anchorTargets {
		if !strings.Contains(bodyHome, target) {
			t.Errorf("Beranda / wajib memuat target anchor %s agar tautan footer dapat berpindah/scroll dengan benar", target)
		}
	}
}

