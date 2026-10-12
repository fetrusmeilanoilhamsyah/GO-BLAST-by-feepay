package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

type mockWorkController struct {
	mu         sync.Mutex
	running    map[int64]bool
	startCount map[int64]int
	stopCount  map[int64]int
}

func newMockWorkController() *mockWorkController {
	return &mockWorkController{
		running:    make(map[int64]bool),
		startCount: make(map[int64]int),
		stopCount:  make(map[int64]int),
	}
}

func (m *mockWorkController) StartAccount(accountID, workerID int64, currency string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCount[accountID]++
	m.running[accountID] = true
}

func (m *mockWorkController) StopAccount(accountID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCount[accountID]++
	delete(m.running, accountID)
}

func (m *mockWorkController) IsRunning(accountID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[accountID]
}

func TestWorkerPanelFlows(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(root + "/locales")

	pool := testutil.TestDB(t)

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	// Create test worker user
	now := time.Now().UnixNano()
	email := fmt.Sprintf("worker_test_%d@example.com", now)
	refCode := fmt.Sprintf("RF%d", now)

	var workerID int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, password_hash, role, display_name, language, currency, referral_code)
		VALUES ($1, 'hash_pass', 'worker', 'Pekerja Uji', 'id', 'IDR', $2)
		RETURNING id
	`, email, refCode).Scan(&workerID)
	if err != nil {
		t.Fatalf("Failed to insert test worker: %v", err)
	}

	createdUserIDs := []int64{workerID}
	var createdCampaignIDs []int64

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, createdUserIDs, createdCampaignIDs)
	})

	srv, err := NewServer(&db.DB{Pool: pool}, "test", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	srv.EncryptionKey = testutil.TestEncryptionKey()

	userSess := UserSession{
		ID:          workerID,
		Role:        "worker",
		Email:       email,
		DisplayName: "Pekerja Uji",
		Language:    "id",
		Currency:    "IDR",
	}

	withWorker := func(req *http.Request) *http.Request {
		ctx := context.WithValue(req.Context(), userContextKey, userSess)
		return req.WithContext(ctx)
	}

	t.Run("HandleWorkerHome rendering", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/worker", nil)
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleWorkerHome(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "Pekerja Uji") {
			t.Errorf("Expected body to contain worker display name")
		}
		if !strings.Contains(body, "GO BLAST") {
			t.Errorf("Expected body to contain GO BLAST brand")
		}
		if !strings.Contains(body, "by feepay") {
			t.Errorf("Expected body to contain by feepay tagline")
		}
	})

	t.Run("HandleWorkerAccounts rendering", func(t *testing.T) {
		// insert test accounts with different status
		var acc1, acc2, acc3 int64
		_ = pool.QueryRow(context.Background(), `INSERT INTO wa_accounts (worker_id, label, jid_hash, speed, status) VALUES ($1, 'WA Scan', 'h1', 'medium', 'menunggu_scan') RETURNING id`, workerID).Scan(&acc1)
		_ = pool.QueryRow(context.Background(), `INSERT INTO wa_accounts (worker_id, label, jid_hash, speed, status) VALUES ($1, 'WA Terhubung', 'h2', 'medium', 'terhubung') RETURNING id`, workerID).Scan(&acc2)
		_ = pool.QueryRow(context.Background(), `INSERT INTO wa_accounts (worker_id, label, jid_hash, speed, status) VALUES ($1, 'WA Terputus', 'h3', 'medium', 'terputus') RETURNING id`, workerID).Scan(&acc3)

		req := httptest.NewRequest("GET", "/worker/accounts", nil)
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleWorkerAccounts(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "WA Terputus") {
			t.Errorf("Expected body to contain WA Terputus")
		}
		if !strings.Contains(body, fmt.Sprintf("/worker/accounts/%d/delete", acc3)) {
			t.Errorf("Expected body to contain delete form for acc3")
		}
		if !strings.Contains(body, fmt.Sprintf("openAddAccountModal(%d)", acc3)) && !strings.Contains(body, fmt.Sprintf("openAddAccountModal( %d )", acc3)) {
			t.Errorf("Expected body to contain reconnect button for acc3")
		}
		if !strings.Contains(body, fmt.Sprintf("/worker/accounts/%d/delete", acc1)) {
			t.Errorf("Expected body to contain delete form for acc1 (menunggu_scan)")
		}

		// Test: Worker deletes pending account via JSON (seperti saat batal tambah akun di modal)
		delReq := httptest.NewRequest("POST", fmt.Sprintf("/worker/accounts/%d/delete", acc1), nil)
		delReq.SetPathValue("id", strconv.FormatInt(acc1, 10))
		delReq.Header.Set("Accept", "application/json")
		delReq = withWorker(delReq)
		delRR := httptest.NewRecorder()

		srv.HandleWorkerAccountDelete(delRR, delReq)
		if delRR.Code != http.StatusOK {
			t.Errorf("Expected 200 for JSON delete, got %d", delRR.Code)
		}
		if !strings.Contains(delRR.Body.String(), `"ok":true`) {
			t.Errorf("Expected JSON response with ok:true, got %s", delRR.Body.String())
		}

		var countAcc1 int
		_ = pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM wa_accounts WHERE id = $1", acc1).Scan(&countAcc1)
		if countAcc1 != 0 {
			t.Errorf("Pending account was not deleted from DB")
		}
	})

	t.Run("HandleWorkerWithdrawalsGET rendering", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/worker/withdrawals", nil)
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleWorkerWithdrawalsGET(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "Penarikan") {
			t.Errorf("Expected body to contain Penarikan title")
		}
	})

	t.Run("HandleWorkerWithdrawalsPOST validation", func(t *testing.T) {
		// 1. Invalid amount
		{
			form := url.Values{
				"amount":       {"-5000"},
				"method":       {"dana"},
				"destination":  {"081234567890"},
				"account_name": {"Budi"},
			}
			req := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerWithdrawalsPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
				t.Errorf("Expected redirect with error for negative amount, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}
		}

		// 2. Unsupported method
		{
			form := url.Values{
				"amount":       {"10000"},
				"method":       {"paypal"},
				"destination":  {"budi@test.com"},
				"account_name": {"Budi"},
			}
			req := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerWithdrawalsPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
				t.Errorf("Expected redirect with error for unsupported method, got code %d", rr.Code)
			}
		}

		// 3. Insufficient balance (balance is 0)
		{
			form := url.Values{
				"amount":       {"50000"},
				"method":       {"dana"},
				"destination":  {"081234567890"},
				"account_name": {"Budi"},
			}
			req := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerWithdrawalsPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
				t.Errorf("Expected redirect with insufficient balance error, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}
		}

		// 4. Successful withdrawal with sufficient balance
		{
			// Give user balance via upah in ledger_entries
			_, err := pool.Exec(context.Background(), `
				INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
				VALUES ($1, 'upah', 'IDR', 100000, 'Upah tes')
			`, workerID)
			if err != nil {
				t.Fatalf("Failed to credit test balance: %v", err)
			}

			form := url.Values{
				"amount":       {"40000"},
				"method":       {"seabank"},
				"destination":  {"9012345678"},
				"account_name": {"Pekerja Uji"},
			}
			req := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerWithdrawalsPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "success=1") {
				t.Errorf("Expected redirect with success=1, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}

			// Verify remaining balance is 60,000
			var remaining int64
			_ = pool.QueryRow(context.Background(), "SELECT user_balance($1, 'IDR')", workerID).Scan(&remaining)
			if remaining != 60000 {
				t.Errorf("Expected remaining balance 60000, got %d", remaining)
			}
		}

		// 5. Penarikan 1-klik menggunakan wallet tersimpan tanpa mengetik ulang rekening
		{
			// Simpan wallet via HandleWorkerWalletPOST
			walletForm := url.Values{
				"method":       {"bca"},
				"destination":  {"1234567890"},
				"account_name": {"Pekerja Uji BCA"},
			}
			wReq := httptest.NewRequest("POST", "/worker/wallet", strings.NewReader(walletForm.Encode()))
			wReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			wReq = withWorker(wReq)
			wRR := httptest.NewRecorder()
			srv.HandleWorkerWalletPOST(wRR, wReq)
			if wRR.Code != http.StatusSeeOther || !strings.Contains(wRR.Header().Get("Location"), "success=") {
				t.Errorf("Expected wallet save success redirect, got code %d, loc %s", wRR.Code, wRR.Header().Get("Location"))
			}

			// Lakukan penarikan instan 20,000 hanya dengan memilih nominal
			form := url.Values{
				"amount": {"20000"},
			}
			req := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerWithdrawalsPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "success=1") {
				t.Errorf("Expected 1-click withdrawal redirect with success=1, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}

			// Sisa saldo sekarang 60,000 - 20,000 = 40,000
			var remaining int64
			_ = pool.QueryRow(context.Background(), "SELECT user_balance($1, 'IDR')", workerID).Scan(&remaining)
			if remaining != 40000 {
				t.Errorf("Expected remaining balance 40000, got %d", remaining)
			}
		}

		// 6. Penarikan gagal jika wallet belum diatur dan user tidak mengisi tujuan
		{
			var freshWorkerID int64
			_ = pool.QueryRow(context.Background(), `
				INSERT INTO users (email, password_hash, role, display_name, language, currency, referral_code)
				VALUES ($1, 'hash_pass', 'worker', 'Pekerja Kosong', 'id', 'IDR', $2)
				RETURNING id
			`, fmt.Sprintf("fresh_%d@example.com", time.Now().UnixNano()), fmt.Sprintf("RF_FRESH_%d", time.Now().UnixNano())).Scan(&freshWorkerID)
			createdUserIDs = append(createdUserIDs, freshWorkerID)

			_, _ = pool.Exec(context.Background(), `
				INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
				VALUES ($1, 'upah', 'IDR', 50000, 'Upah tes')
			`, freshWorkerID)

			freshSess := UserSession{
				ID:       freshWorkerID,
				Role:     "worker",
				Currency: "IDR",
			}
			req := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(url.Values{"amount": {"10000"}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = req.WithContext(context.WithValue(req.Context(), userContextKey, freshSess))
			rr := httptest.NewRecorder()

			srv.HandleWorkerWithdrawalsPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
				t.Errorf("Expected error redirect when wallet is not configured, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}
		}
	})

	t.Run("HandleWorkerReferralGET rendering", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/worker/referral", nil)
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleWorkerReferralGET(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, refCode) {
			t.Errorf("Expected body to contain referral code %s", refCode)
		}
		if !strings.Contains(body, "register?ref="+refCode) {
			t.Errorf("Expected body to contain referral link with refCode")
		}
	})

	t.Run("HandleWorkerProfilePOST updates display_name", func(t *testing.T) {
		form := url.Values{"display_name": {"Pekerja Hebat"}}
		req := httptest.NewRequest("POST", "/worker/profile", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleWorkerProfilePOST(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("Expected redirect 303, got %d", rr.Code)
		}

		var updatedName string
		_ = pool.QueryRow(context.Background(), "SELECT display_name FROM users WHERE id = $1", workerID).Scan(&updatedName)
		if updatedName != "Pekerja Hebat" {
			t.Errorf("Expected display_name 'Pekerja Hebat', got %q", updatedName)
		}
	})

	t.Run("HandleWorkerSecurityPOST validation and update", func(t *testing.T) {
		// Wrong old password
		{
			form := url.Values{
				"old_password":     {"WrongPass123!"},
				"new_password":     {"NewPass12345!"},
				"confirm_password": {"NewPass12345!"},
			}
			req := httptest.NewRequest("POST", "/worker/security", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerSecurityPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
				t.Errorf("Expected redirect with error for wrong old password, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}
		}

		// Valid password update
		{
			// First, set a known password hash for worker
			knownHash, _ := auth.HashPassword("OldSecretPass123!")
			_, _ = pool.Exec(context.Background(), "UPDATE users SET password_hash = $1 WHERE id = $2", knownHash, workerID)

			form := url.Values{
				"old_password":     {"OldSecretPass123!"},
				"new_password":     {"NewBrandSecret123!"},
				"confirm_password": {"NewBrandSecret123!"},
			}
			req := httptest.NewRequest("POST", "/worker/security", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerSecurityPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "success=") {
				t.Errorf("Expected redirect with success, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}

			// Verify new password verifies against db hash
			var newHashInDB string
			_ = pool.QueryRow(context.Background(), "SELECT password_hash FROM users WHERE id = $1", workerID).Scan(&newHashInDB)
			match, err := auth.VerifyPassword("NewBrandSecret123!", newHashInDB)
			if err != nil || !match {
				t.Errorf("Expected new password to verify against hash in DB, match=%v, err=%v", match, err)
			}
		}
	})

	t.Run("HandleLanguagePOST updates cookie and user preference", func(t *testing.T) {
		form := url.Values{"lang": {"en"}}
		req := httptest.NewRequest("POST", "/language", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleLanguagePOST(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("Expected 303, got %d", rr.Code)
		}

		// Verify cookie set
		cookies := rr.Result().Cookies()
		foundLang := false
		for _, c := range cookies {
			if c.Name == "lang" && c.Value == "en" {
				foundLang = true
				break
			}
		}
		if !foundLang {
			t.Errorf("Expected lang cookie set to 'en', got cookies: %v", cookies)
		}

		// Verify DB user language updated
		var userLang string
		_ = pool.QueryRow(context.Background(), "SELECT language FROM users WHERE id = $1", workerID).Scan(&userLang)
		if userLang != "en" {
			t.Errorf("Expected language in DB to be 'en', got %q", userLang)
		}
	})

	t.Run("HandleWorkerHistoryGET rendering and pagination", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/worker/history?page=1", nil)
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleWorkerHistoryGET(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "Riwayat Pengiriman Pesan") && !strings.Contains(body, "Sent Message History") {
			t.Errorf("Expected body to contain history title")
		}
	})

	t.Run("Worker pages do not leak customer phone numbers", func(t *testing.T) {
		// Fiktif nomor customer tes (bukan nomor asli, 100% fiktif)
		dummyCustomerPhone := "6289998887771"

		var campID int64
		_ = pool.QueryRow(context.Background(), `
			INSERT INTO campaigns (name, button_mode, status)
			VALUES ('Campaign Uji Privasi', 'kartu_link', 'berjalan')
			RETURNING id
		`).Scan(&campID)
		if campID > 0 {
			createdCampaignIDs = append(createdCampaignIDs, campID)
			var numID int64
			_ = pool.QueryRow(context.Background(), `
				INSERT INTO campaign_numbers (campaign_id, phone_e164, status)
				VALUES ($1, $2, 'terkirim')
				RETURNING id
			`, campID, dummyCustomerPhone).Scan(&numID)

			if numID > 0 {
				_, _ = pool.Exec(context.Background(), `
					INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id, note)
					VALUES ($1, 'upah', 'IDR', 100, $2, 'upah pesan terkirim')
					ON CONFLICT (campaign_number_id) WHERE kind = 'upah' DO NOTHING
				`, workerID, numID)
			}
		}

		endpoints := []string{"/worker", "/worker/history", "/worker/withdrawals", "/worker/referral"}
		for _, ep := range endpoints {
			req := httptest.NewRequest("GET", ep, nil)
			req = withWorker(req)
			rr := httptest.NewRecorder()

			switch ep {
			case "/worker":
				srv.HandleWorkerHome(rr, req)
			case "/worker/history":
				srv.HandleWorkerHistoryGET(rr, req)
			case "/worker/withdrawals":
				srv.HandleWorkerWithdrawalsGET(rr, req)
			case "/worker/referral":
				srv.HandleWorkerReferralGET(rr, req)
			}

			if strings.Contains(rr.Body.String(), dummyCustomerPhone) {
				t.Errorf("SECURITY LEAK: Customer phone number leaked in response of %s", ep)
			}
		}
	})

	t.Run("Worker A cannot view or modify Worker B account", func(t *testing.T) {
		// Buat worker B
		var workerBID int64
		_ = pool.QueryRow(context.Background(), `
			INSERT INTO users (email, password_hash, role, display_name, language, currency, referral_code)
			VALUES ($1, 'hash_b', 'worker', 'Pekerja B', 'id', 'IDR', $2)
			RETURNING id
		`, fmt.Sprintf("worker_b_%d@example.com", time.Now().UnixNano()), fmt.Sprintf("RFB%d", time.Now().UnixNano())).Scan(&workerBID)
		createdUserIDs = append(createdUserIDs, workerBID)

		// Buat wa_account milik Worker B
		var accBID int64
		_ = pool.QueryRow(context.Background(), `
			INSERT INTO wa_accounts (worker_id, label, speed, status)
			VALUES ($1, 'WA Milik B', 'medium', 'terhubung')
			RETURNING id
		`, workerBID).Scan(&accBID)

		if accBID > 0 {
			// Worker A mencoba mengakses status akun milik Worker B -> harus 404
			reqStatus := httptest.NewRequest("GET", fmt.Sprintf("/worker/accounts/%d/status", accBID), nil)
			reqStatus.SetPathValue("id", strconv.FormatInt(accBID, 10))
			reqStatus = withWorker(reqStatus)
			rrStatus := httptest.NewRecorder()
			srv.HandleWorkerAccountStatus(rrStatus, reqStatus)

			if rrStatus.Code != http.StatusNotFound && rrStatus.Code != http.StatusForbidden {
				t.Errorf("Expected 404/403 when Worker A views Worker B's account status, got %d", rrStatus.Code)
			}

			// Worker A mencoba menghapus akun milik Worker B -> akun B tidak boleh terhapus
			reqDel := httptest.NewRequest("POST", fmt.Sprintf("/worker/accounts/%d/delete", accBID), nil)
			reqDel.SetPathValue("id", strconv.FormatInt(accBID, 10))
			reqDel = withWorker(reqDel)
			rrDel := httptest.NewRecorder()
			srv.HandleWorkerAccountDelete(rrDel, reqDel)

			var countB int
			_ = pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM wa_accounts WHERE id = $1 AND worker_id = $2", accBID, workerBID).Scan(&countB)
			if countB != 1 {
				t.Errorf("SECURITY IDOR: Worker A managed to delete Worker B's account!")
			}
		}
	})

	t.Run("Worker account work start and stop controller tests", func(t *testing.T) {
		mockCtrl := newMockWorkController()
		srvWithWork, err := NewServer(&db.DB{Pool: pool}, "test", nil, mockCtrl)
		if err != nil {
			t.Fatalf("Failed to create server with work controller: %v", err)
		}

		// 1. Buat akun milik Worker A (workerID) dengan status 'terhubung'
		var accAID int64
		err = pool.QueryRow(context.Background(), `
			INSERT INTO wa_accounts (worker_id, label, jid_hash, speed, status)
			VALUES ($1, 'Akun Kerja A', 'hash_work_a', 'medium', 'terhubung')
			RETURNING id
		`, workerID).Scan(&accAID)
		if err != nil {
			t.Fatalf("Failed to insert account for worker A: %v", err)
		}

		// 2. Buat akun milik Worker A dengan status 'menunggu_scan' (tidak terhubung)
		var accDisconnectedID int64
		err = pool.QueryRow(context.Background(), `
			INSERT INTO wa_accounts (worker_id, label, jid_hash, speed, status)
			VALUES ($1, 'Akun Putus A', 'hash_work_putus', 'medium', 'menunggu_scan')
			RETURNING id
		`, workerID).Scan(&accDisconnectedID)
		if err != nil {
			t.Fatalf("Failed to insert disconnected account: %v", err)
		}

		// 3. Buat worker B
		var workerBID int64
		_ = pool.QueryRow(context.Background(), `
			INSERT INTO users (email, password_hash, role, display_name, language, currency, referral_code)
			VALUES ($1, 'hash_b2', 'worker', 'Pekerja B2', 'id', 'IDR', $2)
			RETURNING id
		`, fmt.Sprintf("worker_b2_%d@example.com", time.Now().UnixNano()), fmt.Sprintf("RFB2%d", time.Now().UnixNano())).Scan(&workerBID)
		createdUserIDs = append(createdUserIDs, workerBID)

		sessB := UserSession{
			ID:       workerBID,
			Role:     "worker",
			Currency: "IDR",
		}
		withWorkerB := func(req *http.Request) *http.Request {
			return req.WithContext(context.WithValue(req.Context(), userContextKey, sessB))
		}

		// Test A: Pekerja B tidak bisa memulai akun milik Pekerja A (403 atau 404)
		{
			req := httptest.NewRequest("POST", fmt.Sprintf("/worker/accounts/%d/work/start", accAID), nil)
			req.SetPathValue("id", strconv.FormatInt(accAID, 10))
			req = withWorkerB(req)
			rr := httptest.NewRecorder()
			srvWithWork.HandleWorkerAccountWorkStart(rr, req)

			if rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound {
				t.Errorf("Expected 403 or 404 when Worker B starts Worker A's account, got %d", rr.Code)
			}
			if mockCtrl.IsRunning(accAID) {
				t.Errorf("Account A should not be running after unauthorized start attempt")
			}
		}

		// Test B: Pekerja B tidak bisa menghentikan akun milik Pekerja A (403 atau 404)
		{
			mockCtrl.running[accAID] = true
			req := httptest.NewRequest("POST", fmt.Sprintf("/worker/accounts/%d/work/stop", accAID), nil)
			req.SetPathValue("id", strconv.FormatInt(accAID, 10))
			req = withWorkerB(req)
			rr := httptest.NewRecorder()
			srvWithWork.HandleWorkerAccountWorkStop(rr, req)

			if rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound {
				t.Errorf("Expected 403 or 404 when Worker B stops Worker A's account, got %d", rr.Code)
			}
			mockCtrl.running[accAID] = false
		}

		// Test C: Akun tidak terhubung ditolak saat start
		{
			req := httptest.NewRequest("POST", fmt.Sprintf("/worker/accounts/%d/work/start", accDisconnectedID), nil)
			req.SetPathValue("id", strconv.FormatInt(accDisconnectedID, 10))
			req = withWorker(req)
			rr := httptest.NewRecorder()
			srvWithWork.HandleWorkerAccountWorkStart(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("Expected 400 Bad Request when starting disconnected account, got %d", rr.Code)
			}
			if mockCtrl.IsRunning(accDisconnectedID) {
				t.Errorf("Disconnected account should not be started")
			}
		}

		// Test D: Pekerja A memulai akun miliknya yang terhubung
		{
			req := httptest.NewRequest("POST", fmt.Sprintf("/worker/accounts/%d/work/start", accAID), nil)
			req.SetPathValue("id", strconv.FormatInt(accAID, 10))
			req = withWorker(req)
			rr := httptest.NewRecorder()
			srvWithWork.HandleWorkerAccountWorkStart(rr, req)

			if rr.Code != http.StatusSeeOther {
				t.Errorf("Expected redirect 303 after starting account, got %d", rr.Code)
			}
			if !mockCtrl.IsRunning(accAID) {
				t.Errorf("Account A should be running after worker A started it")
			}
		}

		// Test E: Start dua kali tidak membuat dua goroutine
		{
			req2 := httptest.NewRequest("POST", fmt.Sprintf("/worker/accounts/%d/work/start", accAID), nil)
			req2.SetPathValue("id", strconv.FormatInt(accAID, 10))
			req2 = withWorker(req2)
			rr2 := httptest.NewRecorder()
			srvWithWork.HandleWorkerAccountWorkStart(rr2, req2)

			if rr2.Code != http.StatusSeeOther {
				t.Errorf("Expected redirect 303 after second start, got %d", rr2.Code)
			}
			if !mockCtrl.IsRunning(accAID) {
				t.Errorf("Account A should still be running")
			}
		}

		// Test F: Pekerja A menghentikan kerja akun miliknya
		{
			req := httptest.NewRequest("POST", fmt.Sprintf("/worker/accounts/%d/work/stop", accAID), nil)
			req.SetPathValue("id", strconv.FormatInt(accAID, 10))
			req = withWorker(req)
			rr := httptest.NewRecorder()
			srvWithWork.HandleWorkerAccountWorkStop(rr, req)

			if rr.Code != http.StatusSeeOther {
				t.Errorf("Expected redirect 303 after stopping account, got %d", rr.Code)
			}
			if mockCtrl.IsRunning(accAID) {
				t.Errorf("Account A should not be running after stop")
			}
		}
	})

	t.Run("Risk notice and translations present in all 3 languages", func(t *testing.T) {
		langs := []string{"id", "en", "zh"}
		keys := []string{
			"nav.history",
			"worker.available_numbers_aggregate",
			"worker.risk_title",
			"worker.risk_banned_desc",
			"worker.risk_privacy_desc",
			"worker.risk_agree_checkbox",
			"worker.speed_fast_warning",
			"worker.referral_pending_notice",
			"worker.history_title",
			"wa.work_status",
			"wa.working",
			"wa.not_working",
			"wa.action_start_work",
			"wa.action_stop_work",
			"wa.stop_reason_label",
		}

		for _, l := range langs {
			for _, k := range keys {
				val := i18n.T(l, k)
				if val == "" || val == k {
					t.Errorf("Missing translation for key %q in language %q", k, l)
				}
			}
		}
	})

	t.Run("HandleWorkerTicketPOST validation and submission", func(t *testing.T) {
		// Test 1: Message too short (<5 chars)
		{
			form := url.Values{
				"category": {"Koneksi WhatsApp"},
				"message":  {"abc"},
			}
			req := httptest.NewRequest("POST", "/worker/ticket", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerTicketPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
				t.Errorf("Expected redirect with error for short message, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}
		}

		// Test 2: Valid ticket submission
		{
			form := url.Values{
				"category": {"Penarikan Dana"},
				"message":  {"Mohon dicek penarikan saldo belum masuk ke rekening."},
			}
			req := httptest.NewRequest("POST", "/worker/ticket", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerTicketPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "success=") {
				t.Errorf("Expected redirect with success for valid ticket, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}

			// Verify ticket in DB
			var ticketID int64
			var cat, msg, status string
			err := pool.QueryRow(context.Background(), `
				SELECT id, category, message, status
				FROM support_tickets
				WHERE user_id = $1
				ORDER BY id DESC LIMIT 1
			`, workerID).Scan(&ticketID, &cat, &msg, &status)
			if err != nil {
				t.Fatalf("Failed to query created ticket: %v", err)
			}
			if cat != "Penarikan Dana" || status != "open" {
				t.Errorf("Expected category 'Penarikan Dana' and status 'open', got cat=%s status=%s", cat, status)
			}
		}
	})

	t.Run("HandleWorkerHome renders active broadcast banner", func(t *testing.T) {
		// Insert active broadcast
		var bID int64
		err := pool.QueryRow(context.Background(), `
			INSERT INTO broadcasts (title, message, badge, is_active)
			VALUES ('Pembaruan Rate Spesial', 'Rate per pesan naik menjadi 35 rupiah', 'UPDATE RATE', true)
			RETURNING id
		`).Scan(&bID)
		if err != nil {
			t.Fatalf("Failed to insert broadcast: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), "DELETE FROM broadcasts WHERE id = $1", bID)
		})

		req := httptest.NewRequest("GET", "/worker", nil)
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleWorkerHome(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "Pembaruan Rate Spesial") || !strings.Contains(body, "UPDATE RATE") {
			t.Errorf("Expected worker home to contain broadcast announcement banner")
		}
	})
}
