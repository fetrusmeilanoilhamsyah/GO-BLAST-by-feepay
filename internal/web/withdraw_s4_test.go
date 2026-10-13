package web

import (
	"context"
	"encoding/json"
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

	"goblast/internal/db"
	"goblast/internal/i18n"
	"goblast/internal/testutil"
	"goblast/internal/withdraw"
)

func findS4Root(t *testing.T) string {
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

func setupS4TestServer(t *testing.T) (*Server, *db.DB, int64, int64) {
	root := findS4Root(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	srv, err := NewServer(database, "test", nil, nil)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	srv.EncryptionKey = testutil.TestEncryptionKey()

	ctx := context.Background()
	now := time.Now().UnixNano()

	// Buat Admin
	var adminID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, username, password_hash, role, display_name, language, referral_code)
		VALUES ($1, $2, 'hash_admin', 'admin', 'Admin S4', 'id', $3)
		RETURNING id
	`, fmt.Sprintf("admin_s4_%d@test.local", now), fmt.Sprintf("admin_s4_%d", now), fmt.Sprintf("ADM%d", now)).Scan(&adminID)
	if err != nil {
		t.Fatalf("failed to create admin: %v", err)
	}

	// Buat Worker
	var workerID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, username, password_hash, role, display_name, language, referral_code)
		VALUES ($1, $2, 'hash_worker', 'worker', 'Worker S4', 'id', $3)
		RETURNING id
	`, fmt.Sprintf("worker_s4_%d@test.local", now), fmt.Sprintf("worker_s4_%d", now), fmt.Sprintf("REF%d", now)).Scan(&workerID)
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}

	return srv, database, adminID, workerID
}

// 1. Race condition pengajuan bersamaan saldo cukup untuk satu
func TestS4ConcurrentWithdrawals_RaceCondition(t *testing.T) {
	srv, database, _, workerID := setupS4TestServer(t)
	ctx := context.Background()

	// Beri saldo IDR 15.000 (cukup untuk satu kali Rp 10.000, tapi tidak cukup untuk dua kali Rp 10.000)
	_, err := database.Pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'koreksi', 'IDR', 15000, 'Initial wage')
	`, workerID)
	if err != nil {
		t.Fatalf("failed to seed balance: %v", err)
	}

	// Atur rekening tersimpan
	encDest, _ := withdraw.EncryptDestination("5550001 (Pekerja S4)", srv.EncryptionKey)
	_, _ = database.Pool.Exec(ctx, `
		UPDATE users
		SET payout_method = 'bca', payout_dest_encrypted = $1, payout_account_name = 'Pekerja S4'
		WHERE id = $2
	`, encDest, workerID)

	var wg sync.WaitGroup
	var successCount int32
	var failCount int32

	// Kirim 2 request penarikan paralel masing-masing Rp 10.000 dengan token unik berbeda
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reqToken := fmt.Sprintf("concur_token_%d_%d", idx, time.Now().UnixNano())
			form := url.Values{
				"amount":        {"10000"},
				"request_token": {reqToken},
			}
			req := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorkerSession(req, workerID)

			rr := httptest.NewRecorder()
			srv.HandleWorkerWithdrawalsPOST(rr, req)

			loc := rr.Header().Get("Location")
			if strings.Contains(loc, "success=1") {
				atomic.AddInt32(&successCount, 1)
			} else {
				atomic.AddInt32(&failCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if successCount != 1 {
		t.Fatalf("expected exactly 1 withdrawal to succeed, got %d (failed: %d)", successCount, failCount)
	}
	if failCount != 1 {
		t.Fatalf("expected exactly 1 withdrawal to fail, got %d", failCount)
	}

	// Saldo akhir harus Rp 5.000
	var finalBalance int64
	_ = database.Pool.QueryRow(ctx, "SELECT user_balance($1, 'IDR')", workerID).Scan(&finalBalance)
	if finalBalance != 5000 {
		t.Fatalf("expected final balance IDR 5000, got %d", finalBalance)
	}
}

// 2. Token ganda idempoten
func TestS4IdempotentRequestToken(t *testing.T) {
	srv, database, _, workerID := setupS4TestServer(t)
	ctx := context.Background()

	// Beri saldo IDR 50.000
	_, _ = database.Pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'koreksi', 'IDR', 50000, 'Seed balance')
	`, workerID)

	encDest, _ := withdraw.EncryptDestination("5550002 (Pekerja)", srv.EncryptionKey)
	_, _ = database.Pool.Exec(ctx, `
		UPDATE users
		SET payout_method = 'bca', payout_dest_encrypted = $1, payout_account_name = 'Pekerja'
		WHERE id = $2
	`, encDest, workerID)

	token := "idemp_token_unique_123"

	// Request pertama
	form := url.Values{
		"amount":        {"10000"},
		"request_token": {token},
	}
	req1 := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(form.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req1 = withWorkerSession(req1, workerID)
	rr1 := httptest.NewRecorder()
	srv.HandleWorkerWithdrawalsPOST(rr1, req1)

	if !strings.Contains(rr1.Header().Get("Location"), "success=1") {
		t.Fatalf("expected first request to succeed, got: %s", rr1.Header().Get("Location"))
	}

	// Request kedua dengan token sama
	req2 := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2 = withWorkerSession(req2, workerID)
	rr2 := httptest.NewRecorder()
	srv.HandleWorkerWithdrawalsPOST(rr2, req2)

	if !strings.Contains(rr2.Header().Get("Location"), "success=1") {
		t.Fatalf("expected idempotent response on second request, got: %s", rr2.Header().Get("Location"))
	}

	// Verifikasi hanya 1 baris yang dibuat di tabel withdrawals
	var count int
	_ = database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM withdrawals WHERE user_id = $1 AND request_token = $2", workerID, token).Scan(&count)
	if count != 1 {
		t.Fatalf("expected exactly 1 withdrawal row, got %d", count)
	}

	// Saldo hanya berkurang 10.000 (sisa 40.000)
	var finalBalance int64
	_ = database.Pool.QueryRow(ctx, "SELECT user_balance($1, 'IDR')", workerID).Scan(&finalBalance)
	if finalBalance != 40000 {
		t.Fatalf("expected final balance 40000, got %d", finalBalance)
	}
}

// 3. Permintaan kedua ditolak saat ada yang menunggu (MaxPendingWithdrawals = 1)
func TestS4MaxPendingWithdrawals(t *testing.T) {
	srv, database, _, workerID := setupS4TestServer(t)
	ctx := context.Background()

	// Beri saldo IDR 50.000
	_, _ = database.Pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'koreksi', 'IDR', 50000, 'Seed balance')
	`, workerID)

	encDest, _ := withdraw.EncryptDestination("5550003 (Pekerja)", srv.EncryptionKey)
	_, _ = database.Pool.Exec(ctx, `
		UPDATE users
		SET payout_method = 'bca', payout_dest_encrypted = $1, payout_account_name = 'Pekerja'
		WHERE id = $2
	`, encDest, workerID)

	// Penarikan pertama: sukses
	form1 := url.Values{
		"amount":        {"10000"},
		"request_token": {"pending_test_tok_1"},
	}
	req1 := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(form1.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req1 = withWorkerSession(req1, workerID)
	rr1 := httptest.NewRecorder()
	srv.HandleWorkerWithdrawalsPOST(rr1, req1)
	if !strings.Contains(rr1.Header().Get("Location"), "success=1") {
		t.Fatalf("request 1 should succeed, got: %s", rr1.Header().Get("Location"))
	}

	// Penarikan kedua dengan token baru: harus ditolak karena penarikan 1 masih 'menunggu'
	form2 := url.Values{
		"amount":        {"10000"},
		"request_token": {"pending_test_tok_2"},
	}
	req2 := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2 = withWorkerSession(req2, workerID)
	rr2 := httptest.NewRecorder()
	srv.HandleWorkerWithdrawalsPOST(rr2, req2)

	loc := rr2.Header().Get("Location")
	if !strings.Contains(loc, "error=") || !strings.Contains(loc, "menunggu+diproses") {
		t.Fatalf("expected rejection due to pending withdrawal, got location: %s", loc)
	}
}

// 4. Batas minimal IDR (10000) dan USDT (5000000 micro = 5 USDT)
func TestS4MinWithdrawalLimits(t *testing.T) {
	srv, database, _, workerID := setupS4TestServer(t)
	ctx := context.Background()

	// Beri saldo IDR dan USDT melimpah
	_, _ = database.Pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'koreksi', 'IDR', 100000, 'Seed IDR'),
		       ($1, 'koreksi', 'USDT', 50000000, 'Seed USDT')
	`, workerID)

	// Test IDR di bawah 10000
	encBank, _ := withdraw.EncryptDestination("5550004 (Pekerja)", srv.EncryptionKey)
	_, _ = database.Pool.Exec(ctx, `
		UPDATE users
		SET payout_method = 'bca', payout_dest_encrypted = $1, payout_account_name = 'Pekerja'
		WHERE id = $2
	`, encBank, workerID)

	formIDRSub := url.Values{
		"amount":        {"9999"},
		"request_token": {"min_idr_sub"},
	}
	reqSub := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(formIDRSub.Encode()))
	reqSub.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSub = withWorkerSession(reqSub, workerID)
	rrSub := httptest.NewRecorder()
	srv.HandleWorkerWithdrawalsPOST(rrSub, reqSub)
	if !strings.Contains(rrSub.Header().Get("Location"), "error=") {
		t.Fatalf("expected IDR < 10000 to be rejected, got: %s", rrSub.Header().Get("Location"))
	}

	// Test USDT di bawah 5 USDT
	encCrypto, _ := withdraw.EncryptDestination("0x1234567890abcdef1234567890abcdef12345678", srv.EncryptionKey)
	_, _ = database.Pool.Exec(ctx, `
		UPDATE users
		SET payout_method = 'usdt_bep20', payout_dest_encrypted = $1, payout_account_name = 'USDT BEP20'
		WHERE id = $2
	`, encCrypto, workerID)

	formUSDTSub := url.Values{
		"amount":        {"4.99"},
		"request_token": {"min_usdt_sub"},
	}
	reqUSDTSub := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(formUSDTSub.Encode()))
	reqUSDTSub.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUSDTSub = withWorkerSession(reqUSDTSub, workerID)
	rrUSDTSub := httptest.NewRecorder()
	srv.HandleWorkerWithdrawalsPOST(rrUSDTSub, reqUSDTSub)
	if !strings.Contains(rrUSDTSub.Header().Get("Location"), "error=") {
		t.Fatalf("expected USDT < 5 to be rejected, got: %s", rrUSDTSub.Header().Get("Location"))
	}

	// Test USDT tepat 5 USDT -> sukses
	formUSDTOk := url.Values{
		"amount":        {"5"},
		"request_token": {"min_usdt_ok"},
	}
	reqUSDTOk := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(formUSDTOk.Encode()))
	reqUSDTOk.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUSDTOk = withWorkerSession(reqUSDTOk, workerID)
	rrUSDTOk := httptest.NewRecorder()
	srv.HandleWorkerWithdrawalsPOST(rrUSDTOk, reqUSDTOk)
	if !strings.Contains(rrUSDTOk.Header().Get("Location"), "success=1") {
		t.Fatalf("expected USDT 5 to succeed, got: %s", rrUSDTOk.Header().Get("Location"))
	}
}

// 5. Penahanan penarikan setelah reset kata sandi admin dan setelah ubah rekening
func TestS4WithdrawLockAfterChanges(t *testing.T) {
	srv, database, adminID, workerID := setupS4TestServer(t)
	ctx := context.Background()

	// Beri saldo awal
	_, _ = database.Pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'koreksi', 'IDR', 50000, 'Seed balance')
	`, workerID)

	encDest, _ := withdraw.EncryptDestination("5550009 (Pekerja S4)", srv.EncryptionKey)
	_, _ = database.Pool.Exec(ctx, `
		UPDATE users
		SET payout_method = 'bca', payout_dest_encrypted = $1, payout_account_name = 'Pekerja S4'
		WHERE id = $2
	`, encDest, workerID)

	// Buat web session dummy milik pekerja
	_, _ = database.Pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, expires_at)
		VALUES ($1, 'dummy_hash_session', NOW() + INTERVAL '1 day')
	`, workerID)

	// Kasus A: Reset Sandi oleh Admin
	resetForm := url.Values{
		"new_password": {"newSecret12345"},
	}
	reqReset := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/workers/%d/reset-password", workerID), strings.NewReader(resetForm.Encode()))
	reqReset.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqReset.SetPathValue("id", fmt.Sprintf("%d", workerID))
	reqReset = withAdminSession(reqReset, adminID)
	rrReset := httptest.NewRecorder()
	srv.HandleAdminWorkerResetPasswordPOST(rrReset, reqReset)

	// Verifikasi: must_change_password = true, withdraw_locked_until ada di masa depan, session kosong
	var mustChange bool
	var lockedUntil *time.Time
	err := database.Pool.QueryRow(ctx, "SELECT must_change_password, withdraw_locked_until FROM users WHERE id = $1", workerID).Scan(&mustChange, &lockedUntil)
	if err != nil {
		t.Fatalf("failed to query worker after reset: %v", err)
	}
	if !mustChange {
		t.Fatalf("expected must_change_password to be true")
	}
	if lockedUntil == nil || lockedUntil.Before(time.Now()) {
		t.Fatalf("expected withdraw_locked_until to be set in future, got %v", lockedUntil)
	}

	var sessionCount int
	_ = database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM web_sessions WHERE user_id = $1", workerID).Scan(&sessionCount)
	if sessionCount != 0 {
		t.Fatalf("expected all worker sessions to be deleted, got %d", sessionCount)
	}

	// Pekerja coba tarik dana -> harus ditolak karena lock
	wReq := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(url.Values{"amount": {"10000"}}.Encode()))
	wReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wReq = withWorkerSession(wReq, workerID)
	wRR := httptest.NewRecorder()
	srv.HandleWorkerWithdrawalsPOST(wRR, wReq)
	if !strings.Contains(wRR.Header().Get("Location"), "ditahan+sementara") {
		t.Fatalf("expected withdrawal rejection while locked, got: %s", wRR.Header().Get("Location"))
	}

	// Kasus B: Ubah Rekening oleh Pekerja
	// Lepas lock dulu untuk tes perubahan rekening
	_, _ = database.Pool.Exec(ctx, "UPDATE users SET withdraw_locked_until = NULL WHERE id = $1", workerID)

	walletForm := url.Values{
		"method":       {"dana"},
		"destination":  {"081299990001"},
		"account_name": {"Pekerja DANA"},
	}
	wletReq := httptest.NewRequest(http.MethodPost, "/worker/wallet", strings.NewReader(walletForm.Encode()))
	wletReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wletReq = withWorkerSession(wletReq, workerID)
	wletRR := httptest.NewRecorder()
	srv.HandleWorkerWalletPOST(wletRR, wletReq)

	var lockedUntilWallet *time.Time
	_ = database.Pool.QueryRow(ctx, "SELECT withdraw_locked_until FROM users WHERE id = $1", workerID).Scan(&lockedUntilWallet)
	if lockedUntilWallet == nil || lockedUntilWallet.Before(time.Now()) {
		t.Fatalf("expected withdraw_locked_until after wallet change, got: %v", lockedUntilWallet)
	}
}

// 6. Penolakan mengembalikan saldo via pembalik dan membuat notifikasi
func TestS4RejectWithdrawal_RefundAndNotification(t *testing.T) {
	srv, database, adminID, workerID := setupS4TestServer(t)
	ctx := context.Background()

	// Beri saldo Rp 30.000
	_, _ = database.Pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'koreksi', 'IDR', 30000, 'Initial wage')
	`, workerID)

	encDest, _ := withdraw.EncryptDestination("5550005 (Pekerja)", srv.EncryptionKey)
	_, _ = database.Pool.Exec(ctx, `
		UPDATE users
		SET payout_method = 'bca', payout_dest_encrypted = $1, payout_account_name = 'Pekerja'
		WHERE id = $2
	`, encDest, workerID)

	// Buat pengajuan penarikan Rp 10.000
	form := url.Values{
		"amount":        {"10000"},
		"request_token": {"reject_test_tok"},
	}
	wReq := httptest.NewRequest(http.MethodPost, "/worker/withdrawals", strings.NewReader(form.Encode()))
	wReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wReq = withWorkerSession(wReq, workerID)
	wRR := httptest.NewRecorder()
	srv.HandleWorkerWithdrawalsPOST(wRR, wReq)

	var withdrawalID int64
	_ = database.Pool.QueryRow(ctx, "SELECT id FROM withdrawals WHERE user_id = $1 AND request_token = 'reject_test_tok'", workerID).Scan(&withdrawalID)
	if withdrawalID == 0 {
		t.Fatalf("withdrawal row not created")
	}

	// Saldo terpotong jadi Rp 20.000
	var midBalance int64
	_ = database.Pool.QueryRow(ctx, "SELECT user_balance($1, 'IDR')", workerID).Scan(&midBalance)
	if midBalance != 20000 {
		t.Fatalf("expected balance 20000 after request, got %d", midBalance)
	}

	// Admin tolak tanpa reject_reason -> gagal
	rejReqEmpty := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/withdrawals/%d/reject", withdrawalID), strings.NewReader(""))
	rejReqEmpty.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rejReqEmpty.SetPathValue("id", fmt.Sprintf("%d", withdrawalID))
	rejReqEmpty = withAdminSession(rejReqEmpty, adminID)
	rejRREmpty := httptest.NewRecorder()
	srv.HandleAdminWithdrawalRejectPOST(rejRREmpty, rejReqEmpty)
	if !strings.Contains(rejRREmpty.Header().Get("Location"), "err=") {
		t.Fatalf("expected reject without reason to fail")
	}

	// Admin tolak dengan reject_reason
	rejectReason := "Rekening bank tidak aktif atau salah nomor"
	rejForm := url.Values{"reject_reason": {rejectReason}}
	rejReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/withdrawals/%d/reject", withdrawalID), strings.NewReader(rejForm.Encode()))
	rejReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rejReq.SetPathValue("id", fmt.Sprintf("%d", withdrawalID))
	rejReq = withAdminSession(rejReq, adminID)
	rejRR := httptest.NewRecorder()
	srv.HandleAdminWithdrawalRejectPOST(rejRR, rejReq)

	if !strings.Contains(rejRR.Header().Get("Location"), "msg=") {
		t.Fatalf("expected reject to succeed, got: %s", rejRR.Header().Get("Location"))
	}

	// Verifikasi status dan saldo dikembalikan
	var status, storedReason string
	_ = database.Pool.QueryRow(ctx, "SELECT status, reject_reason FROM withdrawals WHERE id = $1", withdrawalID).Scan(&status, &storedReason)
	if status != "ditolak" || storedReason != rejectReason {
		t.Fatalf("expected status 'ditolak' and stored reason, got: status=%s, reason=%s", status, storedReason)
	}

	var restoredBalance int64
	_ = database.Pool.QueryRow(ctx, "SELECT user_balance($1, 'IDR')", workerID).Scan(&restoredBalance)
	if restoredBalance != 30000 {
		t.Fatalf("expected restored balance 30000, got %d", restoredBalance)
	}

	// Verifikasi notifikasi terkirim ke pekerja
	var notifCount int
	var notifMsg string
	_ = database.Pool.QueryRow(ctx, "SELECT COUNT(*), COALESCE(MAX(message), '') FROM notifications WHERE user_id = $1 AND ref_id = $2", workerID, withdrawalID).Scan(&notifCount, &notifMsg)
	if notifCount != 1 || !strings.Contains(notifMsg, rejectReason) {
		t.Fatalf("expected notification with reject reason, count=%d msg=%s", notifCount, notifMsg)
	}
}

// 7. Rotasi kunci (k0 terbaca bila PREV diisi)
func TestS4KeyRotation_K0Fallback(t *testing.T) {
	keyK0 := []byte("01234567890123456789012345678901") // 32 bytes
	keyK1 := []byte("abcdefabcdefabcdefabcdefabcdefab") // 32 bytes

	destPlain := "081299990002 (Rekening K0)"

	// Enkripsi dengan k0
	ciphertextK0, err := withdraw.EncryptDestinationWithKeyID(destPlain, "k0", keyK0)
	if err != nil {
		t.Fatalf("failed to encrypt with k0: %v", err)
	}

	// 1. Dekripsi dengan server yang HANYA punya k1 -> harus gagal
	srvNoPrev := &Server{
		EncryptionKey: keyK1,
	}
	_, errNoPrev := srvNoPrev.DecryptDestination(ciphertextK0)
	if errNoPrev == nil {
		t.Fatalf("expected decryption to fail without k0")
	}

	// 2. Dekripsi dengan server yang punya k1 (aktif) dan k0 (prev) -> harus berhasil
	srvWithPrev := &Server{
		EncryptionKey:     keyK1,
		EncryptionKeyPrev: keyK0,
	}
	decrypted, errWithPrev := srvWithPrev.DecryptDestination(ciphertextK0)
	if errWithPrev != nil {
		t.Fatalf("expected decryption to succeed with prev key: %v", errWithPrev)
	}
	if decrypted != destPlain {
		t.Fatalf("expected %q, got %q", destPlain, decrypted)
	}
}

// 8. Privasi tujuan penarikan dan audit log view_destination
func TestS4DestinationPrivacy_Logs(t *testing.T) {
	srv, database, adminID, workerID := setupS4TestServer(t)
	ctx := context.Background()

	encDest, _ := withdraw.EncryptDestination("081299990003 (Pekerja Privasi)", srv.EncryptionKey)

	var withdrawalID int64
	err := database.Pool.QueryRow(ctx, `
		INSERT INTO withdrawals (user_id, currency, amount, method, destination_encrypted, request_token, status)
		VALUES ($1, 'IDR', 10000, 'dana', $2, 'tok_privacy', 'menunggu')
		RETURNING id
	`, workerID, encDest).Scan(&withdrawalID)
	if err != nil {
		t.Fatalf("failed to insert withdrawal: %v", err)
	}

	// Admin memanggil endpoint view-destination
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/withdrawals/%d/view-destination", withdrawalID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", withdrawalID))
	req = withAdminSession(req, adminID)
	rr := httptest.NewRecorder()
	srv.HandleAdminWithdrawalViewDestinationPOST(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp struct {
		Status      string `json:"status"`
		Destination string `json:"destination"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Destination != "081299990003 (Pekerja Privasi)" {
		t.Fatalf("unexpected decrypted destination: %s", resp.Destination)
	}

	// Verifikasi tercatat di audit_logs dengan action = 'view_destination'
	var auditCount int
	_ = database.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs
		WHERE actor_id = $1 AND action = 'view_destination' AND entity = 'withdrawals' AND entity_id = $2
	`, adminID, fmt.Sprintf("%d", withdrawalID)).Scan(&auditCount)
	if auditCount < 1 {
		t.Fatalf("expected audit log for view_destination")
	}
}

// 9. Pencarian pekerja via hash di /admin/workers
func TestS4WorkerSearchByDestHash(t *testing.T) {
	srv, database, adminID, workerID := setupS4TestServer(t)
	ctx := context.Background()

	destAccount := "081288889999"
	norm := withdraw.NormalizeDestination(destAccount)
	destHash := withdraw.HashDestination(norm, srv.EncryptionKey)

	enc, _ := withdraw.EncryptDestination(destAccount+" (Pemilik)", srv.EncryptionKey)

	// Simpan ke profil pekerja
	_, _ = database.Pool.Exec(ctx, `
		UPDATE users
		SET payout_method = 'dana', payout_dest_encrypted = $1, payout_account_name = 'Pemilik', payout_dest_hash = $2
		WHERE id = $3
	`, enc, destHash, workerID)

	// Admin mencari di /admin/workers?q=081288889999
	req := httptest.NewRequest(http.MethodGet, "/admin/workers?q=081288889999", nil)
	req = withAdminSession(req, adminID)
	rr := httptest.NewRecorder()
	srv.HandleAdminWorkers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	// Pastikan worker ditemukan dalam HTML hasil pencarian
	var workerName string
	_ = database.Pool.QueryRow(ctx, "SELECT username FROM users WHERE id = $1", workerID).Scan(&workerName)
	if !strings.Contains(body, workerName) {
		t.Fatalf("expected worker %s to be found in search results by destination hash", workerName)
	}
}
