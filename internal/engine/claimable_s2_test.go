package engine_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"goblast/internal/campaign"
	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa"
)

// TestClaimableCount_ThreeCampaignsAndEngineClaim menguji spesifikasi Tahap S2:
// - Tiga campaign (berjalan, dijeda, draf) masing-masing 10 nomor.
// - ClaimableCount sama dengan 10 (hanya campaign berjalan).
// - Engine hanya mengklaim nomor dari campaign yang berjalan.
// - Setelah campaign berjalan dijeda, ClaimableCount menjadi 0 dan engine tidak bisa mengklaim.
func TestClaimableCount_ThreeCampaignsAndEngineClaim(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	database := &db.DB{Pool: pool}

	// Pastikan tidak ada campaign lain yang berstatus 'berjalan' sebelum tes dimulai
	_, _ = pool.Exec(ctx, "UPDATE campaigns SET status = 'draf' WHERE status = 'berjalan'")

	// 1. Setup user pembuat
	var userID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'admin', 'Admin S2', $2)
		 RETURNING id`,
		fmt.Sprintf("admin_s2_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REFS2_%d", time.Now().UnixNano()),
	).Scan(&userID)
	if err != nil {
		t.Fatalf("setup admin gagal: %v", err)
	}

	// 2. Buat tiga campaign:
	// c1: Berjalan (status = 'berjalan')
	var c1ID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, started_at)
		 VALUES ('Campaign Berjalan', 'berjalan', 'kartu_link', 'Pesan 1', '{}', $1, now())
		 RETURNING id`,
		userID,
	).Scan(&c1ID)
	if err != nil {
		t.Fatalf("buat campaign 1 gagal: %v", err)
	}

	// c2: Dijeda (status = 'draf', started_at != NULL)
	var c2ID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, started_at)
		 VALUES ('Campaign Dijeda', 'draf', 'kartu_link', 'Pesan 2', '{}', $1, now())
		 RETURNING id`,
		userID,
	).Scan(&c2ID)
	if err != nil {
		t.Fatalf("buat campaign 2 gagal: %v", err)
	}

	// c3: Draf (status = 'draf', started_at IS NULL)
	var c3ID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, started_at)
		 VALUES ('Campaign Draf', 'draf', 'kartu_link', 'Pesan 3', '{}', $1, NULL)
		 RETURNING id`,
		userID,
	).Scan(&c3ID)
	if err != nil {
		t.Fatalf("buat campaign 3 gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{userID}, []int64{c1ID, c2ID, c3ID})
	})

	// 3. Buat number_batches untuk masing-masing campaign
	var b1ID, b2ID, b3ID int64
	err = pool.QueryRow(ctx, "INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted) VALUES ($1, 'b1.csv', $2, 10, 10) RETURNING id", c1ID, userID).Scan(&b1ID)
	if err != nil {
		t.Fatalf("buat batch 1 gagal: %v", err)
	}
	err = pool.QueryRow(ctx, "INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted) VALUES ($1, 'b2.csv', $2, 10, 10) RETURNING id", c2ID, userID).Scan(&b2ID)
	if err != nil {
		t.Fatalf("buat batch 2 gagal: %v", err)
	}
	err = pool.QueryRow(ctx, "INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted) VALUES ($1, 'b3.csv', $2, 10, 10) RETURNING id", c3ID, userID).Scan(&b3ID)
	if err != nil {
		t.Fatalf("buat batch 3 gagal: %v", err)
	}

	// 4. Masukkan 10 nomor berstatus 'tersedia' ke masing-masing campaign
	// Campaign 1: 10 nomor
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		SELECT $1, $2, '628991' || LPAD(s::text, 6, '0'), '62', 'tersedia', 0
		FROM generate_series(1, 10) AS s
	`, c1ID, b1ID)
	if err != nil {
		t.Fatalf("insert nomor campaign 1 gagal: %v", err)
	}

	// Campaign 2: 10 nomor
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		SELECT $1, $2, '628992' || LPAD(s::text, 6, '0'), '62', 'tersedia', 0
		FROM generate_series(1, 10) AS s
	`, c2ID, b2ID)
	if err != nil {
		t.Fatalf("insert nomor campaign 2 gagal: %v", err)
	}

	// Campaign 3: 10 nomor
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		SELECT $1, $2, '628993' || LPAD(s::text, 6, '0'), '62', 'tersedia', 0
		FROM generate_series(1, 10) AS s
	`, c3ID, b3ID)
	if err != nil {
		t.Fatalf("insert nomor campaign 3 gagal: %v", err)
	}

	// 4. Verifikasi ClaimableCount awal: harus persis sama dengan 10 (hanya campaign berjalan)
	claimableCount, err := campaign.ClaimableCount(ctx, pool)
	if err != nil {
		t.Fatalf("ClaimableCount gagal: %v", err)
	}
	if claimableCount != 10 {
		t.Fatalf("diharapkan ClaimableCount = 10, ditemukan: %d", claimableCount)
	}

	// 5. Inisialisasi Engine dan akun worker
	var accountID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
		 VALUES ($1, 'WA Worker S2', 'hash_s2', 'terhubung')
		 RETURNING id`,
		userID,
	).Scan(&accountID)
	if err != nil {
		t.Fatalf("setup wa_account gagal: %v", err)
	}

	fakeSender := &wa.FakeSender{}
	settings := newTestSettings()
	eng := engine.New(database, fakeSender, nil, settings)

	// Uji RunningCount awal: harus 0
	if running := eng.RunningCount(); running != 0 {
		t.Fatalf("diharapkan RunningCount = 0 saat inisiasi, didapat: %d", running)
	}

	// 6. Engine hanya mengklaim dari campaign yang berjalan (c1ID)
	claimed, err := eng.ClaimNextNumber(ctx, accountID)
	if err != nil {
		t.Fatalf("engine klaim nomor gagal: %v", err)
	}
	if claimed.CampaignID != c1ID {
		t.Fatalf("engine salah mengklaim campaign: diharapkan %d (berjalan), didapat %d", c1ID, claimed.CampaignID)
	}

	// Setelah 1 nomor diklaim, sisa claimable = 9
	claimableCountAfterClaim, err := campaign.ClaimableCount(ctx, pool)
	if err != nil {
		t.Fatalf("ClaimableCount gagal: %v", err)
	}
	if claimableCountAfterClaim != 9 {
		t.Fatalf("diharapkan ClaimableCount = 9 setelah 1 klaim, didapat: %d", claimableCountAfterClaim)
	}

	// 7. Jeda campaign yang sedang berjalan (ubah status c1 menjadi 'draf')
	_, err = pool.Exec(ctx, "UPDATE campaigns SET status = 'draf' WHERE id = $1", c1ID)
	if err != nil {
		t.Fatalf("jeda campaign gagal: %v", err)
	}

	// 8. Setelah campaign dijeda, ClaimableCount harus menjadi 0
	claimableCountAfterPause, err := campaign.ClaimableCount(ctx, pool)
	if err != nil {
		t.Fatalf("ClaimableCount gagal: %v", err)
	}
	if claimableCountAfterPause != 0 {
		t.Fatalf("diharapkan ClaimableCount = 0 setelah campaign dijeda, didapat: %d", claimableCountAfterPause)
	}

	// 9. Engine sekarang tidak dapat mengklaim nomor apa pun lagi
	_, err = eng.ClaimNextNumber(ctx, accountID)
	if err == nil {
		t.Fatal("engine tidak boleh mengklaim nomor ketika tidak ada campaign berjalan!")
	}
}
