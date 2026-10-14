package engine_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa"
	"goblast/internal/wa/watest"
)

// TestH3_50Workers_2000Numbers_NoDuplicates_NoLostClaims_NoDeadlocks menguji 50 akun bekerja konkuren
// mengambil antrean 2.000 nomor fiktif:
// - 0 nomor terkirim ganda
// - 0 race condition (klaim hilang = 0)
// - 0 deadlock database
// - Tepat 2.000 nomor berstatus terkirim & 2.000 entri buku besar
func TestH3_50Workers_2000Numbers_NoDuplicates_NoLostClaims_NoDeadlocks(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'H3 Multi Worker', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_h3_multi_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_H3_MULTI_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign H3 2000', 'berjalan', 'kartu_link', 'Halo {{name}}', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	const totalNumbers = 2000
	const totalAccounts = 50

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'h3_2000.csv', $2, $3, $3) RETURNING id`,
		campaignID, workerID, totalNumbers,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}

	// Buat 50 akun WhatsApp
	accountIDs := make([]int64, totalAccounts)
	for i := 0; i < totalAccounts; i++ {
		err = pool.QueryRow(ctx,
			`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
			 VALUES ($1, $2, $3, 'terhubung', 'fast') RETURNING id`,
			workerID, fmt.Sprintf("Akun H3-%d", i), fmt.Sprintf("hash_h3_acc_%d_%d", i, time.Now().UnixNano()),
		).Scan(&accountIDs[i])
		if err != nil {
			t.Fatalf("setup account %d gagal: %v", i, err)
		}
	}

	// Masukkan 2.000 nomor fiktif secara batch (100 nomor per insert agar cepat)
	for i := 0; i < totalNumbers; i += 100 {
		end := i + 100
		if end > totalNumbers {
			end = totalNumbers
		}

		query := "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts) VALUES "
		vals := []interface{}{}
		for j := i; j < end; j++ {
			idx := (j - i) * 4
			if j > i {
				query += ", "
			}
			phone := fmt.Sprintf("62899%07d", j+1)
			query += fmt.Sprintf("($%d, $%d, $%d, $%d, 'tersedia', 0)", idx+1, idx+2, idx+3, idx+4)
			vals = append(vals, campaignID, batchID, phone, "62")
		}
		_, err = pool.Exec(ctx, query, vals...)
		if err != nil {
			t.Fatalf("insert batch numbers failed: %v", err)
		}
	}

	sender := &watest.FakeSender{}
	settings := newTestSettings()
	settings.SetJSON("rate_wage", "50")

	database := &db.DB{Pool: pool}
	eng := engine.New(database, sender, nil, settings)

	// Jalankan 50 akun secara konkuren
	var wg sync.WaitGroup
	wg.Add(totalAccounts)

	for _, accID := range accountIDs {
		accID := accID
		go func() {
			defer wg.Done()
			for {
				claimed, err := eng.ClaimNextNumber(ctx, accID)
				if err != nil {
					// Nomor sudah habis atau terklaim seluruhnya
					return
				}
				// Eksekusi kirim
				msg := wa.OutMessage{Text: "Halo"}
				_, sendErr := sender.Send(ctx, accID, claimed.PhoneE164, msg)
				eng.HandleSendResult(ctx, accID, claimed.ID, workerID, claimed.CampaignID, claimed.Attempts, sendErr)
			}
		}()
	}

	wg.Wait()

	// 1. Verifikasi: 0 nomor terkirim ganda
	var dupCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
			SELECT phone_e164, COUNT(*) 
			FROM campaign_numbers 
			WHERE campaign_id = $1 AND status = 'terkirim'
			GROUP BY phone_e164 
			HAVING COUNT(*) > 1
		) dups
	`, campaignID).Scan(&dupCount)
	if err != nil {
		t.Fatalf("query cek duplikat gagal: %v", err)
	}
	if dupCount != 0 {
		t.Fatalf("GAGAL: ditemukan %d nomor terkirim ganda!", dupCount)
	}

	// 2. Verifikasi: 0 race condition (klaim hilang = 0)
	stats := eng.Stats()
	if stats.LostClaimsCount != 0 {
		t.Fatalf("GAGAL: ditemukan %d klaim hilang (race condition)!", stats.LostClaimsCount)
	}

	// 3. Verifikasi: seluruh 2.000 nomor berstatus terkirim
	var terkirimCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_numbers 
		WHERE campaign_id = $1 AND status = 'terkirim'
	`, campaignID).Scan(&terkirimCount)
	if err != nil {
		t.Fatalf("query status terkirim gagal: %v", err)
	}
	if terkirimCount != totalNumbers {
		t.Fatalf("expected %d numbers terkirim, got %d", totalNumbers, terkirimCount)
	}

	// 4. Verifikasi: tepat 2.000 entri buku besar dibuat
	var ledgerCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM ledger_entries le
		JOIN campaign_numbers cn ON le.campaign_number_id = cn.id
		WHERE cn.campaign_id = $1
	`, campaignID).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("query ledger gagal: %v", err)
	}
	if ledgerCount != totalNumbers {
		t.Fatalf("expected exactly %d ledger entries, got %d", totalNumbers, ledgerCount)
	}
}

// tracerCounter menghitung query SQL yang dijalankan lewat context tracer.
type tracerCounter struct {
	count atomic.Int64
}

func (tc *tracerCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	tc.count.Add(1)
	return ctx
}

func (tc *tracerCounter) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
}

// TestH3_QueryTracer_RoundTripBudget membuktikan bahwa di jalur sukses pada cache hangat,
// hanya terjadi tepat 2 round trip ke database:
// RT 1: ClaimNextNumber (SELECT FOR UPDATE SKIP LOCKED)
// RT 2: RecordSuccessAtomic CTE (UPDATE campaign_numbers + INSERT INTO ledger_entries)
func TestH3_QueryTracer_RoundTripBudget(t *testing.T) {
	tc := &tracerCounter{}
	rawURL := testutil.TestDBURL(t)

	cfg, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	cfg.ConnConfig.Tracer = tc

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig failed: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	var workerID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Tracer', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_tracer_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_TRACER_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign Tracer', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'tracer.csv', $2, 1, 1) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}

	var numberID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status)
		 VALUES ($1, $2, '62899991234', '62', 'tersedia') RETURNING id`,
		campaignID, batchID,
	).Scan(&numberID)
	if err != nil {
		t.Fatalf("setup number gagal: %v", err)
	}

	var accID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun Tracer', $2, 'terhubung', 'fast') RETURNING id`,
		workerID, fmt.Sprintf("hash_tracer_%d", time.Now().UnixNano()),
	).Scan(&accID)
	if err != nil {
		t.Fatalf("setup account gagal: %v", err)
	}

	database := &db.DB{Pool: pool}
	sender := &watest.FakeSender{}
	settings := newTestSettings()
	settings.SetJSON("rate_wage", "50")

	eng := engine.New(database, sender, nil, settings)

	// Hangatkan cache metadata campaign & settings
	_, _ = eng.ClaimNextNumber(ctx, accID) // memanaskan query pool
	// Reset nomor ke 'tersedia' untuk pengujian presisi
	_, _ = pool.Exec(ctx, `UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL WHERE id = $1`, numberID)

	// Reset penghitung query tracer sebelum jalur kritis
	tc.count.Store(0)

	// Jalur Pengiriman Sukses:
	// 1. Klaim nomor (Round Trip 1)
	claimed, err := eng.ClaimNextNumber(ctx, accID)
	if err != nil {
		t.Fatalf("ClaimNextNumber gagal: %v", err)
	}

	// 2. Catat sukses atomik (Round Trip 2)
	err = eng.RecordSuccessAtomic(ctx, claimed.ID, accID, workerID, 50)
	if err != nil {
		t.Fatalf("RecordSuccessAtomic gagal: %v", err)
	}

	queriesUsed := tc.count.Load()
	if queriesUsed > 2 {
		t.Fatalf("ANGGARAN QUERY TERLAMPAUI: diharapkan <= 2 round trip DB per pesan di jalur normal, tetapi dieksekusi %d query", queriesUsed)
	}
	t.Logf("Anggaran query lulus: %d round trip DB pada jalur kirim sukses", queriesUsed)
}

// TestH3_CircuitBreaker_5ConsecutiveFailures membuktikan bahwa 5 kegagalan buruk berturut-turut
// otomatis menghentikan akun dengan alasan jelas, mencegah pengikisan antrean.
func TestH3_CircuitBreaker_5ConsecutiveFailures(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Circuit', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_cb_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_CB_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign Circuit Breaker', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'cb.csv', $2, 10, 10) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}

	// Insert 10 nomor
	for i := 1; i <= 10; i++ {
		_, err = pool.Exec(ctx,
			`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status)
			 VALUES ($1, $2, $3, '62', 'tersedia')`,
			campaignID, batchID, fmt.Sprintf("6289999880%d", i),
		)
		if err != nil {
			t.Fatalf("insert number failed: %v", err)
		}
	}

	var accID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun Circuit Breaker', $2, 'terhubung', 'fast') RETURNING id`,
		workerID, fmt.Sprintf("hash_cb_%d", time.Now().UnixNano()),
	).Scan(&accID)
	if err != nil {
		t.Fatalf("setup account gagal: %v", err)
	}

	// Sender selalu gagal dengan timeout (ClassTidakPasti)
	sender := &watest.FakeSender{
		ErrFunc: func(accountID int64) error {
			return context.DeadlineExceeded
		},
	}
	settings := newTestSettings()
	settings.SetJSON("rate_wage", "50")

	database := &db.DB{Pool: pool}
	eng := engine.New(database, sender, nil, settings)

	// Jalankan worker akun
	eng.StartAccount(accID, workerID)

	// Tunggu sampai akun berhenti otomatis oleh pemutus sirkuit
	deadline := time.Now().Add(12 * time.Second)
	stopped := false
	for time.Now().Before(deadline) {
		if !eng.IsRunning(accID) {
			stopped = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !stopped {
		t.Fatalf("Akun tidak berhenti otomatis setelah mengalami 5 kegagalan beruntun")
	}

	// Cek status di database
	var accStatus, stopReason string
	err = pool.QueryRow(ctx, `SELECT status, COALESCE(stop_reason, '') FROM wa_accounts WHERE id = $1`, accID).Scan(&accStatus, &stopReason)
	if err != nil {
		t.Fatalf("query wa_accounts gagal: %v", err)
	}

	if accStatus != "berhenti" {
		t.Errorf("expected accStatus 'berhenti', got %q", accStatus)
	}
	if stopReason == "" {
		t.Errorf("stop_reason kosong, harus berisi alasan pemutus sirkuit")
	}
	t.Logf("Pemutus sirkuit berhasil: status=%s, reason=%s", accStatus, stopReason)
}

type drainBlockingSender struct {
	started chan struct{}
	release chan struct{}
}

func (s *drainBlockingSender) Send(ctx context.Context, accountID int64, phone string, msg wa.OutMessage) (wa.Result, error) {
	select {
	case <-s.started:
	default:
		close(s.started)
	}

	select {
	case <-s.release:
		return wa.Result{MessageID: "msg-drain-ok", SentAt: time.Now()}, nil
	case <-ctx.Done():
		return wa.Result{}, ctx.Err()
	}
}

// TestH3_Drain_InFlightSendCompleted membuktikan bahwa saat Drain dipanggil,
// Send yang sedang berjalan tidak dibatalkan paksa melainkan ditunggu sampai selesai.
func TestH3_Drain_InFlightSendCompleted(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Drain', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_drain_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_DRAIN_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign Drain Test', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'drain.csv', $2, 1, 1) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}

	var numberID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status)
		 VALUES ($1, $2, '62899997701', '62', 'tersedia') RETURNING id`,
		campaignID, batchID,
	).Scan(&numberID)
	if err != nil {
		t.Fatalf("setup number gagal: %v", err)
	}

	var accID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun Drain', $2, 'terhubung', 'fast') RETURNING id`,
		workerID, fmt.Sprintf("hash_drain_%d", time.Now().UnixNano()),
	).Scan(&accID)
	if err != nil {
		t.Fatalf("setup account gagal: %v", err)
	}

	sender := &drainBlockingSender{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	settings := newTestSettings()
	settings.SetJSON("rate_wage", "50")

	database := &db.DB{Pool: pool}
	eng := engine.New(database, sender, nil, settings)

	// Mulai worker
	eng.StartAccount(accID, workerID)

	// Tunggu sampai Send mulai berjalan (in-flight)
	select {
	case <-sender.started:
	case <-time.After(3 * time.Second):
		t.Fatalf("Send tidak dimulai dalam batas waktu")
	}

	// Panggil Drain di goroutine terpisah
	drainDone := make(chan struct{})
	go func() {
		drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = eng.Drain(drainCtx)
		close(drainDone)
	}()

	// Beri jeda kecil untuk memastikan Drain sedang menunggu worker
	time.Sleep(100 * time.Millisecond)

	// Lepaskan Send in-flight agar selesai sukses
	close(sender.release)

	// Drain harus selesai dengan bersih
	select {
	case <-drainDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("Drain tidak selesai setelah in-flight dilepaskan")
	}

	// Verifikasi: nomor harus selesai tercatat 'terkirim', bukan dibatalkan
	var status string
	err = pool.QueryRow(ctx, `SELECT status FROM campaign_numbers WHERE id = $1`, numberID).Scan(&status)
	if err != nil {
		t.Fatalf("query status gagal: %v", err)
	}
	if status != "terkirim" {
		t.Errorf("expected status 'terkirim' after drain completed in-flight send, got %q", status)
	}
}

// TestH3_Supervisor_CompletesCampaign membuktikan bahwa background supervisor
// secara otomatis mengubah campaign tanpa sisa nomor menjadi 'selesai',
// tepat sekali tanpa membebani jalur worker per pesan.
func TestH3_Supervisor_CompletesCampaign(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Supervisor', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_sup_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_SUP_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign Supervisor Test', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'sup.csv', $2, 1, 1) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}

	// Buat 1 nomor yang sudah 'terkirim' (sisa 0 nomor tersedia/dikirim)
	_, err = pool.Exec(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, sent_at)
		 VALUES ($1, $2, '62899996601', '62', 'terkirim', now())`,
		campaignID, batchID,
	)
	if err != nil {
		t.Fatalf("insert number failed: %v", err)
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, newTestSettings())

	// Jalankan SuperviseCampaigns
	completed, err := eng.SuperviseCampaigns(ctx)
	if err != nil {
		t.Fatalf("SuperviseCampaigns gagal: %v", err)
	}
	if completed < 1 {
		t.Errorf("expected at least 1 campaign completed by supervisor, got %d", completed)
	}

	// Verifikasi status campaign berubah menjadi 'selesai' dan finished_at terisi
	var status string
	var finishedAt *time.Time
	err = pool.QueryRow(ctx, `SELECT status, finished_at FROM campaigns WHERE id = $1`, campaignID).Scan(&status, &finishedAt)
	if err != nil {
		t.Fatalf("query campaign status gagal: %v", err)
	}

	if status != "selesai" {
		t.Errorf("expected campaign status 'selesai', got %q", status)
	}
	if finishedAt == nil {
		t.Errorf("expected finished_at to be filled, got nil")
	}
}
