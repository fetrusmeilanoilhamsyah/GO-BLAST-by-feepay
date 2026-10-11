package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"goblast/internal/campaign"
	"goblast/internal/db"
	"goblast/internal/wa"
)

// DeliveryLevel mendefinisikan kapan pesan dianggap terkirim.
// Nilai bawaan: "server_ack" (centang satu dari server WhatsApp).
// TODO: keputusan terbuka nomor 1 di DOC_KONSEP bagian 15 —
// apakah cukup server_ack (centang satu) atau butuh delivered (centang dua di perangkat).
const DeliveryLevel = "server_ack"

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

// Engine mengelola pengiriman pesan otomatis per akun WhatsApp pekerja.
type Engine struct {
	db       *db.DB
	sender   wa.Sender
	checker  WhatsAppChecker
	settings SettingsReader

	mu      sync.Mutex
	workers map[int64]context.CancelFunc // accountID -> cancel goroutine
	stopped bool
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
func (e *Engine) StartAccount(accountID, workerID int64, currency string) {
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

	go e.runWorker(ctx, accountID, workerID, currency)
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

// StopAll menghentikan seluruh worker engine.
func (e *Engine) StopAll() {
	e.mu.Lock()
	e.stopped = true
	for id, cancel := range e.workers {
		cancel()
		delete(e.workers, id)
	}
	e.mu.Unlock()
}

// StartCleaner menjalankan pembersih nomor menggantung secara berkala setiap 60 detik.
func (e *Engine) StartCleaner(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.CleanStaleNumbers(ctx)
			}
		}
	}()
}

// CleanStaleNumbers mengembalikan nomor dikirim yang claimed_at lebih tua dari claim_timeout_seconds ke tersedia.
func (e *Engine) CleanStaleNumbers(ctx context.Context) (int64, error) {
	if e.db == nil || e.db.Pool == nil {
		return 0, nil
	}

	timeoutSec, ok, err := e.settings.GetInt(ctx, "claim_timeout_seconds")
	if err != nil || !ok || timeoutSec <= 0 {
		timeoutSec = 300 // Nilai awal 300 detik
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

// ClaimNextNumber mengklaim tepat satu nomor secara atomik dengan FOR UPDATE SKIP LOCKED.
func (e *Engine) ClaimNextNumber(ctx context.Context, accountID int64) (*ClaimedNumber, error) {
	query := `
		UPDATE campaign_numbers
		SET status = 'dikirim', claimed_account_id = $1, claimed_at = now(), attempts = attempts + 1
		WHERE id = (
			SELECT cn.id
			FROM campaign_numbers cn
			JOIN campaigns c ON c.id = cn.campaign_id
			WHERE cn.status = 'tersedia' AND c.status = 'berjalan'
			ORDER BY cn.id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, campaign_id, phone_e164, attempts, COALESCE(recipient_name, '')
	`
	var cn ClaimedNumber
	err := e.db.Pool.QueryRow(ctx, query, accountID).Scan(
		&cn.ID, &cn.CampaignID, &cn.PhoneE164, &cn.Attempts, &cn.RecipientName,
	)
	if err != nil {
		return nil, err
	}
	return &cn, nil
}

// runWorker adalah siklus kerja utama satu akun WhatsApp.
func (e *Engine) runWorker(ctx context.Context, accountID, workerID int64, currency string) {
	defer func() {
		e.mu.Lock()
		delete(e.workers, accountID)
		e.mu.Unlock()
	}()

	consecutiveFailures := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// 1. Ambil pengaturan penting (daily_limits, max_attempts)
		// Jika salah satu belum diisi admin, mesin tidak mengirim apa pun dan mencatat alasannya.
		var dailyLimits map[string]int
		okDaily, err := e.settings.GetJSON(ctx, "daily_limits", &dailyLimits)
		if err != nil || !okDaily || len(dailyLimits) == 0 {
			e.setAccountStopReason(ctx, accountID, "pengaturan daily_limits belum diisi admin (keputusan terbuka #2)")
			return
		}

		maxAttempts, okAttempts, err := e.settings.GetInt(ctx, "max_attempts")
		if err != nil || !okAttempts || maxAttempts <= 0 {
			e.setAccountStopReason(ctx, accountID, "pengaturan max_attempts belum diisi admin (keputusan terbuka #3)")
			return
		}

		// Baca data akun (speed, status)
		var speed, status string
		err = e.db.Pool.QueryRow(ctx, `SELECT speed, status FROM wa_accounts WHERE id = $1`, accountID).Scan(&speed, &status)
		if err != nil || status != "terhubung" {
			// Sesi tidak lagi berstatus terhubung
			return
		}

		// 2. Cek batas harian akun
		dailyLimit, hasLimit := dailyLimits[speed]
		if !hasLimit {
			dailyLimit, hasLimit = dailyLimits["medium"]
		}
		if hasLimit && dailyLimit > 0 {
			var sentToday int
			err = e.db.Pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM campaign_numbers 
				 WHERE claimed_account_id = $1 AND status = 'terkirim' AND sent_at >= date_trunc('day', now())`,
				accountID,
			).Scan(&sentToday)
			if err == nil && sentToday >= dailyLimit {
				e.setAccountStopReason(ctx, accountID, fmt.Sprintf("batas harian tercapai (%d/%d)", sentToday, dailyLimit))
				return
			}
		}

		// 3. Klaim nomor secara atomik
		claimed, err := e.ClaimNextNumber(ctx, accountID)
		if err != nil {
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
			slog.Error("engine: gagal menyusun pesan campaign", "campaign_id", claimed.CampaignID, "err", err)
			e.returnNumberToAvailable(ctx, claimed.ID, "gagal render template")
			continue
		}

		// 5. Cek terdaftar di WhatsApp sebelum kirim
		if e.checker != nil {
			isOnWA, checkErr := e.checker.IsOnWhatsApp(ctx, accountID, claimed.PhoneE164)
			if checkErr == nil && !isOnWA {
				// Tidak terdaftar di WhatsApp: status final tidak_terdaftar, tanpa upah
				_, _ = e.db.Pool.Exec(ctx,
					`UPDATE campaign_numbers SET status = 'tidak_terdaftar', sent_at = now() WHERE id = $1`,
					claimed.ID,
				)
				continue
			}
		}

		// 6. Kirim pesan lewat Sender
		res, sendErr := e.sender.Send(ctx, accountID, claimed.PhoneE164, msg)
		if sendErr != nil {
			// Gagal sementara
			consecutiveFailures++
			e.handleSendFailure(ctx, accountID, claimed.ID, claimed.Attempts, maxAttempts, sendErr.Error())

			// Cek max_consecutive_failures
			maxFail, okFail, _ := e.settings.GetInt(ctx, "max_consecutive_failures")
			if okFail && maxFail > 0 && consecutiveFailures >= maxFail {
				e.stopAccountForFailure(ctx, accountID, fmt.Sprintf("berhenti otomatis: %d kegagalan beruntun", consecutiveFailures))
				return
			}
		} else {
			// Berhasil terkirim!
			consecutiveFailures = 0
			_ = res

			// Catat status 'terkirim' dan buku besar 'upah' dalam satu transaksi idempoten
			if err := e.recordSuccessAndLedger(ctx, claimed.ID, workerID, currency); err != nil {
				slog.Error("engine: record ledger error", "number_id", claimed.ID, "err", err)
			}
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

// recordSuccessAndLedger mencatat keberhasilan pengiriman dan entri buku besar upah dalam satu transaksi.
// Dibuat idempoten melalui indeks unik idx_ledger_entries_upah_unique di level database.
func (e *Engine) recordSuccessAndLedger(ctx context.Context, numberID, workerID int64, currency string) error {
	// Ambil rate saat ini dari settings
	rateKey := "rate_idr"
	if currency == "USDT" {
		rateKey = "rate_usdt"
	}

	rate, ok, err := e.settings.GetInt(ctx, rateKey)
	if err != nil || !ok {
		// Coba fallback key alternatif
		if currency == "IDR" {
			rate, ok, _ = e.settings.GetInt(ctx, "rate_wage_idr")
		} else {
			rate, ok, _ = e.settings.GetInt(ctx, "rate_wage_usdt")
		}
	}
	if !ok || rate < 0 {
		rate = 0
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

	// Masukkan buku besar upah (idempoten dengan ON CONFLICT DO NOTHING)
	_, err = tx.Exec(ctx,
		`INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, campaign_number_id, note)
		 VALUES ($1, 'upah', $2, $3, $4, $5, 'upah pesan terkirim')
		 ON CONFLICT (campaign_number_id) WHERE kind = 'upah' DO NOTHING`,
		workerID, currency, int64(rate), int64(rate), numberID,
	)
	if err != nil {
		return fmt.Errorf("insert ledger upah: %w", err)
	}

	return tx.Commit(ctx)
}

// handleSendFailure menangani kegagalan kirim sementara vs permanen.
func (e *Engine) handleSendFailure(ctx context.Context, accountID, numberID int64, attempts, maxAttempts int, errMsg string) {
	if attempts >= maxAttempts {
		// Sudah mencapai batas percobaan: gagal final
		_, _ = e.db.Pool.Exec(ctx,
			`UPDATE campaign_numbers SET status = 'gagal', last_error = $1 WHERE id = $2`,
			errMsg, numberID,
		)
	} else {
		// Gagal sementara: kembalikan ke tersedia dengan attempts sudah bertambah
		_, _ = e.db.Pool.Exec(ctx,
			`UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL, last_error = $1 WHERE id = $2`,
			errMsg, numberID,
		)
	}
}

// returnNumberToAvailable mengembalikan nomor ke tersedia tanpa menambah attempts.
func (e *Engine) returnNumberToAvailable(ctx context.Context, numberID int64, errMsg string) {
	_, _ = e.db.Pool.Exec(ctx,
		`UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL, last_error = $1 WHERE id = $2`,
		errMsg, numberID,
	)
}

// calculateSpeedDelay menghitung jeda kecepatan dari settings kunci speed_delays ditambah variasi acak ±20%.
func (e *Engine) calculateSpeedDelay(ctx context.Context, speed string) time.Duration {
	var delays map[string]int
	ok, err := e.settings.GetJSON(ctx, "speed_delays", &delays)
	baseSec := 5 // Bawaan medium: 5 detik
	if err == nil && ok && delays != nil {
		if sec, found := delays[speed]; found && sec > 0 {
			baseSec = sec
		}
	} else {
		switch speed {
		case "fast":
			baseSec = 1
		case "medium":
			baseSec = 5
		case "slow":
			baseSec = 15
		case "over_slow":
			baseSec = 30
		}
	}

	// Variasi acak ±20% (antara 0.80 sampai 1.20)
	jitter := 0.8 + rand.Float64()*0.4
	delaySec := float64(baseSec) * jitter
	if delaySec < 0.2 {
		delaySec = 0.2
	}
	return time.Duration(delaySec * float64(time.Second))
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

func (e *Engine) stopAccountForFailure(ctx context.Context, accountID int64, reason string) {
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
