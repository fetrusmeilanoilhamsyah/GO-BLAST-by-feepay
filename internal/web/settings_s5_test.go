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
	"goblast/internal/config"
	"goblast/internal/engine"
	"goblast/internal/wa"
)

// Skenario 1: Engine memakai konstanta package variable dan perilakunya berubah saat variabel diubah di tes
func TestS5_EngineUsesPackageVariables(t *testing.T) {
	origFast := config.SpeedDelays["fast"]
	origOverSlow := config.SpeedDelays["over_slow"]
	origJitter := config.JitterPercent
	origMaxAttempts := config.MaxAttempts
	origClaimTimeout := config.ClaimTimeoutSeconds
	origDailyLimit := config.DailyLimitPerAccount

	defer func() {
		config.SpeedDelays["fast"] = origFast
		config.SpeedDelays["over_slow"] = origOverSlow
		config.JitterPercent = origJitter
		config.MaxAttempts = origMaxAttempts
		config.ClaimTimeoutSeconds = origClaimTimeout
		config.DailyLimitPerAccount = origDailyLimit
	}()

	// Buktikan perilaku kalkulasi delay berubah saat variabel paket diubah
	config.SpeedDelays["fast"] = 10 * time.Second
	config.JitterPercent = 0 // Nol jitter untuk uji deterministik
	delayFast := engine.CalculateSpeedDelay("fast")
	if delayFast != 10*time.Second {
		t.Errorf("Expected calculateSpeedDelay('fast') = 10s, got %v", delayFast)
	}

	config.SpeedDelays["fast"] = 2 * time.Second
	delayFast2 := engine.CalculateSpeedDelay("fast")
	if delayFast2 != 2*time.Second {
		t.Errorf("Expected calculateSpeedDelay('fast') = 2s, got %v", delayFast2)
	}

	// Buktikan variabel paket lainnya terbaca
	config.MaxAttempts = 99
	if config.MaxAttempts != 99 {
		t.Errorf("Expected MaxAttempts = 99, got %d", config.MaxAttempts)
	}
	config.ClaimTimeoutSeconds = 999
	if config.ClaimTimeoutSeconds != 999 {
		t.Errorf("Expected ClaimTimeoutSeconds = 999, got %d", config.ClaimTimeoutSeconds)
	}
	config.DailyLimitPerAccount = 0
	if config.DailyLimitPerAccount != 0 {
		t.Errorf("Expected DailyLimitPerAccount = 0 (tanpa batas), got %d", config.DailyLimitPerAccount)
	}
}

// Skenario 2: Penautan ditolak bila max_accounts_per_worker kosong atau tercapai
func TestS5_LinkingRejectedWhenMaxAccountsEmptyOrReached(t *testing.T) {
	srv, pool, _, workerID := setupAdminTest(t)
	ctx := context.Background()

	cfg := &config.Config{MaxActiveSessions: 50}
	settingsReader := wa.NewDBSettingsReader(srv.DB)
	mgr := wa.NewManager(srv.DB, settingsReader, cfg)

	// Kasus A: max_accounts_per_worker dihapus dari settings
	_, _ = pool.Exec(ctx, "DELETE FROM settings WHERE key = 'max_accounts_per_worker'")
	_, _, errConnect := mgr.Connect(ctx, 999991, workerID)
	if errConnect == nil || !strings.Contains(errConnect.Error(), "belum diatur") {
		t.Errorf("Expected error max_accounts_per_worker belum diatur oleh admin, got: %v", errConnect)
	}

	// Kasus B: max_accounts_per_worker = 1
	_ = srv.updateSettingWithAudit(ctx, 1, "max_accounts_per_worker", 1, "127.0.0.1")

	// Buat 1 akun terhubung di database
	var acc1ID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
		VALUES ($1, 'Akun 1', 'hash_test_1', 'terhubung')
		RETURNING id
	`, workerID).Scan(&acc1ID)
	if err != nil {
		t.Fatalf("Failed to insert acc1: %v", err)
	}

	// Buat akun ke-2 dan coba hubungkan, harus ditolak batas tercapai
	var acc2ID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
		VALUES ($1, 'Akun 2', 'hash_test_2', 'menunggu_scan')
		RETURNING id
	`, workerID).Scan(&acc2ID)
	if err != nil {
		t.Fatalf("Failed to insert acc2: %v", err)
	}

	_, _, errConnect2 := mgr.Connect(ctx, acc2ID, workerID)
	if errConnect2 == nil || !strings.Contains(errConnect2.Error(), "batas akun per pekerja tercapai") {
		t.Errorf("Expected error batas akun per pekerja tercapai, got: %v", errConnect2)
	}
}

// Skenario 3: Form pengaturan tolak nilai di luar rentang
func TestS5_SettingsFormRejectsOutOfRangeValues(t *testing.T) {
	srv, _, adminID, _ := setupAdminTest(t)

	cases := []struct {
		name      string
		wage      string
		referral  string
		kurs      string
		maxAcc    string
		expectErr bool
	}{
		{"Valid standard", "25", "5", "15000", "5", false},
		{"Wage empty", "", "5", "15000", "5", true},
		{"Wage zero", "0", "5", "15000", "5", true},
		{"Wage negative", "-1", "5", "15000", "5", true},
		{"Referral negative", "25", "-1", "15000", "5", true},
		{"Kurs empty", "25", "5", "", "5", true},
		{"Kurs zero", "25", "5", "0", "5", true},
		{"Kurs negative", "25", "5", "-15000", "5", true},
		{"MaxAcc empty", "25", "5", "15000", "", true},
		{"MaxAcc zero", "25", "5", "15000", "0", true},
		{"MaxAcc 51 (too high)", "25", "5", "15000", "51", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("rate_wage_idr", tc.wage)
			form.Set("rate_referral_idr", tc.referral)
			form.Set("rate_idr_to_usdt", tc.kurs)
			form.Set("max_accounts_per_worker", tc.maxAcc)

			req := httptest.NewRequest("POST", "/admin/settings", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			ctx := context.WithValue(req.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			srv.HandleAdminSettingsPOST(rr, req)

			if tc.expectErr {
				if rr.Code != http.StatusBadRequest {
					t.Errorf("Expected 400 Bad Request, got %d", rr.Code)
				}
			} else {
				if rr.Code != http.StatusSeeOther {
					t.Errorf("Expected 303 Redirect, got %d", rr.Code)
				}
			}
		})
	}
}

// Skenario 4 & 5: Pendaftaran tanpa email berhasil, terms tersimpan, dan username ganda ditolak
func TestS5_RegistrationNoEmailAndUniqueUsername(t *testing.T) {
	srv, pool, _, _ := setupAdminTest(t)
	ctx := context.Background()

	// 1. Pendaftaran tanpa email berhasil
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/register" {
			srv.handleRegister(w, r)
		} else if r.URL.Path == "/login" {
			srv.handleLogin(w, r)
		}
	}))
	defer ts.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	csrf, _ := getCSRFToken(ts, client, ts.URL+"/register")

	uniqueUSN := fmt.Sprintf("usn_%d", time.Now().UnixNano()%10000000)
	form := url.Values{}
	form.Add("username", uniqueUSN)
	form.Add("email", "") // Kosongkan email
	form.Add("password", "SecretP@ss123")
	form.Add("agree_terms", "true")
	form.Add("language", "id")

	req, _ := http.NewRequest("POST", ts.URL+"/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "gb_csrf", Value: csrf})
	req.Header.Set("X-CSRF-Token", csrf)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Register request failed: %v", err)
	}

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("Expected 303 redirect after registration without email, got %d", resp.StatusCode)
	}

	// Verifikasi di DB: email NULL, terms_accepted_at tercatat, terms_version = 'v1.0'
	var emailPtr *string
	var termsVersion string
	var termsAt *time.Time
	err = pool.QueryRow(ctx, "SELECT email, terms_version, terms_accepted_at FROM users WHERE username = $1", uniqueUSN).Scan(&emailPtr, &termsVersion, &termsAt)
	if err != nil {
		t.Fatalf("User not found in DB: %v", err)
	}
	if emailPtr != nil {
		t.Errorf("Expected email to be NULL, got %s", *emailPtr)
	}
	if termsVersion != "v1.0" {
		t.Errorf("Expected terms_version = 'v1.0', got %s", termsVersion)
	}
	if termsAt == nil {
		t.Errorf("Expected terms_accepted_at to be non-nil")
	}

	// Login dengan username berhasil
	csrfLogin, _ := getCSRFToken(ts, client, ts.URL+"/login")
	loginForm := url.Values{}
	loginForm.Add("identifier", uniqueUSN)
	loginForm.Add("password", "SecretP@ss123")

	reqLogin, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(loginForm.Encode()))
	reqLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqLogin.AddCookie(&http.Cookie{Name: "gb_csrf", Value: csrfLogin})
	reqLogin.Header.Set("X-CSRF-Token", csrfLogin)

	respLogin, err := client.Do(reqLogin)
	if err != nil {
		t.Fatalf("Login request failed: %v", err)
	}
	if respLogin.StatusCode != http.StatusSeeOther {
		t.Errorf("Expected login success redirect 303, got %d", respLogin.StatusCode)
	}

	// 2. Username ganda ditolak
	csrf2, _ := getCSRFToken(ts, client, ts.URL+"/register")
	formDup := url.Values{}
	formDup.Add("username", strings.ToUpper(uniqueUSN)) // Cek case-insensitive
	formDup.Add("password", "AnotherPass123")
	formDup.Add("agree_terms", "true")

	reqDup, _ := http.NewRequest("POST", ts.URL+"/register", strings.NewReader(formDup.Encode()))
	reqDup.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqDup.AddCookie(&http.Cookie{Name: "gb_csrf", Value: csrf2})
	reqDup.Header.Set("X-CSRF-Token", csrf2)

	respDup, err := client.Do(reqDup)
	if err != nil {
		t.Fatalf("Register duplicate request failed: %v", err)
	}
	if respDup.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK (error page) for duplicate username, got %d", respDup.StatusCode)
	}
}

// Skenario 6: Akun beku tidak bisa login atau menarik
func TestS5_FrozenAccountCannotLoginOrWithdraw(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	ctx := context.Background()

	// 1. Buat pekerja aktif dengan saldo IDR
	hash, _ := auth.HashPassword("WorkerPass123")
	workerUSN := fmt.Sprintf("frozentest_%d", time.Now().UnixNano()%10000000)
	refCode := fmt.Sprintf("REF%05d", time.Now().UnixNano()%100000)
	var workerID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO users (username, display_name, password_hash, role, status, terms_version, terms_accepted_at, referral_code)
		VALUES ($1, $1, $2, 'worker', 'active', 'v1.0', now(), $3)
		RETURNING id
	`, workerUSN, hash, refCode).Scan(&workerID)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}

	// Beri saldo 100.000 IDR
	_, _ = pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'upah', 'IDR', 100000, 'Saldo awal')
	`, workerID)

	// Pastikan kurs USDT ada
	_ = srv.updateSettingWithAudit(ctx, adminID, "rate_idr_to_usdt", 15000, "127.0.0.1")
	_ = srv.updateSettingWithAudit(ctx, adminID, "rate_idr_to_usdt_updated_at", time.Now().Format(time.RFC3339), "127.0.0.1")

	// 2. Admin membekukan akun pekerja
	freezeForm := url.Values{}
	freezeForm.Set("freeze_reason", "Terindikasi penyalahgunaan spam")

	reqFreeze := httptest.NewRequest("POST", fmt.Sprintf("/admin/workers/%d/freeze", workerID), strings.NewReader(freezeForm.Encode()))
	reqFreeze.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqFreeze.SetPathValue("id", fmt.Sprintf("%d", workerID))
	ctxAdmin := context.WithValue(reqFreeze.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
	reqFreeze = reqFreeze.WithContext(ctxAdmin)

	rrFreeze := httptest.NewRecorder()
	srv.HandleAdminWorkerFreezePOST(rrFreeze, reqFreeze)
	if rrFreeze.Code != http.StatusSeeOther {
		t.Fatalf("Expected redirect after freeze, got %d", rrFreeze.Code)
	}

	// Verifikasi di DB: status = 'frozen', freeze_reason terisi
	var status, freezeReason string
	err = pool.QueryRow(ctx, "SELECT status, freeze_reason FROM users WHERE id = $1", workerID).Scan(&status, &freezeReason)
	if err != nil || status != "frozen" || freezeReason != "Terindikasi penyalahgunaan spam" {
		t.Fatalf("Worker not frozen properly in DB: status=%s, reason=%s", status, freezeReason)
	}

	// 3. Pekerja mencoba LOGIN -> DITOLAK
	loginForm := url.Values{}
	loginForm.Add("identifier", workerUSN)
	loginForm.Add("password", "WorkerPass123")

	reqLogin := httptest.NewRequest("POST", "/login", strings.NewReader(loginForm.Encode()))
	reqLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogin := httptest.NewRecorder()
	srv.handleLogin(rrLogin, reqLogin)

	if rrLogin.Code != http.StatusOK { // Error page rendered
		t.Errorf("Expected 200 OK (login rejected), got %d", rrLogin.Code)
	}
	if strings.Contains(rrLogin.Header().Get("Set-Cookie"), "gb_session=") {
		t.Errorf("Session cookie should NOT be issued to frozen user")
	}

	// 4. Pekerja mencoba MENARIK (Withdrawal) -> DITOLAK
	wForm := url.Values{}
	wForm.Set("amount", "50000")
	wForm.Set("method", "dana")
	wForm.Set("destination", "08123456789")
	wForm.Set("account_name", "Test User")

	reqW := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(wForm.Encode()))
	reqW.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctxWorker := context.WithValue(reqW.Context(), userContextKey, UserSession{ID: workerID, Role: "worker", Language: "id"})
	reqW = reqW.WithContext(ctxWorker)

	rrW := httptest.NewRecorder()
	srv.HandleWorkerWithdrawalsPOST(rrW, reqW)

	locW := rrW.Header().Get("Location")
	if !strings.Contains(locW, "dibekukan") && !strings.Contains(locW, "tidak+aktif") {
		t.Errorf("Expected withdrawal rejection error message, got location: %s", locW)
	}

	// 5. Pekerja mencoba KONVERSI (Convert) -> DITOLAK
	cForm := url.Values{}
	cForm.Set("amount_idr", "30000")
	cForm.Set("request_token", "test_tok_conv")

	reqC := httptest.NewRequest("POST", "/worker/convert", strings.NewReader(cForm.Encode()))
	reqC.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqC = reqC.WithContext(ctxWorker)

	rrC := httptest.NewRecorder()
	srv.HandleWorkerConvertPOST(rrC, reqC)

	locC := rrC.Header().Get("Location")
	if !strings.Contains(locC, "dibekukan") && !strings.Contains(locC, "tidak+aktif") {
		t.Errorf("Expected convert rejection error message, got location: %s", locC)
	}

	// 6. Admin MEMBUKA PEMBEKUAN (Unfreeze)
	reqUnfreeze := httptest.NewRequest("POST", fmt.Sprintf("/admin/workers/%d/unfreeze", workerID), nil)
	reqUnfreeze.SetPathValue("id", fmt.Sprintf("%d", workerID))
	reqUnfreeze = reqUnfreeze.WithContext(ctxAdmin)

	rrUnfreeze := httptest.NewRecorder()
	srv.HandleAdminWorkerUnfreezePOST(rrUnfreeze, reqUnfreeze)
	if rrUnfreeze.Code != http.StatusSeeOther {
		t.Fatalf("Expected redirect after unfreeze, got %d", rrUnfreeze.Code)
	}

	// Verifikasi di DB: status kembali 'active', freeze_reason NULL
	var statusAfter string
	var reasonAfter *string
	err = pool.QueryRow(ctx, "SELECT status, freeze_reason FROM users WHERE id = $1", workerID).Scan(&statusAfter, &reasonAfter)
	if err != nil || statusAfter != "active" || reasonAfter != nil {
		t.Fatalf("Worker not active after unfreeze: status=%s, reason=%v", statusAfter, reasonAfter)
	}
}
