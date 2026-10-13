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
	"goblast/internal/auth"
	"goblast/internal/db"
	"goblast/internal/i18n"
	"goblast/internal/testutil"
	"goblast/internal/withdraw"
)

type fakeWorkController struct {
	stoppedAccounts []int64
}

func (f *fakeWorkController) StartAccount(accountID, workerID int64) {}
func (f *fakeWorkController) StopAccount(accountID int64) {
	f.stoppedAccounts = append(f.stoppedAccounts, accountID)
}
func (f *fakeWorkController) IsRunning(accountID int64) bool { return false }
func (f *fakeWorkController) RunningCount() int                { return 0 }

func setupAdminTest(t *testing.T) (*Server, *pgxpool.Pool, int64, int64) {
	t.Helper()
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	_ = i18n.Load("locales")

	pool := testutil.TestDB(t)

	// Create admin user
	nonce := time.Now().UnixNano()
	var adminID, workerID int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, username, password_hash, role, referral_code, display_name)
		VALUES ($1, $2, 'hash', 'admin', $3, 'Admin Test')
		RETURNING id
	`, fmt.Sprintf("admin_%d@test.com", nonce), fmt.Sprintf("admin_%d", nonce), fmt.Sprintf("REFADM%d", nonce)).Scan(&adminID)
	if err != nil {
		t.Fatalf("Failed to create test admin: %v", err)
	}

	// Create worker user
	err = pool.QueryRow(context.Background(), `
		INSERT INTO users (email, username, password_hash, role, referral_code, display_name)
		VALUES ($1, $2, 'hash', 'worker', $3, 'Worker Test')
		RETURNING id
	`, fmt.Sprintf("worker_%d@test.com", nonce), fmt.Sprintf("worker_%d", nonce), fmt.Sprintf("REFWKR%d", nonce)).Scan(&workerID)
	if err != nil {
		t.Fatalf("Failed to create test worker: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		var campIDs []int64
		rows, err := pool.Query(ctx, "SELECT id FROM campaigns WHERE created_by = $1", adminID)
		if err == nil {
			for rows.Next() {
				var cid int64
				if err := rows.Scan(&cid); err == nil {
					campIDs = append(campIDs, cid)
				}
			}
			rows.Close()
		}
		testutil.CleanupTestData(pool, []int64{adminID, workerID}, campIDs)
	})

	srv, err := NewServer(&db.DB{Pool: pool}, "test", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	srv.EncryptionKey = testutil.TestEncryptionKey()

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
		{"POST", "/admin/campaigns/1/pause"},
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
		`id="daily_limit_fast" name="daily_limit_fast" class="input" min="0" value="" placeholder="wajib diisi"`,
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

	// 1. Delete draft campaign with linked ledger entry -> MUST BE REJECTED!
	reqDel := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/delete", draftID), nil)
	reqDel.SetPathValue("id", fmt.Sprintf("%d", draftID))
	reqDel = reqDel.WithContext(ctxAdmin)
	rrDel := httptest.NewRecorder()
	srv.HandleAdminCampaignDelete(rrDel, reqDel)

	if !strings.Contains(rrDel.Header().Get("Location"), "err=") {
		t.Errorf("Expected redirect with err when deleting draft campaign with ledger entries, got location: %s", rrDel.Header().Get("Location"))
	}

	var count int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaigns WHERE id = $1", draftID).Scan(&count)
	if count != 1 {
		t.Errorf("Draft campaign with ledger should NOT be deleted!")
	}

	// 2. Draft campaign with sent numbers ('terkirim') -> MUST BE REJECTED!
	var draftSentID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Draft with Sent Numbers', 'draf', 'kartu_link', 'Test body', '{}', $1)
		RETURNING id
	`, adminID).Scan(&draftSentID)
	if err != nil {
		t.Fatalf("Failed to create draft campaign: %v", err)
	}
	var batchSentID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		VALUES ($1, 'sent.csv', $2, 1, 1)
		RETURNING id
	`, draftSentID, adminID).Scan(&batchSentID)
	if err != nil {
		t.Fatalf("Failed to create number batch: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status)
		VALUES ($1, $2, '628999888777', '62', 'terkirim')
	`, draftSentID, batchSentID)
	if err != nil {
		t.Fatalf("Failed to create sent campaign number: %v", err)
	}

	reqDelSent := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/delete", draftSentID), nil)
	reqDelSent.SetPathValue("id", fmt.Sprintf("%d", draftSentID))
	reqDelSent = reqDelSent.WithContext(ctxAdmin)
	rrDelSent := httptest.NewRecorder()
	srv.HandleAdminCampaignDelete(rrDelSent, reqDelSent)

	if !strings.Contains(rrDelSent.Header().Get("Location"), "err=") {
		t.Errorf("Expected redirect with err when deleting campaign with sent numbers, got location: %s", rrDelSent.Header().Get("Location"))
	}
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaigns WHERE id = $1", draftSentID).Scan(&count)
	if count != 1 {
		t.Errorf("Draft campaign with sent numbers should NOT be deleted!")
	}

	// 3. Empty draft campaign -> MUST SUCCEED!
	var emptyDraftID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Empty Draft Test', 'draf', 'kartu_link', 'Test body', '{}', $1)
		RETURNING id
	`, adminID).Scan(&emptyDraftID)
	if err != nil {
		t.Fatalf("Failed to create empty draft campaign: %v", err)
	}

	reqDelEmpty := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/delete", emptyDraftID), nil)
	reqDelEmpty.SetPathValue("id", fmt.Sprintf("%d", emptyDraftID))
	reqDelEmpty = reqDelEmpty.WithContext(ctxAdmin)
	rrDelEmpty := httptest.NewRecorder()
	srv.HandleAdminCampaignDelete(rrDelEmpty, reqDelEmpty)

	if rrDelEmpty.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 after deleting empty draft campaign, got %d", rrDelEmpty.Code)
	}
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaigns WHERE id = $1", emptyDraftID).Scan(&count)
	if count != 0 {
		t.Errorf("Empty draft campaign should be deleted from DB!")
	}

	// 4. Create a running campaign -> MUST BE BLOCKED!
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

func TestAdminCampaignArchiveAndNumbersUpload(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	ctx := context.Background()
	ctxAdmin := context.WithValue(context.Background(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})

	// 1. Create a draft campaign
	var campID int64
	uniqueName := fmt.Sprintf("Arsip Test Camp %d", time.Now().UnixNano())
	err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ($1, 'draf', 'kartu_link', 'Test body', '{}', $2)
		RETURNING id
	`, uniqueName, adminID).Scan(&campID)
	if err != nil {
		t.Fatalf("Failed to create campaign: %v", err)
	}

	// 2. Archive campaign
	reqArchive := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/archive", campID), nil)
	reqArchive.SetPathValue("id", fmt.Sprintf("%d", campID))
	reqArchive = reqArchive.WithContext(ctxAdmin)
	rrArchive := httptest.NewRecorder()
	srv.HandleAdminCampaignArchive(rrArchive, reqArchive)

	if rrArchive.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 after archiving campaign, got %d", rrArchive.Code)
	}

	var archivedAt *time.Time
	_ = pool.QueryRow(ctx, "SELECT archived_at FROM campaigns WHERE id = $1", campID).Scan(&archivedAt)
	if archivedAt == nil {
		t.Fatalf("Expected campaign archived_at to be set!")
	}

	// 3. Verify archived campaign is hidden from default list
	reqList := httptest.NewRequest("GET", "/admin/campaigns", nil)
	reqList = reqList.WithContext(ctxAdmin)
	rrList := httptest.NewRecorder()
	srv.HandleAdminCampaigns(rrList, reqList)

	if strings.Contains(rrList.Body.String(), uniqueName) {
		t.Errorf("Default campaign list should HIDE archived campaign, but it was found in response!")
	}
	if !strings.Contains(rrList.Body.String(), "Tampilkan Arsip") {
		t.Errorf("Default campaign list should have 'Tampilkan Arsip' link!")
	}

	// 4. Verify archived campaign is shown when ?show_archived=1
	reqListArchived := httptest.NewRequest("GET", "/admin/campaigns?show_archived=1", nil)
	reqListArchived = reqListArchived.WithContext(ctxAdmin)
	rrListArchived := httptest.NewRecorder()
	srv.HandleAdminCampaigns(rrListArchived, reqListArchived)

	if !strings.Contains(rrListArchived.Body.String(), uniqueName) {
		t.Errorf("Campaign list with ?show_archived=1 should show archived campaign!")
	}
	if !strings.Contains(rrListArchived.Body.String(), "Arsip") {
		t.Errorf("Campaign list should display 'Arsip' badge for archived campaign!")
	}

	// 5. Verify archiving running campaign is rejected
	var runCampID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Running Camp Archive Test', 'berjalan', 'kartu_link', 'Test body', '{}', $1)
		RETURNING id
	`, adminID).Scan(&runCampID)

	reqRunArchive := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/archive", runCampID), nil)
	reqRunArchive.SetPathValue("id", fmt.Sprintf("%d", runCampID))
	reqRunArchive = reqRunArchive.WithContext(ctxAdmin)
	rrRunArchive := httptest.NewRecorder()
	srv.HandleAdminCampaignArchive(rrRunArchive, reqRunArchive)

	if !strings.Contains(rrRunArchive.Header().Get("Location"), "err=") {
		t.Errorf("Expected error redirect when archiving running campaign, got: %s", rrRunArchive.Header().Get("Location"))
	}

	// 6. Upload numbers to archived campaign is rejected
	// 6a. GET /admin/campaigns/{id}/numbers
	reqNumGET := httptest.NewRequest("GET", fmt.Sprintf("/admin/campaigns/%d/numbers", campID), nil)
	reqNumGET.SetPathValue("id", fmt.Sprintf("%d", campID))
	reqNumGET = reqNumGET.WithContext(ctxAdmin)
	rrNumGET := httptest.NewRecorder()
	srv.HandleAdminCampaignNumbersGET(rrNumGET, reqNumGET)

	if rrNumGET.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for numbers GET on archived campaign, got %d", rrNumGET.Code)
	}

	// 6b. POST /admin/campaigns/{id}/numbers/preview
	reqNumPrev := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/numbers/preview", campID), strings.NewReader("pasted=628123456789"))
	reqNumPrev.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqNumPrev.SetPathValue("id", fmt.Sprintf("%d", campID))
	reqNumPrev = reqNumPrev.WithContext(ctxAdmin)
	rrNumPrev := httptest.NewRecorder()
	srv.HandleAdminCampaignNumbersPreview(rrNumPrev, reqNumPrev)

	if rrNumPrev.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for numbers preview on archived campaign, got %d", rrNumPrev.Code)
	}

	// 6c. POST /admin/campaigns/{id}/numbers/confirm
	reqNumConf := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/numbers/confirm", campID), strings.NewReader("preview_id=test_preview"))
	reqNumConf.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqNumConf.SetPathValue("id", fmt.Sprintf("%d", campID))
	reqNumConf = reqNumConf.WithContext(ctxAdmin)
	rrNumConf := httptest.NewRecorder()
	srv.HandleAdminCampaignNumbersConfirm(rrNumConf, reqNumConf)

	if rrNumConf.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for numbers confirm on archived campaign, got %d", rrNumConf.Code)
	}

	// 7. Edit archived campaign is rejected
	reqEditGET := httptest.NewRequest("GET", fmt.Sprintf("/admin/campaigns/%d/edit", campID), nil)
	reqEditGET.SetPathValue("id", fmt.Sprintf("%d", campID))
	reqEditGET = reqEditGET.WithContext(ctxAdmin)
	rrEditGET := httptest.NewRecorder()
	srv.HandleAdminCampaignEditGET(rrEditGET, reqEditGET)

	if rrEditGET.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for edit GET on archived campaign, got %d", rrEditGET.Code)
	}
}

func TestAdminCampaignPauseAndResume(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	ctx := context.Background()
	ctxAdmin := context.WithValue(context.Background(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})

	// 1. Create a draft campaign with numbers
	var campID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Pause Resume Test', 'draf', 'kartu_link', 'Test body', '{}', $1)
		RETURNING id
	`, adminID).Scan(&campID)
	if err != nil {
		t.Fatalf("Failed to create campaign: %v", err)
	}

	var batchID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		VALUES ($1, 'test.csv', $2, 1, 1)
		RETURNING id
	`, campID, adminID).Scan(&batchID)
	if err != nil {
		t.Fatalf("Failed to create batch: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status)
		VALUES ($1, $2, '628119998881', '62', 'tersedia')
	`, campID, batchID)
	if err != nil {
		t.Fatalf("Failed to create number: %v", err)
	}

	// 2. Start campaign
	reqStart := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/start", campID), nil)
	reqStart.SetPathValue("id", fmt.Sprintf("%d", campID))
	reqStart = reqStart.WithContext(ctxAdmin)
	rrStart := httptest.NewRecorder()
	srv.HandleAdminCampaignStart(rrStart, reqStart)

	if rrStart.Code != http.StatusSeeOther {
		t.Fatalf("Expected 303 after starting campaign, got %d", rrStart.Code)
	}

	var status string
	var startedAt time.Time
	err = pool.QueryRow(ctx, "SELECT status, started_at FROM campaigns WHERE id = $1", campID).Scan(&status, &startedAt)
	if err != nil || status != "berjalan" {
		t.Fatalf("Expected status berjalan, got %s (err: %v)", status, err)
	}

	// 3. Pause campaign
	reqPause := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/pause", campID), nil)
	reqPause.SetPathValue("id", fmt.Sprintf("%d", campID))
	reqPause = reqPause.WithContext(ctxAdmin)
	rrPause := httptest.NewRecorder()
	srv.HandleAdminCampaignPause(rrPause, reqPause)

	if rrPause.Code != http.StatusSeeOther {
		t.Fatalf("Expected 303 after pausing campaign, got %d", rrPause.Code)
	}

	err = pool.QueryRow(ctx, "SELECT status FROM campaigns WHERE id = $1", campID).Scan(&status)
	if err != nil || status != "draf" {
		t.Fatalf("Expected status draf after pause, got %s (err: %v)", status, err)
	}

	// Verify pause audit log exists
	var auditCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs WHERE action = 'campaign_pause' AND entity_id = $1", fmt.Sprintf("%d", campID)).Scan(&auditCount)
	if auditCount < 1 {
		t.Errorf("Expected audit log for campaign_pause")
	}

	// 4. Pausing a non-running campaign should fail with 400
	reqPauseAgain := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/pause", campID), nil)
	reqPauseAgain.SetPathValue("id", fmt.Sprintf("%d", campID))
	reqPauseAgain = reqPauseAgain.WithContext(ctxAdmin)
	rrPauseAgain := httptest.NewRecorder()
	srv.HandleAdminCampaignPause(rrPauseAgain, reqPauseAgain)
	if rrPauseAgain.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 when pausing already-paused campaign, got %d", rrPauseAgain.Code)
	}

	// 5. Resume campaign -> must preserve original startedAt
	reqResume := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/start", campID), nil)
	reqResume.SetPathValue("id", fmt.Sprintf("%d", campID))
	reqResume = reqResume.WithContext(ctxAdmin)
	rrResume := httptest.NewRecorder()
	srv.HandleAdminCampaignStart(rrResume, reqResume)

	if rrResume.Code != http.StatusSeeOther {
		t.Fatalf("Expected 303 after resuming campaign, got %d", rrResume.Code)
	}

	var resumedStartedAt time.Time
	err = pool.QueryRow(ctx, "SELECT status, started_at FROM campaigns WHERE id = $1", campID).Scan(&status, &resumedStartedAt)
	if err != nil || status != "berjalan" {
		t.Fatalf("Expected status berjalan after resume, got %s", status)
	}
	if !resumedStartedAt.Equal(startedAt) {
		t.Errorf("Expected started_at to be preserved (%v), got %v", startedAt, resumedStartedAt)
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

	// 3. Admin resolves ticket with reply message
	rForm := url.Values{
		"reply_message": {"Laporan Anda sudah kami selesaikan, mutasi berhasil diproses."},
	}
	reqResolve := httptest.NewRequest("POST", fmt.Sprintf("/admin/tickets/%d/resolve", tID), strings.NewReader(rForm.Encode()))
	reqResolve.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqResolve.SetPathValue("id", strconv.FormatInt(tID, 10))
	reqResolve = reqResolve.WithContext(ctxAdmin)
	rrResolve := httptest.NewRecorder()
	srv.HandleAdminTicketResolvePOST(rrResolve, reqResolve)
	if rrResolve.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 redirect after resolving ticket, got %d", rrResolve.Code)
	}

	var status, replyMsg string
	_ = pool.QueryRow(context.Background(), "SELECT status, reply_message FROM support_tickets WHERE id = $1", tID).Scan(&status, &replyMsg)
	if status != "resolved" {
		t.Errorf("Expected ticket status 'resolved', got %s", status)
	}
	if replyMsg != "Laporan Anda sudah kami selesaikan, mutasi berhasil diproses." {
		t.Errorf("Expected reply_message to match, got %q", replyMsg)
	}

	// Verify notification was created for worker
	var notifTitle, notifMsg, notifKind string
	err = pool.QueryRow(context.Background(), `
		SELECT title, message, kind
		FROM notifications
		WHERE user_id = $1 AND ref_id = $2
		ORDER BY id DESC LIMIT 1
	`, workerID, tID).Scan(&notifTitle, &notifMsg, &notifKind)
	if err != nil {
		t.Fatalf("Expected notification for worker after ticket reply: %v", err)
	}
	if notifKind != "ticket_reply" || !strings.Contains(notifMsg, "mutasi berhasil diproses") {
		t.Errorf("Expected ticket_reply notification with custom reply message, got kind=%s msg=%s", notifKind, notifMsg)
	}

	// Verify worker can retrieve ticket via GET /worker/api/tickets
	ctxWorker := context.WithValue(context.Background(), userContextKey, UserSession{ID: workerID, Role: "worker", Language: "id"})
	reqWorkerTickets := httptest.NewRequest("GET", "/worker/api/tickets", nil).WithContext(ctxWorker)
	rrWorkerTickets := httptest.NewRecorder()
	srv.HandleWorkerTicketsAPI(rrWorkerTickets, reqWorkerTickets)
	if rrWorkerTickets.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from GET /worker/api/tickets, got %d", rrWorkerTickets.Code)
	}
	var ticketList []WorkerTicketItem
	if err := json.Unmarshal(rrWorkerTickets.Body.Bytes(), &ticketList); err != nil {
		t.Fatalf("Failed to parse tickets JSON: %v", err)
	}
	if len(ticketList) == 0 || ticketList[0].ReplyMessage != "Laporan Anda sudah kami selesaikan, mutasi berhasil diproses." {
		t.Errorf("Expected worker ticket list to include admin reply message")
	}

	// Verify worker can retrieve notifications via GET /worker/api/notifications
	reqWorkerNotifs := httptest.NewRequest("GET", "/worker/api/notifications", nil).WithContext(ctxWorker)
	rrWorkerNotifs := httptest.NewRecorder()
	srv.HandleWorkerNotificationsAPI(rrWorkerNotifs, reqWorkerNotifs)
	if rrWorkerNotifs.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from GET /worker/api/notifications, got %d", rrWorkerNotifs.Code)
	}
	var notifResp WorkerNotificationsResponse
	if err := json.Unmarshal(rrWorkerNotifs.Body.Bytes(), &notifResp); err != nil {
		t.Fatalf("Failed to parse notifications JSON: %v", err)
	}
	if notifResp.UnreadCount < 1 {
		t.Errorf("Expected unread_count >= 1, got %d", notifResp.UnreadCount)
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

	// Admin toggles broadcast ON again -> should insert/sync notification for worker
	rrBToggle2 := httptest.NewRecorder()
	srv.HandleAdminBroadcastTogglePOST(rrBToggle2, reqBToggle)
	if rrBToggle2.Code != http.StatusSeeOther {
		t.Errorf("Expected 303 after toggling broadcast on, got %d", rrBToggle2.Code)
	}
	_ = pool.QueryRow(context.Background(), "SELECT is_active FROM broadcasts WHERE id = $1", bID).Scan(&bActive)
	if !bActive {
		t.Errorf("Expected broadcast to be active after second toggle")
	}

	// Verify worker notifications includes broadcast
	reqWorkerNotifs2 := httptest.NewRequest("GET", "/worker/api/notifications", nil).WithContext(ctxWorker)
	rrWorkerNotifs2 := httptest.NewRecorder()
	srv.HandleWorkerNotificationsAPI(rrWorkerNotifs2, reqWorkerNotifs2)
	var notifResp2 WorkerNotificationsResponse
	_ = json.Unmarshal(rrWorkerNotifs2.Body.Bytes(), &notifResp2)
	foundBroadcast := false
	for _, n := range notifResp2.Notifications {
		if n.Kind == "broadcast" && n.Title == "Maintenance Pengumuman" {
			foundBroadcast = true
			break
		}
	}
	if !foundBroadcast {
		t.Errorf("Expected worker notifications to contain broadcast after toggle/sync")
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

func TestAdminMaintenanceToggleAndWorkerRedirect(t *testing.T) {
	srv, pool, adminID, workerID := setupAdminTest(t)
	ctx := context.Background()

	// Initial state: maintenance mode is off
	_, _ = pool.Exec(ctx, "DELETE FROM settings WHERE key = 'maintenance_mode'")

	// 1. Admin checks GET /admin/maintenance
	reqMaint := httptest.NewRequest("GET", "/admin/maintenance", nil)
	ctxAdmin := context.WithValue(reqMaint.Context(), userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})
	reqMaint = reqMaint.WithContext(ctxAdmin)
	rrMaint := httptest.NewRecorder()
	srv.HandleAdminMaintenanceGET(rrMaint, reqMaint)
	if rrMaint.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from GET /admin/maintenance, got %d", rrMaint.Code)
	}
	if !strings.Contains(rrMaint.Body.String(), "Pemeliharaan & Diagnostik Sistem") {
		t.Errorf("Expected page title in body, got: %s", rrMaint.Body.String())
	}

	// 2. Admin toggles maintenance mode ON
	reqToggle := httptest.NewRequest("POST", "/admin/maintenance/toggle", nil)
	reqToggle = reqToggle.WithContext(ctxAdmin)
	rrToggle := httptest.NewRecorder()
	srv.HandleAdminMaintenanceTogglePOST(rrToggle, reqToggle)
	if rrToggle.Code != http.StatusSeeOther {
		t.Fatalf("Expected 303 redirect after toggling maintenance, got %d", rrToggle.Code)
	}

	if !srv.IsMaintenanceMode(ctx) {
		t.Fatal("Expected maintenance mode to be true after toggle")
	}

	// 3. Worker visits /worker and gets redirected to /maintenance
	workerHandler := srv.RequireRole(srv.HandleWorkerHome, "worker")
	token, tokenHash, _ := auth.GenerateSessionToken()
	_, _ = pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, NOW() + INTERVAL '1 day', '127.0.0.1', 'test')
	`, workerID, tokenHash)

	reqWorker := httptest.NewRequest("GET", "/worker", nil)
	reqWorker.AddCookie(&http.Cookie{Name: "gb_session", Value: token})
	rrWorker := httptest.NewRecorder()
	workerHandler(rrWorker, reqWorker)
	if rrWorker.Code != http.StatusSeeOther || rrWorker.Header().Get("Location") != "/maintenance" {
		t.Fatalf("Expected redirect to /maintenance for worker during maintenance, got %d (location: %s)", rrWorker.Code, rrWorker.Header().Get("Location"))
	}

	// 4. Public endpoint GET /maintenance returns 200
	reqPublic := httptest.NewRequest("GET", "/maintenance", nil)
	rrPublic := httptest.NewRecorder()
	srv.HandleMaintenancePublicGET(rrPublic, reqPublic)
	if rrPublic.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from GET /maintenance, got %d", rrPublic.Code)
	}

	// 5. Admin toggles maintenance mode OFF
	rrToggle2 := httptest.NewRecorder()
	srv.HandleAdminMaintenanceTogglePOST(rrToggle2, reqToggle)
	if rrToggle2.Code != http.StatusSeeOther {
		t.Fatalf("Expected 303 redirect, got %d", rrToggle2.Code)
	}
	if srv.IsMaintenanceMode(ctx) {
		t.Fatal("Expected maintenance mode to be false after second toggle")
	}

	// 6. Worker visits /worker again and passes through
	rrWorker2 := httptest.NewRecorder()
	workerHandler(rrWorker2, reqWorker)
	if rrWorker2.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for worker after maintenance ended, got %d", rrWorker2.Code)
	}
}

func TestAdminWorkerDetailAndResetPassword(t *testing.T) {
	srv, pool, adminID, workerID := setupAdminTest(t)
	ctx := context.Background()

	// Setup payout data and accounts for worker
	encDest, _ := withdraw.EncryptDestination("081299998888", srv.EncryptionKey)
	_, _ = pool.Exec(ctx, `
		UPDATE users
		SET payout_method = 'dana', payout_account_name = 'Budi Worker', payout_dest_encrypted = $1
		WHERE id = $2
	`, encDest, workerID)

	// Add WA account
	_, _ = pool.Exec(ctx, `
		INSERT INTO wa_accounts (worker_id, label, jid_hash, speed, status)
		VALUES ($1, 'Akun WA Tes', 'jidhash123', 'medium', 'terhubung')
	`, workerID)

	ctxAdmin := context.WithValue(ctx, userContextKey, UserSession{ID: adminID, Role: "admin", Language: "id"})

	// 1. Admin views worker detail GET /admin/workers/{id}
	reqDetail := httptest.NewRequest("GET", fmt.Sprintf("/admin/workers/%d", workerID), nil)
	reqDetail.SetPathValue("id", strconv.FormatInt(workerID, 10))
	reqDetail = reqDetail.WithContext(ctxAdmin)
	rrDetail := httptest.NewRecorder()
	srv.HandleAdminWorkerDetailGET(rrDetail, reqDetail)
	if rrDetail.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from GET /admin/workers/{id}, got %d", rrDetail.Code)
	}
	body := rrDetail.Body.String()
	limit := len(body)
	if limit > 500 {
		limit = 500
	}
	if !strings.Contains(body, "Detail Pekerja:") || !strings.Contains(body, "Budi Worker") {
		t.Errorf("Expected worker details in HTML, got body snippet: %s", body[:limit])
	}

	// 2. Worker session exists before reset
	_, tokenHash, _ := auth.GenerateSessionToken()
	_, _ = pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, NOW() + INTERVAL '1 day', '127.0.0.1', 'test')
	`, workerID, tokenHash)

	// 3. Admin resets password with temporary auto password (empty new_password)
	reqReset := httptest.NewRequest("POST", fmt.Sprintf("/admin/workers/%d/reset-password", workerID), strings.NewReader("new_password="))
	reqReset.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqReset.SetPathValue("id", strconv.FormatInt(workerID, 10))
	reqReset = reqReset.WithContext(ctxAdmin)
	rrReset := httptest.NewRecorder()
	srv.HandleAdminWorkerResetPasswordPOST(rrReset, reqReset)

	if rrReset.Code != http.StatusSeeOther {
		t.Fatalf("Expected 303 redirect after reset password, got %d", rrReset.Code)
	}
	loc := rrReset.Header().Get("Location")
	if !strings.Contains(loc, "reset_pwd=") {
		t.Fatalf("Expected location to contain reset_pwd parameter, got %s", loc)
	}

	// Parse temporary password from redirect location
	u, _ := url.Parse(loc)
	newTempPwd := u.Query().Get("reset_pwd")
	if len(newTempPwd) != 12 {
		t.Fatalf("Expected 12-char temporary password, got %q", newTempPwd)
	}

	// Verify new password in DB
	var newHash string
	_ = pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1", workerID).Scan(&newHash)
	match, err := auth.VerifyPassword(newTempPwd, newHash)
	if err != nil || !match {
		t.Fatalf("Password verification failed for temporary password: match=%v, err=%v", match, err)
	}

	// Verify old session was deleted
	var sessCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM web_sessions WHERE user_id = $1", workerID).Scan(&sessCount)
	if sessCount != 0 {
		t.Errorf("Expected 0 active sessions after password reset, got %d", sessCount)
	}

	// Verify audit log entry
	var logAction string
	_ = pool.QueryRow(ctx, "SELECT action FROM audit_logs WHERE entity = 'users' AND entity_id = $1 ORDER BY id DESC LIMIT 1", strconv.FormatInt(workerID, 10)).Scan(&logAction)
	if logAction != "worker_reset_password" {
		t.Errorf("Expected audit log action 'worker_reset_password', got %q", logAction)
	}
}


