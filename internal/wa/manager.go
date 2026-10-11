package wa

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"goblast/internal/config"
	"goblast/internal/db"

	"github.com/jackc/pgx/v5"
	qrcode "github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Error sentinel untuk kondisi sesi
var (
	ErrSessionNotActive   = errors.New("sesi tidak aktif")
	ErrSessionNotConnected = errors.New("sesi tidak terhubung")
)

// SettingsReader membaca nilai integer dari settings.
type SettingsReader interface {
	GetInt(ctx context.Context, key string) (int, bool, error)
}

// Session menyimpan satu koneksi WhatsApp beserta status dan metadata-nya.
type Session struct {
	mu        sync.RWMutex
	AccountID int64
	Client    *whatsmeow.Client
	Status    string // menunggu_scan | terhubung | berhenti | terputus | antre
	WorkerID  int64
	cancel    context.CancelFunc

	// qrChan dikirim dari goroutine event ke caller Connect saat QR tersedia.
	qrChan         chan string
	lastQR         string
	lastQRImageB64 string
	pairingFn      func(phone string) (string, error)
}

// Manager memegang peta sesi aktif (accountID → *Session).
type Manager struct {
	mu        sync.RWMutex
	sessions  map[int64]*Session // accountID → session
	db        *db.DB
	settings  SettingsReader
	cfg       *config.Config
	container *sqlstore.Container

	// onDisconnect dipanggil engine saat sesi terputus.
	onDisconnect func(accountID int64)
}

// NewManager membuat Manager baru. onDisconnect opsional (boleh nil).
func NewManager(database *db.DB, settings SettingsReader, cfg *config.Config) *Manager {
	return &Manager{
		sessions: make(map[int64]*Session),
		db:       database,
		settings: settings,
		cfg:      cfg,
	}
}

// SetDisconnectCallback mendaftarkan callback yang dipanggil saat sesi terputus (dipakai engine).
func (m *Manager) SetDisconnectCallback(fn func(accountID int64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onDisconnect = fn
}

// GetSession mengembalikan sesi aktif atau nil jika tidak ada.
func (m *Manager) GetSession(accountID int64) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[accountID]
}

// GetClient mengembalikan *whatsmeow.Client untuk accountID, atau nil.
func (m *Manager) GetClient(accountID int64) *whatsmeow.Client {
	sess := m.GetSession(accountID)
	if sess == nil {
		return nil
	}
	return sess.Client
}

// Connect memulai proses sambung untuk accountID.
// Mengembalikan channel QR (string base64 PNG) dan fungsi pairing.
// Caller harus membaca qrChan di goroutine lain karena pengiriman non-blocking.
func (m *Manager) Connect(ctx context.Context, accountID, workerID int64) (<-chan string, func(phone string) (string, error), error) {
	// Cek apakah sesi untuk accountID sudah aktif berjalan
	m.mu.RLock()
	if existing, ok := m.sessions[accountID]; ok && existing != nil {
		m.mu.RUnlock()
		return existing.qrChan, existing.pairingFn, nil
	}
	m.mu.RUnlock()

	// --- Cek batas dari settings ---
	maxPerWorker, okPerWorker, err := m.settings.GetInt(ctx, "max_accounts_per_worker")
	if err != nil || !okPerWorker || maxPerWorker <= 0 {
		maxPerWorker = 5
	}
	maxActive, okActive, err := m.settings.GetInt(ctx, "max_active_sessions")
	if err != nil || !okActive || maxActive <= 0 {
		maxActive = 50
	}

	// Hitung sesi aktif milik workerID (selain akun ini sendiri)
	var countPerWorker int
	err = m.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM wa_accounts WHERE worker_id = $1 AND status IN ('terhubung','menunggu_scan') AND id != $2`,
		workerID, accountID,
	).Scan(&countPerWorker)
	if err != nil {
		return nil, nil, fmt.Errorf("gagal menghitung akun pekerja: %w", err)
	}
	if countPerWorker >= maxPerWorker {
		return nil, nil, fmt.Errorf("batas akun per pekerja tercapai (%d dari %d diizinkan)", countPerWorker, maxPerWorker)
	}

	// Hitung total sesi terhubung
	var totalActive int
	err = m.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM wa_accounts WHERE status = 'terhubung'`,
	).Scan(&totalActive)
	if err != nil {
		return nil, nil, fmt.Errorf("gagal menghitung sesi aktif: %w", err)
	}
	if totalActive >= maxActive {
		// Set status antre
		_, _ = m.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'antre' WHERE id = $1`, accountID)
		return nil, nil, fmt.Errorf("batas sesi aktif tercapai (%d dari %d), akun masuk antre", totalActive, maxActive)
	}

	// --- Gunakan whatsmeow store singleton container ---
	container, err := m.getContainer(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("gagal menyiapkan store whatsmeow: %w", err)
	}

	// Untuk setiap sesi baru yang discan, buat deviceStore baru yang unauthenticated (ID == nil)
	deviceStore := container.NewDevice()

	client := whatsmeow.NewClient(deviceStore, waLog.Noop)

	// --- Update status menunggu_scan ---
	_, err = m.db.Pool.Exec(ctx,
		`UPDATE wa_accounts SET status = 'menunggu_scan' WHERE id = $1`, accountID)
	if err != nil {
		return nil, nil, fmt.Errorf("gagal update status menunggu_scan: %w", err)
	}

	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &Session{
		AccountID: accountID,
		Client:    client,
		Status:    "menunggu_scan",
		WorkerID:  workerID,
		cancel:    cancel,
		qrChan:    make(chan string, 10),
	}

	pairingFn := func(phone string) (string, error) {
		if client.IsLoggedIn() {
			return "", errors.New("sudah terhubung, tidak perlu pairing")
		}
		code, err := client.PairPhone(context.Background(), phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
		if err != nil {
			return "", fmt.Errorf("pairing gagal: %w", err)
		}
		return code, nil
	}
	sess.pairingFn = pairingFn

	m.mu.Lock()
	m.sessions[accountID] = sess
	m.mu.Unlock()

	// Tangani event
	client.AddEventHandler(func(evt interface{}) {
		m.handleEvent(sessCtx, accountID, workerID, evt)
	})

	// Mulai koneksi
	if client.Store.ID == nil {
		// Perlu QR atau pairing
		qrChan, _ := client.GetQRChannel(sessCtx)
		go func() {
			for qr := range qrChan {
				if qr.Event == "code" {
					var b64 string
					if pngBytes, err := qrcode.Encode(qr.Code, qrcode.Medium, 256); err == nil {
						b64 = base64.StdEncoding.EncodeToString(pngBytes)
					}

					sess.mu.Lock()
					sess.lastQR = qr.Code
					sess.lastQRImageB64 = b64
					sess.mu.Unlock()

					select {
					case sess.qrChan <- qr.Code:
					default:
					}
				}
			}
		}()
		err = client.Connect()
		if err != nil {
			m.removeSession(accountID)
			return nil, nil, fmt.Errorf("gagal mulai koneksi: %w", err)
		}
	} else {
		err = client.Connect()
		if err != nil {
			m.removeSession(accountID)
			return nil, nil, fmt.Errorf("gagal mulai koneksi: %w", err)
		}
	}

	return sess.qrChan, pairingFn, nil
}

// handleEvent menangani event whatsmeow. TIDAK menyimpan pesan masuk.
func (m *Manager) handleEvent(ctx context.Context, accountID, workerID int64, evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		_ = v
		slog.Info("wa: terhubung", "account_id", accountID)
		m.mu.Lock()
		if sess, ok := m.sessions[accountID]; ok {
			sess.Status = "terhubung"
		}
		m.mu.Unlock()
		_, _ = m.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'terhubung', connected_at = now() WHERE id = $1`,
			accountID)

	case *events.Disconnected:
		_ = v
		slog.Info("wa: terputus", "account_id", accountID)
		m.mu.Lock()
		if sess, ok := m.sessions[accountID]; ok {
			sess.Status = "terputus"
		}
		cb := m.onDisconnect
		m.mu.Unlock()

		_, _ = m.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'terputus' WHERE id = $1`, accountID)
		// Kembalikan nomor yang sedang dikirim ke tersedia
		m.releaseNumbers(ctx, accountID)

		if cb != nil {
			cb(accountID)
		}

	case *events.LoggedOut:
		_ = v
		slog.Info("wa: logged out", "account_id", accountID)
		m.removeSession(accountID)
		_, _ = m.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = 'logged_out' WHERE id = $1`,
			accountID)
		m.releaseNumbers(ctx, accountID)

	// Semua event pesan masuk DIABAIKAN — tidak disimpan sesuai aturan privasi
	case *events.Message:
		_ = v // sengaja diabaikan

	default:
		// event lain juga diabaikan
	}
}

// Disconnect memutus koneksi (bukan logout). Status menjadi terputus.
// callerWorkerID harus cocok dengan worker_id di DB.
func (m *Manager) Disconnect(ctx context.Context, accountID, callerWorkerID int64) error {
	if err := m.checkOwnership(ctx, accountID, callerWorkerID); err != nil {
		return err
	}

	m.mu.Lock()
	sess, ok := m.sessions[accountID]
	m.mu.Unlock()
	if ok && sess != nil {
		sess.cancel()
		sess.Client.Disconnect()
	}

	m.removeSession(accountID)
	_, _ = m.db.Pool.Exec(ctx,
		`UPDATE wa_accounts SET status = 'terputus' WHERE id = $1`, accountID)
	m.releaseNumbers(ctx, accountID)
	return nil
}

// DisconnectAccount memutus koneksi sebuah akun tanpa memeriksa callerWorkerID (dipakai admin saat menonaktifkan pekerja).
func (m *Manager) DisconnectAccount(ctx context.Context, accountID int64) error {
	m.mu.Lock()
	sess, ok := m.sessions[accountID]
	m.mu.Unlock()
	if ok && sess != nil {
		sess.cancel()
		if sess.Client != nil {
			sess.Client.Disconnect()
		}
	}

	m.removeSession(accountID)
	_, _ = m.db.Pool.Exec(ctx,
		`UPDATE wa_accounts SET status = 'terputus' WHERE id = $1`, accountID)
	m.releaseNumbers(ctx, accountID)
	return nil
}

// Stop logout penuh (hapus sesi whatsmeow), status menjadi berhenti.
// callerWorkerID harus cocok dengan worker_id di DB.
func (m *Manager) Stop(ctx context.Context, accountID, callerWorkerID int64, reason string) error {
	if err := m.checkOwnership(ctx, accountID, callerWorkerID); err != nil {
		return err
	}

	m.mu.Lock()
	sess, ok := m.sessions[accountID]
	m.mu.Unlock()

	if ok && sess != nil {
		sess.cancel()
		if err := sess.Client.Logout(ctx); err != nil {
			// Logout gagal (mungkin sudah terputus) — tetap lanjutkan pembersihan
			slog.Warn("wa: logout gagal (dilanjutkan)", "account_id", accountID, "err", err)
		}
	}

	m.removeSession(accountID)
	_, _ = m.db.Pool.Exec(ctx,
		`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = $1 WHERE id = $2`,
		reason, accountID)
	m.releaseNumbers(ctx, accountID)
	return nil
}

// IsOnWhatsApp memeriksa apakah nomor terdaftar di WhatsApp via client.
// Mengembalikan false jika client tidak ada.
func (m *Manager) IsOnWhatsApp(ctx context.Context, accountID int64, phone string) (bool, error) {
	m.mu.RLock()
	sess, ok := m.sessions[accountID]
	m.mu.RUnlock()
	if !ok || sess == nil || sess.Client == nil {
		return false, ErrSessionNotActive
	}
	jid := types.NewJID(phone, types.DefaultUserServer)
	results, err := sess.Client.IsOnWhatsApp(ctx, []string{jid.User})
	if err != nil {
		return false, err
	}
	for _, r := range results {
		if r.JID.User == phone {
			return r.IsIn, nil
		}
	}
	return false, nil
}

// --- helper ---

func (m *Manager) getContainer(ctx context.Context) (*sqlstore.Container, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.container != nil {
		return m.container, nil
	}
	if m.cfg == nil || m.cfg.DatabaseURL == "" {
		return nil, errors.New("database URL tidak tersedia")
	}
	container, err := sqlstore.New(ctx, "pgx", m.cfg.DatabaseURL, waLog.Noop)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat sqlstore: %w", err)
	}
	m.container = container
	return container, nil
}

func (m *Manager) removeSession(accountID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, accountID)
}

// checkOwnership memastikan callerWorkerID adalah pemilik accountID.
func (m *Manager) checkOwnership(ctx context.Context, accountID, callerWorkerID int64) error {
	var ownerID int64
	err := m.db.Pool.QueryRow(ctx,
		`SELECT worker_id FROM wa_accounts WHERE id = $1`, accountID,
	).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("akun tidak ditemukan")
		}
		return fmt.Errorf("gagal memeriksa kepemilikan: %w", err)
	}
	if ownerID != callerWorkerID {
		return errors.New("akses ditolak: bukan pemilik akun")
	}
	return nil
}

// releaseNumbers mengembalikan nomor berstatus dikirim milik accountID ke tersedia.
func (m *Manager) releaseNumbers(ctx context.Context, accountID int64) {
	_, err := m.db.Pool.Exec(ctx,
		`UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL
		 WHERE status = 'dikirim' AND claimed_account_id = $1`, accountID)
	if err != nil {
		slog.Error("wa: gagal melepas nomor", "account_id", accountID, "err", err)
	}
}

// --- DBSettingsReader ---

// DBSettingsReader mengimplementasikan SettingsReader dengan query ke tabel settings.
type DBSettingsReader struct {
	db *db.DB
}

func NewDBSettingsReader(database *db.DB) *DBSettingsReader {
	return &DBSettingsReader{db: database}
}

// GetInt membaca nilai integer dari settings. Mengembalikan (0, false, nil) jika key tidak ada.
func (r *DBSettingsReader) GetInt(ctx context.Context, key string) (int, bool, error) {
	var raw string
	err := r.db.Pool.QueryRow(ctx,
		`SELECT value FROM settings WHERE key = $1`, key,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("db get int %s: %w", key, err)
	}
	var val int
	if jsonErr := json.Unmarshal([]byte(raw), &val); jsonErr != nil {
		return 0, false, fmt.Errorf("settings %s bukan integer: %w", key, jsonErr)
	}
	return val, true, nil
}

// GetJSON membaca nilai JSON dari settings ke target.
func (r *DBSettingsReader) GetJSON(ctx context.Context, key string, target interface{}) (bool, error) {
	var raw string
	err := r.db.Pool.QueryRow(ctx,
		`SELECT value FROM settings WHERE key = $1`, key,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("db get json %s: %w", key, err)
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return false, fmt.Errorf("settings %s JSON tidak valid: %w", key, err)
	}
	return true, nil
}

// GetString membaca nilai string dari settings.
func (r *DBSettingsReader) GetString(ctx context.Context, key string) (string, bool, error) {
	var raw string
	err := r.db.Pool.QueryRow(ctx,
		`SELECT value FROM settings WHERE key = $1`, key,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("db get string %s: %w", key, err)
	}
	var val string
	if jsonErr := json.Unmarshal([]byte(raw), &val); jsonErr != nil {
		// mungkin tidak di-quote, coba langsung
		return raw, true, nil
	}
	return val, true, nil
}

// GetStatusForAccount mengambil status wa_accounts dari DB (bukan dari map in-memory).
func (m *Manager) GetStatusForAccount(ctx context.Context, accountID int64) (string, error) {
	// Cek in-memory dulu (lebih akurat untuk sesi yang sedang berjalan)
	m.mu.RLock()
	sess, ok := m.sessions[accountID]
	m.mu.RUnlock()
	if ok && sess != nil {
		return sess.Status, nil
	}
	// Fallback ke DB
	var status string
	err := m.db.Pool.QueryRow(ctx,
		`SELECT status FROM wa_accounts WHERE id = $1`, accountID,
	).Scan(&status)
	if err != nil {
		return "", err
	}
	return status, nil
}

// GetQRCode mengambil QR terakhir untuk accountID sebagai string kode (bukan PNG).
// Mengambil dari cache in-memory terlebih dahulu tanpa mengosongkan channel.
func (m *Manager) GetQRCode(accountID int64) (string, bool) {
	m.mu.RLock()
	sess, ok := m.sessions[accountID]
	m.mu.RUnlock()
	if !ok || sess == nil {
		return "", false
	}

	sess.mu.RLock()
	code := sess.lastQR
	sess.mu.RUnlock()
	if code != "" {
		return code, true
	}

	// Jika belum ada di lastQR, tunggu maksimal 2.5 detik untuk event pertama
	select {
	case c := <-sess.qrChan:
		sess.mu.Lock()
		sess.lastQR = c
		if sess.lastQRImageB64 == "" {
			if pngBytes, err := qrcode.Encode(c, qrcode.Medium, 256); err == nil {
				sess.lastQRImageB64 = base64.StdEncoding.EncodeToString(pngBytes)
			}
		}
		sess.mu.Unlock()
		return c, true
	case <-time.After(2500 * time.Millisecond):
		return "", false
	}
}

// GetQRImageB64 mengambil base64 PNG dari QR code akun (diambil dari in-memory cache secepat kilat).
func (m *Manager) GetQRImageB64(accountID int64) (string, bool) {
	m.mu.RLock()
	sess, ok := m.sessions[accountID]
	m.mu.RUnlock()
	if !ok || sess == nil {
		return "", false
	}

	sess.mu.RLock()
	img := sess.lastQRImageB64
	sess.mu.RUnlock()
	if img != "" {
		return img, true
	}

	code, ok := m.GetQRCode(accountID)
	if !ok || code == "" {
		return "", false
	}

	sess.mu.RLock()
	img = sess.lastQRImageB64
	sess.mu.RUnlock()
	if img != "" {
		return img, true
	}

	if pngBytes, err := qrcode.Encode(code, qrcode.Medium, 256); err == nil {
		b64 := base64.StdEncoding.EncodeToString(pngBytes)
		sess.mu.Lock()
		sess.lastQRImageB64 = b64
		sess.mu.Unlock()
		return b64, true
	}
	return "", false
}

// PairPhone meminta kode pairing 8 karakter untuk nomor HP tertentu pada akun yang sedang menunggu scan.
func (m *Manager) PairPhone(accountID int64, phone string) (string, error) {
	m.mu.RLock()
	sess, ok := m.sessions[accountID]
	m.mu.RUnlock()
	if !ok || sess == nil {
		return "", errors.New("sesi tidak ditemukan atau belum dimulai")
	}
	sess.mu.RLock()
	fn := sess.pairingFn
	sess.mu.RUnlock()
	if fn == nil {
		return "", errors.New("fungsi pairing tidak tersedia")
	}
	return fn(phone)
}

// Delete menghapus sesi dan catatan akun dari database.
// callerWorkerID harus cocok dengan worker_id di DB.
func (m *Manager) Delete(ctx context.Context, accountID, callerWorkerID int64) error {
	if err := m.checkOwnership(ctx, accountID, callerWorkerID); err != nil {
		return err
	}
	m.mu.Lock()
	sess, ok := m.sessions[accountID]
	m.mu.Unlock()
	if ok && sess != nil {
		sess.cancel()
		if sess.Client != nil {
			sess.Client.Disconnect()
			if sess.Client.Store != nil {
				_ = sess.Client.Store.Delete(ctx)
			}
		}
	}
	m.removeSession(accountID)
	m.releaseNumbers(ctx, accountID)
	_, _ = m.db.Pool.Exec(ctx, `UPDATE campaign_numbers SET claimed_account_id = NULL WHERE claimed_account_id = $1`, accountID)
	_, err := m.db.Pool.Exec(ctx, `DELETE FROM wa_accounts WHERE id = $1`, accountID)
	return err
}

