package engine_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa"
	"goblast/internal/wa/watest"
)

// TestFinding_P2b_EngineCustomDataDelivery membuktikan bahwa saat ini engine
// tidak memuat custom_data saat mengklaim nomor sehingga variabel kustom {kode} dan {usn}
// tidak masuk ke pesan yang dikirim oleh worker.
func TestFinding_P2b_EngineCustomDataDelivery(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	database := &db.DB{Pool: pool}

	nonce := time.Now().UnixNano()
	var workerID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, display_name, referral_code, status)
		VALUES ($1, $2, 'hash', 'worker', 'Worker P2b', $3, 'active')
		RETURNING id
	`, fmt.Sprintf("work_p2b_%d", nonce), fmt.Sprintf("work_p2b_%d@test.com", nonce), fmt.Sprintf("REF_%d", nonce)).Scan(&workerID)
	if err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Campaign P2b', 'berjalan', 'kartu_link', 'Halo {nama|Kak}, kode Anda {kode} dan id {id}!', '{}', $1)
		RETURNING id
	`, workerID).Scan(&campaignID)
	if err != nil {
		t.Fatalf("create campaign failed: %v", err)
	}

	var batchID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		VALUES ($1, 'test.csv', $2, 1, 1)
		RETURNING id
	`, campaignID, workerID).Scan(&batchID)
	if err != nil {
		t.Fatalf("create batch failed: %v", err)
	}

	// Masukkan nomor dengan custom_data
	var numberID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, recipient_name, custom_data, status)
		VALUES ($1, $2, '628999999001', '62', NULL, '{"kode":"DISKON50","id":"ID-999","usn":"budi99"}'::jsonb, 'tersedia')
		RETURNING id
	`, campaignID, batchID).Scan(&numberID)
	if err != nil {
		t.Fatalf("create number failed: %v", err)
	}

	var accountID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO wa_accounts (worker_id, label, status, speed)
		VALUES ($1, 'WA P2b', 'terhubung', 'fast')
		RETURNING id
	`, workerID).Scan(&accountID)
	if err != nil {
		t.Fatalf("create wa account failed: %v", err)
	}

	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM wa_accounts WHERE id = $1", accountID)
		_, _ = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE id = $1", numberID)
		_, _ = pool.Exec(ctx, "DELETE FROM number_batches WHERE id = $1", batchID)
		_, _ = pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", campaignID)
		testutil.CleanupTestData(pool, []int64{workerID}, nil)
	}()

	sender := &watest.FakeSender{}
	settings := newTestSettings()
	settings.SetInt("rate_wage", 100)

	eng := engine.New(database, sender, nil, settings)

	// Mulai worker engine untuk mengirim nomor
	eng.StartAccount(accountID, workerID)

	// Tunggu hingga nomor terkirim oleh FakeSender
	var sentMsg wa.OutMessage
	var success bool
	for start := time.Now(); time.Since(start) < 4*time.Second; {
		if sender.SentCount() > 0 {
			sentMsg = sender.Sent[0].Msg
			success = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	eng.StopAccount(accountID)

	if !success {
		t.Fatalf("Pengiriman pesan tidak terjadi dalam 4 detik")
	}

	// Periksa apakah variabel custom_data {kode} dan {id} masuk ke pesan terkirim
	if !containsString(sentMsg.Text, "DISKON50") || !containsString(sentMsg.Text, "ID-999") {
		t.Errorf("BUG P2b TERBUKTI: custom_data tidak sampai ke pesan yang dikirim! Teks pesan:\n%s", sentMsg.Text)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
