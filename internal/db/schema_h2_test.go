package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"goblast/internal/testutil"
)

// TestSchemaH2_FlawsOnMigration0018 membuktikan bug dan ketiadaan constraint/indeks
// yang ditargetkan oleh Tahap H2 pada skema saat ini (migrasi 0018).
func TestSchemaH2_FlawsOnMigration0018(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	nonce := time.Now().UnixNano()
	var adminID, workerID int64
	err := pool.QueryRow(ctx, "INSERT INTO users (email, username, password_hash, role, display_name, referral_code) VALUES ($1, $2, 'hash', 'admin', 'AdminH2', $3) RETURNING id",
		fmt.Sprintf("admin_h2_%d@test.local", nonce), fmt.Sprintf("adh2_%d", nonce), fmt.Sprintf("AH%d", nonce)).Scan(&adminID)
	if err != nil {
		t.Fatalf("setup admin failed: %v", err)
	}

	err = pool.QueryRow(ctx, "INSERT INTO users (email, username, password_hash, role, display_name, referral_code) VALUES ($1, $2, 'hash', 'worker', 'WorkerH2', $3) RETURNING id",
		fmt.Sprintf("w_h2_%d@test.local", nonce), fmt.Sprintf("wh2_%d", nonce), fmt.Sprintf("WH%d", nonce)).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker failed: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx, "INSERT INTO campaigns (name, body, elements, created_by) VALUES ('CampH2', 'BodyH2', '{}', $1) RETURNING id", adminID).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign failed: %v", err)
	}

	var batchID int64
	err = pool.QueryRow(ctx, "INSERT INTO number_batches (campaign_id, filename, uploaded_by) VALUES ($1, 'b.csv', $2) RETURNING id", campaignID, adminID).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch failed: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID, workerID}, []int64{campaignID})
	})

	// 1. Email case-sensitivity: saat ini LOWER(email) duplikat dapat masuk (karena index unik hanya case-sensitive)
	t.Run("flaw: duplicate lower email is allowed on 0018", func(t *testing.T) {
		email1 := fmt.Sprintf("case_%d@domain.test", nonce)
		email2 := fmt.Sprintf("CASE_%d@DOMAIN.TEST", nonce)

		var u1, u2 int64
		err := pool.QueryRow(ctx, "INSERT INTO users (email, username, password_hash, role, display_name, referral_code) VALUES ($1, $2, 'hash', 'worker', 'Case1', $3) RETURNING id",
			email1, fmt.Sprintf("c1_%d", nonce), fmt.Sprintf("C1%d", nonce)).Scan(&u1)
		if err != nil {
			t.Fatalf("insert email1 failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id IN ($1, $2)", u1, u2)
		}()

		// Seharusnya DITOLAK jika sudah ada UNIQUE INDEX pada LOWER(email).
		// Di 0018, ini LOLOS (berhasil insert). Tes ini memvalidasi apakah bug tersebut masih ada.
		err = pool.QueryRow(ctx, "INSERT INTO users (email, username, password_hash, role, display_name, referral_code) VALUES ($1, $2, 'hash', 'worker', 'Case2', $3) RETURNING id",
			email2, fmt.Sprintf("c2_%d", nonce), fmt.Sprintf("C2%d", nonce)).Scan(&u2)
		if err != nil {
			t.Logf("Sukses: email duplikat lower ditolak: %v", err)
		} else {
			t.Errorf("BUG TERBUKTI: dua email dengan huruf besar/kecil berbeda (%s dan %s) berhasil dimasukkan!", email1, email2)
		}
	})

	// 2. campaign_numbers: attempts < 0 diizinkan di 0018
	t.Run("flaw: negative attempts allowed on 0018", func(t *testing.T) {
		phone := fmt.Sprintf("62899%d", nonce%1000000)
		var cnID int64
		err := pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, attempts) VALUES ($1, $2, $3, '62', -5) RETURNING id",
			campaignID, batchID, phone).Scan(&cnID)
		if err != nil {
			t.Logf("Sukses: attempts < 0 ditolak: %v", err)
		} else {
			_, _ = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE id = $1", cnID)
			t.Errorf("BUG TERBUKTI: campaign_numbers dengan attempts = -5 berhasil dimasukkan!")
		}
	})

	// 3. campaign_numbers: status = 'dikirim' tanpa claimed_account_id dan claimed_at diizinkan di 0018
	t.Run("flaw: status dikirim without claim account or time allowed on 0018", func(t *testing.T) {
		phone := fmt.Sprintf("62898%d", nonce%1000000)
		var cnID int64
		err := pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at) VALUES ($1, $2, $3, '62', 'dikirim', NULL, NULL) RETURNING id",
			campaignID, batchID, phone).Scan(&cnID)
		if err != nil {
			t.Logf("Sukses: status dikirim tanpa claim ditolak: %v", err)
		} else {
			_, _ = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE id = $1", cnID)
			t.Errorf("BUG TERBUKTI: campaign_numbers status 'dikirim' dengan claimed_account_id NULL berhasil dimasukkan!")
		}
	})

	// 4. campaign_numbers: status = 'tersedia' dengan claimed_account_id diizinkan di 0018
	t.Run("flaw: status tersedia with claimed_account_id allowed on 0018", func(t *testing.T) {
		// Buat wa_account dulu
		var waID int64
		err := pool.QueryRow(ctx, "INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status) VALUES ($1, 'WA1', 'dummyhash', 'terhubung') RETURNING id", workerID).Scan(&waID)
		if err != nil {
			t.Fatalf("insert wa_account failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM wa_accounts WHERE id = $1", waID)
		}()

		phone := fmt.Sprintf("62897%d", nonce%1000000)
		var cnID int64
		err = pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id) VALUES ($1, $2, $3, '62', 'tersedia', $4) RETURNING id",
			campaignID, batchID, phone, waID).Scan(&cnID)
		if err != nil {
			t.Logf("Sukses: status tersedia dengan claimed_account_id ditolak: %v", err)
		} else {
			_, _ = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE id = $1", cnID)
			t.Errorf("BUG TERBUKTI: campaign_numbers status 'tersedia' dengan claimed_account_id != NULL berhasil dimasukkan!")
		}
	})

	// 5. ledger_entries referral: duplicate referral bonus for same campaign_number_id allowed on 0018
	t.Run("flaw: duplicate referral for same campaign_number_id allowed on 0018", func(t *testing.T) {
		phone := fmt.Sprintf("62896%d", nonce%1000000)
		var cnID int64
		err := pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, $3, '62') RETURNING id",
			campaignID, batchID, phone).Scan(&cnID)
		if err != nil {
			t.Fatalf("insert cn failed: %v", err)
		}

		// Insert referral 1
		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id, source_user_id) VALUES ($1, 'referral', 'IDR', 50, $2, $3)",
			adminID, cnID, workerID)
		if err != nil {
			t.Fatalf("insert first referral failed: %v", err)
		}

		// Insert referral 2 (duplikat untuk cnID yang sama)
		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id, source_user_id) VALUES ($1, 'referral', 'IDR', 50, $2, $3)",
			adminID, cnID, workerID)
		if err != nil {
			t.Logf("Sukses: duplikat referral ditolak: %v", err)
		} else {
			t.Errorf("BUG TERBUKTI: dua entri referral untuk campaign_number_id yang sama berhasil masuk ke ledger_entries!")
		}
	})

	// 6. ledger_entries referral: source_user_id NULL allowed on 0018
	t.Run("flaw: referral with source_user_id NULL allowed on 0018", func(t *testing.T) {
		phone := fmt.Sprintf("62895%d", nonce%1000000)
		var cnID int64
		err := pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, $3, '62') RETURNING id",
			campaignID, batchID, phone).Scan(&cnID)
		if err != nil {
			t.Fatalf("insert cn failed: %v", err)
		}

		// Insert referral dengan source_user_id NULL
		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id, source_user_id) VALUES ($1, 'referral', 'IDR', 50, $2, NULL)",
			adminID, cnID)
		if err != nil {
			t.Logf("Sukses: referral tanpa source_user_id ditolak: %v", err)
		} else {
			t.Errorf("BUG TERBUKTI: entri referral dengan source_user_id NULL berhasil masuk ke ledger_entries!")
		}
	})
}
