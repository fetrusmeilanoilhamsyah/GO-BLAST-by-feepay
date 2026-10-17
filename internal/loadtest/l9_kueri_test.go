//go:build load

package loadtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"goblast/internal/timeutil"
)

// EnsureLargeDataset memastikan database tes memiliki dataset 1 juta nomor dan 500 ribu baris buku besar.
// Mengembalikan campaignID dan workerID yang memiliki dataset tersebut.
func EnsureLargeDataset(t testing.TB, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()
	ctx := context.Background()

	var existingCampaignID int64
	var existingWorkerID int64
	err := pool.QueryRow(ctx, `
		SELECT c.id, c.created_by
		FROM campaigns c
		WHERE c.name = 'Campaign Benchmark 1M'
		LIMIT 1
	`).Scan(&existingCampaignID, &existingWorkerID)

	if err == nil && existingCampaignID > 0 {
		var numCount int64
		var ledCount int64
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1", existingCampaignID).Scan(&numCount)
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1", existingWorkerID).Scan(&ledCount)

		if numCount >= 1000000 {
			if ledCount < 500000 {
				t.Logf("Dataset 1M nomor sudah ada (%d baris). Memasukkan 500.000 baris ledger_entries...", numCount)
				for i := int(ledCount); i < 500000; i += 100000 {
					_, err := pool.Exec(ctx, `
						INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, note)
						SELECT $1, 'upah', 'IDR', 50, 50, 'upah pesan load'
						FROM generate_series(1, 100000)
					`, existingWorkerID)
					if err != nil {
						t.Fatalf("Gagal insert ledger entries: %v", err)
					}
				}
				_, _ = pool.Exec(ctx, "ANALYZE campaign_numbers; ANALYZE ledger_entries; ANALYZE campaigns;")
			}
			t.Logf("Dataset 1M nomor (%d baris) dan 500k buku besar lengkap, memakai kembali dataset.", numCount)
			return existingCampaignID, existingWorkerID
		}
	}

	t.Logf("Mempersiapkan dataset besar: 1 juta nomor dan 500 ribu baris buku besar...")
	wID, err := CreateLoadWorker(ctx, pool, "bench1m")
	if err != nil {
		t.Fatalf("Gagal membuat worker benchmark: %v", err)
	}

	cID, bID, err := CreateLoadCampaign(ctx, pool, wID, "Campaign Benchmark 1M")
	if err != nil {
		t.Fatalf("Gagal membuat campaign benchmark: %v", err)
	}

	// 1. Masukkan 1.000.000 nomor fiktif (bagi ke dalam 5 chunk 200k agar tidak OOM)
	startNum := time.Now()
	const totalNum = 1000000
	const chunkSize = 200000
	for i := 0; i < totalNum; i += chunkSize {
		t.Logf("Memasukkan batch nomor %d sampai %d...", i+1, i+chunkSize)
		if err := PopulateFictitiousNumbers(ctx, pool, cID, bID, chunkSize, i+1); err != nil {
			t.Fatalf("Gagal populasi nomor pada chunk %d: %v", i, err)
		}
	}
	t.Logf("1.000.000 nomor berhasil dimasukkan dalam %v", time.Since(startNum))

	// Tandai sebagian nomor dengan status realistis (100k terkirim, 10k gagal) via range scan PK yang cepat
	var minID int64
	_ = pool.QueryRow(ctx, "SELECT MIN(id) FROM campaign_numbers WHERE campaign_id = $1", cID).Scan(&minID)
	if minID > 0 {
		_, _ = pool.Exec(ctx, `
			UPDATE campaign_numbers
			SET status = 'terkirim', sent_at = now()
			WHERE campaign_id = $1 AND id >= $2 AND id < $2 + 100000
		`, cID, minID)
		_, _ = pool.Exec(ctx, `
			UPDATE campaign_numbers
			SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti'
			WHERE campaign_id = $1 AND id >= $2 + 100000 AND id < $2 + 110000
		`, cID, minID)
	}

	// 2. Masukkan 500.000 baris buku besar untuk worker ini
	startLed := time.Now()
	t.Logf("Memasukkan 500.000 baris ledger_entries...")
	const totalLedger = 500000
	for i := 0; i < totalLedger; i += 100000 {
		_, err := pool.Exec(ctx, `
			INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, note)
			SELECT $1, 'upah', 'IDR', 50, 50, 'upah pesan load'
			FROM generate_series(1, 100000)
		`, wID)
		if err != nil {
			t.Fatalf("Gagal insert ledger entries: %v", err)
		}
	}
	t.Logf("500.000 baris buku besar berhasil dimasukkan dalam %v", time.Since(startLed))

	// 3. Jalankan ANALYZE agar statistik query planner akurat
	t.Logf("Menjalankan ANALYZE pada tabel campaign_numbers dan ledger_entries...")
	_, _ = pool.Exec(ctx, "ANALYZE campaign_numbers; ANALYZE ledger_entries; ANALYZE campaigns;")

	return cID, wID
}

// explainQuery menjalankan EXPLAIN (ANALYZE, BUFFERS) dan mengembalikan output teks rencana.
func explainQuery(ctx context.Context, pool *pgxpool.Pool, query string, args ...any) (string, time.Duration, error) {
	explainSQL := "EXPLAIN (ANALYZE, BUFFERS) " + query
	start := time.Now()
	rows, err := pool.Query(ctx, explainSQL, args...)
	dur := time.Since(start)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err == nil {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	return sb.String(), dur, rows.Err()
}

// TestL9_PengujianKueri menjalankan skenario L9: EXPLAIN (ANALYZE, BUFFERS) pada 1 juta nomor dan 500 ribu buku besar.
func TestL9_PengujianKueri(t *testing.T) {
	RequireSafety(t)
	t.Logf("=== Menjalankan Skenario L9: Pengujian Kueri EXPLAIN (ANALYZE, BUFFERS) ===")

	pool := GetTestPool(t, 25, nil)
	ctx := context.Background()

	campaignID, workerID := EnsureLargeDataset(t, pool)

	type QueryTest struct {
		Name  string
		Query string
		Args  []any
	}

	startOfDay := timeutil.GetJakartaStartOfDay()

	queries := []QueryTest{
		{
			Name: "1. Klaim (ClaimNextNumber - FOR UPDATE SKIP LOCKED)",
			Query: `
				SELECT cn.id, cn.campaign_id, cn.phone_e164, cn.attempts, COALESCE(cn.recipient_name, ''), COALESCE(cn.custom_data, '{}'::jsonb)
				FROM campaign_numbers cn
				JOIN campaigns c ON c.id = cn.campaign_id
				WHERE cn.status = 'tersedia' AND c.status = 'berjalan' AND c.archived_at IS NULL
				ORDER BY cn.id ASC
				LIMIT 1
				FOR UPDATE SKIP LOCKED
			`,
		},
		{
			Name: "2. Supervisor (Auto-complete check)",
			Query: `
				SELECT rc.id
				FROM (SELECT id FROM campaigns WHERE status = 'berjalan') rc
				WHERE NOT EXISTS (
					SELECT 1 FROM campaign_numbers cn
					WHERE cn.campaign_id = rc.id AND cn.status IN ('tersedia', 'dikirim')
				)
			`,
		},
		{
			Name: "3. Pembersih Penjaga (CleanStaleNumbers)",
			Query: `
				SELECT id FROM campaign_numbers
				WHERE status = 'dikirim' AND claimed_at < now() - interval '90 seconds'
			`,
		},
		{
			Name: "4. Hitungan Dashboard (Group by status per campaign)",
			Query: `
				SELECT status, COUNT(*) FROM campaign_numbers
				WHERE campaign_id = $1 GROUP BY status
			`,
			Args: []any{campaignID},
		},
		{
			Name: "5. Saldo Satu Pekerja (user_balance dan trigger penarikan)",
			Query: `
				SELECT COALESCE(SUM(amount), 0) FROM ledger_entries
				WHERE user_id = $1 AND currency = 'IDR'
			`,
			Args: []any{workerID},
		},
		{
			Name: "6. Pendapatan Hari Ini",
			Query: `
				SELECT COALESCE(SUM(amount), 0) FROM ledger_entries
				WHERE user_id = $1 AND currency = 'IDR' AND kind = 'upah' AND created_at >= $2
			`,
			Args: []any{workerID, startOfDay},
		},
	}

	var balanceQueryDuration time.Duration

	t.Logf("----------------------------------------------------------------------")
	for _, qt := range queries {
		plan, dur, err := explainQuery(ctx, pool, qt.Query, qt.Args...)
		if err != nil {
			t.Errorf("Kueri %s GAGAL: %v", qt.Name, err)
			continue
		}

		t.Logf("HASIL %s: Durasi = %v", qt.Name, dur)
		// Cetak baris pertama dan baris Planning/Execution time
		lines := strings.Split(strings.TrimSpace(plan), "\n")
		for _, l := range lines {
			if strings.Contains(l, "Execution Time") || strings.Contains(l, "Planning Time") || strings.Contains(l, "Index") || strings.Contains(l, "Scan") || strings.Contains(l, "Buffers") {
				t.Logf("   %s", l)
			}
		}

		if strings.HasPrefix(qt.Name, "5. Saldo") {
			balanceQueryDuration = dur
		}
	}
	t.Logf("----------------------------------------------------------------------")

	// Evaluasi Kriteria Khusus Kueri 5:
	// "Kalau saldo satu pekerja lebih dari 50 ms pada 500 ribu baris, usulkan (jangan buat) rancangan ringkasan saldo dan tanyakan."
	t.Logf("Durasi Kueri Saldo 1 Pekerja (500k baris): %v", balanceQueryDuration)
	if balanceQueryDuration > 50*time.Millisecond {
		t.Logf("CATATAN KHUSUS L9: Durasi kueri saldo %v > 50 ms pada 500.000 baris. Perlu diajukan rancangan tabel ringkasan saldo ke pemilik proyek.", balanceQueryDuration)
	} else {
		t.Logf("LULUS L9: Durasi kueri saldo %v <= 50 ms (efisien berkat indeks idx_ledger_entries_user_curr_time).", balanceQueryDuration)
	}
}
