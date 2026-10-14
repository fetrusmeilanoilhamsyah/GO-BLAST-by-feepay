// =================================================================================================
// TABEL TRANSISI STATUS CAMPAIGN_NUMBERS YANG SAH
// =================================================================================================
//
// Status Awal     Status Tujuan        Pelaku (Aktor)               Syarat & Mekanisme Pengaman
// -------------------------------------------------------------------------------------------------
// (upload)        -> 'tersedia'        Admin/Upload handler         Nomor normalisasi E.164 unik per campaign
// 'tersedia'      -> 'dikirim'         Worker goroutine             Atomik via ClaimNextNumber (FOR UPDATE SKIP LOCKED)
//                                                                   claimed_account_id & claimed_at dicatat, attempts + 1
// 'dikirim'       -> 'terkirim'        Worker goroutine pemilik     Kirim sukses diakui server (server_ack)
//                                                                   CTE atomik UPDATE ... WHERE id = $1 AND status = 'dikirim' AND claimed_account_id = $2
//                                                                   INSERT INTO ledger_entries upah (ON CONFLICT DO NOTHING)
// 'dikirim'       -> 'tersedia'        Worker goroutine pemilik     Hanya jika ClassPastiBelumTerkirim (sesi belum connect / putus sebelum kirim)
//                                                                   Attempts di-rollback: attempts = GREATEST(attempts - 1, 0)
//                                                                   claimed_account_id = NULL, claimed_at = NULL
// 'dikirim'       -> 'tidak_terdaftar' Worker goroutine pemilik     Nomor terbukti tidak terdaftar di WA (checker.IsOnWhatsApp == false)
//                                                                   sent_at = now(), tanpa upah
// 'dikirim'       -> 'gagal'           Worker goroutine pemilik     Gagal final: attempts >= maxAttempts, ClassNomorTidakValid,
//                                                                   ClassTidakPasti (Keputusan A1), atau panic (nomor beracun)
//                                                                   sent_at = now(), last_error dicatat
// 'dikirim'       -> 'gagal'           Boot/Cleaner (penjaga)       Nomor yatim saat boot (RecoverOnBoot) atau stale claims tanpa goroutine
//                                                                   Keputusan A1: last_error = 'hasil_tidak_pasti'
// 'gagal'         -> 'tersedia'        Admin (Aksi Khusus)          Hanya untuk nomor dengan last_error = 'hasil_tidak_pasti'
//                                                                   (Keputusan H1: Antrekan ulang yang tidak pasti), attempts reset ke 0
//
// CATATAN KERAS:
// - Status 'terkirim' dan 'tidak_terdaftar' bersifat FINAL dan IMMUTABLE.
// - Pihak luar (admin, manager WA, websocket callback) TIDAK BOLEH mengubah baris berstatus 'dikirim'.
//   Hanya goroutine yang mengklaim nomor tersebut yang berhak memfinalisasi statusnya.
// =================================================================================================

package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/safe"
	"goblast/internal/timeutil"
	"goblast/internal/wa"
)

// DeliveryLevel mendefinisikan kapan pesan dianggap terkirim.
// Nilai bawaan: "server_ack" (centang satu dari server WhatsApp).
const DeliveryLevel = "server_ack"

// SendTimeout batas waktu pengiriman pesan (60 detik).
const SendTimeout = 60 * time.Second

// Nilai bawaan parameter mesin internal (fallback aman jika tidak disetel di settings).
const (
	DefaultMaxAttempts            = 3
	DefaultMaxConsecutiveFailures = 5
	DefaultClaimTimeoutSeconds    = 300
)

// BuildRetryDelay jeda waktu saat buildCampaignMessage gagal (bawaan 5 detik).
var BuildRetryDelay = 5 * time.Second

// RecordRetryDelays jeda percobaan ulang untuk pencatatan buku besar (bawaan 200ms, 1s, 3s).
var RecordRetryDelays = []time.Duration{200 * time.Millisecond, 1 * time.Second, 3 * time.Second}

// SettingsReader interface untuk membaca konfigurasi dari DB.
type SettingsReader interface {
	GetInt(ctx context.Context, key string) (int, bool, error)
	GetString(ctx context.Context, key string) (string, bool, error)
	GetJSON(ctx context.Context, key string, target interface{}) (bool, error)
}

// WhatsAppChecker antarmuka untuk mengecek status pendaftaran WhatsApp suatu nomor.
type WhatsAppChecker interface {
	IsOnWhatsApp(ctx context.Context, accountID int64, phone string) (bool, error)
}

// ErrEngineLocked dikembalikan saat engine dikunci via lock_engine atau mode pemeliharaan.
var ErrEngineLocked = errors.New("sistem dalam perbaikan")

// GetJakartaStartOfDay mengembalikan awal hari (00:00:00) zona waktu Asia/Jakarta.
func GetJakartaStartOfDay() time.Time {
	return timeutil.GetJakartaStartOfDay()
}

// ClaimedNumber menyimpan data nomor yang berhasil diklaim secara atomik.
type ClaimedNumber struct {
	ID            int64
	CampaignID    int64
	PhoneE164     string
	Attempts      int
	RecipientName string
}

// Engine mengelola pengiriman pesan otomatis per akun WhatsApp pekerja.
type Engine struct {
	db       *db.DB
	sender   wa.Sender
	checker  WhatsAppChecker
	settings SettingsReader

	mu            sync.Mutex
	workers       map[int64]context.CancelFunc // accountID -> cancel goroutine
	activeWorkers map[int64]struct{}
	draining      atomic.Bool
	drainChan     chan struct{}
	drainOnce     sync.Once
	stopped       bool

	settingsCache SettingsCache
	campaignCache *CampaignCache
	accountCache  *AccountCache
	metrics       Metrics

	wakeChan chan struct{}
	wg       sync.WaitGroup
}

// New membuat instance Engine baru.
func New(database *db.DB, sender wa.Sender, checker WhatsAppChecker, settings SettingsReader) *Engine {
	e := &Engine{
		db:            database,
		sender:        sender,
		checker:       checker,
		settings:      settings,
		workers:       make(map[int64]context.CancelFunc),
		activeWorkers: make(map[int64]struct{}),
		campaignCache: newCampaignCache(),
		accountCache:  newAccountCache(),
		wakeChan:      make(chan struct{}, 1),
		drainChan:     make(chan struct{}),
	}
	// Inisialisasi awal snapshot settings
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e.refreshSettings(ctx)
	return e
}

// NotifyCampaignAvailable memberi sinyal ke seluruh worker yang sedang menunggu nomor
// agar segera bangun dan memeriksa antrean nomor yang baru tersedia.
func (e *Engine) NotifyCampaignAvailable() {
	select {
	case e.wakeChan <- struct{}{}:
	default:
	}
}

// StartSettingsRefresher menjalankan pembaruan berkala snapshot pengaturan tiap 3 detik.
func (e *Engine) StartSettingsRefresher(ctx context.Context) {
	safe.Loop(ctx, "engine.settingsRefresher", 3*time.Second, func(c context.Context) {
		e.refreshSettings(c)
	})
}

// InvalidateSettings membersihkan snapshot cache agar pembaruan admin langsung terbaca.
func (e *Engine) InvalidateSettings() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e.refreshSettings(ctx)
}

// InvalidateCampaign menghapus cache metadata kampanye tertentu saat diedit/dimulai/dijeda.
func (e *Engine) InvalidateCampaign(campaignID int64) {
	e.campaignCache.Invalidate(campaignID)
}

// InvalidateAllCampaigns membersihkan seluruh cache kampanye.
func (e *Engine) InvalidateAllCampaigns() {
	e.campaignCache.InvalidateAll()
}

// InvalidateAccount menghapus cache kecepatan dan status akun di memori.
func (e *Engine) InvalidateAccount(accountID int64) {
	e.accountCache.Invalidate(accountID)
}

func (e *Engine) refreshSettings(ctx context.Context) {
	var snap SettingsSnapshot
	snap.UpdatedAt = time.Now()

	// 1. Baca lock_engine & maintenance_mode
	if e.settings != nil {
		_, _ = e.settings.GetJSON(ctx, "lock_engine", &snap.LockEngine)
		_, _ = e.settings.GetJSON(ctx, "maintenance_mode", &snap.MaintenanceMode)
	} else if e.db != nil && e.db.Pool != nil {
		var raw string
		if err := e.db.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'lock_engine'").Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &snap.LockEngine)
		}
		if err := e.db.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'maintenance_mode'").Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &snap.MaintenanceMode)
		}
	}

	// 2. Baca rate_wage
	rate, ok, err := e.readRateWageFromDB(ctx)
	if err == nil && ok {
		snap.RateWage = rate
	}

	// 3. Baca rate_referral
	if e.settings != nil {
		if v, ok, err := e.settings.GetInt(ctx, "rate_referral"); err == nil && ok {
			snap.RateReferral = int64(v)
		}
	} else if e.db != nil && e.db.Pool != nil {
		var v int64
		if err := e.db.Pool.QueryRow(ctx, "SELECT (value->>'IDR')::bigint FROM settings WHERE key = 'rate_referral'").Scan(&v); err == nil {
			snap.RateReferral = v
		}
	}

	e.settingsCache.Set(snap)
}

func (e *Engine) readRateWageFromDB(ctx context.Context) (int64, bool, error) {
	if e.settings != nil {
		var val int64
		okJSON, err := e.settings.GetJSON(ctx, "rate_wage", &val)
		if err == nil && okJSON && val > 0 {
			return val, true, nil
		}

		vInt, okInt, errInt := e.settings.GetInt(ctx, "rate_wage")
		if errInt == nil && okInt && vInt > 0 {
			return int64(vInt), true, nil
		}

		var legacyMap map[string]int64
		okMap, errMap := e.settings.GetJSON(ctx, "rate_wage", &legacyMap)
		if errMap == nil && okMap && legacyMap != nil {
			if idr, exists := legacyMap["IDR"]; exists && idr > 0 {
				return idr, true, nil
			}
		}
	} else if e.db != nil && e.db.Pool != nil {
		var val int64
		err := e.db.Pool.QueryRow(ctx, "SELECT (value)::bigint FROM settings WHERE key = 'rate_wage'").Scan(&val)
		if err == nil && val > 0 {
			return val, true, nil
		}
		err = e.db.Pool.QueryRow(ctx, "SELECT (value->>'IDR')::bigint FROM settings WHERE key = 'rate_wage'").Scan(&val)
		if err == nil && val > 0 {
			return val, true, nil
		}
	}

	return 0, false, nil
}

// IsEngineLocked memeriksa apakah engine sedang dikunci via lock_engine atau mode pemeliharaan.
func (e *Engine) IsEngineLocked(ctx context.Context) bool {
	snap := e.settingsCache.Get()
	return snap.LockEngine || snap.MaintenanceMode
}

func (e *Engine) isEngineLocked() bool {
	snap := e.settingsCache.Get()
	return snap.LockEngine || snap.MaintenanceMode
}

// InvalidateLockCache kompatibilitas lama.
func (e *Engine) InvalidateLockCache() {
	e.InvalidateSettings()
}

// StartAccount memulai goroutine kirim untuk accountID.
func (e *Engine) StartAccount(accountID, workerID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.stopped || e.draining.Load() {
		return
	}
	if _, exists := e.workers[accountID]; exists {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.workers[accountID] = cancel
	e.activeWorkers[accountID] = struct{}{}
	e.wg.Add(1)

	safe.GoWithRecovery(fmt.Sprintf("engine.runWorker-%d", accountID), func() {
		defer e.wg.Done()
		e.runWorker(ctx, accountID, workerID)
	}, func(r any) {
		e.metrics.PanicCount.Add(1)
		slog.Error("engine: panic di goroutine worker", "account_id", accountID, "panic", r)
		timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer timeoutCancel()
		e.StopAccountForFailure(timeoutCtx, accountID, "galat internal")
	})
}

// IsRunning memeriksa apakah goroutine pengiriman untuk accountID sedang aktif.
func (e *Engine) IsRunning(accountID int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, exists := e.workers[accountID]
	return exists
}

// RunningCount mengembalikan jumlah akun yang sedang bekerja secara global.
func (e *Engine) RunningCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.workers)
}

// StopAccount menghentikan goroutine pengiriman untuk accountID.
// Tidak memblokir request web dan TIDAK melepas nomor massal dari luar:
// goroutine pengklaim sendiri yang menyelesaikan nomor yang sedang dipegang.
func (e *Engine) StopAccount(accountID int64) {
	e.mu.Lock()
	cancel, exists := e.workers[accountID]
	if exists {
		delete(e.workers, accountID)
		delete(e.activeWorkers, accountID)
		cancel()
	}
	e.mu.Unlock()
}

// StopAccountWait menghentikan goroutine pengiriman untuk accountID dan menunggu
// goroutine selesai sampai batas waktu context tercapai. Sisa nomor setelah batas diproses sesuai Keputusan A1.
func (e *Engine) StopAccountWait(ctx context.Context, accountID int64) error {
	e.mu.Lock()
	cancel, exists := e.workers[accountID]
	if !exists {
		e.mu.Unlock()
		return nil
	}
	delete(e.workers, accountID)
	cancel()
	e.mu.Unlock()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Batas waktu tercapai, proses sisa nomor yang dipegang akun ini sesuai Keputusan A1
			if e.db != nil && e.db.Pool != nil {
				failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer failCancel()
				_, _ = e.db.Pool.Exec(failCtx,
					`UPDATE campaign_numbers SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti'
					 WHERE status = 'dikirim' AND claimed_account_id = $1`,
					accountID,
				)
			}
			return ctx.Err()
		case <-ticker.C:
			e.mu.Lock()
			_, active := e.activeWorkers[accountID]
			e.mu.Unlock()
			if !active {
				return nil
			}
		}
	}
}

// StopAll menghentikan seluruh worker engine dengan batas waktu 20 detik (Drain).
func (e *Engine) StopAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = e.Drain(ctx)
}

// Drain menghentikan penerimaan nomor baru dan membiarkan kirim yang sedang berlangsung
// selesai secara bersih hingga batas waktu context (maksimal 20 detik).
// Sisa nomor yang tidak selesai setelah batas waktu diproses sesuai Keputusan A1 (gagal hasil_tidak_pasti).
func (e *Engine) Drain(ctx context.Context) error {
	e.draining.Store(true)
	e.drainOnce.Do(func() {
		close(e.drainChan)
	})

	// Kirim sinyal bangun agar worker yang idle langsung keluar
	e.NotifyCampaignAvailable()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("engine: semua worker selesai melalui drain")
		return nil
	case <-ctx.Done():
		slog.Warn("engine: timeout batas drain tercapai, membatalkan sisa worker dan menandai sisa nomor dikirim sebagai gagal hasil_tidak_pasti")
		e.mu.Lock()
		e.stopped = true
		for id, cancel := range e.workers {
			cancel()
			delete(e.workers, id)
		}
		e.mu.Unlock()

		// Pengecualian nomor yatim pasca batas drain: tandai gagal hasil_tidak_pasti (A1)
		if e.db != nil && e.db.Pool != nil {
			failCtx, failCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer failCancel()
			_, _ = e.db.Pool.Exec(failCtx,
				`UPDATE campaign_numbers SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti'
				 WHERE status = 'dikirim'`,
			)
		}
		return ctx.Err()
	}
}

// RecoverOnBoot dipanggil di main.go sebelum menerima request HTTP:
// semua baris dikirim yang tersisa saat boot (karena server restart atau crash)
// diproses sesuai Keputusan A1 (gagal dengan last_error hasil_tidak_pasti).
func (e *Engine) RecoverOnBoot(ctx context.Context) (int64, error) {
	if e.db == nil || e.db.Pool == nil {
		return 0, nil
	}

	res, err := e.db.Pool.Exec(ctx,
		`UPDATE campaign_numbers
		 SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti'
		 WHERE status = 'dikirim'`,
	)
	if err != nil {
		slog.Error("engine: RecoverOnBoot gagal", "err", err)
		return 0, err
	}
	affected := res.RowsAffected()
	if affected > 0 {
		slog.Info("engine: RecoverOnBoot memulihkan nomor menggantung", "jumlah", affected)
	}
	return affected, nil
}

// StartCleaner menjalankan pembersih nomor menggantung secara berkala setiap 60 detik.
func (e *Engine) StartCleaner(ctx context.Context) {
	safe.Loop(ctx, "engine.cleaner", 60*time.Second, func(c context.Context) {
		_, _ = e.CleanStaleNumbers(c)
	})
}

// CleanStaleNumbers membersihkan nomor berstatus dikirim yang claimed_at lebih tua
// dari batas (SendTimeout + toleransi pencatatan + margin = 90s) dan akunnya TIDAK
// memiliki goroutine hidup di engine (A1: gagal dengan last_error hasil_tidak_pasti).
func (e *Engine) CleanStaleNumbers(ctx context.Context) (int64, error) {
	if e.db == nil || e.db.Pool == nil {
		return 0, nil
	}

	e.mu.Lock()
	activeList := make([]int64, 0, len(e.activeWorkers))
	for accID := range e.activeWorkers {
		activeList = append(activeList, accID)
	}
	e.mu.Unlock()

	// Batas waktu = SendTimeout (60s) + toleransi pencatatan (10s) + margin (20s) = 90s
	timeoutSec := int(SendTimeout.Seconds()) + 30

	var res interface{ RowsAffected() int64 }
	var err error
	if len(activeList) == 0 {
		res, err = e.db.Pool.Exec(ctx,
			`UPDATE campaign_numbers
			 SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti'
			 WHERE status = 'dikirim'
			   AND claimed_at < now() - make_interval(secs => $1)`,
			timeoutSec,
		)
	} else {
		res, err = e.db.Pool.Exec(ctx,
			`UPDATE campaign_numbers
			 SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti'
			 WHERE status = 'dikirim'
			   AND claimed_at < now() - make_interval(secs => $1)
			   AND NOT (claimed_account_id = ANY($2))`,
			timeoutSec, activeList,
		)
	}
	if err != nil {
		slog.Error("engine: clean stale numbers error", "err", err)
		return 0, err
	}
	return res.RowsAffected(), nil
}

// StartSupervisor menjalankan goroutine pengawas penyelesaian campaign tiap 5 detik
// StartSupervisor menjalankan goroutine pengawas penyelesaian campaign tiap 5 detik
// di luar jalur pekerja sehingga tidak ada FOR UPDATE lock contention pada baris campaign.
func (e *Engine) StartSupervisor(ctx context.Context) {
	safe.Loop(ctx, "engine.supervisor", 5*time.Second, func(c context.Context) {
		_, _ = e.SuperviseCampaigns(c)
	})
}

// SuperviseCampaigns memeriksa dan menyelesaikan campaign berjalan yang tidak memiliki nomor tersedia atau dikirim.
func (e *Engine) SuperviseCampaigns(ctx context.Context) (int64, error) {
	if e.db == nil || e.db.Pool == nil {
		return 0, nil
	}

	// Satu pernyataan CTE untuk menyelesaikan campaign berjalan tanpa nomor tersedia/dikirim
	query := `
		WITH running_campaigns AS (
			SELECT id FROM campaigns WHERE status = 'berjalan'
		),
		to_complete AS (
			SELECT rc.id
			FROM running_campaigns rc
			WHERE NOT EXISTS (
				SELECT 1 FROM campaign_numbers cn
				WHERE cn.campaign_id = rc.id AND cn.status IN ('tersedia', 'dikirim')
			)
		),
		updated_campaigns AS (
			UPDATE campaigns c
			SET status = 'selesai', finished_at = now()
			FROM to_complete tc
			WHERE c.id = tc.id
			RETURNING c.id
		)
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta)
		SELECT NULL, 'campaign_completed', 'campaign', uc.id::text, '{"reason": "tidak ada nomor tersedia maupun dikirim"}'::jsonb
		FROM updated_campaigns uc
		RETURNING entity_id;
	`
	rows, err := e.db.Pool.Query(ctx, query)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var count int64
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err == nil {
			count++
			slog.Info("engine: campaign otomatis diselesaikan oleh supervisor", "campaign_id", cid)
		}
	}
	return count, rows.Err()
}

// ClaimNextNumber mengklaim tepat satu nomor secara atomik dengan FOR UPDATE SKIP LOCKED.
func (e *Engine) ClaimNextNumber(ctx context.Context, accountID int64) (*ClaimedNumber, error) {
	if e.draining.Load() || e.isEngineLocked() {
		return nil, ErrEngineLocked
	}

	start := time.Now()
	query := fmt.Sprintf(`
		UPDATE campaign_numbers
		SET status = 'dikirim', claimed_account_id = $1, claimed_at = now(), attempts = attempts + 1
		WHERE id = (
			SELECT cn.id
			FROM campaign_numbers cn
			JOIN campaigns c ON c.id = cn.campaign_id
			WHERE %s
			ORDER BY cn.id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, campaign_id, phone_e164, attempts, COALESCE(recipient_name, '')
	`, campaign.ClaimableWhere)

	var cn ClaimedNumber
	err := e.db.Pool.QueryRow(ctx, query, accountID).Scan(
		&cn.ID, &cn.CampaignID, &cn.PhoneE164, &cn.Attempts, &cn.RecipientName,
	)
	e.metrics.ClaimLatency.Record(time.Since(start))
	if err != nil {
		return nil, err
	}

	e.metrics.ClaimedCount.Add(1)
	return &cn, nil
}

// RecordSuccessAtomic mencatat keberhasilan pengiriman (terkirim) dan entri upah buku besar
// dalam satu round trip database (CTE) atomik dan idempoten.
// Memverifikasi kepemilikan: WHERE id = $1 AND status = 'dikirim' AND claimed_account_id = $2.
func (e *Engine) RecordSuccessAtomic(ctx context.Context, numberID, accountID, workerID, rate int64) error {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	start := time.Now()
	query := `
		WITH updated AS (
			UPDATE campaign_numbers
			SET status = 'terkirim', sent_at = now()
			WHERE id = $1 AND status = 'dikirim' AND claimed_account_id = $2
			RETURNING id, campaign_id
		)
		INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, campaign_number_id, note)
		SELECT $3, 'upah', 'IDR', $4, $5, updated.id, 'upah pesan terkirim'
		FROM updated
		ON CONFLICT (campaign_number_id) WHERE kind = 'upah' DO NOTHING
		RETURNING id;
	`
	var ledgerID int64
	err := e.db.Pool.QueryRow(recordCtx, query, numberID, accountID, workerID, rate, rate).Scan(&ledgerID)
	e.metrics.RecordLatency.Record(time.Since(start))

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Periksa apakah baris nomor gagal diperbarui karena klaim hilang
			var currentAccount sql.NullInt64
			var currentStatus string
			_ = e.db.Pool.QueryRow(recordCtx, `SELECT claimed_account_id, status FROM campaign_numbers WHERE id = $1`, numberID).Scan(&currentAccount, &currentStatus)

			slog.Warn("klaim hilang", "number_id", numberID, "expected_account", accountID, "current_account", currentAccount.Int64, "status", currentStatus)
			e.metrics.LostClaimsCount.Add(1)
			return fmt.Errorf("klaim hilang atau nomor bukan milik akun ini")
		}
		// Jika err bukan ErrNoRows, mungkin ON CONFLICT DO NOTHING mengembalikan 0 row tapi updated berhasil
		// Mari verifikasi apakah nomor statusnya terkirim
		var currentStatus string
		if qErr := e.db.Pool.QueryRow(recordCtx, `SELECT status FROM campaign_numbers WHERE id = $1`, numberID).Scan(&currentStatus); qErr == nil && currentStatus == "terkirim" {
			e.metrics.SentCount.Add(1)
			return nil
		}
		return err
	}

	e.metrics.SentCount.Add(1)
	return nil
}

// RecordWithRetry mencatat keberhasilan kirim dan buku besar menggunakan context.WithoutCancel(ctx)
// dengan timeout 10 detik, coba ulang 3 kali.
func (e *Engine) RecordWithRetry(ctx context.Context, numberID, workerID int64) error {
	snap := e.settingsCache.Get()
	rate := snap.RateWage
	if rate <= 0 {
		rate, _, _ = e.readRateWageFromDB(ctx)
	}

	// Cari claimed_account_id dari DB
	var accountID int64
	_ = e.db.Pool.QueryRow(ctx, `SELECT COALESCE(claimed_account_id, 0) FROM campaign_numbers WHERE id = $1`, numberID).Scan(&accountID)

	delays := RecordRetryDelays
	var lastErr error

	for attempt := 0; attempt <= len(delays); attempt++ {
		if rate <= 0 {
			lastErr = fmt.Errorf("rate upah belum diisi admin")
		} else {
			err := e.RecordSuccessAtomic(ctx, numberID, accountID, workerID, rate)
			if err == nil {
				return nil
			}
			lastErr = err
		}

		if attempt < len(delays) {
			time.Sleep(delays[attempt])
		}
	}

	// Jika tetap gagal setelah 3 kali coba ulang, tandai gagal terkirim_tidak_tercatat
	failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer failCancel()
	_, _ = e.db.Pool.Exec(failCtx,
		`UPDATE campaign_numbers SET status = 'gagal', sent_at = now(), last_error = 'terkirim_tidak_tercatat'
		 WHERE id = $1`,
		numberID,
	)
	slog.Error("engine: gagal menulis buku besar setelah coba ulang, nomor ditandai gagal", "number_id", numberID, "err", lastErr)
	return lastErr
}

// RecordSuccessAndLedger metode kompatibilitas untuk tes lama.
func (e *Engine) RecordSuccessAndLedger(ctx context.Context, numberID, workerID int64) error {
	var accountID int64
	_ = e.db.Pool.QueryRow(ctx, `SELECT COALESCE(claimed_account_id, 0) FROM campaign_numbers WHERE id = $1`, numberID).Scan(&accountID)

	snap := e.settingsCache.Get()
	rate := snap.RateWage
	if rate <= 0 {
		var err error
		rate, _, err = e.readRateWageFromDB(ctx)
		if err != nil || rate <= 0 {
			return fmt.Errorf("rate upah belum diisi admin")
		}
	}

	return e.RecordSuccessAtomic(ctx, numberID, accountID, workerID, rate)
}

// HandleSendResult menangani hasil kirim sesuai klasifikasi Classify.
// Menegakkan aturan kepemilikan nomor dan penulisan status akhir via context.WithoutCancel.
func (e *Engine) HandleSendResult(ctx context.Context, accountID, numberID, workerID, campaignID int64, attempts int, sendErr error) ResultClass {
	class := Classify(sendErr)
	e.metrics.IncClass(class)
	if class != ClassTerkirim {
		e.metrics.GagalCount.Add(1)
	}

	statusCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	switch class {
	case ClassTerkirim:
		snap := e.settingsCache.Get()
		_ = e.RecordSuccessAtomic(statusCtx, numberID, accountID, workerID, snap.RateWage)

	case ClassPastiBelumTerkirim:
		// Nomor kembali tersedia, dan attempts yang tadi ditambah klaim dikurangi lagi
		res, err := e.db.Pool.Exec(statusCtx,
			`UPDATE campaign_numbers
			 SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL,
			     attempts = GREATEST(attempts - 1, 0), last_error = $1
			 WHERE id = $2 AND status = 'dikirim' AND claimed_account_id = $3`,
			sendErr.Error(), numberID, accountID,
		)
		if err == nil && res.RowsAffected() == 0 {
			slog.Warn("klaim hilang saat menangani pasti belum terkirim", "number_id", numberID, "account_id", accountID)
			e.metrics.LostClaimsCount.Add(1)
		}

	case ClassTidakPasti:
		// Keputusan A1: gagal dengan last_error hasil_tidak_pasti
		res, err := e.db.Pool.Exec(statusCtx,
			`UPDATE campaign_numbers
			 SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti'
			 WHERE id = $1 AND status = 'dikirim' AND claimed_account_id = $2`,
			numberID, accountID,
		)
		if err == nil && res.RowsAffected() == 0 {
			slog.Warn("klaim hilang saat menangani tidak pasti", "number_id", numberID, "account_id", accountID)
			e.metrics.LostClaimsCount.Add(1)
		}

	case ClassNomorTidakValid:
		// Gagal final
		res, err := e.db.Pool.Exec(statusCtx,
			`UPDATE campaign_numbers
			 SET status = 'gagal', sent_at = now(), last_error = $1
			 WHERE id = $2 AND status = 'dikirim' AND claimed_account_id = $3`,
			sendErr.Error(), numberID, accountID,
		)
		if err == nil && res.RowsAffected() == 0 {
			slog.Warn("klaim hilang saat menangani nomor tidak valid", "number_id", numberID, "account_id", accountID)
			e.metrics.LostClaimsCount.Add(1)
		}

	case ClassMasalahAkun:
		// Akun berhenti, nomor diproses sebagai PastiBelumTerkirim atau TidakPasti sesuai fakta
		e.setAccountStopReason(statusCtx, accountID, sendErr.Error())
		res, err := e.db.Pool.Exec(statusCtx,
			`UPDATE campaign_numbers
			 SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL,
			     attempts = GREATEST(attempts - 1, 0), last_error = $1
			 WHERE id = $2 AND status = 'dikirim' AND claimed_account_id = $3`,
			sendErr.Error(), numberID, accountID,
		)
		if err == nil && res.RowsAffected() == 0 {
			slog.Warn("klaim hilang saat menangani masalah akun", "number_id", numberID, "account_id", accountID)
			e.metrics.LostClaimsCount.Add(1)
		}
	}

	return class
}

// FinalizePoisonPill memfinalisasi nomor beracun yang memicu panic sebagai 'gagal'
// dengan last_error = 'galat_internal' agar tidak terjadi putaran crash berulang.
func (e *Engine) FinalizePoisonPill(ctx context.Context, accountID, numberID int64) {
	statusCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	_, _ = e.db.Pool.Exec(statusCtx,
		`UPDATE campaign_numbers
		 SET status = 'gagal', sent_at = now(), last_error = 'galat_internal'
		 WHERE id = $1 AND status = 'dikirim' AND claimed_account_id = $2`,
		numberID, accountID,
	)
	slog.Error("engine: nomor beracun difinalisasi gagal", "number_id", numberID, "account_id", accountID)
}

// runWorker adalah siklus kerja utama satu akun WhatsApp.
func (e *Engine) runWorker(ctx context.Context, accountID, workerID int64) {
	defer func() {
		e.mu.Lock()
		delete(e.workers, accountID)
		delete(e.activeWorkers, accountID)
		e.mu.Unlock()
	}()

	var currentNumberID int64
	defer func() {
		if r := recover(); r != nil {
			safe.IncPanicCount()
			e.metrics.PanicCount.Add(1)
			slog.Error("engine: panic di goroutine worker", "account_id", accountID, "panic", r)
			if currentNumberID > 0 {
				e.FinalizePoisonPill(context.Background(), accountID, currentNumberID)
			}
			e.StopAccountForFailure(context.Background(), accountID, "galat internal")
		}
	}()

	// 0. Cek apakah engine dikunci dari snapshot memori
	if e.isEngineLocked() {
		e.setAccountStopReason(ctx, accountID, "sistem dalam perbaikan")
		return
	}

	// 1. Ambil pengaturan dari snapshot (sebelum stagger sleep)
	snap := e.settingsCache.Get()
	if snap.RateWage <= 0 {
		rate, ok, _ := e.readRateWageFromDB(ctx)
		if !ok || rate <= 0 {
			e.setAccountStopReason(ctx, accountID, "rate upah belum diisi admin")
			return
		}
	}

	// 5. Awal akun: tunda awal secara acak antara 0 dan satu jeda kecepatannya (mencegah gelombang serentak)
	baseDelay := 1 * time.Second
	stagger := time.Duration(rand.Float64() * float64(baseDelay))
	select {
	case <-ctx.Done():
		return
	case <-time.After(stagger):
	}

	consecutiveFailures := 0
	consecutiveBuildFailures := 0
	backoffDuration := 3 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if e.draining.Load() {
			return
		}

		// 0. Cek apakah engine dikunci dari snapshot memori
		if e.isEngineLocked() {
			e.setAccountStopReason(ctx, accountID, "sistem dalam perbaikan")
			return
		}

		// 1. Ambil pengaturan dari snapshot
		snap := e.settingsCache.Get()
		if snap.RateWage <= 0 {
			e.setAccountStopReason(ctx, accountID, "rate upah belum diisi admin")
			return
		}

		// Baca status & kecepatan akun dari cache memori
		acc, ok := e.accountCache.Get(accountID)
		if !ok {
			var speed, status string
			err := e.db.Pool.QueryRow(ctx, `SELECT speed, status FROM wa_accounts WHERE id = $1`, accountID).Scan(&speed, &status)
			if err != nil || (status != "terhubung" && status != "terputus") {
				return
			}
			e.accountCache.Set(accountID, speed, status)
			acc.Speed = speed
			acc.Status = status
		}
		if acc.Status == "terputus" {
			// Sesi putus sementara: berhenti mengklaim sambil menunggu Connected (whatsmeow auto-reconnect)
			select {
			case <-ctx.Done():
				return
			case <-e.drainChan:
				return
			case <-time.After(1 * time.Second):
				e.accountCache.Invalidate(accountID)
				continue
			}
		} else if acc.Status != "terhubung" {
			return
		}

		// 2. Cek batas harian akun di memori jika disetel (>0)
		if config.DailyLimitPerAccount > 0 {
			var sentToday int
			startOfDayJakarta := timeutil.GetJakartaStartOfDay()
			err := e.db.Pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM campaign_numbers 
				 WHERE claimed_account_id = $1 AND status = 'terkirim' AND sent_at >= $2`,
				accountID, startOfDayJakarta,
			).Scan(&sentToday)
			if err == nil && sentToday >= config.DailyLimitPerAccount {
				e.setAccountStopReason(ctx, accountID, fmt.Sprintf("batas harian tercapai (%d/%d), menunggu hari berikutnya", sentToday, config.DailyLimitPerAccount))
				waitDur := time.Until(timeutil.GetNextJakartaMidnight())
				select {
				case <-ctx.Done():
					return
				case <-e.drainChan:
					return
				case <-time.After(waitDur):
					continue
				}
			}
		}

		// 3. Klaim nomor secara atomik (Round Trip 1)
		claimed, err := e.ClaimNextNumber(ctx, accountID)
		if err != nil {
			if errors.Is(err, ErrEngineLocked) {
				if e.draining.Load() {
					return
				}
				e.setAccountStopReason(ctx, accountID, "sistem dalam perbaikan")
				return
			}

			// Tidak ada nomor yang tersedia: backoff 3s sampai 30s plus jitter, bangun lebih cepat lewat wakeChan
			e.metrics.BackoffCount.Add(1)
			jitter := float64(backoffDuration) * (0.8 + 0.4*rand.Float64())
			wait := time.Duration(jitter)

			select {
			case <-ctx.Done():
				return
			case <-e.drainChan:
				return
			case <-e.wakeChan:
				backoffDuration = 3 * time.Second
				continue
			case <-time.After(wait):
				backoffDuration *= 2
				if backoffDuration > 30*time.Second {
					backoffDuration = 30 * time.Second
				}
				continue
			}
		}

		backoffDuration = 3 * time.Second
		currentNumberID = claimed.ID

		// 4. Siapkan pesan campaign (Cache memori dengan TTL 15s)
		msg, err := e.buildCampaignMessage(ctx, claimed.CampaignID, claimed.RecipientName)
		if err != nil {
			consecutiveBuildFailures++
			slog.Error("engine: gagal menyusun pesan campaign", "campaign_id", claimed.CampaignID, "err", err)

			maxAttempts := config.MaxAttempts
			if maxAttempts <= 0 {
				maxAttempts = DefaultMaxAttempts
			}
			e.HandleSendFailure(ctx, accountID, claimed.ID, claimed.Attempts, maxAttempts, "gagal menyusun pesan campaign")
			currentNumberID = 0

			if consecutiveBuildFailures >= 3 {
				e.StopAccountForFailure(ctx, accountID, "gagal menyusun pesan campaign")
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(BuildRetryDelay):
				continue
			}
		}
		consecutiveBuildFailures = 0

		// 5. Cek terdaftar di WhatsApp sebelum kirim
		if e.checker != nil {
			isOnWA, checkErr := e.checker.IsOnWhatsApp(ctx, accountID, claimed.PhoneE164)
			if checkErr == nil && !isOnWA {
				// Tidak terdaftar di WhatsApp: status final tidak_terdaftar, tanpa upah
				statusCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				_, _ = e.db.Pool.Exec(statusCtx,
					`UPDATE campaign_numbers SET status = 'tidak_terdaftar', sent_at = now()
					 WHERE id = $1 AND status = 'dikirim' AND claimed_account_id = $2`,
					claimed.ID, accountID,
				)
				cancel()
				currentNumberID = 0

				// Setelah hasil tidak_terdaftar, tidak menambah penghitung kegagalan beruntun
				delay := e.calculateSpeedDelay(ctx, acc.Speed)
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
					continue
				}
			}
		}

		// 6. Kirim pesan lewat Sender dengan context timeout 60 detik
		sendStart := time.Now()
		sendCtx, sendCancel := context.WithTimeout(ctx, SendTimeout)
		_, sendErr := e.sender.Send(sendCtx, accountID, claimed.PhoneE164, msg)
		sendCancel()
		e.metrics.SendLatency.Record(time.Since(sendStart))

		// Tangani hasil kirim secara terstruktur
		class := e.HandleSendResult(ctx, accountID, claimed.ID, workerID, claimed.CampaignID, claimed.Attempts, sendErr)
		currentNumberID = 0

		// 3. Pemutus sirkuit per akun: 5 hasil buruk beruntun selain NomorTidakValid & tidak_terdaftar
		if class == ClassTerkirim {
			consecutiveFailures = 0
		} else if class != ClassNomorTidakValid {
			consecutiveFailures++
			maxFail := config.MaxConsecutiveFailures
			if maxFail <= 0 {
				maxFail = DefaultMaxConsecutiveFailures
			}
			if consecutiveFailures >= maxFail {
				e.StopAccountForFailure(ctx, accountID, fmt.Sprintf("berhenti otomatis: %d kegagalan beruntun", consecutiveFailures))
				return
			}
		}

		// 7. Tunggu jeda kecepatan (dengan variasi acak ±20%)
		delay := e.calculateSpeedDelay(ctx, acc.Speed)
		select {
		case <-ctx.Done():
			return
		case <-e.drainChan:
			return
		case <-time.After(delay):
		}
	}
}

// buildCampaignMessage mengambil metadata campaign dengan cache memori TTL 15s.
func (e *Engine) buildCampaignMessage(ctx context.Context, campaignID int64, recipientName string) (wa.OutMessage, error) {
	cItem, ok := e.campaignCache.Get(campaignID)
	if !ok {
		var err error
		cItem, err = fetchCampaignFromDB(ctx, e.db, campaignID)
		if err != nil {
			return wa.OutMessage{}, err
		}
		e.campaignCache.Set(campaignID, cItem)
	}

	if err := campaign.ValidateTemplate(cItem.Body, cItem.ButtonMode, cItem.Elements); err != nil {
		return wa.OutMessage{}, fmt.Errorf("validasi template: %w", err)
	}

	var recVars map[string]string
	if recipientName != "" {
		recVars = map[string]string{"nama": recipientName, "name": recipientName}
	}

	cMsg := campaign.Render(cItem.Body, cItem.ImagePath, cItem.Elements, cItem.ButtonMode, recVars)
	out := wa.OutMessage{Text: cMsg.Text}
	if cMsg.Preview != nil {
		out.Preview = &wa.Preview{
			Title:       cMsg.Preview.Title,
			Description: cMsg.Preview.Description,
			URL:         cMsg.Preview.URL,
			Thumbnail:   cMsg.Preview.Thumbnail,
		}
	}
	return out, nil
}

// HandleSendFailure kompatibilitas lama untuk kegagalan di luar send WhatsApp (misal build message).
func (e *Engine) HandleSendFailure(ctx context.Context, accountID, numberID int64, attempts, maxAttempts int, errMsg string) {
	statusCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if attempts >= maxAttempts {
		_, _ = e.db.Pool.Exec(statusCtx,
			`UPDATE campaign_numbers SET status = 'gagal', sent_at = now(), last_error = $1
			 WHERE id = $2 AND status = 'dikirim' AND claimed_account_id = $3`,
			errMsg, numberID, accountID,
		)
	} else {
		_, _ = e.db.Pool.Exec(statusCtx,
			`UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL, last_error = $1
			 WHERE id = $2 AND status = 'dikirim' AND claimed_account_id = $3`,
			errMsg, numberID, accountID,
		)
	}
}

// CheckAndCompleteCampaign memeriksa apakah suatu campaign sudah selesai (kompatibilitas lama).
func (e *Engine) CheckAndCompleteCampaign(ctx context.Context, campaignID int64) (bool, error) {
	if e.db == nil || e.db.Pool == nil {
		return false, nil
	}

	query := `
		WITH updated AS (
			UPDATE campaigns
			SET status = 'selesai', finished_at = now()
			WHERE id = $1 AND status = 'berjalan'
			  AND NOT EXISTS (
				  SELECT 1 FROM campaign_numbers WHERE campaign_id = $1 AND status IN ('tersedia', 'dikirim')
			  )
			RETURNING id
		)
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta)
		SELECT NULL, 'campaign_completed', 'campaign', updated.id::text, '{"reason": "tidak ada nomor tersedia maupun dikirim"}'::jsonb
		FROM updated
		RETURNING entity_id;
	`
	var eid string
	err := e.db.Pool.QueryRow(ctx, query, campaignID).Scan(&eid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, nil
	}
	return true, nil
}

// CalculateSpeedDelay menghitung jeda kecepatan dari konstanta config.SpeedDelays ditambah variasi acak ±config.JitterPercent%.
func CalculateSpeedDelay(speed string) time.Duration {
	baseDur, ok := config.SpeedDelays[speed]
	if !ok || baseDur <= 0 {
		switch speed {
		case "fast":
			baseDur = 1 * time.Second
		case "medium":
			baseDur = 5 * time.Second
		case "slow":
			baseDur = 15 * time.Second
		case "over_slow":
			baseDur = 30 * time.Second
		default:
			baseDur = 5 * time.Second
		}
	}

	jitterPct := float64(config.JitterPercent) / 100.0
	minMult := 1.0 - jitterPct
	maxMult := 1.0 + jitterPct
	mult := minMult + rand.Float64()*(maxMult-minMult)

	delaySec := baseDur.Seconds() * mult
	if delaySec < 0.2 {
		delaySec = 0.2
	}
	return time.Duration(delaySec * float64(time.Second))
}

func (e *Engine) calculateSpeedDelay(_ context.Context, speed string) time.Duration {
	return CalculateSpeedDelay(speed)
}

func (e *Engine) setAccountStopReason(ctx context.Context, accountID int64, reason string) {
	slog.Warn("engine: akun berhenti", "account_id", accountID, "alasan", reason)
	if e.db != nil && e.db.Pool != nil {
		_, _ = e.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET stop_reason = $1 WHERE id = $2`,
			reason, accountID,
		)
	}
}

// StopAccountForFailure menghentikan akun karena kegagalan beruntun.
// Tidak melepas nomor massal dari luar: goroutine pemilik sendiri yang memproses nomor in-flight.
func (e *Engine) StopAccountForFailure(ctx context.Context, accountID int64, reason string) {
	slog.Warn("engine: akun dihentikan karena kegagalan", "account_id", accountID, "alasan", reason)
	if e.db != nil && e.db.Pool != nil {
		_, _ = e.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = $1 WHERE id = $2`,
			reason, accountID,
		)
	}
}
