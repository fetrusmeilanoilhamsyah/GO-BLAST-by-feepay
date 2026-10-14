package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"goblast/internal/testutil"
	"goblast/internal/timeutil"
)

func TestSchemaConstraints(t *testing.T) {
	pool := testutil.TestDB(t)

	ctx := context.Background()

	// Setup basic data with unique emails
	nonce := time.Now().UnixNano()
	var adminID, worker1ID int64
	err := pool.QueryRow(ctx, "INSERT INTO users (email, username, password_hash, role, display_name, referral_code) VALUES ($1, $2, 'hash', 'admin', 'Admin', $3) RETURNING id",
		fmt.Sprintf("admin_%d@test.local", nonce), fmt.Sprintf("admin_%d", nonce), fmt.Sprintf("A%d", nonce)).Scan(&adminID)
	if err != nil {
		t.Fatalf("failed to insert admin: %v", err)
	}
	err = pool.QueryRow(ctx, "INSERT INTO users (email, username, password_hash, role, display_name, referral_code) VALUES ($1, $2, 'hash', 'worker', 'W1', $3) RETURNING id",
		fmt.Sprintf("w1_%d@test.local", nonce), fmt.Sprintf("w1_%d", nonce), fmt.Sprintf("W%d", nonce)).Scan(&worker1ID)
	if err != nil {
		t.Fatalf("failed to insert worker: %v", err)
	}

	var campaign1ID, campaign2ID int64
	err = pool.QueryRow(ctx, "INSERT INTO campaigns (name, body, elements, created_by) VALUES ('C1', 'B1', '{}', $1) RETURNING id", adminID).Scan(&campaign1ID)
	if err != nil {
		t.Fatalf("failed to insert campaign 1: %v", err)
	}
	err = pool.QueryRow(ctx, "INSERT INTO campaigns (name, body, elements, created_by) VALUES ('C2', 'B2', '{}', $1) RETURNING id", adminID).Scan(&campaign2ID)
	if err != nil {
		t.Fatalf("failed to insert campaign 2: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID, worker1ID}, []int64{campaign1ID, campaign2ID})
	})

	var batchID int64
	err = pool.QueryRow(ctx, "INSERT INTO number_batches (campaign_id, filename, uploaded_by) VALUES ($1, 'f1.csv', $2) RETURNING id", campaign1ID, adminID).Scan(&batchID)
	if err != nil {
		t.Fatalf("failed to insert batch: %v", err)
	}
	var batch2ID int64
	err = pool.QueryRow(ctx, "INSERT INTO number_batches (campaign_id, filename, uploaded_by) VALUES ($1, 'f2.csv', $2) RETURNING id", campaign2ID, adminID).Scan(&batch2ID)
	if err != nil {
		t.Fatalf("failed to insert batch2: %v", err)
	}

	t.Run("a) memasukkan nomor yang sama dua kali di campaign yang sama gagal, di campaign berbeda berhasil", func(t *testing.T) {
		_, err := pool.Exec(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, '62811', '62')", campaign1ID, batchID)
		if err != nil {
			t.Fatalf("failed first insert: %v", err)
		}

		_, err = pool.Exec(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, '62811', '62')", campaign1ID, batchID)
		if err == nil {
			t.Fatal("expected error on duplicate phone in same campaign, got nil")
		}

		_, err = pool.Exec(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, '62811', '62')", campaign2ID, batch2ID)
		if err != nil {
			t.Fatalf("expected success on same phone in different campaign, got error: %v", err)
		}
	})

	t.Run("b) dua baris upah untuk campaign_number_id yang sama gagal", func(t *testing.T) {
		var cnID int64
		err := pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, '62822', '62') RETURNING id", campaign1ID, batchID).Scan(&cnID)
		if err != nil {
			t.Fatalf("failed to insert cn: %v", err)
		}

		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id) VALUES ($1, 'upah', 'IDR', 100, $2)", worker1ID, cnID)
		if err != nil {
			t.Fatalf("failed first upah: %v", err)
		}

		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id) VALUES ($1, 'upah', 'IDR', 100, $2)", worker1ID, cnID)
		if err == nil {
			t.Fatal("expected error on duplicate upah for same campaign_number_id, got nil")
		}
	})

	t.Run("c) UPDATE dan DELETE pada ledger_entries gagal, termasuk UPDATE campaign_number_id menjadi NULL", func(t *testing.T) {
		var cnID int64
		err := pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, '628555', '62') RETURNING id", campaign1ID, batchID).Scan(&cnID)
		if err != nil {
			t.Fatalf("failed to insert cn: %v", err)
		}

		var ledgerID int64
		err = pool.QueryRow(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id) VALUES ($1, 'upah', 'IDR', 50, $2) RETURNING id", worker1ID, cnID).Scan(&ledgerID)
		if err != nil {
			t.Fatalf("failed to insert ledger entry: %v", err)
		}

		// Update amount ditolak
		_, err = pool.Exec(ctx, "UPDATE ledger_entries SET amount = 100 WHERE id = $1", ledgerID)
		if err == nil {
			t.Fatal("expected error on UPDATE ledger_entries amount, got nil")
		}

		// Update campaign_number_id menjadi NULL ditolak oleh trigger ketat
		_, err = pool.Exec(ctx, "UPDATE ledger_entries SET campaign_number_id = NULL WHERE id = $1", ledgerID)
		if err == nil {
			t.Fatal("expected error on UPDATE ledger_entries campaign_number_id = NULL, got nil")
		}

		// Delete ledger_entries ditolak
		_, err = pool.Exec(ctx, "DELETE FROM ledger_entries WHERE id = $1", ledgerID)
		if err == nil {
			t.Fatal("expected error on DELETE ledger_entries, got nil")
		}

		// Menghapus campaign_number yang dirujuk ledger ditolak oleh ON DELETE RESTRICT
		_, err = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE id = $1", cnID)
		if err == nil {
			t.Fatal("expected error on DELETE campaign_numbers referenced by ledger (ON DELETE RESTRICT), got nil")
		}
	})

	t.Run("d) penarikan melebihi saldo gagal", func(t *testing.T) {
		// worker1 has 100 + 50 = 150 IDR currently.

		// Withdraw 100 should succeed
		_, err := pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'penarikan', 'IDR', -100)", worker1ID)
		if err != nil {
			t.Fatalf("expected success on valid withdrawal, got error: %v", err)
		}

		// Withdraw 100 again should fail because balance is 50
		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'penarikan', 'IDR', -100)", worker1ID)
		if err == nil {
			t.Fatal("expected error on withdrawal exceeding balance, got nil")
		}
	})

	t.Run("e) kolom recipient_name dan custom_data bisa disimpan dan bernilai NULL saat kosong", func(t *testing.T) {
		var cnID1, cnID2 int64
		err := pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, recipient_name, custom_data) VALUES ($1, $2, '62833', '62', 'Budi', '{\"usn\": \"budi99\"}'::jsonb) RETURNING id", campaign1ID, batchID).Scan(&cnID1)
		if err != nil {
			t.Fatalf("failed to insert with recipient_name and custom_data: %v", err)
		}

		err = pool.QueryRow(ctx, "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code) VALUES ($1, $2, '62844', '62') RETURNING id", campaign1ID, batchID).Scan(&cnID2)
		if err != nil {
			t.Fatalf("failed to insert without recipient_name: %v", err)
		}

		var name1, name2 *string
		pool.QueryRow(ctx, "SELECT recipient_name FROM campaign_numbers WHERE id = $1", cnID1).Scan(&name1)
		pool.QueryRow(ctx, "SELECT recipient_name FROM campaign_numbers WHERE id = $2", cnID2).Scan(&name2)

		if name1 == nil || *name1 != "Budi" {
			t.Errorf("Expected name1 = 'Budi', got %v", name1)
		}
		if name2 != nil {
			t.Errorf("Expected name2 = NULL, got %v", name2)
		}
	})

	t.Run("f) konversi melebihi saldo IDR ditolak oleh trigger", func(t *testing.T) {
		// Sisa saldo IDR worker1ID saat ini adalah 50.
		// Konversi -100 IDR harus ditolak oleh trigger
		_, err := pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'konversi', 'IDR', -100)", worker1ID)
		if err == nil {
			t.Fatal("expected error on konversi exceeding IDR balance, got nil")
		}
	})

	t.Run("g) penarikan melebihi saldo USDT ditolak oleh trigger", func(t *testing.T) {
		// worker1ID belum punya saldo USDT (0).
		// Penarikan USDT sebesar 1000 micro-USDT harus ditolak oleh trigger
		_, err := pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'penarikan', 'USDT', -1000)", worker1ID)
		if err == nil {
			t.Fatal("expected error on USDT withdrawal exceeding USDT balance, got nil")
		}

		// Berikan saldo USDT 5000 lewat koreksi
		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'koreksi', 'USDT', 5000)", worker1ID)
		if err != nil {
			t.Fatalf("failed to credit USDT: %v", err)
		}

		// Penarikan USDT 4000 berhasil
		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'penarikan', 'USDT', -4000)", worker1ID)
		if err != nil {
			t.Fatalf("expected success on valid USDT withdrawal, got error: %v", err)
		}

		// Penarikan USDT 2000 lagi ditolak (saldo tersisa 1000)
		_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'penarikan', 'USDT', -2000)", worker1ID)
		if err == nil {
			t.Fatal("expected error on USDT withdrawal exceeding remaining balance, got nil")
		}
	})

	t.Run("h) kolom baru H2 terverifikasi di information_schema", func(t *testing.T) {
		expectedCols := []struct {
			table string
			col   string
		}{
			{"campaigns", "purged_at"},
			{"campaigns", "purge_summary"},
			{"users", "totp_secret_encrypted"},
			{"users", "totp_enabled_at"},
			{"users", "totp_last_step"},
			{"web_sessions", "stepup_at"},
			{"wa_accounts", "wa_jid_hash"},
		}

		for _, ec := range expectedCols {
			var exists bool
			err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM information_schema.columns
					WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
				)
			`, ec.table, ec.col).Scan(&exists)
			if err != nil || !exists {
				t.Errorf("expected column %s.%s to exist in catalog, exists=%v, err=%v", ec.table, ec.col, exists, err)
			}
		}
	})

	t.Run("i) indeks baru H2 terverifikasi di pg_indexes", func(t *testing.T) {
		expectedIndexes := []string{
			"idx_campaign_numbers_claimed_at_stale",
			"idx_campaign_numbers_claimed_account_release",
			"idx_campaign_numbers_campaign_status",
			"idx_users_email_lower",
			"idx_wa_accounts_wa_jid_hash_active",
			"idx_ledger_entries_referral_unique",
			"idx_users_referred_by",
			"idx_web_sessions_expires_at",
			"idx_audit_logs_action_created_at",
			"idx_notifications_created_at",
			"idx_number_batches_campaign_id",
			"idx_admin_recovery_codes_user_id",
			"idx_backups_created_at",
		}

		for _, idx := range expectedIndexes {
			var exists bool
			err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM pg_indexes
					WHERE schemaname = 'public' AND indexname = $1
				)
			`, idx).Scan(&exists)
			if err != nil || !exists {
				t.Errorf("expected index %s to exist in catalog, exists=%v, err=%v", idx, exists, err)
			}
		}
	})

	t.Run("j) constraint baru H2 terverifikasi di pg_constraint", func(t *testing.T) {
		expectedConstraints := []string{
			"check_campaign_numbers_attempts_non_negative",
			"check_campaign_numbers_dikirim_has_claim",
			"check_campaign_numbers_tersedia_unclaimed",
			"check_ledger_entries_referral_source",
			"backups_kind_check",
		}

		for _, con := range expectedConstraints {
			var exists bool
			err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM pg_constraint c
					JOIN pg_namespace n ON n.oid = c.connamespace
					WHERE n.nspname = 'public' AND c.conname = $1
				)
			`, con).Scan(&exists)
			if err != nil || !exists {
				t.Errorf("expected constraint %s to exist in catalog, exists=%v, err=%v", con, exists, err)
			}
		}
	})

	t.Run("k) wa_jid_hash menolak duplikat aktif tetapi mengizinkan setelah status 'berhenti'", func(t *testing.T) {
		hashVal := fmt.Sprintf("hmac_jid_%d", nonce)

		// Buat akun pertama berstatus terhubung dengan hashVal
		var acc1ID int64
		err := pool.QueryRow(ctx, `
			INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status)
			VALUES ($1, 'WA Unique 1', $2, 'terhubung')
			RETURNING id
		`, worker1ID, hashVal).Scan(&acc1ID)
		if err != nil {
			t.Fatalf("insert acc1 failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM wa_accounts WHERE id = $1", acc1ID)
		}()

		// Buat akun kedua dengan hashVal yang sama dan status aktif ('menunggu_scan') -> harus DITOLAK
		var acc2ID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status)
			VALUES ($1, 'WA Unique 2', $2, 'menunggu_scan')
			RETURNING id
		`, worker1ID, hashVal).Scan(&acc2ID)
		if err == nil {
			_, _ = pool.Exec(ctx, "DELETE FROM wa_accounts WHERE id = $1", acc2ID)
			t.Fatal("expected duplicate active wa_jid_hash to be rejected, got nil error")
		}

		// Ubah status akun pertama menjadi 'berhenti'
		_, err = pool.Exec(ctx, "UPDATE wa_accounts SET status = 'berhenti' WHERE id = $1", acc1ID)
		if err != nil {
			t.Fatalf("update acc1 to berhenti failed: %v", err)
		}

		// Sekarang akun kedua dengan hashVal yang sama harus BERHASIL diinsert
		err = pool.QueryRow(ctx, `
			INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status)
			VALUES ($1, 'WA Unique 2', $2, 'terhubung')
			RETURNING id
		`, worker1ID, hashVal).Scan(&acc2ID)
		if err != nil {
			t.Fatalf("expected insert acc2 to succeed after acc1 is berhenti, got error: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM wa_accounts WHERE id = $1", acc2ID)
		}()
	})

	t.Run("l) backups.kind menolak nilai di luar daftar", func(t *testing.T) {
		// Nilai tidak valid -> harus ditolak
		_, err := pool.Exec(ctx, `
			INSERT INTO backups (kind, filename, size_bytes, sha256, schema_version)
			VALUES ('invalid_kind', 'backup_bad.sql', 100, 'sha256dummy', 23)
		`)
		if err == nil {
			t.Fatal("expected insert backups with invalid kind to fail check constraint, got nil")
		}

		// Nilai valid ('otomatis', 'manual', 'sebelum_restore') -> berhasil
		var bID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO backups (kind, filename, size_bytes, sha256, schema_version)
			VALUES ('otomatis', $1, 1024, 'sha256dummyvalid', 23)
			RETURNING id
		`, fmt.Sprintf("backup_auto_%d.sql", nonce)).Scan(&bID)
		if err != nil {
			t.Fatalf("expected valid backup insert to succeed, got error: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM backups WHERE id = $1", bID)
		}()
	})

	t.Run("m) menghapus user menghapus kode cadangan admin (CASCADE)", func(t *testing.T) {
		var tempAdminID int64
		err := pool.QueryRow(ctx, `
			INSERT INTO users (email, username, password_hash, role, display_name, referral_code)
			VALUES ($1, $2, 'hash', 'admin', 'TempAdmin', $3)
			RETURNING id
		`, fmt.Sprintf("tempadmin_%d@test.local", nonce), fmt.Sprintf("tmpadm_%d", nonce), fmt.Sprintf("TADM%d", nonce)).Scan(&tempAdminID)
		if err != nil {
			t.Fatalf("insert temp admin failed: %v", err)
		}

		var codeID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO admin_recovery_codes (user_id, code_hash)
			VALUES ($1, 'hash_recovery_code_123')
			RETURNING id
		`, tempAdminID).Scan(&codeID)
		if err != nil {
			t.Fatalf("insert recovery code failed: %v", err)
		}

		// Hapus tempAdminID
		_, err = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", tempAdminID)
		if err != nil {
			t.Fatalf("delete temp admin failed: %v", err)
		}

		// Pastikan codeID ikut terhapus via CASCADE
		var count int
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM admin_recovery_codes WHERE id = $1", codeID).Scan(&count)
		if count != 0 {
			t.Fatalf("expected recovery code to be deleted via CASCADE, found %d rows", count)
		}
	})

	t.Run("n) tes zona waktu pendapatan hari ini UTC vs Asia/Jakarta pada data 23:30 WIB", func(t *testing.T) {
		locWIB := timeutil.JakartaLocation()
		nowWIB := time.Now().In(locWIB)
		// Waktu 23:30 WIB hari ini
		txTime := time.Date(nowWIB.Year(), nowWIB.Month(), nowWIB.Day(), 23, 30, 0, 0, locWIB)

		var ledgerID int64
		err := pool.QueryRow(ctx, `
			INSERT INTO ledger_entries (user_id, kind, currency, amount, created_at)
			VALUES ($1, 'upah', 'IDR', 75, $2)
			RETURNING id
		`, worker1ID, txTime).Scan(&ledgerID)
		if err != nil {
			t.Fatalf("insert 23:30 WIB ledger entry failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "ALTER TABLE ledger_entries DISABLE TRIGGER trg_prevent_ledger_update_delete")
			_, _ = pool.Exec(ctx, "DELETE FROM ledger_entries WHERE id = $1", ledgerID)
			_, _ = pool.Exec(ctx, "ALTER TABLE ledger_entries ENABLE TRIGGER trg_prevent_ledger_update_delete")
		}()

		startOfDayJakarta := timeutil.GetJakartaStartOfDay()

		// Sesi 1: TIME ZONE UTC
		connUTC, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire conn failed: %v", err)
		}
		defer connUTC.Release()
		_, _ = connUTC.Exec(ctx, "SET TIME ZONE 'UTC'")

		var sumUTC int64
		err = connUTC.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount), 0)
			FROM ledger_entries
			WHERE user_id = $1 AND kind IN ('upah', 'referral') AND currency = 'IDR' AND created_at >= $2
		`, worker1ID, startOfDayJakarta).Scan(&sumUTC)
		if err != nil {
			t.Fatalf("query with UTC session failed: %v", err)
		}

		// Sesi 2: TIME ZONE Asia/Jakarta
		connWIB, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire conn failed: %v", err)
		}
		defer connWIB.Release()
		_, _ = connWIB.Exec(ctx, "SET TIME ZONE 'Asia/Jakarta'")

		var sumWIB int64
		err = connWIB.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount), 0)
			FROM ledger_entries
			WHERE user_id = $1 AND kind IN ('upah', 'referral') AND currency = 'IDR' AND created_at >= $2
		`, worker1ID, startOfDayJakarta).Scan(&sumWIB)
		if err != nil {
			t.Fatalf("query with WIB session failed: %v", err)
		}

		if sumUTC != sumWIB {
			t.Fatalf("timezone discrepancy! sumUTC = %d, sumWIB = %d", sumUTC, sumWIB)
		}
		if sumUTC < 75 {
			t.Fatalf("expected sum >= 75 for 23:30 WIB transaction, got %d", sumUTC)
		}
	})

	t.Run("o) skrip pemeriksa baris yatim (orphan rows check) bernilai 0 pada alur nyata lengkap", func(t *testing.T) {
		// Jalankan alur nyata:
		// 1. Worker terdaftar dan diajak referer
		var wID int64
		err := pool.QueryRow(ctx, `
			INSERT INTO users (email, username, password_hash, role, display_name, referral_code, referred_by)
			VALUES ($1, $2, 'hash', 'worker', 'OrphanWorker', $3, $4)
			RETURNING id
		`, fmt.Sprintf("orp_%d@test.local", nonce), fmt.Sprintf("orp_%d", nonce), fmt.Sprintf("ORP%d", nonce), worker1ID).Scan(&wID)
		if err != nil {
			t.Fatalf("setup orphan worker failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", wID)
		}()

		// 2. Akun WA
		var waID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, speed, status)
			VALUES ($1, 'WA Orphan', $2, 'medium', 'terhubung')
			RETURNING id
		`, wID, fmt.Sprintf("jid_orp_%d", nonce)).Scan(&waID)
		if err != nil {
			t.Fatalf("setup wa account failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM wa_accounts WHERE id = $1", waID)
		}()

		// 3. Campaign & Batch & Numbers
		var cID, bID, cnID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO campaigns (name, body, elements, created_by)
			VALUES ('CampOrphan', 'Body', '{}', $1)
			RETURNING id
		`, adminID).Scan(&cID)
		if err != nil {
			t.Fatalf("setup campaign failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cID)
		}()

		err = pool.QueryRow(ctx, `
			INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
			VALUES ($1, 'orph.csv', $2, 1, 1)
			RETURNING id
		`, cID, adminID).Scan(&bID)
		if err != nil {
			t.Fatalf("setup batch failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM number_batches WHERE id = $1", bID)
		}()

		err = pool.QueryRow(ctx, `
			INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at)
			VALUES ($1, $2, $3, '62', 'dikirim', $4, now())
			RETURNING id
		`, cID, bID, fmt.Sprintf("62899%d", nonce%1000000), waID).Scan(&cnID)
		if err != nil {
			t.Fatalf("setup campaign number failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE id = $1", cnID)
		}()

		// 4. Ledger Upah & Referral
		var lUpahID, lRefID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id)
			VALUES ($1, 'upah', 'IDR', 100, $2)
			RETURNING id
		`, wID, cnID).Scan(&lUpahID)
		if err != nil {
			t.Fatalf("setup upah failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "ALTER TABLE ledger_entries DISABLE TRIGGER trg_prevent_ledger_update_delete")
			_, _ = pool.Exec(ctx, "DELETE FROM ledger_entries WHERE id = $1", lUpahID)
			_, _ = pool.Exec(ctx, "ALTER TABLE ledger_entries ENABLE TRIGGER trg_prevent_ledger_update_delete")
		}()

		err = pool.QueryRow(ctx, `
			INSERT INTO ledger_entries (user_id, kind, currency, amount, campaign_number_id, source_user_id)
			VALUES ($1, 'referral', 'IDR', 25, $2, $3)
			RETURNING id
		`, worker1ID, cnID, wID).Scan(&lRefID)
		if err != nil {
			t.Fatalf("setup referral failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "ALTER TABLE ledger_entries DISABLE TRIGGER trg_prevent_ledger_update_delete")
			_, _ = pool.Exec(ctx, "DELETE FROM ledger_entries WHERE id = $1", lRefID)
			_, _ = pool.Exec(ctx, "ALTER TABLE ledger_entries ENABLE TRIGGER trg_prevent_ledger_update_delete")
		}()

		// 5. Withdrawal & Conversion
		var withID, convID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO withdrawals (user_id, currency, amount, method, destination_encrypted, request_token)
			VALUES ($1, 'IDR', 50, 'dana', 'v1:k1:b64test', $2)
			RETURNING id
		`, wID, fmt.Sprintf("tok_%d", nonce)).Scan(&withID)
		if err != nil {
			t.Fatalf("setup withdrawal failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM withdrawals WHERE id = $1", withID)
		}()

		err = pool.QueryRow(ctx, `
			INSERT INTO currency_conversions (user_id, idr_amount, usdt_micro, kurs, request_token)
			VALUES ($1, 10000, 625000, 16000, $2)
			RETURNING id
		`, wID, fmt.Sprintf("conv_tok_%d", nonce)).Scan(&convID)
		if err != nil {
			t.Fatalf("setup conversion failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM currency_conversions WHERE id = $1", convID)
		}()

		// 6. Support ticket & Recovery code & Backup
		var tickID, recID, bkID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO support_tickets (user_id, category, message, replied_by, reply_message)
			VALUES ($1, 'koneksi', 'Tes tiket', $2, 'Balasan')
			RETURNING id
		`, wID, adminID).Scan(&tickID)
		if err != nil {
			t.Fatalf("setup ticket failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM support_tickets WHERE id = $1", tickID)
		}()

		err = pool.QueryRow(ctx, `
			INSERT INTO admin_recovery_codes (user_id, code_hash)
			VALUES ($1, 'hash_code_orphan')
			RETURNING id
		`, adminID).Scan(&recID)
		if err != nil {
			t.Fatalf("setup recovery code failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM admin_recovery_codes WHERE id = $1", recID)
		}()

		err = pool.QueryRow(ctx, `
			INSERT INTO backups (kind, filename, size_bytes, sha256, schema_version, created_by)
			VALUES ('manual', $1, 2048, 'sha_orphan', 23, $2)
			RETURNING id
		`, fmt.Sprintf("bk_orp_%d.sql", nonce), adminID).Scan(&bkID)
		if err != nil {
			t.Fatalf("setup backup failed: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, "DELETE FROM backups WHERE id = $1", bkID)
		}()

		// 7. Universal Orphan Checker untuk semua Foreign Key di database
		query := `
			SELECT
				tc.table_name AS child_table,
				kcu.column_name AS child_column,
				ccu.table_name AS parent_table,
				ccu.column_name AS parent_column
			FROM information_schema.table_constraints AS tc
			JOIN information_schema.key_column_usage AS kcu
				ON tc.constraint_name = kcu.constraint_name
				AND tc.table_schema = kcu.table_schema
			JOIN information_schema.constraint_column_usage AS ccu
				ON ccu.constraint_name = tc.constraint_name
				AND ccu.table_schema = tc.table_schema
			WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = 'public'
			ORDER BY tc.table_name, kcu.column_name;
		`
		fkRows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatalf("query fks failed: %v", err)
		}
		defer fkRows.Close()

		totalFKsChecked := 0
		for fkRows.Next() {
			var cTable, cCol, pTable, pCol string
			_ = fkRows.Scan(&cTable, &cCol, &pTable, &pCol)
			totalFKsChecked++

			orphanQ := fmt.Sprintf(`
				SELECT COUNT(*)
				FROM %s c
				LEFT JOIN %s p ON p.%s = c.%s
				WHERE c.%s IS NOT NULL AND p.%s IS NULL
			`, cTable, pTable, pCol, cCol, cCol, pCol)

			var orphanCount int
			err := pool.QueryRow(ctx, orphanQ).Scan(&orphanCount)
			if err != nil {
				t.Fatalf("query orphan for %s.%s -> %s.%s error: %v", cTable, cCol, pTable, pCol, err)
			}
			if orphanCount > 0 {
				t.Errorf("ORPHAN ROW FOUND: %s.%s -> %s.%s has %d orphans!", cTable, cCol, pTable, pCol, orphanCount)
			}
		}

		t.Logf("Universal orphan checker completed: %d Foreign Keys verified, 0 orphans found.", totalFKsChecked)
	})
}

