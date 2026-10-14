package db_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"goblast/internal/testutil"
)

// TestExplain_CampaignNumbersIndexes verifies:
// "EXPLAIN pada query pembersih dan pelepasan per akun di tabel berisi 200 ribu baris
// fiktif memakai indeks baru (bukan Seq Scan). Hasilnya dicatat di DOC_PROGRES."
func TestExplain_CampaignNumbersIndexes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 200k explain test in short mode")
	}

	pool := testutil.TestDB(t)
	ctx := context.Background()

	// 1. Setup minimal users, campaign, batch, wa_account
	nonce := time.Now().UnixNano()
	var adminID, workerID, waAccountID, campaignID, batchID int64

	err := pool.QueryRow(ctx, "INSERT INTO users (email, username, password_hash, role, display_name, referral_code) VALUES ($1, $2, 'hash', 'admin', 'AdminEx', $3) RETURNING id",
		fmt.Sprintf("adminex_%d@test.local", nonce), fmt.Sprintf("adminex_%d", nonce), fmt.Sprintf("AEX%d", nonce)).Scan(&adminID)
	if err != nil {
		t.Fatalf("setup admin failed: %v", err)
	}

	err = pool.QueryRow(ctx, "INSERT INTO users (email, username, password_hash, role, display_name, referral_code) VALUES ($1, $2, 'hash', 'worker', 'WorkerEx', $3) RETURNING id",
		fmt.Sprintf("workerex_%d@test.local", nonce), fmt.Sprintf("workerex_%d", nonce), fmt.Sprintf("WEX%d", nonce)).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker failed: %v", err)
	}

	err = pool.QueryRow(ctx, "INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, speed, status) VALUES ($1, 'WA Explain', $2, 'fast', 'terhubung') RETURNING id",
		workerID, fmt.Sprintf("jid_ex_%d", nonce)).Scan(&waAccountID)
	if err != nil {
		t.Fatalf("setup wa_account failed: %v", err)
	}

	err = pool.QueryRow(ctx, "INSERT INTO campaigns (name, body, elements, created_by) VALUES ('CampExplain', 'Body', '{}', $1) RETURNING id", adminID).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign failed: %v", err)
	}

	err = pool.QueryRow(ctx, "INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted) VALUES ($1, '200k.csv', $2, 200000, 200000) RETURNING id",
		campaignID, adminID).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch failed: %v", err)
	}

	t.Cleanup(func() {
		// Clean up 200k rows
		_, _ = pool.Exec(context.Background(), "DELETE FROM campaign_numbers WHERE campaign_id = $1", campaignID)
		testutil.CleanupTestData(pool, []int64{adminID, workerID}, []int64{campaignID})
	})

	// 2. Insert 200,000 baris fiktif dengan generate_series
	t.Log("Memasukkan 200.000 baris fiktif ke campaign_numbers...")
	startInsert := time.Now()

	// Disable statement timeout for this bulk insert
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection failed: %v", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, "SET LOCAL statement_timeout = 0")
	if err != nil {
		t.Fatalf("set statement timeout failed: %v", err)
	}

	// Insert 199,990 baris tersedia
	_, err = conn.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		SELECT $1, $2, '62899' || lpad(i::text, 9, '0'), '62', 'tersedia', 0
		FROM generate_series(1, 199990) AS i
	`, campaignID, batchID)
	if err != nil {
		t.Fatalf("bulk insert 199990 rows failed: %v", err)
	}

	// Insert 10 baris dikirim (5 dipegang waAccountID, 5 dengan claimed_at stale 15 menit lalu)
	_, err = conn.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts, claimed_account_id, claimed_at)
		SELECT $1, $2, '62898' || lpad(i::text, 9, '0'), '62', 'dikirim', 1, $3, now() - interval '900 seconds'
		FROM generate_series(1, 10) AS i
	`, campaignID, batchID, waAccountID)
	if err != nil {
		t.Fatalf("insert 10 dikirim rows failed: %v", err)
	}

	t.Logf("200.000 baris berhasil dimasukkan dalam %v", time.Since(startInsert))

	// Jalankan ANALYZE agar perencana query PostgreSQL memiliki data statistik distribusi
	_, err = conn.Exec(ctx, "ANALYZE campaign_numbers")
	if err != nil {
		t.Fatalf("ANALYZE failed: %v", err)
	}

	// 3. EXPLAIN Query Pelepasan Per Akun:
	// UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL
	// WHERE status = 'dikirim' AND claimed_account_id = $1
	t.Run("explain release per account query uses index", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx failed: %v", err)
		}
		defer tx.Rollback(ctx)

		query := `
			EXPLAIN (ANALYZE, BUFFERS)
			UPDATE campaign_numbers
			SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL
			WHERE status = 'dikirim' AND claimed_account_id = $1
		`
		rows, err := tx.Query(ctx, query, waAccountID)
		if err != nil {
			t.Fatalf("explain release query failed: %v", err)
		}
		defer rows.Close()

		var planLines []string
		for rows.Next() {
			var line string
			_ = rows.Scan(&line)
			planLines = append(planLines, line)
		}
		planText := strings.Join(planLines, "\n")
		t.Logf("EXPLAIN Plan Pelepasan Per Akun:\n%s", planText)

		if strings.Contains(planText, "Seq Scan on campaign_numbers") {
			t.Errorf("Query pelepasan per akun memakai Seq Scan! Harusnya memakai indeks. Plan:\n%s", planText)
		}
		if !strings.Contains(planText, "idx_campaign_numbers_claimed_account_release") {
			t.Errorf("Query pelepasan per akun tidak memakai idx_campaign_numbers_claimed_account_release! Plan:\n%s", planText)
		}
	})

	// 4. EXPLAIN Query Pembersih Stale Numbers:
	// UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL
	// WHERE status = 'dikirim' AND claimed_at < now() - make_interval(secs => $1)
	t.Run("explain cleaner query uses index", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx failed: %v", err)
		}
		defer tx.Rollback(ctx)

		query := `
			EXPLAIN (ANALYZE, BUFFERS)
			UPDATE campaign_numbers
			SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL
			WHERE status = 'dikirim' AND claimed_at < now() - interval '60 seconds'
		`
		rows, err := tx.Query(ctx, query)
		if err != nil {
			t.Fatalf("explain cleaner query failed: %v", err)
		}
		defer rows.Close()

		var planLines []string
		for rows.Next() {
			var line string
			_ = rows.Scan(&line)
			planLines = append(planLines, line)
		}
		planText := strings.Join(planLines, "\n")
		t.Logf("EXPLAIN Plan Pembersih Stale Claims:\n%s", planText)

		if strings.Contains(planText, "Seq Scan on campaign_numbers") {
			t.Errorf("Query pembersih memakai Seq Scan! Harusnya memakai indeks. Plan:\n%s", planText)
		}
		if !strings.Contains(planText, "idx_campaign_numbers_claimed_at_stale") && !strings.Contains(planText, "idx_campaign_numbers_claimed_account_release") {
			t.Errorf("Query pembersih tidak memakai indeks parsial! Plan:\n%s", planText)
		}
	})
}
