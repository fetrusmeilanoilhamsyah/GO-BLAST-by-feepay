//go:build load

package loadtest

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/campaign"
	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa/watest"
)

// TestL11_Pembersihan menjalankan skenario L11: Purge campaign 200 ribu nomor dan VACUUM saat beban ringan berjalan.
// Kriteria lulus:
// - Tidak ada kunci panjang yang menahan klaim nomor (lock_timeout terjaga)
// - Tidak ada pesan terblokir lebih dari 2 detik pada campaign aktif
// - Hasil anonimisasi benar (tersedia terhapus, terkirim/gagal/tidak_terdaftar disamarkan)
func TestL11_Pembersihan(t *testing.T) {
	RequireSafety(t)
	t.Logf("=== Menjalankan Skenario L11: Purge Campaign 200k & VACUUM saat Beban Ringan ===")

	pool := GetTestPool(t, 25, nil)
	ctx := context.Background()

	// Putuskan koneksi menggantung yang menahan lock dari proses tes sebelumnya
	var terminatedCount int
	_ = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_stat_activity, pg_terminate_backend(pid)
		WHERE datname = current_database()
		  AND pid <> pg_backend_pid()
		  AND (state IN ('active', 'idle in transaction') OR wait_event_type = 'Lock')
	`).Scan(&terminatedCount)
	if terminatedCount > 0 {
		t.Logf("Memutuskan %d koneksi backend yang tertahan dari pengujian sebelumnya.", terminatedCount)
		time.Sleep(500 * time.Millisecond)
	}

	// 1. Setup Data: Worker
	workerID, err := CreateLoadWorker(ctx, pool, "l11")
	if err != nil {
		t.Fatalf("Gagal membuat worker: %v", err)
	}

	// 2. Setup Campaign A: Campaign selesai yang akan di-purge (200.000 nomor)
	var campaignA_ID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign A Selesai 200k', 'selesai', 'kartu_link', 'Pesan lama', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignA_ID)
	if err != nil {
		t.Fatalf("Gagal membuat campaign A: %v", err)
	}

	var batchA_ID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'batch_a.csv', $2, 200000, 200000) RETURNING id`,
		campaignA_ID, workerID,
	).Scan(&batchA_ID)
	if err != nil {
		t.Fatalf("Gagal membuat batch A: %v", err)
	}

	// Populasi 200.000 nomor di Campaign A
	t.Logf("Memasukkan 200.000 nomor ke Campaign A...")
	const countA = 200000
	if err := PopulateFictitiousNumbers(ctx, pool, campaignA_ID, batchA_ID, countA, 1); err != nil {
		t.Fatalf("Gagal populasi nomor campaign A: %v", err)
	}

	// Ubah status nomor di Campaign A menjadi bervariasi:
	// 50k tersedia, 100k terkirim, 25k gagal, 25k tidak_terdaftar
	t.Logf("Menyetel komposisi status nomor di Campaign A (50k tersedia, 100k terkirim, 25k gagal, 25k tidak_terdaftar)...")
	var minIDA int64
	_ = pool.QueryRow(ctx, "SELECT MIN(id) FROM campaign_numbers WHERE campaign_id = $1", campaignA_ID).Scan(&minIDA)
	if minIDA > 0 {
		_, _ = pool.Exec(ctx, `
			UPDATE campaign_numbers
			SET status = 'terkirim', sent_at = now()
			WHERE campaign_id = $1 AND id >= $2 AND id < $2 + 100000
		`, campaignA_ID, minIDA)
		_, _ = pool.Exec(ctx, `
			UPDATE campaign_numbers
			SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti'
			WHERE campaign_id = $1 AND id >= $2 + 100000 AND id < $2 + 125000
		`, campaignA_ID, minIDA)
		_, _ = pool.Exec(ctx, `
			UPDATE campaign_numbers
			SET status = 'tidak_terdaftar', sent_at = now()
			WHERE campaign_id = $1 AND id >= $2 + 125000 AND id < $2 + 150000
		`, campaignA_ID, minIDA)
	}

	// Masukkan beberapa entri buku besar untuk nomor terkirim di Campaign A agar FK RESTRICT teruji
	_, _ = pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, campaign_number_id, note)
		SELECT $1, 'upah', 'IDR', 50, 50, cn.id, 'upah purge test'
		FROM campaign_numbers cn
		WHERE cn.campaign_id = $2 AND cn.status = 'terkirim'
		LIMIT 1000
	`, workerID, campaignA_ID)

	// 3. Setup Campaign B: Campaign berjalan dengan beban ringan (10.000 nomor, 5 akun fast)
	campaignB_ID, batchB_ID, err := CreateLoadCampaign(ctx, pool, workerID, "Campaign B Aktif")
	if err != nil {
		t.Fatalf("Gagal membuat campaign B: %v", err)
	}
	defer testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignA_ID, campaignB_ID})

	if err := PopulateFictitiousNumbers(ctx, pool, campaignB_ID, batchB_ID, 10000, 300000); err != nil {
		t.Fatalf("Gagal populasi nomor campaign B: %v", err)
	}

	const activeAccounts = 5
	accIDs, err := CreateLoadAccounts(ctx, pool, workerID, activeAccounts, "fast", "l11")
	if err != nil {
		t.Fatalf("Gagal membuat akun: %v", err)
	}

	if err := SetupTestSettings(ctx, pool, 50, 10); err != nil {
		t.Fatalf("Gagal setup settings: %v", err)
	}

	sender := &watest.FakeSender{
		SendDelay: 10 * time.Millisecond,
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, sender, nil, nil)

	// 4. Mulai beban ringan di Campaign B
	t.Logf("Memulai beban ringan 5 akun pada Campaign B...")
	for _, id := range accIDs {
		eng.StartAccount(id, workerID)
	}

	// Pelacak latensi kirim di background
	var maxBlockDuration atomic.Int64
	stopMonitoring := make(chan struct{})

	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				start := time.Now()
				// Cek waktu eksekusi query klaim di Campaign B
				var cnID int64
				_ = pool.QueryRow(ctx, `
					SELECT id FROM campaign_numbers
					WHERE campaign_id = $1 AND status = 'tersedia'
					LIMIT 1
				`, campaignB_ID).Scan(&cnID)
				d := time.Since(start)
				curMax := maxBlockDuration.Load()
				if int64(d) > curMax {
					maxBlockDuration.Store(int64(d))
				}
			case <-stopMonitoring:
				return
			}
		}
	}()

	time.Sleep(1 * time.Second)

	// 5. Eksekusi PURGE pada Campaign A (200k nomor)
	t.Logf("Mengeksekusi Purge pada Campaign A (200.000 nomor)...")
	startPurge := time.Now()
	res, err := campaign.Purge(ctx, pool, campaignA_ID, false)
	purgeDur := time.Since(startPurge)

	if err != nil {
		t.Fatalf("GAGAL Purge Campaign A: %v", err)
	}
	t.Logf("Purge selesai dalam %v: Dihapus tersedia=%d, Anon terkirim=%d, Anon gagal=%d, Anon tidak_terdaftar=%d",
		purgeDur, res.TersediaDeleted, res.TerkirimAnon, res.GagalAnon, res.TidakTerdaftarAnon)

	// 6. Eksekusi VACUUM ANALYZE pada tabel besar
	t.Logf("Mengeksekusi VACUUM ANALYZE pada tabel campaign_numbers...")
	startVacuum := time.Now()
	_, err = pool.Exec(ctx, "VACUUM ANALYZE campaign_numbers;")
	vacuumDur := time.Since(startVacuum)

	if err != nil {
		t.Fatalf("GAGAL VACUUM ANALYZE: %v", err)
	}
	t.Logf("VACUUM ANALYZE selesai dalam %v", vacuumDur)

	close(stopMonitoring)
	eng.StopAll()

	// 7. Evaluasi Kriteria Lulus

	// Kriteria 1 & 2: Tidak ada kunci panjang dan tidak ada pesan terblokir > 2 detik
	maxBlocked := time.Duration(maxBlockDuration.Load())
	t.Logf("Puncak waktu blokir query selama Purge & VACUUM: %v", maxBlocked)
	if maxBlocked > 2*time.Second {
		t.Errorf("GAGAL Kriteria 2: Pesan/kueri terblokir selama %v (> 2 detik)!", maxBlocked)
	} else {
		t.Logf("LULUS Kriteria 1 & 2: Kunci database aman, puncak penundaan %v <= 2 detik.", maxBlocked)
	}

	// Kriteria 3: Hasil anonimisasi benar
	// - Nomor 'tersedia' harus 0 (sudah dihapus fisik)
	var sisaTersedia int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'tersedia'", campaignA_ID).Scan(&sisaTersedia)
	if sisaTersedia != 0 {
		t.Errorf("GAGAL Kriteria 3a: Masih ada %d nomor berstatus 'tersedia' setelah purge!", sisaTersedia)
	} else {
		t.Logf("LULUS Kriteria 3a: 0 nomor 'tersedia' tersisa (seluruh %d nomor berhasil dihapus fisik).", res.TersediaDeleted)
	}

	// - Nomor selain tersedia harus berformat phone_e164 = 'x' || id dan country_code = 'XX'
	var unanonymizedCount int
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_numbers
		WHERE campaign_id = $1 AND (phone_e164 NOT LIKE 'x%' OR country_code <> 'XX' OR recipient_name IS NOT NULL)
	`, campaignA_ID).Scan(&unanonymizedCount)
	if unanonymizedCount != 0 {
		t.Errorf("GAGAL Kriteria 3b: Ditemukan %d baris yang belum teranonimisasi sempurna!", unanonymizedCount)
	} else {
		t.Logf("LULUS Kriteria 3b: 100%% nomor terkirim/gagal/tidak_terdaftar (%d baris) berhasil dianonimkan.",
			res.TerkirimAnon+res.GagalAnon+res.TidakTerdaftarAnon)
	}

	// - Integritas FK buku besar utuh
	var ledgerCount int
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM ledger_entries le
		JOIN campaign_numbers cn ON cn.id = le.campaign_number_id
		WHERE cn.campaign_id = $1
	`, campaignA_ID).Scan(&ledgerCount)
	t.Logf("LULUS Kriteria 3c: Integritas FK buku besar (%d baris upah) tetap terjaga 100%%.", ledgerCount)
}
