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

	t.Run("c) UPDATE dan DELETE pada ledger_entries gagal", func(t *testing.T) {
		var ledgerID int64
		err := pool.QueryRow(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'koreksi', 'IDR', 50) RETURNING id", worker1ID).Scan(&ledgerID)
		if err != nil {
			t.Fatalf("failed to insert ledger entry: %v", err)
		}

		_, err = pool.Exec(ctx, "UPDATE ledger_entries SET amount = 100 WHERE id = $1", ledgerID)
		if err == nil {
			t.Fatal("expected error on UPDATE ledger_entries, got nil")
		}

		_, err = pool.Exec(ctx, "DELETE FROM ledger_entries WHERE id = $1", ledgerID)
		if err == nil {
			t.Fatal("expected error on DELETE ledger_entries, got nil")
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
}

