package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"goblast/internal/testutil"
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
}

