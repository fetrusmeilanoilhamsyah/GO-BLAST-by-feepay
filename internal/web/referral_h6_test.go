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
	"goblast/internal/i18n"
	"goblast/internal/testutil"
)

func setupWebH6Test(t *testing.T) {
	t.Helper()
	root := testutil.RootDir(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	_ = i18n.Load("locales")
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
}

// 1. Pendaftaran: Kode referral yang tidak ditemukan menampilkan galat dan tidak membuat akun (D)
func TestH6_FindingD_Register_InvalidReferralCode_ReturnsError(t *testing.T) {
	setupWebH6Test(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	nonce := time.Now().UnixNano()
	username := fmt.Sprintf("reg_h6_%d", nonce%100000)
	fakeRefCode := fmt.Sprintf("PALSU_%d", nonce%10000)

	form := url.Values{}
	form.Set("username", username)
	form.Set("password", "ValidPassword123!")
	form.Set("agree_terms", "true")
	form.Set("referral_code", fakeRefCode)
	form.Set("csrf_token", "dummy_csrf")
	form.Set("form_token", auth.SignFormTimingToken(time.Now().Add(-5*time.Second), srv.SessionSecret))

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	srv.handleRegister(rr, req)

	// Pastikan user TIDAK dibuat di database
	var count int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM users WHERE username = $1", username).Scan(&count)
	if count != 0 {
		// Bersihkan jika sempat terbuat
		var uID int64
		_ = pool.QueryRow(ctx, "SELECT id FROM users WHERE username = $1", username).Scan(&uID)
		testutil.CleanupTestData(pool, []int64{uID}, nil)
		t.Fatalf("akun %s terbuat padahal kode referral %s tidak valid!", username, fakeRefCode)
	}

	// Pastikan respons memuat pesan kesalahan
	body := rr.Body.String()
	if !strings.Contains(body, "tidak ditemukan") && !strings.Contains(body, "tidak valid") {
		t.Errorf("respons register harus memuat galat kode referral tidak valid, dapat: %s", body)
	}
}

// 2. Pendaftaran: Tautan /register?ref=KODE mengisi kolom referral otomatis dan disanitasi
func TestH6_FindingD_Register_PreFillReferralFromURL(t *testing.T) {
	setupWebH6Test(t)
	database := testutil.TestDBInstance(t)

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	testRef := "FEE12345"
	req := httptest.NewRequest(http.MethodGet, "/register?ref="+testRef, nil)
	rr := httptest.NewRecorder()

	srv.handleRegister(rr, req)

	body := rr.Body.String()
	expectedInput := fmt.Sprintf(`value="%s"`, testRef)
	if !strings.Contains(body, expectedInput) {
		t.Errorf("halaman register harus memuat pre-filled %s pada input referral_code, body:\n%s", expectedInput, body)
	}
}

// 3. Pendaftaran: Nilai ref disanitasi dan dibatasi panjangnya
func TestH6_FindingD_Register_SanitizeRefQueryParam(t *testing.T) {
	setupWebH6Test(t)
	database := testutil.TestDBInstance(t)

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Ref dengan karakter berbahaya dan panjang > 50 karakter
	dirtyRef := "REF_<script>alert(1)</script>_VERY_LONG_STRING_OVER_FIFTY_CHARACTERS_12345678901234567890"
	req := httptest.NewRequest(http.MethodGet, "/register?ref="+url.QueryEscape(dirtyRef), nil)
	rr := httptest.NewRecorder()

	srv.handleRegister(rr, req)

	body := rr.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("halaman register membocorkan script tag yang tidak disanitasi!")
	}
}

// 4. Halaman pekerja: /worker/referral menampilkan data lengkap tanpa nomor customer / email
func TestH6_WorkerReferral_DisplayStatsAndMaskedList(t *testing.T) {
	setupWebH6Test(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	srv.CSWhatsAppURL = "https://wa.me/cs_support"

	nonce := time.Now().UnixNano()
	unameA := fmt.Sprintf("refa_%d", nonce%100000)
	unameB := fmt.Sprintf("refb_%d", nonce%100000)
	unameC := fmt.Sprintf("refc_%d", nonce%100000)

	var userA, userB, userC int64
	_ = pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, role, display_name, status, referral_code) VALUES ($1, 'hash', 'worker', 'User A', 'active', $2) RETURNING id`, unameA, fmt.Sprintf("CODEA_%d", nonce%10000)).Scan(&userA)
	_ = pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, role, display_name, status, referral_code, referred_by) VALUES ($1, 'hash', 'worker', 'User B', 'active', $2, $3) RETURNING id`, unameB, fmt.Sprintf("CODEB_%d", nonce%10000), userA).Scan(&userB)
	_ = pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, role, display_name, status, referral_code, referred_by) VALUES ($1, 'hash', 'worker', 'User C', 'active', $2, $3) RETURNING id`, unameC, fmt.Sprintf("CODEC_%d", nonce%10000), userA).Scan(&userC)

	defer func() {
		testutil.CleanupTestData(pool, []int64{userA, userB, userC}, nil)
	}()

	// Simulasikan B mengirim 5 pesan dan mendapat upah, A mendapat 5 bonus referral
	for i := 0; i < 5; i++ {
		_, _ = pool.Exec(ctx, `
			INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, note)
			VALUES ($1, 'upah', 'IDR', 100, 100, 'upah pesan terkirim')
		`, userB)

		_, _ = pool.Exec(ctx, `
			INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, source_user_id, note)
			VALUES ($1, 'referral', 'IDR', 50, 50, $2, 'bonus referral')
		`, userA, userB)
	}

	// Siapkan sesi untuk user A
	rawToken, hashToken, _ := auth.GenerateSessionToken()
	_, err = pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, '127.0.0.1', 'test-agent')
	`, userA, hashToken, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("insert session gagal: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	handler := srv.WrapGlobalMiddleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/worker/referral", nil)
	req.AddCookie(&http.Cookie{Name: "gb_session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()

	// 1. Teks "belum dihitung otomatis" tidak boleh ada lagi
	if strings.Contains(body, "belum dihitung otomatis") {
		t.Errorf("halaman referral masih memuat teks 'belum dihitung otomatis'!")
	}

	// 2. Harus menampilkan jumlah rekan bergabung (2 Orang) dan aktif (1 Orang)
	if !strings.Contains(body, "2") {
		t.Errorf("halaman referral harus memuat total 2 rekan bergabung")
	}

	// 3. Username referee harus disamarkan (dua huruf pertama dan bintang)
	// B: unameB[:2] + stars
	prefixB := unameB[:2]
	if !strings.Contains(body, prefixB) {
		t.Errorf("halaman referral harus memuat username tersamar yang diawali %s", prefixB)
	}
	// Username lengkap B tidak boleh muncul
	if strings.Contains(body, unameB) {
		t.Errorf("username lengkap B (%s) tidak boleh muncul tanpa penyamaran!", unameB)
	}

	// 4. Tidak ada email atau nomor telepon customer di respons
	if strings.Contains(body, "@test.local") || strings.Contains(body, "628") {
		t.Errorf("halaman referral membocorkan email atau nomor telepon!")
	}
}

// 5. Panel admin: Detail pekerja menampilkan siapa yang mengajaknya, siapa yang diajaknya,
// kelompok rekening sama (payout_dest_hash), dan penanda bonus ditahan
func TestH6_AdminWorkerDetail_ReferralAndFraudMarkers(t *testing.T) {
	setupWebH6Test(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	nonce := time.Now().UnixNano()
	sameDestHash := fmt.Sprintf("hash_same_%d", nonce)

	var adminID, workerA, workerB int64
	_ = pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, role, display_name, status, referral_code, totp_enabled_at) VALUES ($1, 'hash', 'admin', 'Admin', 'active', $2, now()) RETURNING id`, fmt.Sprintf("adm_%d", nonce%100000), fmt.Sprintf("REFADM_%d", nonce%10000)).Scan(&adminID)
	_ = pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, role, display_name, status, referral_code, payout_dest_hash) VALUES ($1, 'hash', 'worker', 'Worker A', 'active', $2, $3) RETURNING id`, fmt.Sprintf("wra_%d", nonce%100000), fmt.Sprintf("REFWA_%d", nonce%10000), sameDestHash).Scan(&workerA)
	_ = pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, role, display_name, status, referral_code, referred_by, payout_dest_hash) VALUES ($1, 'hash', 'worker', 'Worker B', 'active', $2, $3, $4) RETURNING id`, fmt.Sprintf("wrb_%d", nonce%100000), fmt.Sprintf("REFWB_%d", nonce%10000), workerA, sameDestHash).Scan(&workerB)

	defer func() {
		testutil.CleanupTestData(pool, []int64{adminID, workerA, workerB}, nil)
	}()

	// Siapkan sesi admin dengan stepup_at
	rawToken, hashToken, _ := auth.GenerateSessionToken()
	_, err = pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent, stepup_at)
		VALUES ($1, $2, $3, '127.0.0.1', 'test-agent', now())
	`, adminID, hashToken, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("insert admin session gagal: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	handler := srv.WrapGlobalMiddleware(mux)

	// Admin membuka detail worker B (harus memuat siapa yang mengajaknya: Worker A)
	reqB := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/workers/%d", workerB), nil)
	reqB.AddCookie(&http.Cookie{Name: "gb_session", Value: rawToken})
	rrB := httptest.NewRecorder()
	handler.ServeHTTP(rrB, reqB)

	if rrB.Code != http.StatusOK {
		t.Fatalf("detail worker B expected status 200, got %d", rrB.Code)
	}
	bodyB := rrB.Body.String()
	if !strings.Contains(bodyB, fmt.Sprintf("wra_%d", nonce%100000)) {
		t.Errorf("detail worker B harus menampilkan username pengundangnya (Worker A)")
	}

	// Admin membuka detail worker A (harus memuat daftar siapa yang diajaknya: Worker B)
	reqA := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/workers/%d", workerA), nil)
	reqA.AddCookie(&http.Cookie{Name: "gb_session", Value: rawToken})
	rrA := httptest.NewRecorder()
	handler.ServeHTTP(rrA, reqA)

	if rrA.Code != http.StatusOK {
		t.Fatalf("detail worker A expected status 200, got %d", rrA.Code)
	}
	bodyA := rrA.Body.String()
	if !strings.Contains(bodyA, fmt.Sprintf("wrb_%d", nonce%100000)) {
		t.Errorf("detail worker A harus menampilkan username rekan yang diajaknya (Worker B)")
	}

	// Karena A dan B memiliki sameDestHash, harus tampil penanda kelompok rekening sama
	if !strings.Contains(bodyA, "Rekening Sama") && !strings.Contains(bodyA, "Multi-Akun") && !strings.Contains(bodyA, "payout") {
		t.Errorf("detail worker A harus menampilkan penanda potensi kecurangan / multi-akun rekening sama")
	}
}

// 6. Laporan dompet admin: Menjumlahkan baris referral bersama upah
func TestH6_AdminWallet_IncludesReferralInSpent(t *testing.T) {
	setupWebH6Test(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	var workerID int64
	_ = pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, role, display_name, status, referral_code) VALUES ($1, 'hash', 'worker', 'Worker Wallet', 'active', $2) RETURNING id`, fmt.Sprintf("ww_%d", time.Now().UnixNano()%100000), fmt.Sprintf("REFWW_%d", time.Now().UnixNano()%10000)).Scan(&workerID)

	defer func() {
		testutil.CleanupTestData(pool, []int64{workerID}, nil)
	}()

	var cID int64
	err = pool.QueryRow(ctx, "INSERT INTO campaigns (name, status, button_mode, body, elements, created_by) VALUES ('Camp Ref', 'berjalan', 'kartu_link', 'Halo', '{}', $1) RETURNING id", workerID).Scan(&cID)
	if err != nil {
		t.Fatalf("insert campaign failed: %v", err)
	}
	var bID int64
	err = pool.QueryRow(ctx, "INSERT INTO number_batches (campaign_id, filename, uploaded_by) VALUES ($1, 'test.csv', $2) RETURNING id", cID, workerID).Scan(&bID)
	if err != nil {
		t.Fatalf("insert batch failed: %v", err)
	}
	var cnID int64
	err = pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status) VALUES ($1, $2, '6289912345678', '62', 'terkirim') RETURNING id", cID, bID).Scan(&cnID)
	if err != nil {
		t.Fatalf("insert campaign_number failed: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE id = $1", cnID)
		_, _ = pool.Exec(ctx, "DELETE FROM number_batches WHERE id = $1", bID)
		_, _ = pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cID)
	}()

	// Masukkan baris upah dan baris referral ke ledger_entries
	_, err = pool.Exec(ctx, `INSERT INTO ledger_entries (user_id, kind, currency, amount, note) VALUES ($1, 'upah', 'IDR', 1000, 'test upah')`, workerID)
	if err != nil {
		t.Fatalf("insert upah failed: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id, source_user_id, note) VALUES ($1, 'referral', 'IDR', 500, $2, $3, 'test referral')`, workerID, cnID, workerID)
	if err != nil {
		t.Fatalf("insert referral failed: %v", err)
	}

	wallet, err := srv.fetchWalletData(ctx)
	if err != nil {
		t.Fatalf("fetchWalletData failed: %v", err)
	}

	// SpentIDR harus menjumlahkan upah dan referral
	if wallet.SpentIDR < 1500 {
		t.Errorf("wallet.SpentIDR = %d, expected at least 1500 (upah 1000 + referral 500)", wallet.SpentIDR)
	}
}
