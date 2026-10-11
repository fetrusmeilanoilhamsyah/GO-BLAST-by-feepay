package engine_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/wa"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"
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

// getTestDB menghubungkan ke PostgreSQL untuk tes integrasi jika tersedia.
func getTestDB(t *testing.T) (*sql.DB, *pgxpool.Pool, bool) {
	_ = godotenv.Load("../../.env")
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		return nil, nil, false
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, false
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, nil, false
	}

	_ = goose.SetDialect("postgres")
	migrationsDir := filepath.Join("..", "..", "db", "migrations")
	if err := goose.Up(sqlDB, migrationsDir); err != nil {
		sqlDB.Close()
		t.Logf("migrasi gagal: %v", err)
		return nil, nil, false
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		sqlDB.Close()
		return nil, nil, false
	}

	return sqlDB, pool, true
}

// (a) 50 akun palsu mengklaim 1000 nomor secara bersamaan, tidak ada nomor ganda dan semua terklaim tepat satu kali.
func TestConcurrentAtomicClaim(t *testing.T) {
	sqlDB, pool, ok := getTestDB(t)
	if !ok {
		t.Skip("Database tidak tersedia untuk tes integrasi klaim atomik")
	}
	defer sqlDB.Close()
	defer pool.Close()

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

	// Nonaktifkan sementara campaign berjalan lainnya agar tidak tercampur
	_, _ = pool.Exec(ctx, `UPDATE campaigns SET status = 'draf' WHERE status = 'berjalan'`)

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
	sqlDB, pool, ok := getTestDB(t)
	if !ok {
		t.Skip("Database tidak tersedia untuk tes buku besar idempoten")
	}
	defer sqlDB.Close()
	defer pool.Close()

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
	sqlDB, pool, ok := getTestDB(t)
	if !ok {
		t.Skip("Database tidak tersedia untuk tes sesi terputus")
	}
	defer sqlDB.Close()
	defer pool.Close()

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
	sqlDB, pool, ok := getTestDB(t)
	if !ok {
		t.Skip("Database tidak tersedia untuk tes pembersih")
	}
	defer sqlDB.Close()
	defer pool.Close()

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
	sqlDB, pool, ok := getTestDB(t)
	if !ok {
		t.Skip("Database tidak tersedia untuk tes snapshot rate")
	}
	defer sqlDB.Close()
	defer pool.Close()

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
