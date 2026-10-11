package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"goblast/internal/auth"
	"goblast/internal/db"
	"goblast/internal/i18n"
	"goblast/internal/withdraw"
)

type fakeWorkController struct {
	stoppedAccounts []int64
}

func (f *fakeWorkController) StartAccount(accountID, workerID int64, currency string) {}
func (f *fakeWorkController) StopAccount(accountID int64) {
	f.stoppedAccounts = append(f.stoppedAccounts, accountID)
}
func (f *fakeWorkController) IsRunning(accountID int64) bool { return false }

func setupAdminTest(t *testing.T) (*Server, *pgxpool.Pool, int64, int64) {
	t.Helper()
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	_ = i18n.Load("locales")
	godotenv.Load()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skip("Database not configured: ", err)
	}
	t.Cleanup(func() { pool.Close() })

	// Create admin user
	nonce := time.Now().UnixNano()
	var adminID, workerID int64
	err = pool.QueryRow(context.Background(), `
		INSERT INTO users (email, username, password_hash, role, currency, referral_code, display_name)
		VALUES ($1, $2, 'hash', 'admin', 'IDR', $3, 'Admin Test')
		RETURNING id
	`, fmt.Sprintf("admin_%d@test.com", nonce), fmt.Sprintf("admin_%d", nonce), fmt.Sprintf("REFADM%d", nonce)).Scan(&adminID)
	if err != nil {
		t.Fatalf("Failed to create test admin: %v", err)
	}

	// Create worker user
	err = pool.QueryRow(context.Background(), `
		INSERT INTO users (email, username, password_hash, role, currency, referral_code, display_name)
		VALUES ($1, $2, 'hash', 'worker', 'IDR', $3, 'Worker Test')
		RETURNING id
	`, fmt.Sprintf("worker_%d@test.com", nonce), fmt.Sprintf("worker_%d", nonce), fmt.Sprintf("REFWKR%d", nonce)).Scan(&workerID)
	if err != nil {
		t.Fatalf("Failed to create test worker: %v", err)
	}

	srv, err := NewServer(&db.DB{Pool: pool}, "test", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	return srv, pool, adminID, workerID
}

// (a) pengaturan tidak valid ditolak
func TestAdminSettingsValidationRejected(t *testing.T) {
	srv, _, adminID, _ := setupAdminTest(t)

	validForm := func() url.Values {
		v := url.Values{}
		v.Set("csrf_token", "test-csrf")
		v.Set("speed_delay_fast", "2")
		v.Set("speed_delay_medium", "5")
		v.Set("speed_delay_slow", "15")
		v.Set("speed_delay_over_slow", "30")
		v.Set("daily_limit_fast", "1000")
		v.Set("daily_limit_medium", "500")
		v.Set("daily_limit_slow", "200")
		v.Set("daily_limit_over_slow", "100")
		v.Set("max_attempts", "3")
		v.Set("max_consecutive_failures", "5")
		v.Set("claim_timeout_seconds", "300")
		v.Set("max_accounts_per_worker", "5")
		v.Set("max_active_sessions", "50")
		v.Set("default_country_code", "62")
		v.Set("rate_wage_idr", "50")
		v.Set("rate_wage_usdt", "3000")
		v.Set("rate_referral_idr", "10")
		v.Set("rate_referral_usdt", "500")
		return v
	}

	testCases := []struct {
		name      string
		mutate    func(v url.Values)
		errSubstr string
	}{
		{
			name: "speed_delays too low (0)",
			mutate: func(v url.Values) {
				v.Set("speed_delay_fast", "0")
			},
		},
		{
			name: "speed_delays too high (601)",
			mutate: func(v url.Values) {
				v.Set("speed_delay_medium", "601")
			},
		},
		{
			name: "daily_limits empty",
			mutate: func(v url.Values) {
				v.Set("daily_limit_fast", "")
			},
		},
		{
			name: "daily_limits negative (-1)",
			mutate: func(v url.Values) {
				v.Set("daily_limit_slow", "-1")
			},
		},
		{
			name: "max_attempts too low (0)",
			mutate: func(v url.Values) {
				v.Set("max_attempts", "0")
			},
		},
		{
			name: "max_attempts too high (11)",
			mutate: func(v url.Values) {
				v.Set("max_attempts", "11")
			},
		},
		{
			name: "max_consecutive_failures too low (0)",
			mutate: func(v url.Values) {
				v.Set("max_consecutive_failures", "0")
			},
		},
		{
			name: "max_consecutive_failures too high (51)",
			mutate: func(v url.Values) {
				v.Set("max_consecutive_failures", "51")
			},
		},
		{
			name: "claim_timeout_seconds too low (60)",
			mutate: func(v url.Values) {
				v.Set("claim_timeout_seconds", "60")
			},
		},
		{
			name: "claim_timeout_seconds too high (3601)",
			mutate: func(v url.Values) {
				v.Set("claim_timeout_seconds", "3601")
			},
		},
		{
			name: "max_accounts_per_worker too high (51)",
			mutate: func(v url.Values) {
				v.Set("max_accounts_per_worker", "51")
			},
		},
		{
			name: "max_active_sessions too high (501)",
			mutate: func(v url.Values) {
				v.Set("max_active_sessions", "501")
			},
		},
		{
			name: "default_country_code invalid (99999)",
			mutate: func(v url.Values) {
				v.Set("default_country_code", "99999")
			},
		},
		{
			name: "default_country_code letters",
			mutate: func(v url.Values) {
				v.Set("default_country_code", "xyz")
			},
		},
		{
			name: "rate_wage empty",
			mutate: func(v url.Values) {
				v.Set("rate_wage_idr", "")
			},
		},
		{
			name: "rate_wage 0",
			mutate: func(v url.Values) {
				v.Set("rate_wage_idr", "0")
			},
		},
		{
			name: "rate_referral negative",
			mutate: func(v url.Values) {
				v.Set("rate_referral_idr", "-5")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			form := validForm()
			tc.mutate(form)

			req := httptest.NewRequest("POST", "/admin/settings", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			ctx := context.WithValue(req.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			srv.HandleAdminSettingsPOST(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("Expected status 400 Bad Request for %s, got %d", tc.name, rr.Code)
			}
		})
	}
}

// (b) mengubah rate tidak mengubah baris buku besar lama
func TestAdminRateChangeDoesNotAlterOldLedger(t *testing.T) {
	srv, pool, adminID, workerID := setupAdminTest(t)

	// Insert old ledger row with rate 50
	var ledgerID int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, note)
		VALUES ($1, 'upah', 'IDR', 50, 50, 'upah pesan terkirim awal')
		RETURNING id
	`, workerID).Scan(&ledgerID)
	if err != nil {
		t.Fatalf("Failed to insert initial ledger: %v", err)
	}

	// Submit settings update changing rate_wage to IDR 120, USDT 4500
	form := url.Values{}
	form.Set("csrf_token", "test-csrf")
	form.Set("speed_delay_fast", "1")
	form.Set("speed_delay_medium", "5")
	form.Set("speed_delay_slow", "15")
	form.Set("speed_delay_over_slow", "30")
	form.Set("daily_limit_fast", "1000")
	form.Set("daily_limit_medium", "500")
	form.Set("daily_limit_slow", "200")
	form.Set("daily_limit_over_slow", "100")
	form.Set("max_attempts", "3")
	form.Set("max_consecutive_failures", "5")
	form.Set("claim_timeout_seconds", "300")
	form.Set("max_accounts_per_worker", "5")
	form.Set("max_active_sessions", "50")
	form.Set("default_country_code", "62")
	form.Set("rate_wage_idr", "120")
	form.Set("rate_wage_usdt", "4500")
	form.Set("rate_referral_idr", "10")
	form.Set("rate_referral_usdt", "500")

	req := httptest.NewRequest("POST", "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	srv.HandleAdminSettingsPOST(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("Expected 303 redirect after saving settings, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	// Verify old ledger entry has NOT changed
	var oldAmount, oldSnapshot int64
	err = pool.QueryRow(context.Background(), `
		SELECT amount, rate_snapshot FROM ledger_entries WHERE id = $1
	`, ledgerID).Scan(&oldAmount, &oldSnapshot)
	if err != nil {
		t.Fatalf("Failed to re-query ledger entry: %v", err)
	}

	if oldAmount != 50 || oldSnapshot != 50 {
		t.Errorf("Old ledger entry was modified! Expected amount=50, snapshot=50; got amount=%d, snapshot=%d", oldAmount, oldSnapshot)
	}
}

// (c) perubahan pengaturan tercatat di audit dengan nilai lama dan baru
func TestAdminSettingsAuditLogRecordedOldAndNew(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)

	// Step 1: Set initial claim_timeout_seconds = 250
	form1 := url.Values{}
	form1.Set("speed_delay_fast", "1")
	form1.Set("speed_delay_medium", "5")
	form1.Set("speed_delay_slow", "15")
	form1.Set("speed_delay_over_slow", "30")
	form1.Set("daily_limit_fast", "1000")
	form1.Set("daily_limit_medium", "500")
	form1.Set("daily_limit_slow", "200")
	form1.Set("daily_limit_over_slow", "100")
	form1.Set("max_attempts", "3")
	form1.Set("max_consecutive_failures", "5")
	form1.Set("claim_timeout_seconds", "250")
	form1.Set("max_accounts_per_worker", "5")
	form1.Set("max_active_sessions", "50")
	form1.Set("default_country_code", "62")
	form1.Set("rate_wage_idr", "50")
	form1.Set("rate_wage_usdt", "3000")

	req1 := httptest.NewRequest("POST", "/admin/settings", strings.NewReader(form1.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx1 := context.WithValue(req1.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
	req1 = req1.WithContext(ctx1)
	rr1 := httptest.NewRecorder()
	srv.HandleAdminSettingsPOST(rr1, req1)
	if rr1.Code != http.StatusSeeOther {
		t.Fatalf("Step 1 settings save failed: %d", rr1.Code)
	}

	// Step 2: Change claim_timeout_seconds = 450
	form2 := form1
	form2.Set("claim_timeout_seconds", "450")

	req2 := httptest.NewRequest("POST", "/admin/settings", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx2 := context.WithValue(req2.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
	req2 = req2.WithContext(ctx2)
	rr2 := httptest.NewRecorder()
	srv.HandleAdminSettingsPOST(rr2, req2)
	if rr2.Code != http.StatusSeeOther {
		t.Fatalf("Step 2 settings save failed: %d", rr2.Code)
	}

	// Check audit log for claim_timeout_seconds
	var metaJSON string
	var actorID int64
	var action string
	err := pool.QueryRow(context.Background(), `
		SELECT actor_id, action, meta::text 
		FROM audit_logs 
		WHERE entity = 'settings' AND entity_id = 'claim_timeout_seconds'
		ORDER BY id DESC LIMIT 1
	`).Scan(&actorID, &action, &metaJSON)
	if err != nil {
		t.Fatalf("Failed to query audit_logs: %v", err)
	}

	if actorID != adminID {
		t.Errorf("Expected audit actor_id = %d, got %d", adminID, actorID)
	}
	if action != "setting_update" {
		t.Errorf("Expected audit action = 'setting_update', got %s", action)
	}

	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		t.Fatalf("Failed to parse meta JSON: %v", err)
	}

	oldVal, okOld := meta["old"].(float64)
	newVal, okNew := meta["new"].(float64)
	if !okOld || int(oldVal) != 250 {
		t.Errorf("Expected meta.old == 250, got %+v", meta["old"])
	}
	if !okNew || int(newVal) != 450 {
		t.Errorf("Expected meta.new == 450, got %+v", meta["new"])
	}
}

// (d) pekerja membuka /admin/* atau /admin/api/stats mendapat 403
func TestWorkerAccessToAdminRoutesForbidden(t *testing.T) {
	srv, pool, _, workerID := setupAdminTest(t)

	// Create session for worker
	rawToken, hashToken, _ := auth.GenerateSessionToken()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, now() + interval '1 day', '127.0.0.1', 'GoTest')
	`, workerID, hashToken)
	if err != nil {
		t.Fatalf("Failed to create worker session: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	adminRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/admin"},
		{"GET", "/admin/api/stats"},
		{"GET", "/admin/settings"},
		{"POST", "/admin/settings"},
		{"GET", "/admin/wallet"},
		{"POST", "/admin/wallet"},
		{"GET", "/admin/workers"},
		{"POST", "/admin/workers/1/disable"},
		{"POST", "/admin/workers/1/enable"},
		{"GET", "/admin/campaigns"},
		{"GET", "/admin/tickets"},
		{"GET", "/admin/broadcasts"},
		{"GET", "/admin/nonexistent_route"},
	}

	for _, route := range adminRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, nil)
			req.AddCookie(&http.Cookie{Name: "gb_session", Value: rawToken})
			req.AddCookie(&http.Cookie{Name: "gb_csrf", Value: "test-csrf"})
			req.Header.Set("X-CSRF-Token", "test-csrf")

			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Errorf("Expected status 403 Forbidden for worker accessing %s %s, got %d", route.method, route.path, rr.Code)
			}
		})
	}
}

// (e) kunci yang keputusannya terbuka tampil kosong tanpa bawaan
func TestOpenDecisionKeysRenderedEmptyWithoutDefaults(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)

	// Ensure open decision keys are removed from DB
	_, _ = pool.Exec(context.Background(), `
		DELETE FROM settings WHERE key IN (
			'daily_limits', 'max_attempts', 'max_consecutive_failures',
			'max_accounts_per_worker', 'max_active_sessions', 'rate_wage'
		)
	`)

	req := httptest.NewRequest("GET", "/admin/settings", nil)
	ctx := context.WithValue(req.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	srv.HandleAdminSettingsGET(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from GET /admin/settings, got %d", rr.Code)
	}

	body := rr.Body.String()

	// Verify open decision inputs have empty value and "wajib diisi"
	checks := []string{
		`id="rate_wage_idr" name="rate_wage_idr" class="input" min="1" value="" placeholder="wajib diisi"`,
		`id="rate_wage_usdt" name="rate_wage_usdt" class="input" min="1" value="" placeholder="wajib diisi"`,
		`id="daily_limit_fast" name="daily_limit_fast" class="input" min="0" value="" placeholder="wajib diisi"`,
		`id="max_attempts" name="max_attempts" class="input" min="1" max="10" value="" placeholder="wajib diisi"`,
		`id="max_consecutive_failures" name="max_consecutive_failures" class="input" min="1" max="50" value="" placeholder="wajib diisi"`,
		`id="max_accounts_per_worker" name="max_accounts_per_worker" class="input" min="1" max="50" value="" placeholder="wajib diisi"`,
		`id="max_active_sessions" name="max_active_sessions" class="input" min="1" max="500" value="" placeholder="wajib diisi"`,
	}

	for _, snippet := range checks {
		if !strings.Contains(body, snippet) {
			t.Errorf("Expected snippet not found in rendered settings HTML:\n%s\nBody: %s", snippet, body)
		}
	}
}

// (f) saldo dompet sama dengan isi ulang dikurangi upah (uji dengan angka)
func TestAdminWalletBalanceEqualsTopupMinusWages(t *testing.T) {
	srv, pool, adminID, workerID := setupAdminTest(t)

	// Fetch initial baseline wallet data
	before, err := srv.fetchWalletData(context.Background())
	if err != nil {
		t.Fatalf("fetchWalletData before failed: %v", err)
	}

	// 1. Top-up IDR 500,000
	_, err = pool.Exec(context.Background(), `
		INSERT INTO wallet_entries (currency, amount, note, created_by)
		VALUES ('IDR', 500000, 'Top-up awal test', $1)
	`, adminID)
	if err != nil {
		t.Fatalf("Failed to insert wallet entry: %v", err)
	}

	// 2. Worker earns upah IDR 50 + IDR 50 + referral IDR 10 = IDR 110
	_, err = pool.Exec(context.Background(), `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, note)
		VALUES 
			($1, 'upah', 'IDR', 50, 50, 'upah 1'),
			($1, 'upah', 'IDR', 50, 50, 'upah 2'),
			($1, 'referral', 'IDR', 10, 10, 'bonus ref')
	`, workerID)
	if err != nil {
		t.Fatalf("Failed to insert ledger entries: %v", err)
	}

	// 3. Top-up USDT 1,000,000 micro-USDT (1 USDT)
	_, err = pool.Exec(context.Background(), `
		INSERT INTO wallet_entries (currency, amount, note, created_by)
		VALUES ('USDT', 1000000, 'Top-up USDT test', $1)
	`, adminID)
	if err != nil {
		t.Fatalf("Failed to insert wallet USDT entry: %v", err)
	}

	// 4. Worker earns upah USDT 3,000 micro-USDT
	_, err = pool.Exec(context.Background(), `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, note)
		VALUES ($1, 'upah', 'USDT', 3000, 3000, 'upah usdt')
	`, workerID)
	if err != nil {
		t.Fatalf("Failed to insert ledger USDT: %v", err)
	}

	// Fetch wallet data after
	wData, err := srv.fetchWalletData(context.Background())
	if err != nil {
		t.Fatalf("fetchWalletData failed: %v", err)
	}

	// Test IDR delta: topup +500,000, spent +110, delta balance = +499,890
	if deltaTopup := wData.TopupIDR - before.TopupIDR; deltaTopup != 500000 {
		t.Errorf("IDR Topup delta incorrect! Expected 500000, got %d", deltaTopup)
	}
	if deltaSpent := wData.SpentIDR - before.SpentIDR; deltaSpent != 110 {
		t.Errorf("IDR Spent delta incorrect! Expected 110, got %d", deltaSpent)
	}
	expectedDeltaBalanceIDR := int64(500000 - 110) // 499,890
	if deltaBal := wData.BalanceIDR - before.BalanceIDR; deltaBal != expectedDeltaBalanceIDR {
		t.Errorf("IDR Wallet delta balance incorrect! Expected %d, got %d", expectedDeltaBalanceIDR, deltaBal)
	}
	if wData.BalanceIDR != wData.TopupIDR-wData.SpentIDR {
		t.Errorf("IDR Balance != Topup - Spent! (%d != %d - %d)", wData.BalanceIDR, wData.TopupIDR, wData.SpentIDR)
	}

	// Test USDT delta: topup +1,000,000, spent +3,000, delta balance = +997,000
	if deltaTopup := wData.TopupUSDT - before.TopupUSDT; deltaTopup != 1000000 {
		t.Errorf("USDT Topup delta incorrect! Expected 1000000, got %d", deltaTopup)
	}
	if deltaSpent := wData.SpentUSDT - before.SpentUSDT; deltaSpent != 3000 {
		t.Errorf("USDT Spent delta incorrect! Expected 3000, got %d", deltaSpent)
	}
	expectedDeltaBalanceUSDT := int64(1000000 - 3000) // 997,000
	if deltaBal := wData.BalanceUSDT - before.BalanceUSDT; deltaBal != expectedDeltaBalanceUSDT {
		t.Errorf("USDT Wallet delta balance incorrect! Expected %d, got %d", expectedDeltaBalanceUSDT, deltaBal)
	}
	if wData.BalanceUSDT != wData.TopupUSDT-wData.SpentUSDT {
		t.Errorf("USDT Balance != Topup - Spent! (%d != %d - %d)", wData.BalanceUSDT, wData.TopupUSDT, wData.SpentUSDT)
	}
}

// (g) menonaktifkan pekerja memanggil StopAccount (pakai pengendali palsu) dan menghapus sesi webnya
func TestDisableWorkerStopsAccountAndClearsWebSessions(t *testing.T) {
	srv, pool, adminID, workerID := setupAdminTest(t)

	// Equip server with fakeWorkController
	fakeCtrl := &fakeWorkController{}
	srv.WorkCtrl = fakeCtrl

	// Create 2 WhatsApp accounts for worker
	var acc1, acc2 int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
		VALUES ($1, 'Account 1', 'hash1', 'terhubung')
		RETURNING id
	`, workerID).Scan(&acc1)
	if err != nil {
		t.Fatalf("Failed to insert account 1: %v", err)
	}

	err = pool.QueryRow(context.Background(), `
		INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
		VALUES ($1, 'Account 2', 'hash2', 'terhubung')
		RETURNING id
	`, workerID).Scan(&acc2)
	if err != nil {
		t.Fatalf("Failed to insert account 2: %v", err)
	}

	// Create web_sessions for worker
	_, _ = pool.Exec(context.Background(), `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, 'tokenhashworker1', now() + interval '1 day', '127.0.0.1', 'TestWorkerSession')
	`, workerID)

	// Execute POST /admin/workers/{id}/disable
	req := httptest.NewRequest("POST", fmt.Sprintf("/admin/workers/%d/disable", workerID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", workerID))
	ctx := context.WithValue(req.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	srv.HandleAdminWorkerDisablePOST(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("Expected 303 redirect after disable worker, got %d", rr.Code)
	}

	// 1. Verify StopAccount was called for both accounts
	if len(fakeCtrl.stoppedAccounts) != 2 {
		t.Errorf("Expected fakeWorkController to stop 2 accounts, stopped %d: %+v", len(fakeCtrl.stoppedAccounts), fakeCtrl.stoppedAccounts)
	}

	// 2. Verify all web_sessions for worker are deleted
	var sessionCount int
	err = pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM web_sessions WHERE user_id = $1", workerID).Scan(&sessionCount)
	if err != nil {
		t.Fatalf("Failed to count web_sessions: %v", err)
	}
	if sessionCount != 0 {
		t.Errorf("Expected 0 web_sessions for disabled worker, found %d", sessionCount)
	}

	// 3. Verify worker user status is 'disabled'
	var workerStatus string
	err = pool.QueryRow(context.Background(), "SELECT status FROM users WHERE id = $1", workerID).Scan(&workerStatus)
	if err != nil {
		t.Fatalf("Failed to get worker status: %v", err)
	}
	if workerStatus != "disabled" {
		t.Errorf("Expected worker status 'disabled', got %s", workerStatus)
	}

	// 4. Verify audit log entry
	var auditAction string
	err = pool.QueryRow(context.Background(), `
		SELECT action FROM audit_logs 
		WHERE entity = 'users' AND entity_id = $1 AND action = 'worker_disable'
		ORDER BY id DESC LIMIT 1
	`, fmt.Sprintf("%d", workerID)).Scan(&auditAction)
	if err != nil {
		t.Errorf("Audit log for worker_disable not found: %v", err)
	}
}

func TestAdminWithdrawalsApproveAndReject(t *testing.T) {
	srv, pool, adminID, workerID := setupAdminTest(t)
	ctx := context.Background()

	// Initial balance for worker: 500,000 IDR
	_, err := pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'upah', 'IDR', 500000, 'saldo awal test')
	`, workerID)
	if err != nil {
		t.Fatalf("Failed to credit initial balance: %v", err)
	}

	encDest, _ := withdraw.EncryptDestination("BCA 12345678 (Budi)", srv.EncryptionKey)

	// Create withdrawal 1 (100,000 IDR)
	var w1ID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO withdrawals (user_id, currency, amount, method, destination_encrypted, status)
		VALUES ($1, 'IDR', 100000, 'bca', $2, 'menunggu')
		RETURNING id
	`, workerID, encDest).Scan(&w1ID)
	if err != nil {
		t.Fatalf("Failed to create withdrawal 1: %v", err)
	}
	_, _ = pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'penarikan', 'IDR', -100000, 'Pengajuan penarikan #1')
	`, workerID)

	// Create withdrawal 2 (50,000 IDR)
	var w2ID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO withdrawals (user_id, currency, amount, method, destination_encrypted, status)
		VALUES ($1, 'IDR', 50000, 'bca', $2, 'menunggu')
		RETURNING id
	`, workerID, encDest).Scan(&w2ID)
	if err != nil {
		t.Fatalf("Failed to create withdrawal 2: %v", err)
	}
	_, _ = pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'penarikan', 'IDR', -50000, 'Pengajuan penarikan #2')
	`, workerID)

	// Verify current balance is 350,000
	var curBal int64
	_ = pool.QueryRow(ctx, "SELECT user_balance($1, 'IDR')", workerID).Scan(&curBal)
	if curBal != 350000 {
		t.Fatalf("Expected worker balance 350000, got %d", curBal)
	}

	// 1. GET /admin/withdrawals
	reqGet := httptest.NewRequest("GET", "/admin/withdrawals", nil)
	ctxAdmin := context.WithValue(reqGet.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
	reqGet = reqGet.WithContext(ctxAdmin)
	rrGet := httptest.NewRecorder()
	srv.HandleAdminWithdrawalsGET(rrGet, reqGet)
	if rrGet.Code != http.StatusOK {
		t.Errorf("Expected GET /admin/withdrawals 200 OK, got %d", rrGet.Code)
	}

	// 2. Approve withdrawal 1
	reqApprove := httptest.NewRequest("POST", fmt.Sprintf("/admin/withdrawals/%d/approve", w1ID), nil)
	reqApprove.SetPathValue("id", fmt.Sprintf("%d", w1ID))
	reqApprove = reqApprove.WithContext(ctxAdmin)
	rrApprove := httptest.NewRecorder()
	srv.HandleAdminWithdrawalApprovePOST(rrApprove, reqApprove)
	if rrApprove.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 after approve, got %d", rrApprove.Code)
	}

	var status1 string
	_ = pool.QueryRow(ctx, "SELECT status FROM withdrawals WHERE id = $1", w1ID).Scan(&status1)
	if status1 != "selesai" {
		t.Errorf("Expected status selesai for withdrawal 1, got %s", status1)
	}

	// 3. Reject withdrawal 2 (must refund 50,000)
	reqReject := httptest.NewRequest("POST", fmt.Sprintf("/admin/withdrawals/%d/reject", w2ID), nil)
	reqReject.SetPathValue("id", fmt.Sprintf("%d", w2ID))
	reqReject = reqReject.WithContext(ctxAdmin)
	rrReject := httptest.NewRecorder()
	srv.HandleAdminWithdrawalRejectPOST(rrReject, reqReject)
	if rrReject.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 after reject, got %d", rrReject.Code)
	}

	var status2 string
	_ = pool.QueryRow(ctx, "SELECT status FROM withdrawals WHERE id = $1", w2ID).Scan(&status2)
	if status2 != "ditolak" {
		t.Errorf("Expected status ditolak for withdrawal 2, got %s", status2)
	}

	// Verify worker balance was refunded to 400,000 (350,000 + 50,000)
	_ = pool.QueryRow(ctx, "SELECT user_balance($1, 'IDR')", workerID).Scan(&curBal)
	if curBal != 400000 {
		t.Errorf("Expected worker balance refunded to 400000, got %d", curBal)
	}

	// Verify refund entry exists in ledger
	var refundCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'koreksi' AND amount = 50000", workerID).Scan(&refundCount)
	if refundCount != 1 {
		t.Errorf("Expected 1 refund entry in ledger, got %d", refundCount)
	}
}

func TestAdminCampaignDelete(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	ctx := context.Background()
	ctxAdmin := context.WithValue(context.Background(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})

	// 1. Create a draft campaign with batches, numbers, and ledger entries
	var draftID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Draft Campaign Test', 'draf', 'kartu_link', 'Test body', '{}', $1)
		RETURNING id
	`, adminID).Scan(&draftID)
	if err != nil {
		t.Fatalf("Failed to create draft campaign: %v", err)
	}

	var batchID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		VALUES ($1, 'test.csv', $2, 1, 1)
		RETURNING id
	`, draftID, adminID).Scan(&batchID)
	if err != nil {
		t.Fatalf("Failed to create number batch: %v", err)
	}

	var numID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status)
		VALUES ($1, $2, '628111222333', '62', 'tersedia')
		RETURNING id
	`, draftID, batchID).Scan(&numID)
	if err != nil {
		t.Fatalf("Failed to create campaign number: %v", err)
	}

	var ledgerEntryID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id, note)
		VALUES ($1, 'upah', 'IDR', 10, $2, 'test ledger linked')
		RETURNING id
	`, adminID, numID).Scan(&ledgerEntryID)
	if err != nil {
		t.Fatalf("Failed to insert linked ledger entry: %v", err)
	}

	// Delete draft campaign -> must succeed and remove batches, numbers, unlinking ledger
	reqDel := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/delete", draftID), nil)
	reqDel.SetPathValue("id", fmt.Sprintf("%d", draftID))
	reqDel = reqDel.WithContext(ctxAdmin)
	rrDel := httptest.NewRecorder()
	srv.HandleAdminCampaignDelete(rrDel, reqDel)

	if rrDel.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 after deleting draft campaign, got %d: %s", rrDel.Code, rrDel.Body.String())
	}

	var count int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaigns WHERE id = $1", draftID).Scan(&count)
	if count != 0 {
		t.Errorf("Draft campaign was not deleted from DB!")
	}
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM number_batches WHERE campaign_id = $1", draftID).Scan(&count)
	if count != 0 {
		t.Errorf("Number batches were not deleted from DB!")
	}
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1", draftID).Scan(&count)
	if count != 0 {
		t.Errorf("Campaign numbers were not deleted from DB!")
	}

	var unlinkedNumID *int64
	_ = pool.QueryRow(ctx, "SELECT campaign_number_id FROM ledger_entries WHERE id = $1", ledgerEntryID).Scan(&unlinkedNumID)
	if unlinkedNumID != nil {
		t.Errorf("Expected ledger entry to have campaign_number_id unlinked (NULL), got %v", *unlinkedNumID)
	}

	// 2. Create a running campaign
	var runningID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Running Campaign Test', 'berjalan', 'kartu_link', 'Test body', '{}', $1)
		RETURNING id
	`, adminID).Scan(&runningID)
	if err != nil {
		t.Fatalf("Failed to create running campaign: %v", err)
	}

	// Attempt delete running campaign -> must be blocked!
	reqDelRun := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/delete", runningID), nil)
	reqDelRun.SetPathValue("id", fmt.Sprintf("%d", runningID))
	reqDelRun = reqDelRun.WithContext(ctxAdmin)
	rrDelRun := httptest.NewRecorder()
	srv.HandleAdminCampaignDelete(rrDelRun, reqDelRun)

	if !strings.Contains(rrDelRun.Header().Get("Location"), "err=") {
		t.Errorf("Expected redirect with err for running campaign deletion, got location: %s", rrDelRun.Header().Get("Location"))
	}

	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaigns WHERE id = $1", runningID).Scan(&count)
	if count != 1 {
		t.Errorf("Running campaign should NOT be deleted!")
	}
}

func TestAdminWorkersSearch(t *testing.T) {
	srv, _, adminID, _ := setupAdminTest(t)

	req := httptest.NewRequest("GET", "/admin/workers?q=Worker+Test", nil)
	ctx := context.WithValue(req.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	srv.HandleAdminWorkers(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for search workers, got %d", rr.Code)
	}
}

func TestAdminTicketsAndBroadcastsManagement(t *testing.T) {
	srv, pool, adminID, workerID := setupAdminTest(t)
	ctxAdmin := context.WithValue(context.Background(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})

	// 1. Worker creates a ticket
	var tID int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO support_tickets (user_id, category, message, status)
		VALUES ($1, 'Penarikan Dana', 'Kendala pencairan saldo', 'open')
		RETURNING id
	`, workerID).Scan(&tID)
	if err != nil {
		t.Fatalf("Failed to insert ticket: %v", err)
	}

	// 2. Admin views tickets
	reqGet := httptest.NewRequest("GET", "/admin/tickets?status=open", nil)
	reqGet = reqGet.WithContext(ctxAdmin)
	rrGet := httptest.NewRecorder()
	srv.HandleAdminTicketsGET(rrGet, reqGet)
	if rrGet.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for GET /admin/tickets, got %d", rrGet.Code)
	}
	if !strings.Contains(rrGet.Body.String(), "Kendala pencairan saldo") {
		t.Errorf("Expected tickets page to contain ticket message")
	}

	// 3. Admin resolves ticket
	reqResolve := httptest.NewRequest("POST", fmt.Sprintf("/admin/tickets/%d/resolve", tID), nil)
	reqResolve.SetPathValue("id", strconv.FormatInt(tID, 10))
	reqResolve = reqResolve.WithContext(ctxAdmin)
	rrResolve := httptest.NewRecorder()
	srv.HandleAdminTicketResolvePOST(rrResolve, reqResolve)
	if rrResolve.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 redirect after resolving ticket, got %d", rrResolve.Code)
	}

	var status string
	_ = pool.QueryRow(context.Background(), "SELECT status FROM support_tickets WHERE id = $1", tID).Scan(&status)
	if status != "resolved" {
		t.Errorf("Expected ticket status 'resolved', got %s", status)
	}

	// 4. Admin creates a broadcast
	bForm := url.Values{
		"title":     {"Maintenance Pengumuman"},
		"message":   {"Server akan mengalami pemeliharaan rutin."},
		"badge":     {"MAINTENANCE"},
		"is_active": {"true"},
	}
	reqBNew := httptest.NewRequest("POST", "/admin/broadcasts/new", strings.NewReader(bForm.Encode()))
	reqBNew.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqBNew = reqBNew.WithContext(ctxAdmin)
	rrBNew := httptest.NewRecorder()
	srv.HandleAdminBroadcastNewPOST(rrBNew, reqBNew)
	if rrBNew.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 after creating broadcast, got %d", rrBNew.Code)
	}

	var bID int64
	var bActive bool
	err = pool.QueryRow(context.Background(), `
		SELECT id, is_active FROM broadcasts WHERE title = 'Maintenance Pengumuman' ORDER BY id DESC LIMIT 1
	`).Scan(&bID, &bActive)
	if err != nil || !bActive {
		t.Fatalf("Broadcast not found or not active: %v", err)
	}

	// 5. Admin toggles broadcast
	reqBToggle := httptest.NewRequest("POST", fmt.Sprintf("/admin/broadcasts/%d/toggle", bID), nil)
	reqBToggle.SetPathValue("id", strconv.FormatInt(bID, 10))
	reqBToggle = reqBToggle.WithContext(ctxAdmin)
	rrBToggle := httptest.NewRecorder()
	srv.HandleAdminBroadcastTogglePOST(rrBToggle, reqBToggle)
	if rrBToggle.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 after toggling broadcast, got %d", rrBToggle.Code)
	}

	_ = pool.QueryRow(context.Background(), "SELECT is_active FROM broadcasts WHERE id = $1", bID).Scan(&bActive)
	if bActive {
		t.Errorf("Expected broadcast to be inactive after toggle")
	}

	// 6. Admin deletes broadcast
	reqBDel := httptest.NewRequest("POST", fmt.Sprintf("/admin/broadcasts/%d/delete", bID), nil)
	reqBDel.SetPathValue("id", strconv.FormatInt(bID, 10))
	reqBDel = reqBDel.WithContext(ctxAdmin)
	rrBDel := httptest.NewRecorder()
	srv.HandleAdminBroadcastDeletePOST(rrBDel, reqBDel)
	if rrBDel.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 after deleting broadcast, got %d", rrBDel.Code)
	}

	var count int
	_ = pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM broadcasts WHERE id = $1", bID).Scan(&count)
	if count != 0 {
		t.Errorf("Expected broadcast to be deleted")
	}
}

