package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"goblast/internal/auth"
	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa"
)

type s3MockWorkController struct {
	runningCount int
}

func (m *s3MockWorkController) StartAccount(accountID, workerID int64) {}
func (m *s3MockWorkController) StopAccount(accountID int64)            {}
func (m *s3MockWorkController) IsRunning(accountID int64) bool         { return false }
func (m *s3MockWorkController) RunningCount() int                      { return m.runningCount }

func withWorkerSession(req *http.Request, workerID int64) *http.Request {
	ctx := context.WithValue(req.Context(), userContextKey, UserSession{
		ID:          workerID,
		Role:        "worker",
		Language:    "id",
		DisplayName: "Worker S3",
	})
	return req.WithContext(ctx)
}

func withAdminSession(req *http.Request, adminID int64) *http.Request {
	ctx := context.WithValue(req.Context(), userContextKey, UserSession{
		ID:          adminID,
		Role:        "admin",
		Language:    "id",
		DisplayName: "Admin S3",
	})
	return req.WithContext(ctx)
}

func setupS3TestServer(t *testing.T) (*Server, *db.DB, int64, int64, string, string) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}
	ctx := context.Background()

	// 1. Admin user
	var adminID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'admin', 'Admin S3', $2)
		 RETURNING id`,
		fmt.Sprintf("admin_s3_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REFADM_%d", time.Now().UnixNano()),
	).Scan(&adminID)
	if err != nil {
		t.Fatalf("setup admin gagal: %v", err)
	}

	adminToken, adminHash, _ := auth.GenerateSessionToken()
	_, _ = pool.Exec(ctx,
		`INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		 VALUES ($1, $2, now() + interval '1 day', '127.0.0.1', 'test-agent')`,
		adminID, adminHash,
	)

	// 2. Worker user
	var workerID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker S3', $2)
		 RETURNING id`,
		fmt.Sprintf("worker_s3_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REFWKR_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	workerToken, workerHash, _ := auth.GenerateSessionToken()
	_, _ = pool.Exec(ctx,
		`INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		 VALUES ($1, $2, now() + interval '1 day', '127.0.0.1', 'test-agent')`,
		workerID, workerHash,
	)

	// 32-byte encryption key
	encKey := []byte("12345678901234567890123456789012")

	srv, err := NewServer(database, "test", nil, &s3MockWorkController{runningCount: 0})
	if err != nil {
		t.Fatalf("NewServer gagal: %v", err)
	}
	srv.EncryptionKey = encKey

	cleanLocks := func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM settings WHERE key IN ('lock_withdrawals', 'lock_linking', 'lock_engine', 'lock_message', 'maintenance_mode')")
		srv.InvalidateSystemLockCache()
	}
	cleanLocks()
	t.Cleanup(cleanLocks)

	return srv, database, adminID, workerID, adminToken, workerToken
}

// 1. Tes Penolakan Tiap Kunci Granular (lock_withdrawals, lock_linking)
func TestGranularLocks_Rejections(t *testing.T) {
	srv, database, adminID, workerID, _, _ := setupS3TestServer(t)
	ctx := context.Background()

	// A. Uji lock_withdrawals
	// 1. Set lock_withdrawals = true
	if err := srv.updateSettingWithAudit(ctx, adminID, "lock_withdrawals", true, "127.0.0.1"); err != nil {
		t.Fatalf("update setting gagal: %v", err)
	}
	srv.InvalidateSystemLockCache()

	// Coba POST penarikan
	formW := url.Values{
		"amount":       {"20000"},
		"method":       {"dana"},
		"destination":  {"081200000001"},
		"account_name": {"Uji Worker"},
	}
	reqW := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(formW.Encode()))
	reqW.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqW = withWorkerSession(reqW, workerID)
	rrW := httptest.NewRecorder()

	srv.HandleWorkerWithdrawalsPOST(rrW, reqW)
	if rrW.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect on locked withdrawals, got %d", rrW.Code)
	}
	if !strings.Contains(rrW.Header().Get("Location"), "dikunci") {
		t.Errorf("expected redirect location with 'dikunci', got %s", rrW.Header().Get("Location"))
	}

	// Coba POST konversi
	formC := url.Values{
		"request_token": {"token-uji-123"},
		"amount_idr":    {"15000"},
	}
	reqC := httptest.NewRequest(http.MethodPost, "/worker/convert", strings.NewReader(formC.Encode()))
	reqC.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqC = withWorkerSession(reqC, workerID)
	rrC := httptest.NewRecorder()

	srv.HandleWorkerConvertPOST(rrC, reqC)
	if rrC.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect on locked convert, got %d", rrC.Code)
	}
	if !strings.Contains(rrC.Header().Get("Location"), "dikunci") {
		t.Errorf("expected redirect location with 'dikunci', got %s", rrC.Header().Get("Location"))
	}

	// B. Uji lock_linking
	if err := srv.updateSettingWithAudit(ctx, adminID, "lock_linking", true, "127.0.0.1"); err != nil {
		t.Fatalf("update setting gagal: %v", err)
	}
	srv.InvalidateSystemLockCache()

	// 1. Coba POST /worker/accounts/init
	reqInit := httptest.NewRequest(http.MethodPost, "/worker/accounts/init", nil)
	reqInit = withWorkerSession(reqInit, workerID)
	rrInit := httptest.NewRecorder()

	srv.HandleWorkerAccountInitPOST(rrInit, reqInit)
	if rrInit.Code != http.StatusForbidden {
		t.Errorf("expected 403 on locked account init, got %d", rrInit.Code)
	}
	var initResp map[string]interface{}
	_ = json.Unmarshal(rrInit.Body.Bytes(), &initResp)
	if initResp["success"] != false || !strings.Contains(fmt.Sprintf("%v", initResp["error"]), "dikunci") {
		t.Errorf("expected error message with 'dikunci', got %v", initResp)
	}

	// 2. Coba POST /worker/accounts/new
	formNew := url.Values{"label": {"WA Baru"}, "speed": {"medium"}}
	reqNew := httptest.NewRequest(http.MethodPost, "/worker/accounts/new", strings.NewReader(formNew.Encode()))
	reqNew.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqNew = withWorkerSession(reqNew, workerID)
	rrNew := httptest.NewRecorder()

	srv.HandleWorkerAccountNewPOST(rrNew, reqNew)
	if rrNew.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect on locked account new, got %d", rrNew.Code)
	}
	if !strings.Contains(rrNew.Header().Get("Location"), "dikunci") {
		t.Errorf("expected redirect with 'dikunci', got %s", rrNew.Header().Get("Location"))
	}

	// 3. Coba POST /worker/accounts/{id}/pair
	formPair := url.Values{"phone": {"081200000002"}, "format": {"json"}}
	reqPair := httptest.NewRequest(http.MethodPost, "/worker/accounts/1/pair", strings.NewReader(formPair.Encode()))
	reqPair.SetPathValue("id", "1")
	reqPair.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPair = withWorkerSession(reqPair, workerID)
	rrPair := httptest.NewRecorder()

	srv.HandleWorkerAccountPairPOST(rrPair, reqPair)
	if rrPair.Code != http.StatusForbidden {
		t.Errorf("expected 403 on locked pair, got %d", rrPair.Code)
	}

	// C. Buka kembali kunci dan pastikan tidak ditolak kunci
	_ = srv.updateSettingWithAudit(ctx, adminID, "lock_withdrawals", false, "127.0.0.1")
	_ = srv.updateSettingWithAudit(ctx, adminID, "lock_linking", false, "127.0.0.1")
	srv.InvalidateSystemLockCache()

	if srv.IsWithdrawalsLocked(ctx) {
		t.Errorf("expected IsWithdrawalsLocked to be false")
	}
	if srv.IsLinkingLocked(ctx) {
		t.Errorf("expected IsLinkingLocked to be false")
	}

	_ = database
}

// 2. Tes Engine Berhenti Saat lock_engine Aktif & Mode Pemeliharaan
func TestLockEngine_StopsEngineAndReason(t *testing.T) {
	srv, database, adminID, workerID, _, _ := setupS3TestServer(t)
	ctx := context.Background()

	// 1. Setup akun WhatsApp terhubung
	var accountID int64
	err := database.Pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, speed, status)
		 VALUES ($1, 'WA S3 Engine', $2, 'medium', 'terhubung')
		 RETURNING id`,
		workerID, fmt.Sprintf("jid_s3_%d", time.Now().UnixNano()),
	).Scan(&accountID)
	if err != nil {
		t.Fatalf("setup wa_account gagal: %v", err)
	}

	// 2. Setup campaign berjalan dengan 1 nomor
	var cID, bID, numID int64
	err = database.Pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, started_at)
		 VALUES ('Camp S3 Engine', 'berjalan', 'kartu_link', 'Halo', '{}', $1, now())
		 RETURNING id`, adminID,
	).Scan(&cID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}
	err = database.Pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'b_s3.csv', $2, 1, 1) RETURNING id`, cID, adminID,
	).Scan(&bID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}
	err = database.Pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status)
		 VALUES ($1, $2, '6281200000003', '62', 'tersedia')
		 RETURNING id`, cID, bID,
	).Scan(&numID)
	if err != nil {
		t.Fatalf("setup number gagal: %v", err)
	}

	// Settings reader untuk engine
	settingsReader := wa.NewDBSettingsReader(database)
	fakeSender := &wa.FakeSender{}
	eng := engine.New(database, fakeSender, nil, settingsReader)

	// A. Uji saat lock_engine = true
	_ = srv.updateSettingWithAudit(ctx, adminID, "lock_engine", true, "127.0.0.1")
	eng.InvalidateLockCache()

	// ClaimNextNumber harus menolak dengan ErrEngineLocked
	claimed, errClaim := eng.ClaimNextNumber(ctx, accountID)
	if errClaim != engine.ErrEngineLocked {
		t.Errorf("expected ErrEngineLocked, got err=%v, claimed=%v", errClaim, claimed)
	}

	// StartAccount lalu tunggu runWorker berhenti
	eng.StartAccount(accountID, workerID)
	time.Sleep(150 * time.Millisecond)

	// Periksa wa_accounts: stop_reason harus "sistem dalam perbaikan"
	var stopReason, waStatus string
	_ = database.Pool.QueryRow(ctx, "SELECT status, COALESCE(stop_reason, '') FROM wa_accounts WHERE id = $1", accountID).Scan(&waStatus, &stopReason)
	if stopReason != "sistem dalam perbaikan" {
		t.Errorf("expected stop_reason 'sistem dalam perbaikan', got '%s'", stopReason)
	}
	// Sesi WhatsApp TIDAK diputus
	if waStatus != "terhubung" {
		t.Errorf("expected wa_accounts.status to stay 'terhubung', got '%s'", waStatus)
	}

	// B. Buka lock_engine, lalu uji maintenance_mode = true
	_ = srv.updateSettingWithAudit(ctx, adminID, "lock_engine", false, "127.0.0.1")
	_ = srv.updateSettingWithAudit(ctx, adminID, "maintenance_mode", true, "127.0.0.1")
	eng.InvalidateLockCache()

	claimedM, errClaimM := eng.ClaimNextNumber(ctx, accountID)
	if errClaimM != engine.ErrEngineLocked {
		t.Errorf("expected ErrEngineLocked during maintenance_mode, got err=%v, claimed=%v", errClaimM, claimedM)
	}

	// C. Bersihkan maintenance_mode
	_ = srv.updateSettingWithAudit(ctx, adminID, "maintenance_mode", false, "127.0.0.1")
	eng.InvalidateLockCache()

	// Sekarang klaim harus berhasil
	claimedOk, errClaimOk := eng.ClaimNextNumber(ctx, accountID)
	if errClaimOk != nil {
		t.Errorf("expected successful claim when unlocked, got err=%v", errClaimOk)
	} else if claimedOk.ID != numID {
		t.Errorf("expected claimed ID %d, got %d", numID, claimedOk.ID)
	}
}

// 3. Tes Jendela Peringatan Tampil di Halaman Pekerja
func TestLockModal_RenderedInWorkerPages(t *testing.T) {
	srv, database, adminID, workerID, _, _ := setupS3TestServer(t)
	ctx := context.Background()

	// 1. Set kunci aktif & pesan admin
	pesanAdmin := "Sinkronisasi buku besar sedang berlangsung."
	_ = srv.updateSettingWithAudit(ctx, adminID, "lock_withdrawals", true, "127.0.0.1")
	_ = srv.updateSettingWithAudit(ctx, adminID, "lock_message", pesanAdmin, "127.0.0.1")
	srv.InvalidateSystemLockCache()

	// Request GET /worker (HandleWorkerHome)
	reqHome := httptest.NewRequest(http.MethodGet, "/worker", nil)
	reqHome = withWorkerSession(reqHome, workerID)
	rrHome := httptest.NewRecorder()

	srv.HandleWorkerHome(rrHome, reqHome)
	if rrHome.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /worker, got %d", rrHome.Code)
	}
	homeHTML := rrHome.Body.String()

	// Periksa modal ada, memuat fitur dikunci, pesan admin, dan tombol Mengerti
	if !strings.Contains(homeHTML, `id="system-lock-modal"`) {
		t.Errorf("expected system-lock-modal in /worker HTML")
	}
	if !strings.Contains(homeHTML, "Penarikan Saldo") {
		t.Errorf("expected locked feature 'Penarikan Saldo' in /worker HTML")
	}
	if !strings.Contains(homeHTML, pesanAdmin) {
		t.Errorf("expected admin message '%s' in /worker HTML", pesanAdmin)
	}
	if !strings.Contains(homeHTML, "Mengerti") {
		t.Errorf("expected 'Mengerti' button in /worker HTML")
	}
	if !strings.Contains(homeHTML, "dismissSystemLockModal") {
		t.Errorf("expected dismissSystemLockModal function in /worker HTML")
	}

	// Request GET /worker/accounts (HandleWorkerAccounts)
	reqAcc := httptest.NewRequest(http.MethodGet, "/worker/accounts", nil)
	reqAcc = withWorkerSession(reqAcc, workerID)
	rrAcc := httptest.NewRecorder()

	srv.HandleWorkerAccounts(rrAcc, reqAcc)
	if rrAcc.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /worker/accounts, got %d", rrAcc.Code)
	}
	accHTML := rrAcc.Body.String()
	if !strings.Contains(accHTML, `id="system-lock-modal"`) {
		t.Errorf("expected system-lock-modal in /worker/accounts HTML")
	}

	// 2. Matikan semua kunci
	_ = srv.updateSettingWithAudit(ctx, adminID, "lock_withdrawals", false, "127.0.0.1")
	srv.InvalidateSystemLockCache()

	rrHome2 := httptest.NewRecorder()
	srv.HandleWorkerHome(rrHome2, reqHome)
	homeHTML2 := rrHome2.Body.String()
	if strings.Contains(homeHTML2, `id="system-lock-modal"`) {
		t.Errorf("system-lock-modal should NOT be rendered when no lock is active")
	}

	_ = database
}

// 4. Tes Semua 9 Pemeriksaan Kesehatan (Keadaan OK & GAGAL/PERINGATAN)
func TestHealthChecks_AllNine_OkAndFail(t *testing.T) {
	srv, database, adminID, workerID, _, _ := setupS3TestServer(t)
	ctx := context.Background()

	// Pastikan kurs aktif di awal agar pemeriksaan kurs OK
	_ = srv.updateSettingWithAudit(ctx, adminID, "rate_idr_to_usdt", 15000, "127.0.0.1")
	_ = srv.updateSettingWithAudit(ctx, adminID, "rate_idr_to_usdt_updated_at", time.Now().Format(time.RFC3339), "127.0.0.1")

	// ==================== KEADAAN AWAL / OK ====================
	checks := srv.RunHealthChecks(ctx)
	checkMap := make(map[string]HealthCheckItem)
	for _, c := range checks {
		checkMap[c.Key] = c
	}

	requiredKeys := []string{
		"db_latency", "migration_version", "engine", "wa_sessions",
		"stale_numbers", "pending_withdrawals", "usdt_rate", "encryption",
		"ledger_reconciliation",
	}

	for _, k := range requiredKeys {
		item, exists := checkMap[k]
		if !exists {
			t.Errorf("missing health check key: %s", k)
			continue
		}
		// Di keadaan normal, status harus ok (atau peringatan wajar jika data WA 0)
		if item.Status == "gagal" {
			t.Errorf("health check '%s' unexpectedly failed in normal state: %s", k, item.Message)
		}
	}

	// ==================== KEADAAN GAGAL 1: Nomor Menggantung ====================
	// Rusak data sengaja: masukkan campaign_numbers berstatus 'dikirim' dengan claimed_at 1 jam lalu
	var cID, bID, staleNumID int64
	var err error
	err = database.Pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp Stale', 'berjalan', 'kartu_link', 'Halo', '{}', $1) RETURNING id`, adminID,
	).Scan(&cID)
	if err != nil {
		t.Fatalf("setup stale camp gagal: %v", err)
	}
	err = database.Pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'stale.csv', $2, 1, 1) RETURNING id`, cID, adminID,
	).Scan(&bID)
	if err != nil {
		t.Fatalf("setup stale batch gagal: %v", err)
	}
	err = database.Pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_at)
		 VALUES ($1, $2, '6281200000099', '62', 'dikirim', now() - interval '1 hour')
		 RETURNING id`, cID, bID,
	).Scan(&staleNumID)
	if err != nil {
		t.Fatalf("setup stale num gagal: %v", err)
	}

	checksStale := srv.RunHealthChecks(ctx)
	for _, c := range checksStale {
		if c.Key == "stale_numbers" {
			if c.Status != "gagal" {
				t.Errorf("expected stale_numbers to fail when stale number exists, got status=%s, msg=%s", c.Status, c.Message)
			}
			if !strings.Contains(c.Message, "menggantung") {
				t.Errorf("expected message to mention 'menggantung', got: %s", c.Message)
			}
		}
	}
	// Bersihkan data rusak
	_, _ = database.Pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE id = $1", staleNumID)

	// ==================== KEADAAN GAGAL 2: Rekonsiliasi Buku Besar (Saldo Negatif) ====================
	// Buat saldo negatif sengaja via kind 'koreksi'
	var badLedgerID int64
	err = database.Pool.QueryRow(ctx,
		`INSERT INTO ledger_entries (user_id, kind, currency, amount)
		 VALUES ($1, 'koreksi', 'IDR', -99999999)
		 RETURNING id`, workerID,
	).Scan(&badLedgerID)
	if err != nil {
		t.Fatalf("setup bad ledger gagal: %v", err)
	}

	checksLedger := srv.RunHealthChecks(ctx)
	for _, c := range checksLedger {
		if c.Key == "ledger_reconciliation" {
			if c.Status != "gagal" {
				t.Errorf("expected ledger_reconciliation to fail on negative balance, got status=%s", c.Status)
			}
			if !strings.Contains(c.Message, "saldo negatif") {
				t.Errorf("expected message to mention 'saldo negatif', got: %s", c.Message)
			}
		}
	}
	// Pulihkan saldo (karena ledger_entries append-only)
	_, _ = database.Pool.Exec(ctx, `INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'koreksi', 'IDR', 99999999)`, workerID)

	// ==================== KEADAAN GAGAL 3: Rekonsiliasi Buku Besar (Nomor Terkirim vs Upah) ====================
	// Buat nomor terkirim tanpa upah
	var unrewardedNumID int64
	err = database.Pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, sent_at)
		 VALUES ($1, $2, '6281200000098', '62', 'terkirim', now())
		 RETURNING id`, cID, bID,
	).Scan(&unrewardedNumID)
	if err != nil {
		t.Fatalf("setup unrewarded num gagal: %v", err)
	}

	checksDiff := srv.RunHealthChecks(ctx)
	for _, c := range checksDiff {
		if c.Key == "ledger_reconciliation" {
			if c.Status != "gagal" {
				t.Errorf("expected ledger_reconciliation to fail on mismatch wage vs sent, got status=%s", c.Status)
			}
			if !strings.Contains(c.Message, "Selisih") {
				t.Errorf("expected message to mention 'Selisih', got: %s", c.Message)
			}
		}
	}
	// Bersihkan nomor
	_, _ = database.Pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE id = $1", unrewardedNumID)

	// ==================== KEADAAN PERINGATAN 4: Penarikan Menunggu > 24 Jam ====================
	var oldWID int64
	err = database.Pool.QueryRow(ctx,
		`INSERT INTO withdrawals (user_id, currency, amount, method, destination_encrypted, status, requested_at)
		 VALUES ($1, 'IDR', 50000, 'dana', 'enc', 'menunggu', now() - interval '26 hours')
		 RETURNING id`, workerID,
	).Scan(&oldWID)
	if err != nil {
		t.Fatalf("setup overdue withdrawal gagal: %v", err)
	}

	checksW := srv.RunHealthChecks(ctx)
	for _, c := range checksW {
		if c.Key == "pending_withdrawals" {
			if c.Status != "peringatan" {
				t.Errorf("expected pending_withdrawals to warn when withdrawal > 24h, got status=%s", c.Status)
			}
			if !strings.Contains(c.Message, "24 jam") {
				t.Errorf("expected message to mention 24 jam, got: %s", c.Message)
			}
		}
	}
	// Bersihkan withdrawal
	_, _ = database.Pool.Exec(ctx, "DELETE FROM withdrawals WHERE id = $1", oldWID)

	// ==================== KEADAAN PERINGATAN 5: Kurs USDT Basi ====================
	_ = srv.updateSettingWithAudit(ctx, adminID, "rate_idr_to_usdt_updated_at", time.Now().Add(-30*time.Hour).Format(time.RFC3339), "127.0.0.1")
	checksKurs := srv.RunHealthChecks(ctx)
	for _, c := range checksKurs {
		if c.Key == "usdt_rate" {
			if c.Status != "peringatan" {
				t.Errorf("expected usdt_rate to warn on stale rate, got status=%s", c.Status)
			}
			if !strings.Contains(c.Message, "kedaluwarsa") {
				t.Errorf("expected message to mention kedaluwarsa, got: %s", c.Message)
			}
		}
	}
	// Pulihkan kurs segar
	_ = srv.updateSettingWithAudit(ctx, adminID, "rate_idr_to_usdt_updated_at", time.Now().Format(time.RFC3339), "127.0.0.1")

	// ==================== KEADAAN GAGAL 6: Kunci Enkripsi Rusak ====================
	origKey := srv.EncryptionKey
	srv.EncryptionKey = []byte("short-bad-key")
	checksEnc := srv.RunHealthChecks(ctx)
	for _, c := range checksEnc {
		if c.Key == "encryption" {
			if c.Status != "gagal" {
				t.Errorf("expected encryption to fail with bad key, got status=%s", c.Status)
			}
		}
	}
	srv.EncryptionKey = origKey
}

// 5. Tes Pekerja Tidak Bisa Membuka Halaman Admin
func TestWorkerCannotAccessAdmin(t *testing.T) {
	srv, _, _, _, _, workerToken := setupS3TestServer(t)

	adminRoutes := []string{
		"/admin/maintenance",
		"/admin/settings",
		"/admin/campaigns",
	}

	for _, route := range adminRoutes {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		req.AddCookie(&http.Cookie{Name: "gb_session", Value: workerToken})
		rr := httptest.NewRecorder()

		// Bungkus dengan RequireRole role admin
		srv.RequireRole(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}, "admin")(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("worker accessing %s expected 403 Forbidden, got %d", route, rr.Code)
		}
	}
}

// 6. Tes Perubahan Kunci Tercatat di Audit Log
func TestLockChange_RecordedInAudit(t *testing.T) {
	srv, database, adminID, _, _, _ := setupS3TestServer(t)
	ctx := context.Background()

	// Kirim POST /admin/maintenance/locks
	formLocks := url.Values{
		"lock_withdrawals": {"1"},
		"lock_linking":     {"1"},
		"lock_engine":      {"0"},
		"lock_message":     {"Uji audit pencatatan kunci"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/maintenance/locks", strings.NewReader(formLocks.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withAdminSession(req, adminID)
	rr := httptest.NewRecorder()

	srv.HandleAdminMaintenanceLocksPOST(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on saving locks, got %d", rr.Code)
	}

	// Verifikasi audit log mencatat aksi setting_update
	var countAudit int
	err := database.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs
		WHERE actor_id = $1 AND action = 'setting_update' AND entity = 'settings'
		  AND entity_id IN ('lock_withdrawals', 'lock_linking', 'lock_engine', 'lock_message')
	`, adminID).Scan(&countAudit)
	if err != nil {
		t.Fatalf("query audit_logs gagal: %v", err)
	}
	if countAudit < 4 {
		t.Errorf("expected at least 4 audit logs for 4 keys, got %d", countAudit)
	}

	// Verifikasi nilai tersimpan di database
	locks := srv.GetSystemLocks(ctx)
	if !locks.LockWithdrawals {
		t.Errorf("expected LockWithdrawals to be true")
	}
	if !locks.LockLinking {
		t.Errorf("expected LockLinking to be true")
	}
	if locks.LockEngine {
		t.Errorf("expected LockEngine to be false")
	}
	if locks.LockMessage != "Uji audit pencatatan kunci" {
		t.Errorf("expected LockMessage 'Uji audit pencatatan kunci', got '%s'", locks.LockMessage)
	}
}
