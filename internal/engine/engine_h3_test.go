package engine_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa"
	"goblast/internal/wa/watest"

	"go.mau.fi/whatsmeow"
)

// TestH3_FindingM3_Classify_TableDriven menguji klasifikasi seluruh error whatsmeow & standard.
// Sebelum perbaikan, fungsi engine.Classify belum ada atau klasifikasi salah.
func TestH3_FindingM3_Classify_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected engine.ResultClass
	}{
		{"nil error adalah Terkirim", nil, engine.ClassTerkirim},
		{"wa.ErrSessionNotActive adalah PastiBelumTerkirim", wa.ErrSessionNotActive, engine.ClassPastiBelumTerkirim},
		{"wa.ErrSessionNotConnected adalah PastiBelumTerkirim", wa.ErrSessionNotConnected, engine.ClassPastiBelumTerkirim},
		{"whatsmeow.ErrNotConnected adalah PastiBelumTerkirim", whatsmeow.ErrNotConnected, engine.ClassPastiBelumTerkirim},
		{"whatsmeow.ErrClientIsNil adalah PastiBelumTerkirim", whatsmeow.ErrClientIsNil, engine.ClassPastiBelumTerkirim},
		{"whatsmeow.ErrNotLoggedIn adalah PastiBelumTerkirim", whatsmeow.ErrNotLoggedIn, engine.ClassPastiBelumTerkirim},
		{"whatsmeow.ErrMessageTimedOut adalah TidakPasti", whatsmeow.ErrMessageTimedOut, engine.ClassTidakPasti},
		{"whatsmeow.ErrIQTimedOut adalah TidakPasti", whatsmeow.ErrIQTimedOut, engine.ClassTidakPasti},
		{"context.DeadlineExceeded adalah TidakPasti", context.DeadlineExceeded, engine.ClassTidakPasti},
		{"whatsmeow.ErrRecipientADJID adalah NomorTidakValid", whatsmeow.ErrRecipientADJID, engine.ClassNomorTidakValid},
		{"whatsmeow.ErrUnknownServer adalah NomorTidakValid", whatsmeow.ErrUnknownServer, engine.ClassNomorTidakValid},
		{"whatsmeow.ErrPhoneNumberTooShort adalah NomorTidakValid", whatsmeow.ErrPhoneNumberTooShort, engine.ClassNomorTidakValid},
		{"whatsmeow.ErrIQBadRequest adalah NomorTidakValid", whatsmeow.ErrIQBadRequest, engine.ClassNomorTidakValid},
		{"whatsmeow.ErrIQNotFound adalah NomorTidakValid", whatsmeow.ErrIQNotFound, engine.ClassNomorTidakValid},
		{"whatsmeow.ErrIQNotAuthorized adalah MasalahAkun", whatsmeow.ErrIQNotAuthorized, engine.ClassMasalahAkun},
		{"whatsmeow.ErrIQForbidden adalah MasalahAkun", whatsmeow.ErrIQForbidden, engine.ClassMasalahAkun},
		{"whatsmeow.ErrIQRateOverLimit adalah MasalahAkun", whatsmeow.ErrIQRateOverLimit, engine.ClassMasalahAkun},
		{"error tak dikenal diperlakukan sebagai TidakPasti", errors.New("some strange network glitch"), engine.ClassTidakPasti},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := engine.Classify(tc.err)
			if got != tc.expected {
				t.Errorf("Classify(%v) = %v; want %v", tc.err, got, tc.expected)
			}
		})
	}
}

// TestH3_FindingM3_PastiBelumTerkirim_RollsBackAttempts membuktikan bahwa kegagalan
// sementara (ClassPastiBelumTerkirim) tidak boleh mengikis jatah attempts nomor.
func TestH3_FindingM3_PastiBelumTerkirim_RollsBackAttempts(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Test', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_m3_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_M3_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign M3 Test', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'test.csv', $2, 1, 1) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}

	var numberID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		 VALUES ($1, $2, '62899990001', '62', 'tersedia', 0) RETURNING id`,
		campaignID, batchID,
	).Scan(&numberID)
	if err != nil {
		t.Fatalf("setup number gagal: %v", err)
	}

	var accID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun M3', $2, 'terhubung', 'fast') RETURNING id`,
		workerID, fmt.Sprintf("hash_m3_%d", time.Now().UnixNano()),
	).Scan(&accID)
	if err != nil {
		t.Fatalf("setup account gagal: %v", err)
	}

	database := &db.DB{Pool: pool}
	sender := &watest.FakeSender{
		ErrFunc: func(accountID int64) error {
			// Simulasi koneksi websocket putus sebelum kirim (PastiBelumTerkirim)
			return whatsmeow.ErrNotConnected
		},
	}
	settings := newTestSettings()
	settings.SetJSON("rate_wage", "50")

	eng := engine.New(database, sender, nil, settings)

	// Klaim nomor: attempts bertambah dari 0 ke 1
	claimed, err := eng.ClaimNextNumber(ctx, accID)
	if err != nil {
		t.Fatalf("ClaimNextNumber gagal: %v", err)
	}
	if claimed.Attempts != 1 {
		t.Fatalf("expected attempts 1 after claim, got %d", claimed.Attempts)
	}

	// Tangani kegagalan PastiBelumTerkirim
	eng.HandleSendResult(ctx, accID, claimed.ID, workerID, claimed.CampaignID, claimed.Attempts, whatsmeow.ErrNotConnected)

	// Nomor harus kembali 'tersedia' dan attempts HARUS di-rollback kembali ke 0!
	var status string
	var attempts int
	err = pool.QueryRow(ctx, `SELECT status, attempts FROM campaign_numbers WHERE id = $1`, numberID).Scan(&status, &attempts)
	if err != nil {
		t.Fatalf("query status gagal: %v", err)
	}

	if status != "tersedia" {
		t.Errorf("expected status 'tersedia', got %q", status)
	}
	if attempts != 0 {
		t.Errorf("expected attempts rolled back to 0, got %d (PastiBelumTerkirim tidak boleh mengikis jatah)", attempts)
	}
}

// TestH3_FindingM7_PoisonPillPanic_MarksNumberGagal membuktikan bahwa jika terjadi panic
// saat memproses suatu nomor, nomor harus difinalisasi sebagai 'gagal' dengan last_error = 'galat_internal',
// bukan dikembalikan ke 'tersedia' yang menyebabkan loop crash.
func TestH3_FindingM7_PoisonPillPanic_MarksNumberGagal(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Test', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_m7_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_M7_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign M7 Test', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'test.csv', $2, 1, 1) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}

	var numberID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status)
		 VALUES ($1, $2, '62899990002', '62', 'tersedia') RETURNING id`,
		campaignID, batchID,
	).Scan(&numberID)
	if err != nil {
		t.Fatalf("setup number gagal: %v", err)
	}

	var accID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun M7', $2, 'terhubung', 'fast') RETURNING id`,
		workerID, fmt.Sprintf("hash_m7_%d", time.Now().UnixNano()),
	).Scan(&accID)
	if err != nil {
		t.Fatalf("setup account gagal: %v", err)
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, newTestSettings())

	claimed, err := eng.ClaimNextNumber(ctx, accID)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Finalisasi nomor beracun akibat panic
	eng.FinalizePoisonPill(ctx, accID, claimed.ID)

	var status, lastError string
	err = pool.QueryRow(ctx, `SELECT status, COALESCE(last_error, '') FROM campaign_numbers WHERE id = $1`, numberID).Scan(&status, &lastError)
	if err != nil {
		t.Fatalf("query status gagal: %v", err)
	}

	if status != "gagal" {
		t.Errorf("expected status 'gagal' for poison pill, got %q", status)
	}
	if lastError != "galat_internal" {
		t.Errorf("expected last_error 'galat_internal', got %q", lastError)
	}
}

// TestH3_FindingM6_RecoverOnBoot membuktikan bahwa nomor dikirim yang tersisa saat boot
// (karena server restart atau crash) ditandai gagal dengan last_error = 'hasil_tidak_pasti' (A1).
func TestH3_FindingM6_RecoverOnBoot(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Test', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_m6_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_M6_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign M6 Test', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'test.csv', $2, 2, 2) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}

	var accID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun M6', $2, 'terhubung', 'fast') RETURNING id`,
		workerID, fmt.Sprintf("hash_m6_%d", time.Now().UnixNano()),
	).Scan(&accID)
	if err != nil {
		t.Fatalf("setup account gagal: %v", err)
	}

	// Buat 2 nomor berstatus 'dikirim' yang menggantung sebelum boot
	var numID1, numID2 int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at)
		 VALUES ($1, $2, '62899990003', '62', 'dikirim', $3, now() - interval '5 minutes') RETURNING id`,
		campaignID, batchID, accID,
	).Scan(&numID1)
	if err != nil {
		t.Fatalf("setup num1 gagal: %v", err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at)
		 VALUES ($1, $2, '62899990004', '62', 'dikirim', $3, now() - interval '10 minutes') RETURNING id`,
		campaignID, batchID, accID,
	).Scan(&numID2)
	if err != nil {
		t.Fatalf("setup num2 gagal: %v", err)
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, newTestSettings())

	// Jalankan RecoverOnBoot
	recovered, err := eng.RecoverOnBoot(ctx)
	if err != nil {
		t.Fatalf("RecoverOnBoot gagal: %v", err)
	}
	if recovered < 2 {
		t.Errorf("expected at least 2 numbers recovered, got %d", recovered)
	}

	// Cek status kedua nomor: harus 'gagal' dengan last_error = 'hasil_tidak_pasti'
	for _, nid := range []int64{numID1, numID2} {
		var status, lastError string
		err = pool.QueryRow(ctx, `SELECT status, COALESCE(last_error, '') FROM campaign_numbers WHERE id = $1`, nid).Scan(&status, &lastError)
		if err != nil {
			t.Fatalf("query status gagal: %v", err)
		}
		if status != "gagal" {
			t.Errorf("number %d: expected status 'gagal', got %q", nid, status)
		}
		if lastError != "hasil_tidak_pasti" {
			t.Errorf("number %d: expected last_error 'hasil_tidak_pasti', got %q", nid, lastError)
		}
	}
}

// TestH3_FindingM2_OwnershipEnforcement membuktikan bahwa UPDATE nomor 'dikirim'
// wajib memeriksa claimed_account_id dan jika kepemilikan hilang, tidak menulis buku besar.
func TestH3_FindingM2_OwnershipEnforcement(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Test', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_m2_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_M2_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign M2 Test', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'test.csv', $2, 1, 1) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}

	var accID1, accID2 int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun M2-1', $2, 'terhubung', 'fast') RETURNING id`,
		workerID, fmt.Sprintf("hash_m2_1_%d", time.Now().UnixNano()),
	).Scan(&accID1)
	if err != nil {
		t.Fatalf("setup acc1 gagal: %v", err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun M2-2', $2, 'terhubung', 'fast') RETURNING id`,
		workerID, fmt.Sprintf("hash_m2_2_%d", time.Now().UnixNano()),
	).Scan(&accID2)
	if err != nil {
		t.Fatalf("setup acc2 gagal: %v", err)
	}

	var numberID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at)
		 VALUES ($1, $2, '62899990005', '62', 'dikirim', $3, now()) RETURNING id`,
		campaignID, batchID, accID1,
	).Scan(&numberID)
	if err != nil {
		t.Fatalf("setup number gagal: %v", err)
	}

	database := &db.DB{Pool: pool}
	settings := newTestSettings()
	settings.SetJSON("rate_wage", "50")
	eng := engine.New(database, &watest.FakeSender{}, nil, settings)

	// Coba catat sukses dengan accID2 (bukan pemilik klaim)
	err = eng.RecordSuccessAtomic(ctx, numberID, accID2, workerID, 50)
	if err == nil {
		t.Fatalf("expected error or lost claim when non-owner tries to record success")
	}

	// Buku besar tidak boleh bertambah
	var ledgerCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE campaign_number_id = $1`, numberID).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("query ledger gagal: %v", err)
	}
	if ledgerCount != 0 {
		t.Errorf("expected 0 ledger entries, got %d", ledgerCount)
	}

	// Sekarang catat sukses dengan accID1 (pemilik sah)
	err = eng.RecordSuccessAtomic(ctx, numberID, accID1, workerID, 50)
	if err != nil {
		t.Fatalf("RecordSuccessAtomic gagal untuk pemilik sah: %v", err)
	}

	// Buku besar harus bertambah tepat 1
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE campaign_number_id = $1`, numberID).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("query ledger gagal: %v", err)
	}
	if ledgerCount != 1 {
		t.Errorf("expected exactly 1 ledger entry, got %d", ledgerCount)
	}
}
