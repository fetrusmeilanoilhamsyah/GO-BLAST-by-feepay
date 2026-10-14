package engine_test

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa/watest"
)

// TestEngineShutdown_FiveAccounts memverifikasi bahwa penghentian melalui eng.StopAll()
// pada 5 akun yang sedang aktif mengirim pesan:
// 1. Menunggu seluruh worker selesai tanpa data race.
// 2. Tidak meninggalkan goroutine worker yang menggantung.
// 3. Tidak ada upah yang hilang (setiap nomor sukses tercatat di worker_wages).
// 4. Tidak ada nomor yang tertinggal di status 'dikirim'.
func TestEngineShutdown_FiveAccounts(t *testing.T) {
	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}
	ctx := context.Background()

	// 1. Buat worker
	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Shutdown', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_shut_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_SHUT_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("gagal membuat user worker: %v", err)
	}

	// 2. Buat campaign berjalan
	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign Test Shutdown', 'berjalan', 'kartu_link', 'Halo {name}', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("gagal membuat campaign: %v", err)
	}

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'test_shut.csv', $2, 100, 100) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("gagal membuat number_batch: %v", err)
	}

	// 3. Masukkan 100 nomor fiktif berstatus 'tersedia'
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		SELECT $1, $2, '62810' || LPAD(s::text, 7, '0'), '62', 'tersedia', 0
		FROM generate_series(1, 100) AS s
	`, campaignID, batchID)
	if err != nil {
		t.Fatalf("gagal bulk insert nomor: %v", err)
	}

	// 4. Buat 5 akun WA
	accIDs := make([]int64, 5)
	for i := 0; i < 5; i++ {
		err = pool.QueryRow(ctx,
			`INSERT INTO wa_accounts (worker_id, label, jid_hash, status, speed)
			 VALUES ($1, $2, $3, 'terhubung', 'fast') RETURNING id`,
			workerID, fmt.Sprintf("Akun Shutdown %d", i+1), fmt.Sprintf("jid_shut_%d_%d", i+1, time.Now().UnixNano()),
		).Scan(&accIDs[i])
		if err != nil {
			t.Fatalf("gagal membuat akun %d: %v", i+1, err)
		}
	}

	defer testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})

	fakeSender := &watest.FakeSender{
		SendDelay: 20 * time.Millisecond, // Delay realistis pengiriman
	}

	settings := newTestSettings()
	settings.ints["rate_wage"] = 250

	eng := engine.New(database, fakeSender, nil, settings)

	// Catat goroutine awal sebelum worker engine dimulai
	// Berikan waktu sejenak agar background goroutine lain stabil
	time.Sleep(50 * time.Millisecond)
	baseGoroutines := runtime.NumGoroutine()

	// 5. Mulai 5 akun sekaligus
	for _, id := range accIDs {
		eng.StartAccount(id, workerID)
	}

	// Verifikasi semua worker terdaftar
	if count := eng.RunningCount(); count != 5 {
		t.Fatalf("Diharapkan 5 worker terdaftar, didapat %d", count)
	}

	// Biarkan worker bekerja selama 200ms
	time.Sleep(200 * time.Millisecond)

	// 6. Jalankan StopAll()
	stopStart := time.Now()
	eng.StopAll()
	stopDuration := time.Since(stopStart)

	if stopDuration > 5*time.Second {
		t.Fatalf("StopAll memakan waktu terlalu lama: %v", stopDuration)
	}

	// 7. Verifikasi tidak ada worker yang masih aktif
	if count := eng.RunningCount(); count != 0 {
		t.Fatalf("Diharapkan 0 worker setelah StopAll, didapat %d", count)
	}
	for _, id := range accIDs {
		if eng.IsRunning(id) {
			t.Fatalf("Akun %d masih dilaporkan IsRunning=true setelah StopAll", id)
		}
	}

	// 8. Verifikasi goroutine pekerja engine telah berhenti
	time.Sleep(100 * time.Millisecond)
	afterGoroutines := runtime.NumGoroutine()
	// Goroutine setelah StopAll tidak boleh bertambah lebih dari margin kecil (misalnya toleransi pgx pool background activity 2)
	if afterGoroutines > baseGoroutines+3 {
		t.Errorf("Potensi kebocoran goroutine: sebelum=%d, sesudah=%d", baseGoroutines, afterGoroutines)
	}

	// 9. Verifikasi nomor di database: tidak ada yang menggantung di status 'dikirim'
	var hungCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'dikirim'`, campaignID).Scan(&hungCount)
	if err != nil {
		t.Fatalf("gagal query nomor menggantung: %v", err)
	}
	if hungCount != 0 {
		t.Fatalf("Terdapat %d nomor menggantung di status 'dikirim' setelah StopAll", hungCount)
	}

	// 10. Verifikasi upah: setiap pengiriman sukses harus tercatat di ledger_entries (kind = 'upah')
	sentCount := fakeSender.SentCount()
	var wageCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'upah'`, workerID).Scan(&wageCount)
	if err != nil {
		t.Fatalf("gagal query ledger_entries: %v", err)
	}

	var successNumbers int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'terkirim'`, campaignID).Scan(&successNumbers)
	if err != nil {
		t.Fatalf("gagal query campaign_numbers terkirim: %v", err)
	}

	if successNumbers != wageCount {
		t.Fatalf("Inkonsistensi data: nomor terkirim (%d) != jumlah upah (%d)", successNumbers, wageCount)
	}
	if sentCount < wageCount {
		t.Fatalf("FakeSender sent (%d) lebih sedikit daripada wageCount (%d)", sentCount, wageCount)
	}
}
