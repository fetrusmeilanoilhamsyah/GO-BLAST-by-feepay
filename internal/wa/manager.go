package wa

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/safe"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	qrcode "github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Error sentinel untuk kondisi sesi
var (
	ErrSessionNotActive    = errors.New("sesi tidak aktif")
	ErrSessionNotConnected = errors.New("sesi tidak terhubung")
)

const (
	numEventWorkers = 4
	eventQueueCap   = 64
)

// SettingsReader membaca nilai integer dari settings.
type SettingsReader interface {
	GetInt(ctx context.Context, key string) (int, bool, error)
}

// Session menyimpan satu koneksi WhatsApp beserta status dan metadata-nya.
type Session struct {
	mu              sync.RWMutex
	AccountID       int64
	Client          WAClient
	Status          string // menunggu_scan | terhubung | berhenti | terputus | antre
	WorkerID        int64
	cancel          context.CancelFunc
	deviceStore     *store.Device
	disconnectTimer *time.Timer

	// qrChan dikirim dari goroutine event ke caller Connect saat QR tersedia.
	qrChan         chan string
	lastQR         string
	lastQRImageB64 string
	pairingFn      func(phone string) (string, error)
}

// GetStatus mengambil status sesi yang dilindungi sess.mu.
func (s *Session) GetStatus() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status
}

// SetStatus mengubah status sesi yang dilindungi sess.mu.
func (s *Session) SetStatus(st string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = st
}

func (s *Session) startDisconnectTimer(d time.Duration, onTimeout func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disconnectTimer != nil {
		s.disconnectTimer.Stop()
	}
	s.disconnectTimer = time.AfterFunc(d, onTimeout)
}

func (s *Session) cancelDisconnectTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disconnectTimer != nil {
		s.disconnectTimer.Stop()
		s.disconnectTimer = nil
	}
}

type eventJob struct {
	accountID int64
	workerID  int64
	evt       interface{}
}

// Manager memegang peta sesi aktif (accountID → *Session) dan antrean event.
type Manager struct {
	mu            sync.RWMutex
	sessions      map[int64]*Session // accountID → session
	db            *db.DB
	settings      SettingsReader
	cfg           *config.Config
	container     *sqlstore.Container
	clientFactory func(deviceStore *store.Device) WAClient
	engineStopper func(ctx context.Context, accountID int64) error

	clientOutdated atomic.Bool

	eventQueues []chan eventJob
	eventWG     sync.WaitGroup
	isClosed    bool
}

// NewManager membuat Manager baru dan menginisialisasi pool pekerja event whatsmeow.
func NewManager(database *db.DB, settings SettingsReader, cfg *config.Config) *Manager {
	m := &Manager{
		sessions:    make(map[int64]*Session),
		db:          database,
		settings:    settings,
		cfg:         cfg,
		eventQueues: make([]chan eventJob, numEventWorkers),
	}
	m.clientFactory = func(deviceStore *store.Device) WAClient {
		if deviceStore == nil {
			return nil
		}
		return whatsmeow.NewClient(deviceStore, waLog.Noop)
	}

	for i := 0; i < numEventWorkers; i++ {
		q := make(chan eventJob, eventQueueCap)
		m.eventQueues[i] = q
		workerIdx := i
		m.eventWG.Add(1)
		safe.Go(fmt.Sprintf("wa.eventWorker-%d", workerIdx), func() {
			defer m.eventWG.Done()
			for job := range q {
				processCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
				m.processEvent(processCtx, job.accountID, job.workerID, job.evt)
				cancel()
			}
		})
	}

	return m
}

// SetClientFactory mengganti fungsi pembuat client (dipakai pada pengujian dengan FakeClient).
func (m *Manager) SetClientFactory(fn func(deviceStore *store.Device) WAClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clientFactory = fn
}

// SetEngineStopper mendaftarkan fungsi untuk menghentikan goroutine engine akun secara anggun dan menunggu selesai.
func (m *Manager) SetEngineStopper(fn func(ctx context.Context, accountID int64) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.engineStopper = fn
}

// SetDisconnectCallback mempertahankan kompatibilitas tanda tangan lama.
func (m *Manager) SetDisconnectCallback(fn func(accountID int64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fn != nil {
		m.engineStopper = func(_ context.Context, accID int64) error {
			fn(accID)
			return nil
		}
	}
}

// IsClientOutdated memeriksa apakah pernah diterima event ClientOutdated secara global.
func (m *Manager) IsClientOutdated() bool {
	return m.clientOutdated.Load()
}

// ReconcileOnBoot menjalankan rekonsiliasi saat boot sebelum menerima request (Keputusan B1):
// 1. Mengubah semua baris wa_accounts yang berstatus terhubung, menunggu_scan, antre, atau terputus
//    menjadi terputus dengan stop_reason 'server restart'. Baris 'berhenti' tidak diubah.
// 2. Membersihkan sisa perangkat whatsmeow lewat API pustaka (GetAllDevices lalu Delete per perangkat),
//    bukan lewat DELETE mentah ke tabel whatsmeow_*. Hanya jumlah yang dicatat di log, tidak ada JID.
// 3. Perilaku pembersihan perangkat dapat dimatikan dengan WA_PURGE_ORPHANS_ON_BOOT=false (bawaan true).
func (m *Manager) ReconcileOnBoot(ctx context.Context) (reconciledAccounts int64, purgedDevices int, err error) {
	if m.db != nil && m.db.Pool != nil {
		res, execErr := m.db.Pool.Exec(ctx,
			`UPDATE wa_accounts
			 SET status = 'terputus', stop_reason = 'server restart'
			 WHERE status IN ('terhubung', 'menunggu_scan', 'antre', 'terputus')`,
		)
		if execErr != nil {
			return 0, 0, fmt.Errorf("gagal rekonsiliasi akun wa saat boot: %w", execErr)
		}
		reconciledAccounts = res.RowsAffected()
	}

	purgeOrphans := true
	if v := os.Getenv("WA_PURGE_ORPHANS_ON_BOOT"); v == "false" || v == "0" {
		purgeOrphans = false
	}

	if purgeOrphans {
		container, contErr := m.getContainer(ctx)
		if contErr != nil {
			slog.Warn("wa: tidak dapat membuka store whatsmeow untuk pembersihan perangkat boot", "err", contErr)
		} else {
			devices, devErr := container.GetAllDevices(ctx)
			if devErr != nil {
				slog.Warn("wa: gagal mengambil daftar perangkat saat boot", "err", devErr)
			} else {
				for _, dev := range devices {
					if dev != nil {
						if delErr := dev.Delete(ctx); delErr != nil {
							slog.Warn("wa: gagal menghapus perangkat yatim saat boot", "err", delErr)
						} else {
							purgedDevices++
						}
					}
				}
				slog.Info("wa: pembersihan perangkat yatim saat boot selesai", "count", purgedDevices)
			}
		}
	}

	return reconciledAccounts, purgedDevices, nil
}

// GetSession mengembalikan sesi aktif atau nil jika tidak ada.
func (m *Manager) GetSession(accountID int64) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[accountID]
}

// GetClient mengembalikan WAClient untuk accountID, atau nil.
func (m *Manager) GetClient(accountID int64) WAClient {
	sess := m.GetSession(accountID)
	if sess == nil {
		return nil
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	return sess.Client
}

// Connect memulai proses sambung untuk accountID.
// Reservasi slot dilakukan secara atomik di bawah m.mu sebelum pekerjaan lambat dimulai (mencegah race condition).
func (m *Manager) Connect(ctx context.Context, accountID, workerID int64) (<-chan string, func(phone string) (string, error), error) {
	// Ambil konfigurasi batas
	maxPerWorker := 10
	if m.settings != nil {
		val, ok, err := m.settings.GetInt(ctx, "max_accounts_per_worker")
		if err != nil || !ok || val <= 0 {
			return nil, nil, fmt.Errorf("pengaturan max_accounts_per_worker belum diatur oleh admin")
		}
		maxPerWorker = val
	}
	maxActive := 50
	if m.cfg != nil && m.cfg.MaxActiveSessions > 0 {
		maxActive = m.cfg.MaxActiveSessions
	}

	// Kumpulkan ID akun aktif dari database agar akun yang ada di DB tetap terhitung
	dbTotalActive := make(map[int64]bool)
	dbWorkerActive := make(map[int64]bool)
	if m.db != nil && m.db.Pool != nil {
		rows, err := m.db.Pool.Query(ctx,
			`SELECT id, worker_id FROM wa_accounts WHERE status IN ('terhubung', 'menunggu_scan')`,
		)
		if err == nil {
			for rows.Next() {
				var aid, wid int64
				if scanErr := rows.Scan(&aid, &wid); scanErr == nil {
					dbTotalActive[aid] = true
					if wid == workerID && aid != accountID {
						dbWorkerActive[aid] = true
					}
				}
			}
			rows.Close()
		}
	}

	// RESERVASI SLOT SECARA ATOMIK DI BAWAH MUTEX
	m.mu.Lock()
	if existing, ok := m.sessions[accountID]; ok && existing != nil {
		st := existing.GetStatus()
		if st == "terhubung" || st == "menunggu_scan" {
			qr := existing.qrChan
			pair := existing.pairingFn
			m.mu.Unlock()
			return qr, pair, nil
		}
	}

	// Gabungkan sesi memori dengan DB
	totalActiveIDs := make(map[int64]bool, len(dbTotalActive)+len(m.sessions))
	for id := range dbTotalActive {
		totalActiveIDs[id] = true
	}
	workerActiveIDs := make(map[int64]bool, len(dbWorkerActive)+len(m.sessions))
	for id := range dbWorkerActive {
		workerActiveIDs[id] = true
	}

	for id, s := range m.sessions {
		st := s.GetStatus()
		if st == "terhubung" || st == "menunggu_scan" {
			totalActiveIDs[id] = true
			if s.WorkerID == workerID && id != accountID {
				workerActiveIDs[id] = true
			}
		}
	}

	totalActive := len(totalActiveIDs)
	workerActive := len(workerActiveIDs)

	if totalActive >= maxActive {
		m.mu.Unlock()
		if m.db != nil && m.db.Pool != nil {
			_, _ = m.db.Pool.Exec(ctx, `UPDATE wa_accounts SET status = 'antre' WHERE id = $1`, accountID)
		}
		return nil, nil, fmt.Errorf("batas sesi aktif tercapai (%d dari %d), akun masuk antre", totalActive, maxActive)
	}

	if workerActive >= maxPerWorker {
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("batas akun per pekerja tercapai (%d dari %d diizinkan)", workerActive, maxPerWorker)
	}

	// Reservasi slot segera dengan membuat entri placeholder di m.sessions
	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &Session{
		AccountID: accountID,
		Status:    "menunggu_scan",
		WorkerID:  workerID,
		cancel:    cancel,
		qrChan:    make(chan string, 10),
	}
	m.sessions[accountID] = sess
	m.mu.Unlock()

	// Pekerjaan lambat dieksekusi di luar lock
	cleanupOnErr := func() {
		m.mu.Lock()
		delete(m.sessions, accountID)
		m.mu.Unlock()
		cancel()
	}

	// Update status di database ke menunggu_scan
	if m.db != nil && m.db.Pool != nil {
		_, err := m.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'menunggu_scan' WHERE id = $1`, accountID)
		if err != nil {
			cleanupOnErr()
			return nil, nil, fmt.Errorf("gagal update status menunggu_scan: %w", err)
		}
	}

	var deviceStore *store.Device
	container, err := m.getContainer(ctx)
	if err == nil && container != nil {
		deviceStore = container.NewDevice()
		sess.deviceStore = deviceStore
	}

	var client WAClient
	m.mu.RLock()
	factory := m.clientFactory
	m.mu.RUnlock()
	if factory != nil {
		client = factory(deviceStore)
	} else if deviceStore != nil {
		client = whatsmeow.NewClient(deviceStore, waLog.Noop)
	}

	if client == nil {
		cleanupOnErr()
		if err != nil {
			return nil, nil, fmt.Errorf("gagal menyiapkan store whatsmeow: %w", err)
		}
		return nil, nil, fmt.Errorf("gagal membuat client whatsmeow untuk akun %d", accountID)
	}

	sess.mu.Lock()
	sess.Client = client
	pairingFn := func(phone string) (string, error) {
		if client.IsLoggedIn() {
			return "", errors.New("sudah terhubung, tidak perlu pairing")
		}
		// Sesuai H4 butir 6: Pairing memakai context dengan timeout 30 detik
		pairCtx, pairCancel := context.WithTimeout(context.Background(), config.PairingTimeout)
		defer pairCancel()
		code, pairErr := client.PairPhone(pairCtx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
		if pairErr != nil {
			return "", fmt.Errorf("pairing gagal: %w", pairErr)
		}
		return code, nil
	}
	sess.pairingFn = pairingFn
	sess.mu.Unlock()

	// Daftarkan event handler whatsmeow dengan pendelegasian ke worker pool
	client.AddEventHandler(func(evt interface{}) {
		safe.RunWithRecovery(fmt.Sprintf("wa.eventHandler-%d", accountID), func() {
			m.handleEvent(accountID, workerID, evt)
		})
	})

	// Mulai koneksi QR / Websocket
	if !client.IsLoggedIn() {
		qrChan, _ := client.GetQRChannel(sessCtx)
		if qrChan != nil {
			safe.Go(fmt.Sprintf("wa.qrReader-%d", accountID), func() {
				for qr := range qrChan {
					if qr.Event == "code" {
						var b64 string
						if pngBytes, encErr := qrcode.Encode(qr.Code, qrcode.Medium, 256); encErr == nil {
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
			})
		}
	}

	err = client.Connect()
	if err != nil {
		cleanupOnErr()
		return nil, nil, fmt.Errorf("gagal mulai koneksi: %w", err)
	}

	return sess.qrChan, pairingFn, nil
}

// handleEvent meneruskan event dari whatsmeow ke antrean terbatas (non-blocking).
// Jika antrean penuh, event dicatat dan dibuang tanpa memblokir thread whatsmeow.
func (m *Manager) handleEvent(accountID, workerID int64, evt interface{}) {
	idx := int(accountID % numEventWorkers)
	if idx < 0 {
		idx = -idx
	}

	m.mu.RLock()
	closed := m.isClosed
	m.mu.RUnlock()
	if closed {
		return
	}

	select {
	case m.eventQueues[idx] <- eventJob{accountID: accountID, workerID: workerID, evt: evt}:
	default:
		slog.Warn("wa: event queue full, event dibuang non-blocking",
			"account_id", accountID,
			"event_type", fmt.Sprintf("%T", evt),
		)
	}
}

// processEvent memproses event whatsmeow di worker pool menggunakan context.WithoutCancel dengan batas waktu 5 detik.
func (m *Manager) processEvent(ctx context.Context, accountID, workerID int64, evt interface{}) {
	sess := m.GetSession(accountID)

	switch v := evt.(type) {
	case *events.Connected:
		slog.Info("wa: terhubung", "account_id", accountID)
		if sess != nil {
			sess.SetStatus("terhubung")
			sess.cancelDisconnectTimer()
		}
		if m.db != nil && m.db.Pool != nil {
			_, _ = m.db.Pool.Exec(ctx,
				`UPDATE wa_accounts SET status = 'terhubung', connected_at = now() WHERE id = $1`,
				accountID,
			)
		}

	case *events.Disconnected:
		slog.Info("wa: terputus", "account_id", accountID)
		if sess != nil {
			sess.SetStatus("terputus")
			sess.startDisconnectTimer(config.DisconnectTimeout, func() {
				m.handleDisconnectTimeout(accountID, workerID)
			})
		}
		if m.db != nil && m.db.Pool != nil {
			_, _ = m.db.Pool.Exec(ctx,
				`UPDATE wa_accounts SET status = 'terputus' WHERE id = $1`,
				accountID,
			)
		}
		// Sesuai H4 butir 4: jangan menghentikan engine dan jangan melepas nomor.
		// Whatsmeow menyambung ulang sendiri via autoReconnect.

	case *events.LoggedOut:
		slog.Info("wa: logged out", "account_id", accountID, "on_connect", v.OnConnect, "reason", v.Reason)
		if sess != nil {
			sess.SetStatus("berhenti")
			sess.cancelDisconnectTimer()
			sess.cancel()
		}
		m.stopEngineForAccount(accountID)
		m.removeSession(accountID)
		if sess != nil && sess.deviceStore != nil {
			_ = sess.deviceStore.Delete(ctx)
		}
		if m.db != nil && m.db.Pool != nil {
			_, _ = m.db.Pool.Exec(ctx,
				`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = 'logged_out' WHERE id = $1`,
				accountID,
			)
		}

	case *events.TemporaryBan:
		reason := fmt.Sprintf("banned sementara: %s", v.Code.String())
		slog.Warn("wa: akun terkena temporary ban", "account_id", accountID, "code", v.Code, "expire", v.Expire)
		if sess != nil {
			sess.SetStatus("berhenti")
			sess.cancelDisconnectTimer()
			sess.cancel()
			if sess.Client != nil {
				sess.Client.Disconnect()
			}
		}
		m.stopEngineForAccount(accountID)
		m.removeSession(accountID)
		if sess != nil && sess.deviceStore != nil {
			_ = sess.deviceStore.Delete(ctx)
		}
		if m.db != nil && m.db.Pool != nil {
			_, _ = m.db.Pool.Exec(ctx,
				`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = $1 WHERE id = $2`,
				reason, accountID,
			)
			m.insertNotification(ctx, workerID, "Akun WhatsApp Dibatasi", "Akun WhatsApp Anda dibatasi sementara oleh WhatsApp.")
		}

	case *events.ConnectFailure:
		if v.Reason == events.ConnectFailureClientOutdated {
			m.clientOutdated.Store(true)
			slog.Error("wa: client is outdated, library update required", "account_id", accountID, "reason", v.Reason)
			return
		}

		if v.Reason == events.ConnectFailureTempBanned {
			reason := "akun dibatasi (temporarily banned)"
			slog.Warn("wa: connect failure temp banned", "account_id", accountID)
			if sess != nil {
				sess.SetStatus("berhenti")
				sess.cancelDisconnectTimer()
				sess.cancel()
				if sess.Client != nil {
					sess.Client.Disconnect()
				}
			}
			m.stopEngineForAccount(accountID)
			m.removeSession(accountID)
			if sess != nil && sess.deviceStore != nil {
				_ = sess.deviceStore.Delete(ctx)
			}
			if m.db != nil && m.db.Pool != nil {
				_, _ = m.db.Pool.Exec(ctx,
					`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = $1 WHERE id = $2`,
					reason, accountID,
				)
				m.insertNotification(ctx, workerID, "Akun WhatsApp Dibatasi", "Akun WhatsApp Anda dibatasi sementara oleh WhatsApp.")
			}
			return
		}

		if v.Reason.IsLoggedOut() || v.Reason == events.ConnectFailureMainDeviceGone || v.Reason == events.ConnectFailureUnknownLogout {
			reason := fmt.Sprintf("akun dibatasi (%s)", v.Reason.String())
			slog.Warn("wa: connect failure account restricted", "account_id", accountID, "reason", v.Reason)
			if sess != nil {
				sess.SetStatus("berhenti")
				sess.cancelDisconnectTimer()
				sess.cancel()
				if sess.Client != nil {
					sess.Client.Disconnect()
				}
			}
			m.stopEngineForAccount(accountID)
			m.removeSession(accountID)
			if sess != nil && sess.deviceStore != nil {
				_ = sess.deviceStore.Delete(ctx)
			}
			if m.db != nil && m.db.Pool != nil {
				_, _ = m.db.Pool.Exec(ctx,
					`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = $1 WHERE id = $2`,
					reason, accountID,
				)
				m.insertNotification(ctx, workerID, "Akun WhatsApp Dibatasi", "Akun WhatsApp Anda dibatasi atau sesi utama telah keluar.")
			}
			return
		}

		slog.Warn("wa: connect failure", "account_id", accountID, "reason", v.Reason, "message", v.Message)

	case *events.ClientOutdated:
		m.clientOutdated.Store(true)
		slog.Error("wa: client is outdated, library update required", "account_id", accountID)

	case *events.StreamReplaced:
		reason := "stream_replaced"
		slog.Warn("wa: stream replaced, akun dipakai di tempat lain", "account_id", accountID)
		if sess != nil {
			sess.SetStatus("berhenti")
			sess.cancelDisconnectTimer()
			sess.cancel()
			if sess.Client != nil {
				sess.Client.Disconnect()
			}
		}
		m.stopEngineForAccount(accountID)
		m.removeSession(accountID)
		if sess != nil && sess.deviceStore != nil {
			_ = sess.deviceStore.Delete(ctx)
		}
		if m.db != nil && m.db.Pool != nil {
			_, _ = m.db.Pool.Exec(ctx,
				`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = $1 WHERE id = $2`,
				reason, accountID,
			)
			m.insertNotification(ctx, workerID, "Sesi WhatsApp Diputus", "Akun WhatsApp Anda dipakai di tempat lain.")
		}

	case *events.PairSuccess:
		jidUser := v.ID.User
		slog.Info("wa: pair success", "account_id", accountID, "platform", v.Platform)
		encKey := m.getEncryptionKey()
		hash := HashWAJID(jidUser, encKey)

		if m.db != nil && m.db.Pool != nil && hash != "" {
			_, err := m.db.Pool.Exec(ctx,
				`UPDATE wa_accounts SET wa_jid_hash = $1 WHERE id = $2`,
				hash, accountID,
			)
			if err != nil {
				if strings.Contains(err.Error(), "idx_wa_accounts_wa_jid_hash_active") || strings.Contains(err.Error(), "23505") {
					slog.Warn("wa: penautan ditolak, nomor sudah aktif di akun lain", "account_id", accountID)
					if sess != nil {
						sess.SetStatus("berhenti")
						sess.cancelDisconnectTimer()
						sess.cancel()
						if sess.Client != nil {
							_ = sess.Client.Logout(ctx)
							sess.Client.Disconnect()
						}
					}
					m.stopEngineForAccount(accountID)
					m.removeSession(accountID)
					if sess != nil && sess.deviceStore != nil {
						_ = sess.deviceStore.Delete(ctx)
					}
					_, _ = m.db.Pool.Exec(ctx,
						`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = 'nomor_sudah_terhubung' WHERE id = $1`,
						accountID,
					)
					m.insertNotification(ctx, workerID, "Penautan WhatsApp Gagal", "Nomor ini sudah terhubung di akun lain.")
					return
				}
				slog.Error("wa: gagal menyimpan wa_jid_hash", "account_id", accountID, "err", err)
			}
		}

	default:
		// Semua event pesan masuk atau event lain diabaikan sesuai aturan privasi
	}
}

func (m *Manager) handleDisconnectTimeout(accountID, workerID int64) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 10*time.Second)
	defer cancel()

	slog.Warn("wa: sesi putus terlalu lama (timeout tercapai), menghentikan akun", "account_id", accountID)
	sess := m.GetSession(accountID)
	if sess != nil {
		sess.SetStatus("berhenti")
		sess.cancel()
		if sess.Client != nil {
			sess.Client.Disconnect()
		}
	}
	m.stopEngineForAccount(accountID)
	m.removeSession(accountID)

	if m.db != nil && m.db.Pool != nil {
		_, _ = m.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = 'sesi putus terlalu lama' WHERE id = $1`,
			accountID,
		)
	}
}

func (m *Manager) stopEngineForAccount(accountID int64) {
	m.mu.RLock()
	stopper := m.engineStopper
	m.mu.RUnlock()
	if stopper != nil {
		waitCtx, cancel := context.WithTimeout(context.Background(), config.DrainAccountTimeout)
		defer cancel()
		_ = stopper(waitCtx, accountID)
	}
}

func (m *Manager) insertNotification(ctx context.Context, userID int64, title, message string) {
	if m.db == nil || m.db.Pool == nil || userID <= 0 {
		return
	}
	_, _ = m.db.Pool.Exec(ctx,
		`INSERT INTO notifications (user_id, title, message, kind) VALUES ($1, $2, $3, 'system')`,
		userID, title, message,
	)
}

func (m *Manager) getEncryptionKey() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg != nil && len(m.cfg.EncryptionKey) == 32 {
		return m.cfg.EncryptionKey
	}
	keyB64 := os.Getenv("ENCRYPTION_KEY")
	if keyB64 != "" {
		if raw, err := base64.StdEncoding.DecodeString(keyB64); err == nil && len(raw) == 32 {
			return raw
		}
	}
	return nil
}

// Disconnect memutus koneksi (bukan logout). Status menjadi terputus.
func (m *Manager) Disconnect(ctx context.Context, accountID, callerWorkerID int64) error {
	if err := m.checkOwnership(ctx, accountID, callerWorkerID); err != nil {
		return err
	}

	m.mu.Lock()
	sess, ok := m.sessions[accountID]
	m.mu.Unlock()
	if ok && sess != nil {
		sess.SetStatus("terputus")
		sess.cancelDisconnectTimer()
		sess.cancel()
		if sess.Client != nil {
			sess.Client.Disconnect()
		}
	}

	m.removeSession(accountID)
	if m.db != nil && m.db.Pool != nil {
		_, _ = m.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'terputus' WHERE id = $1`, accountID)
	}
	return nil
}

// DisconnectAccount memutus koneksi sebuah akun tanpa memeriksa callerWorkerID (dipakai admin).
func (m *Manager) DisconnectAccount(ctx context.Context, accountID int64) error {
	m.mu.Lock()
	sess, ok := m.sessions[accountID]
	m.mu.Unlock()
	if ok && sess != nil {
		sess.SetStatus("terputus")
		sess.cancelDisconnectTimer()
		sess.cancel()
		if sess.Client != nil {
			sess.Client.Disconnect()
		}
	}

	m.removeSession(accountID)
	if m.db != nil && m.db.Pool != nil {
		_, _ = m.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'terputus' WHERE id = $1`, accountID)
	}
	return nil
}

// Stop logout penuh (hapus sesi whatsmeow), status menjadi berhenti.
// Menunggu goroutine engine selesai (dengan batas waktu) sebelum memfinalisasi akun.
func (m *Manager) Stop(ctx context.Context, accountID, callerWorkerID int64, reason string) error {
	if err := m.checkOwnership(ctx, accountID, callerWorkerID); err != nil {
		return err
	}

	// 1. Tunggu goroutine engine akun ini selesai
	m.stopEngineForAccount(accountID)

	m.mu.Lock()
	sess, ok := m.sessions[accountID]
	m.mu.Unlock()

	if ok && sess != nil {
		sess.SetStatus("berhenti")
		sess.cancelDisconnectTimer()
		sess.cancel()
		if sess.Client != nil {
			if err := sess.Client.Logout(ctx); err != nil {
				slog.Warn("wa: logout gagal (dilanjutkan)", "account_id", accountID, "err", err)
			}
		}
		if sess.deviceStore != nil {
			_ = sess.deviceStore.Delete(ctx)
		}
	}

	m.removeSession(accountID)
	if m.db != nil && m.db.Pool != nil {
		_, _ = m.db.Pool.Exec(ctx,
			`UPDATE wa_accounts SET status = 'berhenti', stopped_at = now(), stop_reason = $1 WHERE id = $2`,
			reason, accountID)
	}
	return nil
}

// Delete menghapus sesi dan catatan akun dari database.
// Menunggu goroutine engine akun itu selesai sebelum menghapus baris wa_accounts.
func (m *Manager) Delete(ctx context.Context, accountID, callerWorkerID int64) error {
	if err := m.checkOwnership(ctx, accountID, callerWorkerID); err != nil {
		return err
	}

	// 1. Tunggu goroutine engine akun ini selesai
	m.stopEngineForAccount(accountID)

	m.mu.Lock()
	sess, ok := m.sessions[accountID]
	m.mu.Unlock()

	if ok && sess != nil {
		sess.SetStatus("berhenti")
		sess.cancelDisconnectTimer()
		sess.cancel()
		if sess.Client != nil {
			sess.Client.Disconnect()
		}
		if sess.deviceStore != nil {
			_ = sess.deviceStore.Delete(ctx)
		}
	}
	m.removeSession(accountID)

	if m.db != nil && m.db.Pool != nil {
		// Sisa nomor yang masih dikirim diproses sesuai Keputusan A1 (gagal hasil_tidak_pasti)
		_, _ = m.db.Pool.Exec(ctx,
			`UPDATE campaign_numbers
			 SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti', claimed_account_id = NULL
			 WHERE status = 'dikirim' AND claimed_account_id = $1`,
			accountID,
		)
		// Lepaskan relasi foreign key pada nomor riwayat agar tidak melanggar fk_campaign_numbers_account
		_, _ = m.db.Pool.Exec(ctx,
			`UPDATE campaign_numbers SET claimed_account_id = NULL WHERE claimed_account_id = $1`,
			accountID,
		)
		_, err := m.db.Pool.Exec(ctx, `DELETE FROM wa_accounts WHERE id = $1`, accountID)
		return err
	}
	return nil
}

// IsOnWhatsApp memeriksa apakah nomor terdaftar di WhatsApp via client.
func (m *Manager) IsOnWhatsApp(ctx context.Context, accountID int64, phone string) (bool, error) {
	sess := m.GetSession(accountID)
	if sess == nil {
		return false, ErrSessionNotActive
	}
	sess.mu.RLock()
	cli := sess.Client
	sess.mu.RUnlock()
	if cli == nil {
		return false, ErrSessionNotActive
	}

	results, err := cli.IsOnWhatsApp(ctx, []string{phone})
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

// DisconnectAll memutus seluruh sesi WhatsApp aktif tanpa logout (dipakai saat shutdown).
func (m *Manager) DisconnectAll() {
	m.mu.Lock()
	m.isClosed = true
	sessionsCopy := make([]*Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		if sess != nil {
			sessionsCopy = append(sessionsCopy, sess)
		}
	}
	m.sessions = make(map[int64]*Session)

	// Tutup antrean worker
	for _, q := range m.eventQueues {
		close(q)
	}
	m.mu.Unlock()

	m.eventWG.Wait()

	for _, sess := range sessionsCopy {
		sess.SetStatus("terputus")
		sess.cancelDisconnectTimer()
		sess.cancel()
		sess.mu.RLock()
		cli := sess.Client
		sess.mu.RUnlock()
		if cli != nil {
			cli.Disconnect()
		}
	}

	m.mu.Lock()
	if m.container != nil {
		_ = m.container.Close()
		m.container = nil
	}
	m.mu.Unlock()
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

	rawDB, err := sql.Open("pgx", m.cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka koneksi database whatsmeow: %w", err)
	}
	rawDB.SetMaxOpenConns(config.DefaultWhatsmeowMaxOpenConns)
	rawDB.SetMaxIdleConns(config.DefaultWhatsmeowMaxIdleConns)
	rawDB.SetConnMaxLifetime(config.DefaultWhatsmeowConnMaxLifetime)

	container := sqlstore.NewWithDB(rawDB, "postgres", waLog.Noop)
	if err := container.Upgrade(ctx); err != nil {
		_ = container.Close()
		return nil, fmt.Errorf("gagal upgrade database whatsmeow: %w", err)
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
	if m.db == nil || m.db.Pool == nil {
		return nil
	}
	var ownerID int64
	err := m.db.Pool.QueryRow(ctx,
		`SELECT worker_id FROM wa_accounts WHERE id = $1`, accountID,
	).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return errors.New("akun tidak ditemukan")
		}
		return fmt.Errorf("gagal memeriksa kepemilikan: %w", err)
	}
	if ownerID != callerWorkerID {
		return errors.New("akses ditolak: bukan pemilik akun")
	}
	return nil
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
	if r.db == nil || r.db.Pool == nil {
		return 0, false, nil
	}
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
	if r.db == nil || r.db.Pool == nil {
		return false, nil
	}
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
	if r.db == nil || r.db.Pool == nil {
		return "", false, nil
	}
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
		return raw, true, nil
	}
	return val, true, nil
}

// GetStatusForAccount mengambil status wa_accounts dari memori atau DB.
func (m *Manager) GetStatusForAccount(ctx context.Context, accountID int64) (string, error) {
	sess := m.GetSession(accountID)
	if sess != nil {
		return sess.GetStatus(), nil
	}
	if m.db == nil || m.db.Pool == nil {
		return "", nil
	}
	var status string
	err := m.db.Pool.QueryRow(ctx,
		`SELECT status FROM wa_accounts WHERE id = $1`, accountID,
	).Scan(&status)
	if err != nil {
		return "", err
	}
	return status, nil
}

// GetQRCode mengambil QR terakhir untuk accountID sebagai string kode.
func (m *Manager) GetQRCode(accountID int64) (string, bool) {
	sess := m.GetSession(accountID)
	if sess == nil {
		return "", false
	}

	sess.mu.RLock()
	code := sess.lastQR
	sess.mu.RUnlock()
	if code != "" {
		return code, true
	}

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

// GetQRImageB64 mengambil base64 PNG dari QR code akun.
func (m *Manager) GetQRImageB64(accountID int64) (string, bool) {
	sess := m.GetSession(accountID)
	if sess == nil {
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
	sess := m.GetSession(accountID)
	if sess == nil {
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
