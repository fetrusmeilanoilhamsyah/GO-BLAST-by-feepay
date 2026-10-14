package web

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"goblast/internal/auth"
	"goblast/internal/campaign"
	"goblast/internal/db"
	"goblast/internal/i18n"
	"goblast/internal/safe"
	"goblast/internal/wa"
	"goblast/internal/withdraw"
)

// WorkController antarmuka kontrol kerja pengiriman pesan.
type WorkController interface {
	StartAccount(accountID, workerID int64)
	StopAccount(accountID int64)
	IsRunning(accountID int64) bool
	RunningCount() int
}

type globalStatsCache struct {
	mu           sync.Mutex
	lastFetch    time.Time
	claimable    int
	runningCount int
}

type systemLockCache struct {
	mu              sync.RWMutex
	lastFetch       time.Time
	lockWithdrawals bool
	lockLinking     bool
	lockEngine      bool
	maintenanceMode bool
	lockMessage     string
}

// SystemLocksViewData menampung data status kunci sistem dan pesan untuk pekerja.
type SystemLocksViewData struct {
	LockWithdrawals bool     `json:"lock_withdrawals"`
	LockLinking     bool     `json:"lock_linking"`
	LockEngine      bool     `json:"lock_engine"`
	LockMessage     string   `json:"lock_message"`
	AnyLocked       bool     `json:"any_locked"`
	LockedFeatures  []string `json:"locked_features"`
}

type Server struct {
	DB                 *db.DB
	Tmpl               map[string]*template.Template
	Env                string
	WaManager          *wa.Manager
	EncryptionKey      []byte
	EncryptionKeyPrev  []byte
	WorkCtrl           WorkController
	CSWhatsAppURL      string
	CSTelegramURL      string
	ChannelTelegramURL string
	statsCache         globalStatsCache
	lockCache          systemLockCache
	draining           atomic.Bool
}

// SetDraining menyetel status penutupan server (draining).
func (s *Server) SetDraining(val bool) {
	s.draining.Store(val)
}

// IsDraining memeriksa apakah server sedang dalam status draining.
func (s *Server) IsDraining() bool {
	return s.draining.Load()
}

// DecryptDestination mendekripsi tujuan penarikan dengan kunci aktif dan kunci rotasi sebelumnya.
func (s *Server) DecryptDestination(ciphertext string) (string, error) {
	return withdraw.DecryptDestinationWithPrev(ciphertext, s.EncryptionKey, s.EncryptionKeyPrev)
}

func (s *Server) getGlobalStats(ctx context.Context) (int, int, error) {
	s.statsCache.mu.Lock()
	defer s.statsCache.mu.Unlock()

	if !s.statsCache.lastFetch.IsZero() && time.Since(s.statsCache.lastFetch) < 2*time.Second {
		return s.statsCache.claimable, s.statsCache.runningCount, nil
	}

	count, err := campaign.ClaimableCount(ctx, s.DB.Pool)
	if err != nil {
		if !s.statsCache.lastFetch.IsZero() {
			return s.statsCache.claimable, s.statsCache.runningCount, nil
		}
		return 0, 0, err
	}

	running := 0
	if s.WorkCtrl != nil {
		running = s.WorkCtrl.RunningCount()
	}

	s.statsCache.claimable = count
	s.statsCache.runningCount = running
	s.statsCache.lastFetch = time.Now()

	return count, running, nil
}

// InvalidateSystemLockCache mengosongkan cache kunci agar pembacaan berikutnya langsung ke database.
func (s *Server) InvalidateSystemLockCache() {
	s.lockCache.mu.Lock()
	s.lockCache.lastFetch = time.Time{}
	s.lockCache.mu.Unlock()
}

// GetSystemLocks mengambil status semua kunci granular sistem dan pesan admin (cache 2 detik).
func (s *Server) GetSystemLocks(ctx context.Context) SystemLocksViewData {
	s.lockCache.mu.RLock()
	if !s.lockCache.lastFetch.IsZero() && time.Since(s.lockCache.lastFetch) < 2*time.Second {
		lw := s.lockCache.lockWithdrawals
		ll := s.lockCache.lockLinking
		le := s.lockCache.lockEngine
		msg := s.lockCache.lockMessage
		s.lockCache.mu.RUnlock()

		var features []string
		if lw {
			features = append(features, "Penarikan Saldo & Konversi USDT")
		}
		if ll {
			features = append(features, "Penautan Akun WhatsApp Baru")
		}
		if le {
			features = append(features, "Mesin Pengiriman Pesan (Broadcasting)")
		}
		return SystemLocksViewData{
			LockWithdrawals: lw,
			LockLinking:     ll,
			LockEngine:      le,
			LockMessage:     msg,
			AnyLocked:       lw || ll || le,
			LockedFeatures:  features,
		}
	}
	s.lockCache.mu.RUnlock()

	s.lockCache.mu.Lock()
	defer s.lockCache.mu.Unlock()

	if !s.lockCache.lastFetch.IsZero() && time.Since(s.lockCache.lastFetch) < 2*time.Second {
		var features []string
		if s.lockCache.lockWithdrawals {
			features = append(features, "Penarikan Saldo & Konversi USDT")
		}
		if s.lockCache.lockLinking {
			features = append(features, "Penautan Akun WhatsApp Baru")
		}
		if s.lockCache.lockEngine {
			features = append(features, "Mesin Pengiriman Pesan (Broadcasting)")
		}
		return SystemLocksViewData{
			LockWithdrawals: s.lockCache.lockWithdrawals,
			LockLinking:     s.lockCache.lockLinking,
			LockEngine:      s.lockCache.lockEngine,
			LockMessage:     s.lockCache.lockMessage,
			AnyLocked:       s.lockCache.lockWithdrawals || s.lockCache.lockLinking || s.lockCache.lockEngine,
			LockedFeatures:  features,
		}
	}

	if s.DB != nil && s.DB.Pool != nil {
		rows, err := s.DB.Pool.Query(ctx, "SELECT key, value::text FROM settings WHERE key IN ('lock_withdrawals', 'lock_linking', 'lock_engine', 'maintenance_mode', 'lock_message')")
		if err == nil {
			defer rows.Close()
			var lw, ll, le, mm bool
			var msg string
			for rows.Next() {
				var k, v string
				if err := rows.Scan(&k, &v); err == nil {
					switch k {
					case "lock_withdrawals":
						_ = json.Unmarshal([]byte(v), &lw)
					case "lock_linking":
						_ = json.Unmarshal([]byte(v), &ll)
					case "lock_engine":
						_ = json.Unmarshal([]byte(v), &le)
					case "maintenance_mode":
						_ = json.Unmarshal([]byte(v), &mm)
					case "lock_message":
						if err := json.Unmarshal([]byte(v), &msg); err != nil {
							msg = strings.Trim(v, `"`)
						}
					}
				}
			}
			s.lockCache.lockWithdrawals = lw
			s.lockCache.lockLinking = ll
			s.lockCache.lockEngine = le
			s.lockCache.maintenanceMode = mm
			s.lockCache.lockMessage = msg
			s.lockCache.lastFetch = time.Now()
		}
	}

	var features []string
	if s.lockCache.lockWithdrawals {
		features = append(features, "Penarikan Saldo & Konversi USDT")
	}
	if s.lockCache.lockLinking {
		features = append(features, "Penautan Akun WhatsApp Baru")
	}
	if s.lockCache.lockEngine {
		features = append(features, "Mesin Pengiriman Pesan (Broadcasting)")
	}

	return SystemLocksViewData{
		LockWithdrawals: s.lockCache.lockWithdrawals,
		LockLinking:     s.lockCache.lockLinking,
		LockEngine:      s.lockCache.lockEngine,
		LockMessage:     s.lockCache.lockMessage,
		AnyLocked:       s.lockCache.lockWithdrawals || s.lockCache.lockLinking || s.lockCache.lockEngine,
		LockedFeatures:  features,
	}
}

func (s *Server) IsWithdrawalsLocked(ctx context.Context) bool {
	return s.GetSystemLocks(ctx).LockWithdrawals
}

func (s *Server) IsLinkingLocked(ctx context.Context) bool {
	return s.GetSystemLocks(ctx).LockLinking
}

func (s *Server) IsEngineLocked(ctx context.Context) bool {
	locks := s.GetSystemLocks(ctx)
	return locks.LockEngine || s.IsMaintenanceMode(ctx)
}

func NewServer(database *db.DB, env string, waMgr *wa.Manager, workCtrl WorkController) (*Server, error) {
	base := filepath.Join("web", "templates", "base.html")
	workerNav := filepath.Join("web", "templates", "worker_nav.html")
	adminNav := filepath.Join("web", "templates", "admin_nav.html")

	templates := make(map[string]*template.Template)

	workerPages := map[string]bool{
		"worker_home.html":        true,
		"worker_accounts.html":    true,
		"worker_account_new.html": true,
		"worker_withdrawals.html": true,
		"worker_referral.html":    true,
		"worker_history.html":     true,
	}

	adminPages := map[string]bool{
		"admin_dashboard.html":                true,
		"admin_settings.html":                 true,
		"admin_wallet.html":                   true,
		"admin_withdrawals.html":              true,
		"admin_workers.html":                  true,
		"admin_worker_detail.html":            true,
		"admin_maintenance.html":              true,
		"admin_campaigns.html":                true,
		"admin_campaign_form.html":            true,
		"admin_campaign_detail.html":          true,
		"admin_campaign_numbers.html":         true,
		"admin_campaign_numbers_preview.html": true,
		"admin_tickets.html":                  true,
		"admin_broadcasts.html":               true,
	}

	pages := []string{
		"index.html", "login.html", "register.html", "maintenance.html",
		"admin_dashboard.html", "admin_settings.html", "admin_wallet.html", "admin_withdrawals.html", "admin_workers.html",
		"admin_worker_detail.html", "admin_maintenance.html",
		"admin_campaigns.html", "admin_campaign_form.html", "admin_campaign_detail.html",
		"admin_campaign_numbers.html", "admin_campaign_numbers_preview.html",
		"admin_tickets.html", "admin_broadcasts.html",
		"worker_accounts.html", "worker_account_new.html",
		"worker_home.html", "worker_withdrawals.html", "worker_referral.html",
		"worker_history.html",
	}
	for _, page := range pages {
		var tmpl *template.Template
		var err error
		pagePath := filepath.Join("web", "templates", page)
		if workerPages[page] {
			tmpl, err = template.ParseFiles(base, workerNav, pagePath)
		} else if adminPages[page] {
			tmpl, err = template.ParseFiles(base, adminNav, pagePath)
		} else {
			tmpl, err = template.ParseFiles(base, pagePath)
		}
		if err != nil {
			return nil, err
		}
		templates[page] = tmpl
	}

	// Preview template doesn't use base.html
	previewTmpl, err := template.ParseFiles(filepath.Join("web", "templates", "admin_campaign_preview.html"))
	if err == nil {
		templates["admin_campaign_preview.html"] = previewTmpl
	}

	return &Server{
		DB:                 database,
		Tmpl:               templates,
		Env:                env,
		WaManager:          waMgr,
		WorkCtrl:           workCtrl,
		CSWhatsAppURL:      "https://wa.me/",
		CSTelegramURL:      "https://t.me/feepay_cs",
		ChannelTelegramURL: "https://t.me/goblast_channel",
	}, nil
}

// neuteredFileSystem membungkus http.FileSystem untuk menolak directory listing (mengembalikan 404).
type neuteredFileSystem struct {
	fs http.FileSystem
}

func (nfs neuteredFileSystem) Open(name string) (http.File, error) {
	f, err := nfs.fs.Open(name)
	if err != nil {
		return nil, err
	}
	s, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if s.IsDir() {
		index := filepath.Join(name, "index.html")
		if _, err := nfs.fs.Open(index); err != nil {
			_ = f.Close()
			return nil, os.ErrNotExist
		}
	}
	return f, nil
}

// isUploadRoute memeriksa apakah rute merupakan endpoint unggahan (batas 6 MB vs 1 MB rute umum).
func isUploadRoute(pattern string) bool {
	parts := strings.Fields(pattern)
	path := pattern
	if len(parts) == 2 {
		path = parts[1]
	}
	if path == "/admin/campaigns/new" || path == "/admin/campaigns/preview" {
		return true
	}
	if strings.HasPrefix(path, "/admin/campaigns/") {
		if strings.HasSuffix(path, "/edit") || strings.HasSuffix(path, "/numbers/preview") {
			return true
		}
	}
	return false
}

// MaxBytesMiddleware membatasi ukuran request body (1 MB rute umum, 6 MB rute upload).
func (s *Server) MaxBytesMiddleware(next http.Handler, isUpload bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(1 * 1024 * 1024) // 1 MB bawaan
		if isUpload {
			limit = 6 * 1024 * 1024 // 6 MB upload
		}

		if r.ContentLength > limit {
			http.Error(w, "Ukuran request melebihi batas", http.StatusRequestEntityTooLarge)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// RecoverMiddleware menangkap panic pada handler HTTP, mencatat via slog, dan membalas 500 generik.
func (s *Server) RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				safe.IncPanicCount()
				slog.Error("web: panic pada HTTP handler",
					"path", r.URL.Path,
					"method", r.Method,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "Terjadi kesalahan internal server", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// WrapGlobalMiddleware membungkus handler utama dengan RecoverMiddleware dan MaxBytes dinamis.
func (s *Server) WrapGlobalMiddleware(next http.Handler) http.Handler {
	return s.RecoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isUpload := isUploadRoute(r.URL.Path)
		s.MaxBytesMiddleware(next, isUpload).ServeHTTP(w, r)
	}))
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	handle := func(pattern string, handler http.Handler) {
		isUpload := isUploadRoute(pattern)
		mux.Handle(pattern, s.RecoverMiddleware(s.MaxBytesMiddleware(handler, isUpload)))
	}
	handleFunc := func(pattern string, fn http.HandlerFunc) {
		isUpload := isUploadRoute(pattern)
		mux.Handle(pattern, s.RecoverMiddleware(s.MaxBytesMiddleware(fn, isUpload)))
	}

	// Serve static files dengan neutered file system (mencegah directory listing)
	staticHandler := http.FileServer(neuteredFileSystem{fs: http.Dir(filepath.Join("web", "static"))})
	handle("/static/", http.StripPrefix("/static/", staticHandler))

	handleFunc("/healthz", s.handleHealthz)
	handleFunc("GET /healthz", s.handleHealthz)
	handleFunc("/readyz", s.handleReadyz)
	handleFunc("GET /readyz", s.handleReadyz)

	handle("/", s.CSRFMiddleware(http.HandlerFunc(s.handleIndex)))
	handle("POST /theme", s.CSRFMiddleware(http.HandlerFunc(s.HandleThemePOST)))
	handle("POST /language", s.CSRFMiddleware(http.HandlerFunc(s.HandleLanguagePOST)))
	handle("/login", s.CSRFMiddleware(RateLimitLogin(s.handleLogin)))
	handle("/register", s.CSRFMiddleware(RateLimitRegister(s.handleRegister)))

	handle("/logout", s.CSRFMiddleware(s.RequireRole(s.handleLogout, "admin", "worker")))
	handle("GET /maintenance", s.CSRFMiddleware(http.HandlerFunc(s.HandleMaintenancePublicGET)))

	// Tahap 12: Admin Dashboard, Settings, Wallet, Workers
	handle("GET /admin", s.CSRFMiddleware(s.RequireRole(s.HandleAdminHome, "admin")))
	handle("GET /admin/api/stats", s.RequireRole(s.HandleAdminAPIStats, "admin"))
	handle("GET /admin/settings", s.CSRFMiddleware(s.RequireRole(s.HandleAdminSettingsGET, "admin")))
	handle("POST /admin/settings", s.CSRFMiddleware(s.RequireRole(s.HandleAdminSettingsPOST, "admin")))
	handle("GET /admin/wallet", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWalletGET, "admin")))
	handle("POST /admin/wallet", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWalletTopupPOST, "admin")))
	handle("POST /admin/wallet/topup", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWalletTopupPOST, "admin")))
	handle("GET /admin/withdrawals", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWithdrawalsGET, "admin")))
	handle("POST /admin/withdrawals/{id}/view-destination", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWithdrawalViewDestinationPOST, "admin")))
	handle("POST /admin/withdrawals/{id}/approve", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWithdrawalApprovePOST, "admin")))
	handle("POST /admin/withdrawals/{id}/reject", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWithdrawalRejectPOST, "admin")))
	handle("GET /admin/workers", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWorkers, "admin")))
	handle("GET /admin/workers/{id}", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWorkerDetailGET, "admin")))
	handle("POST /admin/workers/{id}/reset-password", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWorkerResetPasswordPOST, "admin")))
	handle("POST /admin/workers/{id}/freeze", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWorkerFreezePOST, "admin")))
	handle("POST /admin/workers/{id}/unfreeze", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWorkerUnfreezePOST, "admin")))
	handle("POST /admin/workers/{id}/disable", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWorkerDisablePOST, "admin")))
	handle("POST /admin/workers/{id}/enable", s.CSRFMiddleware(s.RequireRole(s.HandleAdminWorkerEnablePOST, "admin")))
	handle("GET /admin/tickets", s.CSRFMiddleware(s.RequireRole(s.HandleAdminTicketsGET, "admin")))
	handle("POST /admin/tickets/{id}/resolve", s.CSRFMiddleware(s.RequireRole(s.HandleAdminTicketResolvePOST, "admin")))
	handle("GET /admin/broadcasts", s.CSRFMiddleware(s.RequireRole(s.HandleAdminBroadcastsGET, "admin")))
	handle("POST /admin/broadcasts/new", s.CSRFMiddleware(s.RequireRole(s.HandleAdminBroadcastNewPOST, "admin")))
	handle("POST /admin/broadcasts/{id}/toggle", s.CSRFMiddleware(s.RequireRole(s.HandleAdminBroadcastTogglePOST, "admin")))
	handle("POST /admin/broadcasts/{id}/delete", s.CSRFMiddleware(s.RequireRole(s.HandleAdminBroadcastDeletePOST, "admin")))
	handle("GET /admin/maintenance", s.CSRFMiddleware(s.RequireRole(s.HandleAdminMaintenanceGET, "admin")))
	handle("POST /admin/maintenance/toggle", s.CSRFMiddleware(s.RequireRole(s.HandleAdminMaintenanceTogglePOST, "admin")))
	handle("POST /admin/maintenance/locks", s.CSRFMiddleware(s.RequireRole(s.HandleAdminMaintenanceLocksPOST, "admin")))
	handle("POST /admin/maintenance/lock/toggle", s.CSRFMiddleware(s.RequireRole(s.HandleAdminMaintenanceLockTogglePOST, "admin")))
	handle("/admin/", s.CSRFMiddleware(s.RequireRole(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, "admin")))

	// Tahap 8
	handle("GET /admin/campaigns", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaigns, "admin")))
	handle("GET /admin/campaigns/new", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNewGET, "admin")))
	handle("POST /admin/campaigns/new", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNewPOST, "admin")))
	handle("GET /admin/campaigns/{id}", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignDetail, "admin")))
	handle("POST /admin/campaigns/{id}/start", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignStart, "admin")))
	handle("POST /admin/campaigns/{id}/pause", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignPause, "admin")))
	handle("POST /admin/campaigns/{id}/finish", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignFinish, "admin")))
	handle("POST /admin/campaigns/{id}/delete", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignDelete, "admin")))
	handle("POST /admin/campaigns/{id}/archive", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignArchive, "admin")))
	handle("GET /admin/campaigns/{id}/edit", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignEditGET, "admin")))
	handle("POST /admin/campaigns/{id}/edit", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignEditPOST, "admin")))
	handle("POST /admin/campaigns/preview", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignPreview, "admin")))
	handle("GET /uploads/{filename}", s.RequireRole(s.HandleServeUpload, "admin"))

	// Tahap 7
	handle("GET /admin/campaigns/{id}/numbers", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersGET, "admin")))
	handle("GET /admin/campaigns/{id}/numbers/upload", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersGET, "admin")))
	handle("POST /admin/campaigns/{id}/numbers/preview", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersPreview, "admin")))
	handle("POST /admin/campaigns/{id}/numbers/confirm", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersConfirm, "admin")))
	handle("GET /admin/campaigns/{id}/numbers/rejected/{previewId}", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersRejected, "admin")))

	// Tahap 11: Worker Panel
	handle("GET /worker", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerHome, "worker")))
	handle("POST /worker/profile", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerProfilePOST, "worker")))
	handle("POST /worker/wallet", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerWalletPOST, "worker")))
	handle("POST /worker/security", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerSecurityPOST, "worker")))
	handle("GET /worker/withdrawals", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerWithdrawalsGET, "worker")))
	handle("POST /worker/withdrawals", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerWithdrawalsPOST, "worker")))
	handle("POST /worker/convert", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerConvertPOST, "worker")))
	handle("GET /worker/referral", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerReferralGET, "worker")))
	handle("GET /worker/history", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerHistoryGET, "worker")))
	handle("POST /worker/ticket", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerTicketPOST, "worker")))
	handle("GET /worker/api/notifications", s.RequireRole(s.HandleWorkerNotificationsAPI, "worker"))
	handle("GET /worker/api/tickets", s.RequireRole(s.HandleWorkerTicketsAPI, "worker"))
	handle("GET /worker/api/stats", s.RequireRole(s.HandleWorkerAPIStats, "worker"))
	handle("POST /worker/notifications/read", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerNotificationsReadAllPOST, "worker")))
	handle("POST /worker/notifications/{id}/read", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerNotificationReadOnePOST, "worker")))

	// Tahap 9
	handle("GET /worker/accounts", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccounts, "worker")))
	handle("POST /worker/accounts/init", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountInitPOST, "worker")))
	handle("GET /worker/accounts/new", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountNewGET, "worker")))
	handle("POST /worker/accounts/new", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountNewPOST, "worker")))
	handle("GET /worker/accounts/{id}/status", s.RequireRole(s.HandleWorkerAccountStatus, "worker"))
	handle("POST /worker/accounts/{id}/pair", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountPairPOST, "worker")))
	handle("POST /worker/accounts/{id}/settings", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountSettingsPOST, "worker")))
	handle("POST /worker/accounts/{id}/delete", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountDelete, "worker")))
	handle("POST /worker/accounts/{id}/disconnect", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountDisconnect, "worker")))
	handle("POST /worker/accounts/{id}/stop", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountStop, "worker")))

	// Tahap 10b: Kerja kirim pesan
	handle("POST /worker/accounts/{id}/work/start", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountWorkStart, "worker")))
	handle("POST /worker/accounts/{id}/work/stop", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountWorkStop, "worker")))
}

// handleHealthz adalah liveness probe (200 OK selama proses web hidup).
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleReadyz adalah readiness probe (200 OK hanya jika database reachable dan server tidak sedang draining).
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.IsDraining() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"draining","ready":false}`))
		return
	}

	if s.DB == nil || s.DB.Pool == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"error","db":"nil","ready":false}`))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.DB.Pool.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"error","db":"unreachable","ready":false}`))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","db":"ok","ready":true}`))
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	lang := r.URL.Query().Get("lang")
	if lang != "" {
		lang = validateLang(lang)
		http.SetCookie(w, &http.Cookie{
			Name:     "lang",
			Value:    lang,
			Path:     "/",
			Expires:  time.Now().Add(30 * 24 * time.Hour),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	} else {
		cookie, err := r.Cookie("lang")
		if err == nil {
			lang = validateLang(cookie.Value)
		} else {
			lang = "id" // default
		}
	}

	data := struct {
		Lang           string
		Title          string
		AppName        string
		AppTagline     string
		SelectLanguage string
		Theme          string
		CSRFToken      string
		T              func(string) string
	}{
		Lang:           lang,
		Title:          i18n.T(lang, "app.name"),
		AppName:        i18n.T(lang, "app.name"),
		AppTagline:     i18n.T(lang, "app.tagline"),
		SelectLanguage: i18n.T(lang, "common.select_language"),
		Theme:          getTheme(r),
		CSRFToken:      getCSRF(r),
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["index.html"].ExecuteTemplate(w, "base.html", data)
}

func getCSRF(r *http.Request) string {
	if token, ok := r.Context().Value(csrfContextKey).(string); ok && token != "" {
		return token
	}
	if cookie, err := r.Cookie("gb_csrf"); err == nil {
		return cookie.Value
	}
	return ""
}

func getTheme(r *http.Request) string {
	cookie, err := r.Cookie("theme")
	if err == nil {
		switch cookie.Value {
		case "light", "dark":
			return cookie.Value
		}
	}
	return ""
}

func (s *Server) HandleThemePOST(w http.ResponseWriter, r *http.Request) {
	theme := r.FormValue("theme")
	if theme != "auto" && theme != "light" && theme != "dark" {
		http.Error(w, "Invalid theme value", http.StatusBadRequest)
		return
	}

	secure := s.Env != "dev"
	if theme == "auto" {
		http.SetCookie(w, &http.Cookie{
			Name:     "theme",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: false,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
	} else {
		http.SetCookie(w, &http.Cookie{
			Name:     "theme",
			Value:    theme,
			Path:     "/",
			MaxAge:   365 * 24 * 60 * 60,
			HttpOnly: false,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
	}

	target := "/"
	referer := r.Header.Get("Referer")
	if referer != "" {
		if refURL, err := url.Parse(referer); err == nil {
			if (refURL.Host == "" || refURL.Host == r.Host) && strings.HasPrefix(refURL.Path, "/") && !strings.HasPrefix(refURL.Path, "//") {
				target = refURL.RequestURI()
			}
		}
	}

	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) HandleLanguagePOST(w http.ResponseWriter, r *http.Request) {
	lang := validateLang(r.FormValue("lang"))

	secure := s.Env != "dev"
	http.SetCookie(w, &http.Cookie{
		Name:     "lang",
		Value:    lang,
		Path:     "/",
		Expires:  time.Now().Add(30 * 24 * time.Hour),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	if user, ok := currentUser(r); ok {
		_, _ = s.DB.Pool.Exec(r.Context(), "UPDATE users SET language = $1 WHERE id = $2", lang, user.ID)
	} else if cookie, err := r.Cookie("gb_session"); err == nil && cookie.Value != "" {
		hash := auth.HashSessionToken(cookie.Value)
		_, _ = s.DB.Pool.Exec(r.Context(), `
			UPDATE users SET language = $1
			FROM web_sessions ws
			WHERE ws.user_id = users.id AND ws.token_hash = $2
		`, lang, hash)
	}

	target := "/"
	referer := r.Header.Get("Referer")
	if referer != "" {
		if refURL, err := url.Parse(referer); err == nil {
			if (refURL.Host == "" || refURL.Host == r.Host) && strings.HasPrefix(refURL.Path, "/") && !strings.HasPrefix(refURL.Path, "//") {
				target = refURL.RequestURI()
			}
		}
	}

	http.Redirect(w, r, target, http.StatusSeeOther)
}

func validateLang(lang string) string {
	switch lang {
	case "id", "en", "zh":
		return lang
	default:
		return "id"
	}
}
