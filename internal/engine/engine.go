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
	"time"

	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/safe"
	"goblast/internal/wa"
)

// DeliveryLevel mendefinisikan kapan pesan dianggap terkirim.
// Nilai bawaan: "server_ack" (centang satu dari server WhatsApp).
// TODO: keputusan terbuka nomor 1 di DOC_KONSEP bagian 15 —
// apakah cukup server_ack (centang satu) atau butuh delivered (centang dua di perangkat).
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

type engineLockCache struct {
	mu        sync.RWMutex
	locked    bool
	checkedAt time.Time
}

// Engine mengelola pengiriman pesan otomatis per akun WhatsApp pekerja.
type Engine struct {
	db       *db.DB
	sender   wa.Sender
	checker  WhatsAppChecker
	settings SettingsReader

	mu        sync.Mutex
	workers   map[int64]context.CancelFunc // accountID -> cancel goroutine
	stopped   bool
	lockCache engineLockCache
	wg        sync.WaitGroup
}

// New membuat instance Engine baru.
func New(database *db.DB, sender wa.Sender, checker WhatsAppChecker, settings SettingsReader) *Engine {
	return &Engine{
		db:       database,
		sender:   sender,
		checker:  checker,
		settings: settings,
		workers:  make(map[int64]context.CancelFunc),
	}
}

// StartAccount memulai goroutine kirim untuk accountID.
// Jika sudah berjalan, tidak ada aksi ganda.
func (e *Engine) StartAccount(accountID, workerID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.stopped {
		return
	}
	if _, exists := e.workers[accountID]; exists {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.workers[accountID] = cancel
	e.wg.Add(1)

	safe.GoWithRecovery(fmt.Sprintf("engine.runWorker-%d", accountID), func() {
		defer e.wg.Done()
		e.runWorker(ctx, accountID, workerID)
	}, func(r any) {
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

// StopAccount menghentikan goroutine pengiriman untuk accountID dan melepaskan nomor yang dipegang.
func (e *Engine) StopAccount(accountID int64) {
	e.mu.Lock()
	cancel, exists := e.workers[accountID]
	if exists {
		delete(e.workers, accountID)
		cancel()
	}
	e.mu.Unlock()

	// Kembalikan nomor berstatus dikirim milik akun ini ke tersedia
	if e.db != nil && e.db.Pool != nil {
		ctx, cancelCtx := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCtx()
		_, _ = e.db.Pool.Exec(ctx,
			`UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL
			 WHERE status = 'dikirim' AND claimed_account_id = $1`, accountID)
	}
}

// StopAll menghentikan seluruh worker engine dan menunggu seluruh goroutine selesai (maksimal 20 detik).
func (e *Engine) StopAll() {
	e.mu.Lock()
	e.stopped = true
	for id, cancel := range e.workers {
		cancel()
		delete(e.workers, id)
	}
	e.mu.Unlock()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("engine: semua worker selesai")
	case <-time.After(20 * time.Second):
		slog.Warn("engine: timeout 20s menunggu worker selesai saat StopAll")
	}
}

// StartCleaner menjalankan pembersih nomor menggantung secara berkala setiap 60 detik.
func (e *Engine) StartCleaner(ctx context.Context) {
	safe.Loop(ctx, "engine.cleaner", 60*time.Second, func(c context.Context) {
		_, _ = e.CleanStaleNumbers(c)
	})
}

// CleanStaleNumbers mengembalikan nomor dikirim yang claimed_at lebih tua dari claim_timeout_seconds ke tersedia.
// claim_timeout_seconds dipastikan selalu lebih besar dari batas waktu kirim (SendTimeout: 60 detik).
func (e *Engine) CleanStaleNumbers(ctx context.Context) (int64, error) {
	if e.db == nil || e.db.Pool == nil {
		return 0, nil
	}

	timeoutSec := config.ClaimTimeoutSeconds
	if timeoutSec <= int(SendTimeout.Seconds()) {
		timeoutSec = DefaultClaimTimeoutSeconds
	}

	res, err := e.db.Pool.Exec(ctx,
		`UPDATE campaign_numbers
		 SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL
		 WHERE status = 'dikirim'
		   AND claimed_at < now() - make_interval(secs => $1)`,
		timeoutSec,
	)
	if err != nil {
		slog.Error("engine: clean stale numbers error", "err", err)
		return 0, err
	}
	return res.RowsAffected(), nil
}

// ClaimedNumber menyimpan data nomor yang berhasil diklaim secara atomik.
type ClaimedNumber struct {
	ID            int64
	CampaignID    int64
	PhoneE164     string
	Attempts      int
	RecipientName string
}

// IsEngineLocked memeriksa apakah engine sedang dikunci via lock_engine atau mode pemeliharaan (cache 2 detik).
func (e *Engine) IsEngineLocked(ctx context.Context) bool {
	return e.isEngineLocked(ctx)
}

func (e *Engine) isEngineLocked(ctx context.Context) bool {
	e.lockCache.mu.RLock()
	if time.Since(e.lockCache.checkedAt) < 2*time.Second {
		locked := e.lockCache.locked
		e.lockCache.mu.RUnlock()
		return locked
	}
	e.lockCache.mu.RUnlock()

	e.lockCache.mu.Lock()
	defer e.lockCache.mu.Unlock()

	// Double-check setelah exclusive lock didapat
	if time.Since(e.lockCache.checkedAt) < 2*time.Second {
		return e.lockCache.locked
	}

	var lockEngine bool
	if e.settings != nil {
		_, _ = e.settings.GetJSON(ctx, "lock_engine", &lockEngine)
	} else if e.db != nil && e.db.Pool != nil {
		var raw string
		if err := e.db.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'lock_engine'").Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &lockEngine)
		}
	}

	var maintMode bool
	if e.settings != nil {
		_, _ = e.settings.GetJSON(ctx, "maintenance_mode", &maintMode)
	} else if e.db != nil && e.db.Pool != nil {
		var raw string
		if err := e.db.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'maintenance_mode'").Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &maintMode)
		}
	}

	e.lockCache.locked = lockEngine || maintMode
	e.lockCache.checkedAt = time.Now()
	return e.lockCache.locked
}

// InvalidateLockCache mengosongkan cache kunci agar pembacaan berikutnya langsung mengecek pengaturan.
func (e *Engine) InvalidateLockCache() {
	e.lockCache.mu.Lock()
	e.lockCache.checkedAt = time.Time{}
	e.lockCache.mu.Unlock()
}

// ClaimNextNumber mengklaim tepat satu nomor secara atomik dengan FOR UPDATE SKIP LOCKED.
func (e *Engine) ClaimNextNumber(ctx context.Context, accountID int64) (*ClaimedNumber, error) {
	if e.isEngineLocked(ctx) {
		return nil, ErrEngineLocked
	}
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
	if err != nil {
		return nil, err
	}
	return &cn, nil
}

// GetJakartaStartOfDay mengembalikan awal hari (00:00:00) zona waktu Asia/Jakarta.
func GetJakartaStartOfDay() time.Time {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("Asia/Jakarta", 7*3600)
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

func getJakartaNextDayWaitDuration() time.Duration {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("Asia/Jakarta", 7*3600)
	}
	now := time.Now().In(loc)
	nextDay := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 1, 0, loc)
	return nextDay.Sub(now)
}

// getRateWage membaca rate_wage sebagai bilangan bulat rupiah.
// Mendukung integer JSON langsung, GetInt, dan fallback legacy map {"IDR": ...}.
func (e *Engine) getRateWage(ctx context.Context) (int64, bool, error) {
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

	return 0, false, nil
}

// runWorker adalah siklus kerja utama satu akun WhatsApp.
func (e *Engine) runWorker(ctx context.Context, accountID, workerID int64) {
	defer func() {
		e.mu.Lock()
		delete(e.workers, accountID)
		e.mu.Unlock()
	}()

	consecutiveFailures := 0
	consecutiveBuildFailures := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// 0. Cek apakah engine dikunci (lock_engine atau mode pemeliharaan)
		if e.isEngineLocked(ctx) {
			e.setAccountStopReason(ctx, accountID, "sistem dalam perbaikan")
			return
		}

		// 1. Ambil pengaturan penting (max_attempts, rate_wage)
		maxAttempts := config.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = DefaultMaxAttempts
		}

		// Validasi rate_wage: engine membaca rate_wage sebagai bilangan bulat rupiah.
		// Jika rate_wage kosong atau tidak lebih dari 0, engine tidak mengirim apa pun
		// dan menulis stop_reason "rate upah belum diisi admin".
		rateWage, okWage, err := e.getRateWage(ctx)
		if err != nil || !okWage || rateWage <= 0 {
			e.setAccountStopReason(ctx, accountID, "rate upah belum diisi admin")
			return
		}

		// Baca data akun (speed, status)
		var speed, status string
		err = e.db.Pool.QueryRow(ctx, `SELECT speed, status FROM wa_accounts WHERE id = $1`, accountID).Scan(&speed, &status)
		if err != nil || status != "terhubung" {
			// Sesi tidak lagi berstatus terhubung
			return
		}

		// 2. Cek batas harian akun (jika DailyLimitPerAccount > 0; 0 berarti tanpa batas)
		if config.DailyLimitPerAccount > 0 {
			var sentToday int
			startOfDayJakarta := GetJakartaStartOfDay()
			err = e.db.Pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM campaign_numbers 
				 WHERE claimed_account_id = $1 AND status = 'terkirim' AND sent_at >= $2`,
				accountID, startOfDayJakarta,
			).Scan(&sentToday)
			if err == nil && sentToday >= config.DailyLimitPerAccount {
				// Setelah batas tercapai, akun tidak berhenti permanen:
				// goroutine menunggu sampai awal hari berikutnya lalu lanjut.
				e.setAccountStopReason(ctx, accountID, fmt.Sprintf("batas harian tercapai (%d/%d), menunggu hari berikutnya", sentToday, config.DailyLimitPerAccount))
				waitDur := getJakartaNextDayWaitDuration()
				select {
				case <-ctx.Done():
					return
				case <-time.After(waitDur):
					continue
				}
			}
		}

		// 3. Klaim nomor secara atomik
		claimed, err := e.ClaimNextNumber(ctx, accountID)
		if err != nil {
			if errors.Is(err, ErrEngineLocked) {
				e.setAccountStopReason(ctx, accountID, "sistem dalam perbaikan")
				return
			}

			// Periksa apakah campaign yang berjalan sudah selesai (tidak ada nomor tersedia maupun dikirim)
			e.checkAllRunningCampaigns(ctx)

			// Tidak ada nomor yang tersedia saat ini, tunggu jeda singkat sebelum mencoba lagi
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
				continue
			}
		}

		// 4. Siapkan pesan campaign
		msg, err := e.buildCampaignMessage(ctx, claimed.CampaignID, claimed.RecipientName)
		if err != nil {
			consecutiveBuildFailures++
			slog.Error("engine: gagal menyusun pesan campaign", "campaign_id", claimed.CampaignID, "err", err)

			// Nomor yang gagal disusun tidak boleh diklaim ulang tanpa batas: pakai jalur max_attempts
			e.HandleSendFailure(ctx, accountID, claimed.ID, claimed.Attempts, maxAttempts, "gagal menyusun pesan campaign")

			// Setelah 3 kegagalan beruntun, hentikan akun dengan stop_reason "gagal menyusun pesan campaign"
			if consecutiveBuildFailures >= 3 {
				e.StopAccountForFailure(ctx, accountID, "gagal menyusun pesan campaign")
				return
			}

			// Beri jeda jika gagal menyusun pesan campaign
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
				_, _ = e.db.Pool.Exec(ctx,
					`UPDATE campaign_numbers SET status = 'tidak_terdaftar', sent_at = now() WHERE id = $1`,
					claimed.ID,
				)
				_, _ = e.CheckAndCompleteCampaign(ctx, claimed.CampaignID)

				// Setelah hasil tidak_terdaftar, terapkan jeda kecepatan yang sama seperti setelah kirim
				delay := e.calculateSpeedDelay(ctx, speed)
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
					continue
				}
			}
		}

		// 6. Kirim pesan lewat Sender dengan context timeout 60 detik
		sendCtx, sendCancel := context.WithTimeout(ctx, SendTimeout)
		res, sendErr := e.sender.Send(sendCtx, accountID, claimed.PhoneE164, msg)
		sendCancel()

		if sendErr != nil {
			consecutiveFailures++
			if errors.Is(sendErr, context.DeadlineExceeded) || sendCtx.Err() == context.DeadlineExceeded {
				// Error batas waktu dianggap hasil tidak pasti: nomor menjadi gagal dan tidak dicoba ulang
				_, _ = e.db.Pool.Exec(ctx,
					`UPDATE campaign_numbers SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti' WHERE id = $1`,
					claimed.ID,
				)
				_, _ = e.CheckAndCompleteCampaign(ctx, claimed.CampaignID)
			} else if errors.Is(sendErr, wa.ErrSessionNotActive) || errors.Is(sendErr, wa.ErrSessionNotConnected) {
				// Hanya error yang jelas berarti belum terkirim yang boleh mengembalikan nomor ke tersedia
				e.HandleSendFailure(ctx, accountID, claimed.ID, claimed.Attempts, maxAttempts, sendErr.Error())
			} else {
				// Error lain tidak pasti: nomor menjadi gagal dan tidak dicoba ulang
				_, _ = e.db.Pool.Exec(ctx,
					`UPDATE campaign_numbers SET status = 'gagal', sent_at = now(), last_error = $1 WHERE id = $2`,
					sendErr.Error(), claimed.ID,
				)
				_, _ = e.CheckAndCompleteCampaign(ctx, claimed.CampaignID)
			}

			// Cek max_consecutive_failures
			maxFail := config.MaxConsecutiveFailures
			if maxFail <= 0 {
				maxFail = DefaultMaxConsecutiveFailures
			}
			if consecutiveFailures >= maxFail {
				e.StopAccountForFailure(ctx, accountID, fmt.Sprintf("berhenti otomatis: %d kegagalan beruntun", consecutiveFailures))
				return
			}
		} else {
			// Berhasil terkirim!
			consecutiveFailures = 0
			_ = res

			// Catat status 'terkirim' dan buku besar 'upah' dengan WithoutCancel, timeout 10s, dan 3 kali coba ulang
			if err := e.RecordWithRetry(ctx, claimed.ID, workerID); err != nil {
				slog.Error("engine: gagal mencatat buku besar upah", "number_id", claimed.ID, "err", err)
			}
			_, _ = e.CheckAndCompleteCampaign(ctx, claimed.CampaignID)
		}

		// 7. Tunggu jeda kecepatan (dengan variasi acak ±20%)
		delay := e.calculateSpeedDelay(ctx, speed)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// buildCampaignMessage mengambil metadata campaign dan merender wa.OutMessage.
func (e *Engine) buildCampaignMessage(ctx context.Context, campaignID int64, recipientName string) (wa.OutMessage, error) {
	var body, mode, imagePath sql.NullString
	var elementsRaw []byte

	err := e.db.Pool.QueryRow(ctx,
		`SELECT body, button_mode, COALESCE(image_path, ''), elements FROM campaigns WHERE id = $1`,
		campaignID,
	).Scan(&body, &mode, &imagePath, &elementsRaw)
	if err != nil {
		return wa.OutMessage{}, err
	}

	var elem campaign.Elements
	if len(elementsRaw) > 0 {
		_ = json.Unmarshal(elementsRaw, &elem)
	}

	if err := campaign.ValidateTemplate(body.String, mode.String, elem); err != nil {
		return wa.OutMessage{}, fmt.Errorf("validasi template: %w", err)
	}

	var recVars map[string]string
	if recipientName != "" {
		recVars = map[string]string{"nama": recipientName, "name": recipientName}
	}

	cMsg := campaign.Render(body.String, imagePath.String, elem, mode.String, recVars)

	out := wa.OutMessage{
		Text: cMsg.Text,
	}
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

// RecordWithRetry mencatat keberhasilan kirim dan buku besar menggunakan context.WithoutCancel(ctx)
// dengan timeout 10 detik, coba ulang 3 kali (jeda 200 ms, 1 detik, 3 detik).
// Jika tetap gagal, nomor diubah statusnya menjadi 'gagal' dengan last_error "terkirim_tidak_tercatat",
// dicatat slog.Error beserta ID nomor, dan tidak pernah dikembalikan ke tersedia.
func (e *Engine) RecordWithRetry(ctx context.Context, numberID, workerID int64) error {
	delays := RecordRetryDelays
	var lastErr error

	for attempt := 0; attempt <= len(delays); attempt++ {
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		err := e.RecordSuccessAndLedger(recordCtx, numberID, workerID)
		cancel()

		if err == nil {
			return nil
		}
		lastErr = err

		if attempt < len(delays) {
			time.Sleep(delays[attempt])
		}
	}

	// Jika tetap gagal setelah coba ulang 3 kali:
	failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer failCancel()
	_, _ = e.db.Pool.Exec(failCtx,
		`UPDATE campaign_numbers SET status = 'gagal', sent_at = now(), last_error = 'terkirim_tidak_tercatat' WHERE id = $1`,
		numberID,
	)
	slog.Error("engine: gagal menulis buku besar setelah 3 kali coba ulang, nomor ditandai gagal", "number_id", numberID, "err", lastErr)

	return lastErr
}

// RecordSuccessAndLedger mencatat keberhasilan pengiriman dan entri buku besar upah dalam satu transaksi.
// Selalu menulis ledger_entries dengan currency 'IDR'.
func (e *Engine) RecordSuccessAndLedger(ctx context.Context, numberID, workerID int64) error {
	rate, okRate, err := e.getRateWage(ctx)
	if err != nil || !okRate || rate <= 0 {
		return fmt.Errorf("rate upah belum diisi admin")
	}

	tx, err := e.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Update status nomor
	_, err = tx.Exec(ctx,
		`UPDATE campaign_numbers SET status = 'terkirim', sent_at = now() WHERE id = $1`,
		numberID,
	)
	if err != nil {
		return fmt.Errorf("update status terkirim: %w", err)
	}

	// Masukkan buku besar upah (selalu IDR, idempoten dengan ON CONFLICT DO NOTHING)
	_, err = tx.Exec(ctx,
		`INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, campaign_number_id, note)
		 VALUES ($1, 'upah', 'IDR', $2, $3, $4, 'upah pesan terkirim')
		 ON CONFLICT (campaign_number_id) WHERE kind = 'upah' DO NOTHING`,
		workerID, rate, rate, numberID,
	)
	if err != nil {
		return fmt.Errorf("insert ledger upah: %w", err)
	}

	return tx.Commit(ctx)
}

// HandleSendFailure menangani kegagalan kirim sementara vs permanen sesuai max_attempts.
func (e *Engine) HandleSendFailure(ctx context.Context, accountID, numberID int64, attempts, maxAttempts int, errMsg string) {
	if attempts >= maxAttempts {
		// Sudah mencapai batas percobaan: gagal final
		_, _ = e.db.Pool.Exec(ctx,
			`UPDATE campaign_numbers SET status = 'gagal', sent_at = now(), last_error = $1 WHERE id = $2`,
			errMsg, numberID,
		)
		var cid int64
		_ = e.db.Pool.QueryRow(ctx, `SELECT campaign_id FROM campaign_numbers WHERE id = $1`, numberID).Scan(&cid)
		if cid > 0 {
			_, _ = e.CheckAndCompleteCampaign(ctx, cid)
		}
	} else {
		// Gagal sementara: kembalikan ke tersedia dengan attempts sudah bertambah
		_, _ = e.db.Pool.Exec(ctx,
			`UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL, last_error = $1 WHERE id = $2`,
			errMsg, numberID,
		)
	}
}

// CheckAndCompleteCampaign memeriksa apakah suatu campaign sudah tidak memiliki nomor 'tersedia' maupun 'dikirim'.
// Jika ya, status campaign diubah menjadi 'selesai' dan dicatat di audit_logs dalam satu transaksi.
func (e *Engine) CheckAndCompleteCampaign(ctx context.Context, campaignID int64) (bool, error) {
	if e.db == nil || e.db.Pool == nil {
		return false, nil
	}

	tx, err := e.db.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1 FOR UPDATE`, campaignID).Scan(&status)
	if err != nil {
		return false, err
	}
	if status != "berjalan" {
		return false, nil
	}

	var remaining int
	err = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status IN ('tersedia', 'dikirim')`,
		campaignID,
	).Scan(&remaining)
	if err != nil {
		return false, err
	}

	if remaining == 0 {
		_, err = tx.Exec(ctx,
			`UPDATE campaigns SET status = 'selesai', finished_at = now() WHERE id = $1`,
			campaignID,
		)
		if err != nil {
			return false, fmt.Errorf("update campaign selesai: %w", err)
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta)
			 VALUES (NULL, 'campaign_completed', 'campaign', $1, '{"reason": "tidak ada nomor tersedia maupun dikirim"}'::jsonb)`,
			fmt.Sprintf("%d", campaignID),
		)
		if err != nil {
			return false, fmt.Errorf("insert audit_logs campaign selesai: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		slog.Info("engine: campaign otomatis selesai", "campaign_id", campaignID)
		return true, nil
	}

	return false, nil
}

func (e *Engine) checkAllRunningCampaigns(ctx context.Context) {
	if e.db == nil || e.db.Pool == nil {
		return
	}
	rows, err := e.db.Pool.Query(ctx, `SELECT id FROM campaigns WHERE status = 'berjalan'`)
	if err != nil {
		return
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		_, _ = e.CheckAndCompleteCampaign(ctx, id)
	}
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
	// Variasi acak antara (1 - jitterPct) sampai (1 + jitterPct)
	minMult := 1.0 - jitterPct
	maxMult := 1.0 + jitterPct
	mult := minMult + rand.Float64()*(maxMult-minMult)

	delaySec := baseDur.Seconds() * mult
	if delaySec < 0.2 {
		delaySec = 0.2
	}
	return time.Duration(delaySec * float64(time.Second))
}

// calculateSpeedDelay menghitung jeda kecepatan dari konstanta config.SpeedDelays ditambah variasi acak ±config.JitterPercent%.
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

// StopAccountForFailure menghentikan akun karena kegagalan beruntun dan melepaskan nomor yang dipegang.
func (e *Engine) StopAccountForFailure(ctx context.Context, accountID int64, reason string) {
	slog.Warn("engine: akun berhenti otomatis karena kegagalan beruntun", "account_id", accountID, "alasan", reason)
	if e.db != nil && e.db.Pool != nil {
		_, _ = e.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = $1 WHERE id = $2`,
			reason, accountID,
		)
		_, _ = e.db.Pool.Exec(ctx,
			`UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL
			 WHERE status = 'dikirim' AND claimed_account_id = $1`, accountID,
		)
	}
}
