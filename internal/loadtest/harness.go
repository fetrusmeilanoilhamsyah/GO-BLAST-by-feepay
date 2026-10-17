//go:build load

package loadtest

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"goblast/internal/testutil"
)

// RequireSafety memvalidasi aturan keselamatan keras:
// 1. APP_ENV tidak boleh 'production'.
// 2. Database wajib berakhiran '_test'.
// 3. Menolak jika URL mengarah ke database utama/dev.
func RequireSafety(t testing.TB) {
	t.Helper()

	env := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if env == "production" || env == "prod" {
		t.Fatal("FATAL: Harness uji beban DITOLAK karena APP_ENV adalah production!")
	}

	testDSN := testutil.TestDBURL(t)
	cfg, err := pgxpool.ParseConfig(testDSN)
	if err != nil {
		t.Fatalf("RequireSafety: parse TEST_DATABASE_URL gagal: %v", err)
	}

	dbName := cfg.ConnConfig.Database
	if !strings.HasSuffix(dbName, "_test") {
		t.Fatalf("FATAL: Database %q tidak berakhiran '_test'. Harness hanya boleh jalan di database tes!", dbName)
	}
}

// GetTestPool membuat koneksi pgxpool khusus untuk load test dengan opsi tracer dan max conns.
func GetTestPool(t testing.TB, maxConns int32, tracer pgx.QueryTracer) *pgxpool.Pool {
	t.Helper()
	RequireSafety(t)

	// Pastikan migrasi up-to-date
	_ = testutil.TestDB(t)

	testDSN := testutil.TestDBURL(t)
	cfg, err := pgxpool.ParseConfig(testDSN)
	if err != nil {
		t.Fatalf("GetTestPool: parse config gagal: %v", err)
	}

	if maxConns > 0 {
		cfg.MaxConns = maxConns
	} else {
		cfg.MaxConns = 30
	}
	cfg.MinConns = 2

	if tracer != nil {
		cfg.ConnConfig.Tracer = tracer
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("GetTestPool: gagal menghubungkan pool: %v", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("GetTestPool: ping database gagal: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}

// PercentileTracker mengumpulkan sampel durasi dan menghitung nilai persentil (p50, p90, p95, p99, max, mean).
type PercentileTracker struct {
	mu      sync.Mutex
	samples []time.Duration
}

func (pt *PercentileTracker) Record(d time.Duration) {
	pt.mu.Lock()
	pt.samples = append(pt.samples, d)
	pt.mu.Unlock()
}

func (pt *PercentileTracker) Count() int {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return len(pt.samples)
}

func (pt *PercentileTracker) Percentile(p float64) time.Duration {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	n := len(pt.samples)
	if n == 0 {
		return 0
	}

	sorted := make([]time.Duration, n)
	copy(sorted, pt.samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := int(float64(n) * p / 100.0)
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
}

func (pt *PercentileTracker) Mean() time.Duration {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	if len(pt.samples) == 0 {
		return 0
	}
	var total time.Duration
	for _, d := range pt.samples {
		total += d
	}
	return total / time.Duration(len(pt.samples))
}

func (pt *PercentileTracker) Max() time.Duration {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	if len(pt.samples) == 0 {
		return 0
	}
	var m time.Duration
	for _, d := range pt.samples {
		if d > m {
			m = d
		}
	}
	return m
}

// RoundTripTracer menghitung setiap panggilan query ke database untuk mengukur round-trip per pesan.
type RoundTripTracer struct {
	TotalQueries atomic.Uint64
}

func (rt *RoundTripTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	rt.TotalQueries.Add(1)
	return ctx
}

func (rt *RoundTripTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {}

// ChaosLatencyTracer menyuntikkan latensi buatan secara acak dengan probabilitas tertentu (Skenario L3).
type ChaosLatencyTracer struct {
	Probability    float64
	Delay          time.Duration
	DelayedQueries atomic.Uint64
	NormalQueries  atomic.Uint64
	TotalQueries   atomic.Uint64
	active         atomic.Bool
}

func NewChaosLatencyTracer(prob float64, delay time.Duration) *ChaosLatencyTracer {
	t := &ChaosLatencyTracer{
		Probability: prob,
		Delay:       delay,
	}
	t.active.Store(true)
	return t
}

func (ct *ChaosLatencyTracer) SetActive(active bool) {
	ct.active.Store(active)
}

func (ct *ChaosLatencyTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ct.TotalQueries.Add(1)
	if ct.active.Load() && ct.Probability > 0 {
		// Jangan tunda ping / readyz internal jika query adalah SELECT 1 murni
		if !strings.EqualFold(strings.TrimSpace(data.SQL), "SELECT 1") {
			if rand.Float64() < ct.Probability {
				ct.DelayedQueries.Add(1)
				time.Sleep(ct.Delay)
				return ctx
			}
		}
	}
	ct.NormalQueries.Add(1)
	return ctx
}

func (ct *ChaosLatencyTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {}

// Helper pembuatan data uji beban:

func CreateLoadWorker(ctx context.Context, pool *pgxpool.Pool, prefix string) (int64, error) {
	nonce := time.Now().UnixNano()
	var workerID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, display_name, referral_code, status)
		 VALUES ($1, 'hash', 'worker', $2, $3, 'active')
		 RETURNING id`,
		fmt.Sprintf("worker_%s_%d@test.local", prefix, nonce),
		fmt.Sprintf("Worker %s", prefix),
		fmt.Sprintf("REF_%s_%d", prefix, nonce),
	).Scan(&workerID)
	return workerID, err
}

func CreateLoadCampaign(ctx context.Context, pool *pgxpool.Pool, workerID int64, name string) (int64, int64, error) {
	var campaignID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ($1, 'berjalan', 'kartu_link', 'Halo pesan promo load test', '{}', $2)
		 RETURNING id`,
		name, workerID,
	).Scan(&campaignID)
	if err != nil {
		return 0, 0, err
	}

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'loadtest_batch.csv', $2, 0, 0)
		 RETURNING id`,
		campaignID, workerID,
	).Scan(&batchID)
	if err != nil {
		return 0, 0, err
	}

	return campaignID, batchID, nil
}

// PopulateFictitiousNumbers memasukkan sejumlah nomor fiktif (prefix 628100...) secara cepat via generate_series.
func PopulateFictitiousNumbers(ctx context.Context, pool *pgxpool.Pool, campaignID, batchID int64, count int, startIdx int) error {
	endIdx := startIdx + count - 1
	query := `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
		SELECT $1, $2, '62810' || LPAD(s::text, 8, '0'), '62', 'tersedia', 0
		FROM generate_series($3::int, $4::int) AS s
	`
	_, err := pool.Exec(ctx, query, campaignID, batchID, startIdx, endIdx)
	if err != nil {
		return fmt.Errorf("PopulateFictitiousNumbers gagal: %w", err)
	}

	_, _ = pool.Exec(ctx, `UPDATE number_batches SET total = total + $1, accepted = accepted + $1 WHERE id = $2`, count, batchID)
	return nil
}

// CreateLoadAccounts membuat sejumlah akun WhatsApp dengan profil kecepatan tertentu.
func CreateLoadAccounts(ctx context.Context, pool *pgxpool.Pool, workerID int64, count int, speed string, prefix string) ([]int64, error) {
	accIDs := make([]int64, count)
	nowNano := time.Now().UnixNano()
	for i := 0; i < count; i++ {
		label := fmt.Sprintf("Acc_%s_%d", prefix, i+1)
		jidHash := fmt.Sprintf("jid_%s_%d_%d", prefix, nowNano, i+1)
		var id int64
		err := pool.QueryRow(ctx,
			`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
			 VALUES ($1, $2, $3, 'terhubung', $4)
			 RETURNING id`,
			workerID, label, jidHash, speed,
		).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("CreateLoadAccounts gagal pada akun %d: %w", i+1, err)
		}
		accIDs[i] = id
	}
	return accIDs, nil
}

// SetupTestSettings memastikan pengaturan bisnis dasar tersimpan di tabel settings.
func SetupTestSettings(ctx context.Context, pool *pgxpool.Pool, wageIDR int64, refIDR int64) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES
			('rate_wage', $1::jsonb),
			('rate_referral', $2::jsonb),
			('lock_engine', 'false'::jsonb),
			('maintenance_mode', 'false'::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, fmt.Sprintf("%d", wageIDR), fmt.Sprintf("%d", refIDR))
	return err
}

// GetDurationEnv membaca durasi dari variabel lingkungan atau mengembalikan default.
func GetDurationEnv(key string, defaultVal time.Duration) time.Duration {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d > 0 {
			return d
		}
	}
	return defaultVal
}

// FindProjectRoot mencari direktori root yang memuat go.mod.
func FindProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("Tidak dapat menemukan project root (go.mod tidak ditemukan)")
	return ""
}

// ChangeToProjectRoot mengubah direktori kerja proses tes ke project root dan memulihkannya saat tes selesai.
func ChangeToProjectRoot(t *testing.T) {
	t.Helper()
	root := FindProjectRoot(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Gagal beralih ke project root: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
}

