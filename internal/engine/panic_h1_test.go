package engine_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/safe"
	"goblast/internal/testutil"
	"goblast/internal/wa"
	"goblast/internal/wa/watest"
)

type panicOnAccountSender struct {
	panicAccountID int64
	fake           *watest.FakeSender
}

func (s *panicOnAccountSender) Send(ctx context.Context, accountID int64, phone string, msg wa.OutMessage) (wa.Result, error) {
	if accountID == s.panicAccountID {
		panic(fmt.Sprintf("panic sengaja disimulasikan untuk akun %d", accountID))
	}
	return s.fake.Send(ctx, accountID, phone, msg)
}

// TestEnginePanicRecovery_FakeSender memverifikasi bahwa panic sengaja di FakeSender pada satu akun
// tidak mematikan proses tes, akun itu berhenti dengan stop_reason 'galat internal', nomor dilepaskan,
// dan akun lain tetap dapat mengirim pesan dengan normal.
func TestEnginePanicRecovery_FakeSender(t *testing.T) {
	safe.ResetPanicCount()

	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}
	ctx := context.Background()

	// 1. Buat worker dan admin untuk data uji
	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Panic', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_panic_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_PANIC_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("gagal membuat user worker: %v", err)
	}

	// 2. Buat campaign berjalan
	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign Test Panic', 'berjalan', 'kartu_link', 'Halo dari test panic {name}', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("gagal membuat campaign: %v", err)
	}

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'test_panic.csv', $2, 20, 20) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("gagal membuat number_batch: %v", err)
	}

	// 3. Masukkan nomor fiktif berstatus 'tersedia'
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		SELECT $1, $2, '62810' || LPAD(s::text, 7, '0'), '62', 'tersedia', 0
		FROM generate_series(1, 20) AS s
	`, campaignID, batchID)
	if err != nil {
		t.Fatalf("gagal bulk insert nomor: %v", err)
	}

	// 4. Buat 2 akun: akun 1 (akan panic) dan akun 2 (berjalan normal)
	var acc1ID, acc2ID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun Panic 1', 'jid_panic_1', 'terhubung', 'fast') RETURNING id`,
		workerID,
	).Scan(&acc1ID)
	if err != nil {
		t.Fatalf("gagal membuat akun 1: %v", err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun Normal 2', 'jid_normal_2', 'terhubung', 'fast') RETURNING id`,
		workerID,
	).Scan(&acc2ID)
	if err != nil {
		t.Fatalf("gagal membuat akun 2: %v", err)
	}

	defer testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})

	// 5. Setup engine dengan PanickingSender
	realFake := &watest.FakeSender{}
	panickingSender := &panicOnAccountSender{
		panicAccountID: acc1ID,
		fake:           realFake,
	}

	settings := newTestSettings()
	settings.ints["rate_wage"] = 250 // rate wage Rp 250 per pesan

	eng := engine.New(database, panickingSender, nil, settings)

	// 6. Jalankan akun 1 (yang akan panic) dan akun 2 (yang normal)
	eng.StartAccount(acc1ID, workerID)
	eng.StartAccount(acc2ID, workerID)

	// Tunggu sebentar agar kedua worker menjalankan siklus kirim
	deadline := time.Now().Add(4 * time.Second)
	var acc1Status, acc1StopReason string
	for time.Now().Before(deadline) {
		_ = pool.QueryRow(ctx, `SELECT status, COALESCE(stop_reason, '') FROM wa_accounts WHERE id = $1`, acc1ID).Scan(&acc1Status, &acc1StopReason)
		if acc1Status == "berhenti" && acc1StopReason == "galat internal" && realFake.SentCount() > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Hentikan engine
	eng.StopAll()

	// 7. Verifikasi akun 1
	if acc1Status != "berhenti" {
		t.Fatalf("Diharapkan status akun 1 adalah 'berhenti', didapat %q", acc1Status)
	}
	if acc1StopReason != "galat internal" {
		t.Fatalf("Diharapkan stop_reason akun 1 adalah 'galat internal', didapat %q", acc1StopReason)
	}

	// Verifikasi nomor yang dipegang akun 1 tidak menggantung di 'dikirim'
	var hungNumbers int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM campaign_numbers WHERE claimed_account_id = $1 AND status = 'dikirim'`, acc1ID).Scan(&hungNumbers)
	if hungNumbers != 0 {
		t.Fatalf("Nomor akun 1 masih menggantung di status 'dikirim': %d nomor", hungNumbers)
	}

	// 8. Verifikasi akun 2 tetap mengirim dengan normal
	if realFake.SentCount() == 0 {
		t.Fatal("Akun 2 seharusnya tetap berhasil mengirim pesan, namun SentCount bernilai 0")
	}

	// 9. Verifikasi panic tercatat di penghitung safe
	if safe.PanicCount() == 0 {
		t.Fatal("Diharapkan safe.PanicCount() > 0 karena akun 1 mengalami panic")
	}
}
