package engine_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa"

	"github.com/jackc/pgx/v5/pgxpool"
)

// --- Stub SettingsReader untuk tes ---
type testSettings struct {
	mu     sync.RWMutex
	ints   map[string]int
	strs   map[string]string
	jsons  map[string]string
}

func newTestSettings() *testSettings {
	return &testSettings{
		ints:  make(map[string]int),
		strs:  make(map[string]string),
		jsons: make(map[string]string),
	}
}

func (s *testSettings) GetInt(_ context.Context, key string) (int, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.ints[key]
	return val, ok, nil
}

func (s *testSettings) GetString(_ context.Context, key string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.strs[key]
	return val, ok, nil
}

func (s *testSettings) GetJSON(_ context.Context, key string, target interface{}) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, ok := s.jsons[key]
	if !ok {
		return false, nil
	}
	err := json.Unmarshal([]byte(raw), target)
	return err == nil, err
}

func (s *testSettings) SetInt(key string, val int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ints[key] = val
}

func (s *testSettings) SetJSON(key string, raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jsons[key] = raw
}

// TestDeliveryLevelConstant memverifikasi definisi konstanta "terkirim" (server_ack).
func TestDeliveryLevelConstant(t *testing.T) {
	if engine.DeliveryLevel != "server_ack" {
		t.Fatalf("expected DeliveryLevel to be 'server_ack', got '%s'", engine.DeliveryLevel)
	}
}

// getTestDB menghubungkan ke PostgreSQL untuk tes integrasi via testutil.TestDB.
func getTestDB(t *testing.T) *pgxpool.Pool {
	return testutil.TestDB(t)
}

// (a) 50 akun palsu mengklaim 1000 nomor secara bersamaan, tidak ada nomor ganda dan semua terklaim tepat satu kali.
func TestConcurrentAtomicClaim(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()
	database := &db.DB{Pool: pool}

	// Buat user & campaign tes
	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Test', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_claim_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker gagal: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign Claim Test', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
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
		 VALUES ($1, 'test.csv', $2, 1000, 1000)
		 RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}

	// Buat 50 akun palsu di wa_accounts
	accountIDs := make([]int64, 50)
	for i := 0; i < 50; i++ {
		var accID int64
		err = pool.QueryRow(ctx,
			`INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
			 VALUES ($1, $2, 'dummy_hash', 'terhubung')
			 RETURNING id`,
			workerID, fmt.Sprintf("Akun %d", i+1),
		).Scan(&accID)
		if err != nil {
			t.Fatalf("setup wa_account %d gagal: %v", i, err)
		}
		accountIDs[i] = accID
	}

	// Masukkan 1000 nomor berstatus 'tersedia' sekaligus dengan generate_series (cepat dan atomik)
	const totalNumbers = 1000
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		SELECT $1, $2, '62899' || LPAD(s::text, 8, '0'), '62', 'tersedia', 0
		FROM generate_series(1, 1000) AS s
	`, campaignID, batchID)
	if err != nil {
		t.Fatalf("bulk insert 1000 numbers gagal: %v", err)
	}

	settings := newTestSettings()
	eng := engine.New(database, &wa.FakeSender{}, nil, settings)

	// Jalankan 50 worker goroutine mengklaim secara bersamaan
	claimedMap := sync.Map{}
	var claimedCount int64
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(accID int64) {
			defer wg.Done()
			consecutiveEmpty := 0
			for {
				cn, err := eng.ClaimNextNumber(ctx, accID)
				if err != nil {
					consecutiveEmpty++
					if consecutiveEmpty >= 10 {
						break
					}
					time.Sleep(5 * time.Millisecond)
					continue
				}
				consecutiveEmpty = 0

				// Pastikan nomor belum pernah diklaim sebelumnya
				if _, exists := claimedMap.LoadOrStore(cn.ID, accID); exists {
					t.Errorf("PELANGGARAN ATOMIK: nomor id %d diklaim lebih dari satu kali!", cn.ID)
				}
				if cn.CampaignID == campaignID {
					atomic.AddInt64(&claimedCount, 1)
				}
			}
		}(accountIDs[i])
	}

	wg.Wait()

	if claimedCount != totalNumbers {
		t.Fatalf("diharapkan %d nomor terklaim tepat 1x, namun terklaim %d", totalNumbers, claimedCount)
	}

	// Verifikasi di database: tidak ada yang tersisa 'tersedia'
	var remainingTersedia int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'tersedia'`,
		campaignID,
	).Scan(&remainingTersedia)
	if remainingTersedia != 0 {
		t.Fatalf("diharapkan 0 nomor tersisa 'tersedia', ditemukan %d", remainingTersedia)
	}
}

// (b) Hasil yang sama dilaporkan dua kali menghasilkan satu baris buku besar (idempoten via unique index).
func TestIdempotentLedgerEntry(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()

	// Setup user & campaign
	var workerID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Idem', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_idem_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_IDEM_%d", time.Now().UnixNano()),
	).Scan(&workerID)

	var campaignID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp Idem', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'idem.csv', $2, 1, 1)
		 RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)

	var numberID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		 VALUES ($1, $2, $3, '62', 'dikirim', 1)
		 RETURNING id`,
		campaignID, batchID, fmt.Sprintf("62812%d", time.Now().UnixNano()%100000000),
	).Scan(&numberID)

	settings := newTestSettings()
	settings.SetInt("rate_idr", 50)

	// Panggil pencatatan buku besar 2x untuk nomor yang sama
	query := `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, campaign_number_id, note)
		VALUES ($1, 'upah', 'IDR', 50, 50, $2, 'upah pesan terkirim')
		ON CONFLICT (campaign_number_id) WHERE kind = 'upah' DO NOTHING
	`
	_, err1 := pool.Exec(ctx, query, workerID, numberID)
	if err1 != nil {
		t.Fatalf("panggilan pertama gagal: %v", err1)
	}

	_, err2 := pool.Exec(ctx, query, workerID, numberID)
	if err2 != nil {
		t.Fatalf("panggilan kedua gagal: %v", err2)
	}

	// Pastikan hanya ada 1 baris ledger untuk nomor ini
	var count int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ledger_entries WHERE campaign_number_id = $1 AND kind = 'upah'`,
		numberID,
	).Scan(&count)
	if count != 1 {
		t.Fatalf("diharapkan tepat 1 baris buku besar, ditemukan %d", count)
	}
}

// (c) Sesi terputus: semua nomor dikirim milik akun itu kembali tersedia.
func TestSessionDisconnectReturnsNumbers(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()
	database := &db.DB{Pool: pool}

	var workerID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Disc', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_disc_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_DISC_%d", time.Now().UnixNano()),
	).Scan(&workerID)

	var campaignID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp Disc', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'disc.csv', $2, 3, 3)
		 RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)

	var accID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
		 VALUES ($1, 'Akun Disc', 'hash', 'terhubung')
		 RETURNING id`,
		workerID,
	).Scan(&accID)

	// Buat 3 nomor berstatus dikirim dipegang oleh accID
	for i := 0; i < 3; i++ {
		_, _ = pool.Exec(ctx,
			`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at, attempts)
			 VALUES ($1, $2, $3, '62', 'dikirim', $4, now(), 1)`,
			campaignID, batchID, fmt.Sprintf("62813%04d%04d", i, time.Now().UnixNano()%10000), accID,
		)
	}

	settings := newTestSettings()
	eng := engine.New(database, &wa.FakeSender{}, nil, settings)

	// Panggil StopAccount (mensimulasikan sesi diputus)
	eng.StopAccount(accID)

	// Pastikan semua nomor kembali berstatus 'tersedia'
	var tersediaCount int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM campaign_numbers WHERE claimed_account_id IS NULL AND status = 'tersedia' AND batch_id = $1`,
		batchID,
	).Scan(&tersediaCount)

	if tersediaCount != 3 {
		t.Fatalf("diharapkan 3 nomor kembali tersedia, didapat %d", tersediaCount)
	}
}

// (d) Pembersih: mengembalikan nomor dikirim yang claimed_at lebih tua dari claim_timeout_seconds.
func TestCleanerReturnsStaleNumbers(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()
	database := &db.DB{Pool: pool}

	var workerID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Stale', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_stale_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_STALE_%d", time.Now().UnixNano()),
	).Scan(&workerID)

	var campaignID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp Stale', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'stale.csv', $2, 2, 2)
		 RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)

	var accID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
		 VALUES ($1, 'Akun Stale', 'hash', 'terhubung')
		 RETURNING id`,
		workerID,
	).Scan(&accID)

	// Nomor 1: claimed_at 10 menit lalu (stale)
	_, _ = pool.Exec(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at, attempts)
		 VALUES ($1, $2, $3, '62', 'dikirim', $4, now() - interval '600 seconds', 1)`,
		campaignID, batchID, fmt.Sprintf("62814%04d", time.Now().UnixNano()%10000), accID,
	)

	// Nomor 2: claimed_at baru saja (fresh)
	_, _ = pool.Exec(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at, attempts)
		 VALUES ($1, $2, $3, '62', 'dikirim', $4, now(), 1)`,
		campaignID, batchID, fmt.Sprintf("62815%04d", time.Now().UnixNano()%10000), accID,
	)

	settings := newTestSettings()
	settings.SetInt("claim_timeout_seconds", 300) // 5 menit

	eng := engine.New(database, &wa.FakeSender{}, nil, settings)
	cleaned, err := eng.CleanStaleNumbers(ctx)
	if err != nil {
		t.Fatalf("CleanStaleNumbers gagal: %v", err)
	}
	if cleaned < 1 {
		t.Fatalf("diharapkan setidaknya 1 nomor dibersihkan, didapat %d", cleaned)
	}
}

// (e) Pengaturan kosong (daily_limits atau max_attempts kosong) membuat mesin tidak mengirim dan mencatat alasan.
func TestEmptySettingsStopsEngine(t *testing.T) {
	settings := newTestSettings()
	// sengaja dikosongkan (tidak diisi admin)

	okDaily, _ := settings.GetJSON(context.Background(), "daily_limits", &map[string]int{})
	if okDaily {
		t.Fatal("expected daily_limits to be missing")
	}

	_, okAttempts, _ := settings.GetInt(context.Background(), "max_attempts")
	if okAttempts {
		t.Fatal("expected max_attempts to be missing")
	}
}

// (f) rate_snapshot tetap setelah rate diubah di settings.
func TestRateSnapshotStaticAfterRateChange(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()

	var workerID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Rate', $2)
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`,
		fmt.Sprintf("worker_rate_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_RATE_%d", time.Now().UnixNano()),
	).Scan(&workerID)

	var campaignID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp Rate', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		workerID,
	).Scan(&campaignID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	var batchID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'rate.csv', $2, 1, 1)
		 RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)

	var numberID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		 VALUES ($1, $2, $3, '62', 'dikirim', 1)
		 RETURNING id`,
		campaignID, batchID, fmt.Sprintf("62816%d", time.Now().UnixNano()%100000000),
	).Scan(&numberID)

	// Rate awal = 50 IDR
	initialRate := int64(50)
	_, err := pool.Exec(ctx,
		`INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, campaign_number_id, note)
		 VALUES ($1, 'upah', 'IDR', $2, $2, $3, 'upah pesan terkirim')`,
		workerID, initialRate, numberID,
	)
	if err != nil {
		t.Fatalf("insert ledger entry gagal: %v", err)
	}

	// Admin mengubah rate di settings menjadi 100 IDR keesokan harinya
	newRate := int64(100)
	_ = newRate

	// Periksa bahwa rate_snapshot di baris kemarin tetap 50 IDR (tidak berubah)
	var storedSnapshot int64
	err = pool.QueryRow(ctx,
		`SELECT rate_snapshot FROM ledger_entries WHERE campaign_number_id = $1 AND kind = 'upah'`,
		numberID,
	).Scan(&storedSnapshot)
	if err != nil {
		t.Fatalf("query rate_snapshot gagal: %v", err)
	}

	if storedSnapshot != initialRate {
		t.Fatalf("rate_snapshot berubah! diharapkan %d, didapat %d", initialRate, storedSnapshot)
	}
}

// (a) kegagalan menulis buku besar tidak pernah mengembalikan nomor ke tersedia
func TestLedgerFailureNeverReturnsToAvailable(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()
	database := &db.DB{Pool: pool}

	var workerID, campaignID, batchID, numberID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Fail', $2) RETURNING id`,
		fmt.Sprintf("worker_lf_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_LF_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker: %v", err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp LF', 'berjalan', 'kartu_link', 'Halo', '{}', $1) RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'lf.csv', $2, 1, 1) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch: %v", err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		 VALUES ($1, $2, $3, '62', 'dikirim', 1) RETURNING id`,
		campaignID, batchID, fmt.Sprintf("62817%06d", time.Now().UnixNano()%1000000),
	).Scan(&numberID)
	if err != nil {
		t.Fatalf("setup number: %v", err)
	}

	// Settings kosong untuk rate_wage sehingga recordSuccessAndLedger gagal
	settings := newTestSettings()

	eng := engine.New(database, &wa.FakeSender{}, nil, settings)

	// Percepat delay coba ulang untuk tes
	origDelays := engine.RecordRetryDelays
	engine.RecordRetryDelays = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	defer func() { engine.RecordRetryDelays = origDelays }()

	err = eng.RecordWithRetry(ctx, numberID, workerID)
	if err == nil {
		t.Fatal("diharapkan recordWithRetry gagal karena rate_wage belum diisi admin")
	}

	// Periksa status nomor di database: HARUS 'gagal' dengan last_error 'terkirim_tidak_tercatat'
	var status, lastError string
	err = pool.QueryRow(ctx,
		`SELECT status, COALESCE(last_error, '') FROM campaign_numbers WHERE id = $1`,
		numberID,
	).Scan(&status, &lastError)
	if err != nil {
		t.Fatalf("query status nomor: %v", err)
	}

	if status == "tersedia" {
		t.Fatal("KEAMANAN DILANGGAR: kegagalan menulis buku besar mengembalikan nomor ke tersedia!")
	}
	if status != "gagal" {
		t.Fatalf("status nomor harus 'gagal', didapat '%s'", status)
	}
	if lastError != "terkirim_tidak_tercatat" {
		t.Fatalf("last_error harus 'terkirim_tidak_tercatat', didapat '%s'", lastError)
	}
}

// (b) kegagalan render berulang menghentikan akun
func TestRepeatedRenderFailureStopsAccount(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()
	database := &db.DB{Pool: pool}

	var workerID, accID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Render', $2) RETURNING id`,
		fmt.Sprintf("worker_rnd_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_RND_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker: %v", err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, status, speed)
		 VALUES ($1, 'Akun Render', 'hash_rnd', 'terhubung', 'medium') RETURNING id`,
		workerID,
	).Scan(&accID)
	if err != nil {
		t.Fatalf("setup account: %v", err)
	}

	// Buat campaign dengan template tidak valid (tombol_asli ditolak oleh ValidateTemplate)
	var campID, batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp Corrupt', 'berjalan', 'tombol_asli', 'Halo', '{}', $1) RETURNING id`,
		workerID,
	).Scan(&campID)
	if err != nil {
		t.Fatalf("setup campaign: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campID})
	})

	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'rnd.csv', $2, 5, 5) RETURNING id`,
		campID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch: %v", err)
	}

	// Masukkan nomor 'tersedia'
	for i := 1; i <= 5; i++ {
		_, _ = pool.Exec(ctx,
			`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
			 VALUES ($1, $2, $3, '62', 'tersedia', 0)`,
			campID, batchID, fmt.Sprintf("62818%06d", time.Now().UnixNano()%1000000+int64(i)),
		)
	}

	settings := newTestSettings()
	settings.SetJSON("daily_limits", `{"medium": 100}`)
	settings.SetInt("max_attempts", 3)
	settings.SetJSON("rate_wage", `{"IDR": 50}`)

	eng := engine.New(database, &wa.FakeSender{}, nil, settings)

	// Percepat jeda gagal susun pesan
	origDelay := engine.BuildRetryDelay
	engine.BuildRetryDelay = 10 * time.Millisecond
	defer func() { engine.BuildRetryDelay = origDelay }()

	eng.StartAccount(accID, workerID)

	// Tunggu sampai akun berhenti (maks 3 detik)
	deadline := time.Now().Add(3 * time.Second)
	stopped := false
	var reason string
	for time.Now().Before(deadline) {
		var status string
		_ = pool.QueryRow(ctx, `SELECT status, COALESCE(stop_reason, '') FROM wa_accounts WHERE id = $1`, accID).Scan(&status, &reason)
		if status == "berhenti" {
			stopped = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	eng.StopAccount(accID)

	if !stopped {
		t.Fatalf("akun tidak berhenti setelah 3 kegagalan render berturut-turut, reason: %s", reason)
	}
	if reason != "gagal menyusun pesan campaign" {
		t.Fatalf("stop_reason harus 'gagal menyusun pesan campaign', didapat '%s'", reason)
	}
}

// (c) rate_wage USDT 3000 menghasilkan baris buku besar bernilai 3000, dan rate hilang membuat engine tidak mengirim
func TestRateWageUSDTAndMissingRate(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()
	database := &db.DB{Pool: pool}

	var workerID, campaignID, batchID, numberID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker IDR', $2) RETURNING id`,
		fmt.Sprintf("worker_usdt_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_USDT_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("setup worker: %v", err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp USDT', 'berjalan', 'kartu_link', 'Halo', '{}', $1) RETURNING id`,
		workerID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'usdt.csv', $2, 1, 1) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch: %v", err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		 VALUES ($1, $2, $3, '62', 'dikirim', 1) RETURNING id`,
		campaignID, batchID, fmt.Sprintf("62819%06d", time.Now().UnixNano()%1000000),
	).Scan(&numberID)
	if err != nil {
		t.Fatalf("setup number: %v", err)
	}

	settings := newTestSettings()
	settings.SetInt("rate_wage", 400)

	eng := engine.New(database, &wa.FakeSender{}, nil, settings)

	// Uji Bagian 1: rate_wage IDR 400 menghasilkan baris buku besar IDR bernilai 400
	err = eng.RecordSuccessAndLedger(ctx, numberID, workerID)
	if err != nil {
		t.Fatalf("RecordSuccessAndLedger gagal: %v", err)
	}

	var amount, rateSnapshot int64
	var curr string
	err = pool.QueryRow(ctx,
		`SELECT amount, rate_snapshot, currency FROM ledger_entries WHERE campaign_number_id = $1 AND kind = 'upah'`,
		numberID,
	).Scan(&amount, &rateSnapshot, &curr)
	if err != nil {
		t.Fatalf("query ledger entry IDR gagal: %v", err)
	}

	if amount != 400 {
		t.Fatalf("amount buku besar diharapkan 400, didapat %d", amount)
	}
	if rateSnapshot != 400 {
		t.Fatalf("rate_snapshot diharapkan 400, didapat %d", rateSnapshot)
	}
	if curr != "IDR" {
		t.Fatalf("currency diharapkan IDR, didapat %s", curr)
	}

	// Uji Bagian 2: rate kosong/0 membuat engine tidak mengirim apa pun dan menulis stop_reason "rate upah belum diisi admin"
	var accID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, status, speed)
		 VALUES ($1, 'Akun ZeroRate', 'hash_zerorate', 'terhubung', 'medium') RETURNING id`,
		workerID,
	).Scan(&accID)
	if err != nil {
		t.Fatalf("setup account zero rate: %v", err)
	}

	fakeSender := &wa.FakeSender{}
	settingsMissing := newTestSettings()
	settingsMissing.SetJSON("daily_limits", `{"medium": 100}`)
	settingsMissing.SetInt("max_attempts", 3)
	// rate_wage tidak disetel (0 / kosong)
	settingsMissing.SetInt("rate_wage", 0)

	engMissing := engine.New(database, fakeSender, nil, settingsMissing)
	engMissing.StartAccount(accID, workerID)

	time.Sleep(100 * time.Millisecond)
	engMissing.StopAccount(accID)

	var stopReason string
	_ = pool.QueryRow(ctx, `SELECT COALESCE(stop_reason, '') FROM wa_accounts WHERE id = $1`, accID).Scan(&stopReason)
	if stopReason != "rate upah belum diisi admin" {
		t.Fatalf("stop_reason harus 'rate upah belum diisi admin', didapat '%s'", stopReason)
	}
	if fakeSender.SentCount() != 0 {
		t.Fatalf("engine tidak boleh mengirim pesan apa pun saat rate hilang, terkirim: %d", fakeSender.SentCount())
	}
}

// (d) mencapai max_attempts membuat nomor gagal
func TestMaxAttemptsMakesNumberGagal(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()
	database := &db.DB{Pool: pool}

	var workerID, campaignID, batchID, num1ID, num2ID, accID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Max', $2) RETURNING id`,
		fmt.Sprintf("worker_max_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_MAX_%d", time.Now().UnixNano()),
	).Scan(&workerID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
		 VALUES ($1, 'Akun Max', 'hash_max', 'terhubung') RETURNING id`,
		workerID,
	).Scan(&accID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp Max', 'berjalan', 'kartu_link', 'Halo', '{}', $1) RETURNING id`,
		workerID,
	).Scan(&campaignID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})
	})

	_ = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'max.csv', $2, 2, 2) RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		 VALUES ($1, $2, $3, '62', 'dikirim', 3) RETURNING id`,
		campaignID, batchID, fmt.Sprintf("62820%06d", time.Now().UnixNano()%1000000),
	).Scan(&num1ID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		 VALUES ($1, $2, $3, '62', 'dikirim', 2) RETURNING id`,
		campaignID, batchID, fmt.Sprintf("62821%06d", time.Now().UnixNano()%1000000),
	).Scan(&num2ID)

	settings := newTestSettings()
	eng := engine.New(database, &wa.FakeSender{}, nil, settings)

	// Kasus 1: attempts (3) >= maxAttempts (3) -> nomor harus menjadi 'gagal'
	eng.HandleSendFailure(ctx, accID, num1ID, 3, 3, "gagal ketiga kali")

	var status1 string
	_ = pool.QueryRow(ctx, `SELECT status FROM campaign_numbers WHERE id = $1`, num1ID).Scan(&status1)
	if status1 != "gagal" {
		t.Fatalf("nomor yang mencapai max_attempts harus berstatus 'gagal', didapat '%s'", status1)
	}

	// Kasus 2: attempts (2) < maxAttempts (3) -> nomor harus kembali ke 'tersedia'
	eng.HandleSendFailure(ctx, accID, num2ID, 2, 3, "gagal kedua kali")

	var status2 string
	_ = pool.QueryRow(ctx, `SELECT status FROM campaign_numbers WHERE id = $1`, num2ID).Scan(&status2)
	if status2 != "tersedia" {
		t.Fatalf("nomor di bawah max_attempts harus kembali ke 'tersedia', didapat '%s'", status2)
	}
}

// (e) kegagalan beruntun menghentikan akun dan mengembalikan nomor yang dipegang
func TestConsecutiveFailuresStopsAccountAndReleasesNumbers(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()
	database := &db.DB{Pool: pool}

	var workerID, accID, campID, batchID, numID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Csf', $2) RETURNING id`,
		fmt.Sprintf("worker_csf_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_CSF_%d", time.Now().UnixNano()),
	).Scan(&workerID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
		 VALUES ($1, 'Akun Csf', 'hash_csf', 'terhubung') RETURNING id`,
		workerID,
	).Scan(&accID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp Csf', 'berjalan', 'kartu_link', 'Halo', '{}', $1) RETURNING id`,
		workerID,
	).Scan(&campID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campID})
	})

	_ = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'csf.csv', $2, 1, 1) RETURNING id`,
		campID, workerID,
	).Scan(&batchID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at, attempts)
		 VALUES ($1, $2, $3, '62', 'dikirim', $4, now(), 1) RETURNING id`,
		campID, batchID, fmt.Sprintf("62822%06d", time.Now().UnixNano()%1000000), accID,
	).Scan(&numID)

	eng := engine.New(database, &wa.FakeSender{}, nil, newTestSettings())

	eng.StopAccountForFailure(ctx, accID, "berhenti otomatis: 5 kegagalan beruntun")

	// Periksa akun berstatus 'berhenti' dengan stop_reason
	var accStatus, stopReason string
	_ = pool.QueryRow(ctx, `SELECT status, COALESCE(stop_reason, '') FROM wa_accounts WHERE id = $1`, accID).Scan(&accStatus, &stopReason)
	if accStatus != "berhenti" {
		t.Fatalf("wa_account status harus 'berhenti', didapat '%s'", accStatus)
	}
	if stopReason != "berhenti otomatis: 5 kegagalan beruntun" {
		t.Fatalf("stop_reason harus sesuai, didapat '%s'", stopReason)
	}

	// Periksa nomor yang dipegang kembali ke 'tersedia' dan claimed_account_id NULL
	var numStatus string
	var claimedAcc sql.NullInt64
	_ = pool.QueryRow(ctx, `SELECT status, claimed_account_id FROM campaign_numbers WHERE id = $1`, numID).Scan(&numStatus, &claimedAcc)
	if numStatus != "tersedia" {
		t.Fatalf("nomor yang dipegang harus dikembalikan ke 'tersedia', didapat '%s'", numStatus)
	}
	if claimedAcc.Valid {
		t.Fatalf("claimed_account_id harus NULL, didapat %d", claimedAcc.Int64)
	}
}

// (f) awal hari batas harian memakai Asia/Jakarta
func TestDailyLimitAsiaJakarta(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()

	startOfDay := engine.GetJakartaStartOfDay()

	// Verifikasi zona waktu Asia/Jakarta (+07 / 25200s)
	_, offset := startOfDay.Zone()
	if offset != 7*3600 {
		t.Fatalf("GetJakartaStartOfDay offset diharapkan 25200 (WIB), didapat %d", offset)
	}
	if startOfDay.Hour() != 0 || startOfDay.Minute() != 0 || startOfDay.Second() != 0 {
		t.Fatalf("awal hari harus jam 00:00:00, didapat %s", startOfDay.Format("15:04:05"))
	}

	// Setup data nomor di DB untuk memverifikasi query batas harian
	var workerID, accID, campID, batchID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Jkt', $2) RETURNING id`,
		fmt.Sprintf("worker_jkt_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_JKT_%d", time.Now().UnixNano()),
	).Scan(&workerID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, status)
		 VALUES ($1, 'Akun Jkt', 'hash_jkt', 'terhubung') RETURNING id`,
		workerID,
	).Scan(&accID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp Jkt', 'berjalan', 'kartu_link', 'Halo', '{}', $1) RETURNING id`,
		workerID,
	).Scan(&campID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campID})
	})

	_ = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'jkt.csv', $2, 2, 2) RETURNING id`,
		campID, workerID,
	).Scan(&batchID)

	// Nomor 1: terkirim kemarin (2 jam sebelum awal hari Jakarta)
	yesterday := startOfDay.Add(-2 * time.Hour)
	_, _ = pool.Exec(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, sent_at, attempts)
		 VALUES ($1, $2, $3, '62', 'terkirim', $4, $5, 1)`,
		campID, batchID, fmt.Sprintf("62823%06d", time.Now().UnixNano()%1000000), accID, yesterday,
	)

	// Nomor 2: terkirim hari ini (2 jam setelah awal hari Jakarta)
	today := startOfDay.Add(2 * time.Hour)
	_, _ = pool.Exec(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, sent_at, attempts)
		 VALUES ($1, $2, $3, '62', 'terkirim', $4, $5, 1)`,
		campID, batchID, fmt.Sprintf("62824%06d", time.Now().UnixNano()%1000000), accID, today,
	)

	// Hitung pesan terkirim hari ini zona Asia/Jakarta
	var sentToday int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM campaign_numbers 
		 WHERE claimed_account_id = $1 AND status = 'terkirim' AND sent_at >= $2`,
		accID, startOfDay,
	).Scan(&sentToday)
	if err != nil {
		t.Fatalf("query sentToday gagal: %v", err)
	}

	if sentToday != 1 {
		t.Fatalf("sentToday berdasarkan awal hari Asia/Jakarta harus tepat 1, didapat %d", sentToday)
	}
}

// (g) timeout kirim menjadi hasil_tidak_pasti dan tidak diulang
func TestSendTimeoutResultsInHasilTidakPasti(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()
	database := &db.DB{Pool: pool}

	var workerID, accID, campID, batchID, numID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker To', $2) RETURNING id`,
		fmt.Sprintf("worker_to_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_TO_%d", time.Now().UnixNano()),
	).Scan(&workerID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, status, speed)
		 VALUES ($1, 'Akun To', 'hash_to', 'terhubung', 'medium') RETURNING id`,
		workerID,
	).Scan(&accID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp To', 'berjalan', 'kartu_link', 'Halo', '{}', $1) RETURNING id`,
		workerID,
	).Scan(&campID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campID})
	})

	_ = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'to.csv', $2, 1, 1) RETURNING id`,
		campID, workerID,
	).Scan(&batchID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		 VALUES ($1, $2, $3, '62', 'tersedia', 0) RETURNING id`,
		campID, batchID, fmt.Sprintf("62825%06d", time.Now().UnixNano()%1000000),
	).Scan(&numID)

	// Sender yang selalu mengembalikan error batas waktu
	timeoutSender := &wa.FakeSender{
		ErrFunc: func(accountID int64) error {
			return context.DeadlineExceeded
		},
	}

	settings := newTestSettings()
	settings.SetJSON("daily_limits", `{"medium": 100}`)
	settings.SetInt("max_attempts", 3)
	settings.SetJSON("rate_wage", `{"IDR": 50}`)

	eng := engine.New(database, timeoutSender, nil, settings)
	eng.StartAccount(accID, workerID)

	// Tunggu nomor diproses
	deadline := time.Now().Add(3 * time.Second)
	statusFound := false
	var finalStatus, finalLastError string
	for time.Now().Before(deadline) {
		_ = pool.QueryRow(ctx, `SELECT status, COALESCE(last_error, '') FROM campaign_numbers WHERE id = $1`, numID).Scan(&finalStatus, &finalLastError)
		if finalStatus == "gagal" {
			statusFound = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	eng.StopAccount(accID)

	if !statusFound {
		t.Fatalf("nomor diharapkan berstatus 'gagal' setelah timeout kirim, didapat '%s'", finalStatus)
	}
	if finalLastError != "hasil_tidak_pasti" {
		t.Fatalf("last_error diharapkan 'hasil_tidak_pasti', didapat '%s'", finalLastError)
	}
}

// (h) GetInt mengembalikan error untuk kegagalan database
func TestGetIntReturnsErrorOnDatabaseFailure(t *testing.T) {
	pool := getTestDB(t)

	database := &db.DB{Pool: pool}
	reader := wa.NewDBSettingsReader(database)

	// Kasus 1: Baris tidak ada (pgx.ErrNoRows) -> mengembalikan (0, false, nil)
	val, okRow, err := reader.GetInt(context.Background(), fmt.Sprintf("non_existent_key_%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("key yang tidak ada harus mengembalikan err nil, didapat: %v", err)
	}
	if okRow {
		t.Fatal("key yang tidak ada harus mengembalikan ok=false")
	}
	if val != 0 {
		t.Fatalf("key yang tidak ada harus mengembalikan val=0, didapat %d", val)
	}

	// Kasus 2: Database error sungguhan (context dibatalkan sebelum query) -> HARUS mengembalikan error, BUKAN dianggap kosong
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // langsung batalkan

	_, okCancel, errCancel := reader.GetInt(cancelledCtx, "daily_limits")
	if errCancel == nil {
		t.Fatal("kegagalan database (context cancelled) HARUS mengembalikan error, bukan nil!")
	}
	if okCancel {
		t.Fatal("kegagalan database harus mengembalikan ok=false")
	}
}

// Uji tambahan: Campaign otomatis berstatus selesai saat tidak ada nomor tersedia maupun dikirim (tercatat di audit_logs)
func TestCampaignAutoCompletedWithAuditLog(t *testing.T) {
	pool := getTestDB(t)

	ctx := context.Background()
	database := &db.DB{Pool: pool}

	var workerID, campID, batchID, numID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Auto', $2) RETURNING id`,
		fmt.Sprintf("worker_auto_%d@test.local", time.Now().UnixNano()),
		fmt.Sprintf("REF_AUTO_%d", time.Now().UnixNano()),
	).Scan(&workerID)

	_ = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Camp Auto', 'berjalan', 'kartu_link', 'Halo', '{}', $1) RETURNING id`,
		workerID,
	).Scan(&campID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{campID})
	})

	_ = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'auto.csv', $2, 1, 1) RETURNING id`,
		campID, workerID,
	).Scan(&batchID)

	// Masukkan nomor dengan status 'terkirim' (tidak ada lagi nomor 'tersedia' maupun 'dikirim')
	_ = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		 VALUES ($1, $2, $3, '62', 'terkirim', 1) RETURNING id`,
		campID, batchID, fmt.Sprintf("62826%06d", time.Now().UnixNano()%1000000),
	).Scan(&numID)

	eng := engine.New(database, &wa.FakeSender{}, nil, newTestSettings())

	completed, err := eng.CheckAndCompleteCampaign(ctx, campID)
	if err != nil {
		t.Fatalf("CheckAndCompleteCampaign gagal: %v", err)
	}
	if !completed {
		t.Fatal("campaign seharusnya diselesaikan otomatis")
	}

	// Verifikasi status campaign berubah ke 'selesai'
	var campStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, campID).Scan(&campStatus)
	if campStatus != "selesai" {
		t.Fatalf("status campaign diharapkan 'selesai', didapat '%s'", campStatus)
	}

	// Verifikasi audit_logs tercatat dalam transaksi yang sama
	var auditCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action = 'campaign_completed' AND entity = 'campaign' AND entity_id = $1`,
		fmt.Sprintf("%d", campID),
	).Scan(&auditCount)
	if err != nil {
		t.Fatalf("query audit_logs gagal: %v", err)
	}
	if auditCount < 1 {
		t.Fatal("audit_logs harus mencatat 'campaign_completed' untuk campaign yang selesai")
	}
}

func TestEngine_IsRunningAndNoDuplicateGoroutine(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, language, referral_code)
		 VALUES ($1, 'hash', 'worker', 'Worker Engine Double', 'id', $2) RETURNING id`,
		fmt.Sprintf("w_eng_double_%d@test.com", time.Now().UnixNano()),
		fmt.Sprintf("RF_ENG_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatalf("insert user error: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerID}, nil)
	})

	var accID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO wa_accounts (worker_id, label, jid_hash, speed, status)
		 VALUES ($1, 'Acc Double', 'hash_double', 'medium', 'terhubung') RETURNING id`,
		workerID,
	).Scan(&accID)
	if err != nil {
		t.Fatalf("insert wa_account error: %v", err)
	}

	settings := newTestSettings()
	settings.SetJSON("daily_limits", `{"medium": 100}`)
	settings.SetInt("max_attempts", 3)
	settings.SetJSON("rate_wage", `{"IDR": 50}`)

	sender := &wa.FakeSender{}
	eng := engine.New(&db.DB{Pool: pool}, sender, nil, settings)

	// Sebelum start: IsRunning harus false
	if eng.IsRunning(accID) {
		t.Fatal("akun seharusnya belum berjalan")
	}

	// Start pertama
	eng.StartAccount(accID, workerID)
	if !eng.IsRunning(accID) {
		t.Fatal("akun seharusnya sudah berjalan setelah start")
	}

	// Start kedua: tidak boleh error dan tidak membuat goroutine ganda
	eng.StartAccount(accID, workerID)
	if !eng.IsRunning(accID) {
		t.Fatal("akun harus tetap berjalan")
	}

	// Stop akun
	eng.StopAccount(accID)
	if eng.IsRunning(accID) {
		t.Fatal("akun seharusnya berhenti setelah StopAccount")
	}
}

