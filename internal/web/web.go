package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"goblast/internal/auth"
	"goblast/internal/backup"
	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/i18n"
	"goblast/internal/money"
	"goblast/internal/ratelimit"
	"goblast/internal/safe"
	"goblast/internal/turnstile"
	"goblast/internal/wa"
	"goblast/internal/withdraw"

	"golang.org/x/time/rate"
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

// publicStatsCache menyimpan angka agregat untuk halaman publik dengan TTL 60 detik.
// Perlindungan thundering-herd: satu goroutine yang menang lock refresh, sisanya pakai nilai lama.
type publicStatsCache struct {
	mu        sync.Mutex
	lastFetch time.Time
	claimable int
	wageIDR   int64
	refIDR    int64
	hasWage   bool
	hasRef    bool
	loaded    bool
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
	Engine             *engine.Engine
	CSWhatsAppURL      string
	CSTelegramURL      string
	ChannelTelegramURL string
	statsCache         globalStatsCache
	pubStatsCache      publicStatsCache
	lockCache          systemLockCache
	draining           atomic.Bool

	// H5a Keamanan Akses
	SessionSecret           string
	TrustedProxyCIDRs       []*net.IPNet
	ClientIPHeader          string
	Turnstile               *turnstile.Client
	LoginIPLimiter          *ratelimit.Limiter
	LoginIdentLimiter       *ratelimit.Limiter
	LoginPairLimiter        *ratelimit.Limiter
	LoginGlobalLimiter      *ratelimit.Limiter
	RegisterHourlyLimiter   *ratelimit.Limiter
	RegisterDailyLimiter    *ratelimit.Limiter
	AuditLoginFailedLimiter *ratelimit.Limiter

	// H5b Batas per Endpoint
	WorkerTicket10MinLimiter *ratelimit.Limiter
	WorkerTicketDailyLimiter *ratelimit.Limiter
	WorkerSecurityLimiter    *ratelimit.Limiter
	WorkerFinancialLimiter   *ratelimit.Limiter
	WorkerAccountUserLimiter *ratelimit.Limiter
	WorkerAccountIPLimiter   *ratelimit.Limiter
	WorkerWorkCtrlLimiter    *ratelimit.Limiter
	WorkerAPIPollingLimiter  *ratelimit.Limiter
	PublicRouteLimiter       *ratelimit.Limiter
	StaticRouteLimiter       *ratelimit.Limiter
	AdminGlobalLimiter                *ratelimit.Limiter
	AdminTOTPVerifyLimiter            *ratelimit.Limiter
	AdminCampaignExportNumbersLimiter *ratelimit.Limiter
	AdminStoragePurgeLimiter          *ratelimit.Limiter
	AdminStorageCleanLimiter          *ratelimit.Limiter

	// H9 Cadangan Database & Pemulihan
	BackupMgr       *backup.Manager
	AllowWebRestore bool
}

func (s *Server) IsProd() bool {
	return s.Env == "prod" || s.Env == "production"
}

func (s *Server) isProd() bool {
	return s.IsProd()
}

func (s *Server) sessionCookieName() string {
	if s.isProd() {
		return "__Host-gb_session"
	}
	return "gb_session"
}

func (s *Server) csrfCookieName() string {
	if s.isProd() {
		return "__Host-gb_csrf"
	}
	return "gb_csrf"
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

// getPublicStats mengambil data agregat nomor dan rate untuk halaman publik dengan cache memori 60 detik.
// Dilindungi mutex untuk mencegah thundering-herd saat banyak pengunjung serentak.
// Jika database gagal, mengembalikan nilai cache lama (jika ada) atau nol/false tanpa memicu galat halaman.
func (s *Server) getPublicStats(ctx context.Context) (claimable int, wageIDR int64, refIDR int64, hasWage bool, hasRef bool) {
	s.pubStatsCache.mu.Lock()
	defer s.pubStatsCache.mu.Unlock()

	if s.pubStatsCache.loaded && time.Since(s.pubStatsCache.lastFetch) < 60*time.Second {
		return s.pubStatsCache.claimable, s.pubStatsCache.wageIDR, s.pubStatsCache.refIDR, s.pubStatsCache.hasWage, s.pubStatsCache.hasRef
	}

	if s.DB == nil || s.DB.Pool == nil {
		if s.pubStatsCache.loaded {
			return s.pubStatsCache.claimable, s.pubStatsCache.wageIDR, s.pubStatsCache.refIDR, s.pubStatsCache.hasWage, s.pubStatsCache.hasRef
		}
		return 0, 0, 0, false, false
	}

	count, err := campaign.ClaimableCount(ctx, s.DB.Pool)
	if err != nil {
		if s.pubStatsCache.loaded {
			return s.pubStatsCache.claimable, s.pubStatsCache.wageIDR, s.pubStatsCache.refIDR, s.pubStatsCache.hasWage, s.pubStatsCache.hasRef
		}
		return 0, 0, 0, false, false
	}

	var wageVal, refVal int64
	var hasW, hasR bool

	rows, err := s.DB.Pool.Query(ctx, "SELECT key, value::text FROM settings WHERE key IN ('rate_wage', 'rate_referral')")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err == nil {
				v = strings.Trim(v, `"`)
				if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
					if k == "rate_wage" {
						wageVal = n
						hasW = true
					} else if k == "rate_referral" {
						refVal = n
						hasR = true
					}
				}
			}
		}
	}

	s.pubStatsCache.claimable = count
	s.pubStatsCache.wageIDR = wageVal
	s.pubStatsCache.refIDR = refVal
	s.pubStatsCache.hasWage = hasW
	s.pubStatsCache.hasRef = hasR
	s.pubStatsCache.lastFetch = time.Now()
	s.pubStatsCache.loaded = true

	return count, wageVal, refVal, hasW, hasR
}

func isSecureHTTPS(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "https") && u.Host != ""
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
	publicFooter := filepath.Join("web", "templates", "public_footer.html")

	templates := make(map[string]*template.Template)

	publicPages := map[string]bool{
		"index.html":     true,
		"syarat.html":    true,
		"privasi.html":   true,
		"teknologi.html": true,
		"login.html":     true,
		"register.html":  true,
	}

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
		"admin_totp_setup.html":               true,
		"admin_stepup.html":                   true,
		"admin_storage.html":                  true,
		"admin_backups.html":                  true,
	}

	pages := []string{
		"index.html", "login.html", "register.html", "maintenance.html", "login_totp.html",
		"syarat.html", "privasi.html", "teknologi.html",
		"admin_dashboard.html", "admin_settings.html", "admin_wallet.html", "admin_withdrawals.html", "admin_workers.html",
		"admin_worker_detail.html", "admin_maintenance.html",
		"admin_campaigns.html", "admin_campaign_form.html", "admin_campaign_detail.html",
		"admin_campaign_numbers.html", "admin_campaign_numbers_preview.html",
		"admin_tickets.html", "admin_broadcasts.html",
		"admin_totp_setup.html", "admin_stepup.html", "admin_storage.html", "admin_backups.html",
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
		} else if publicPages[page] {
			tmpl, err = template.ParseFiles(base, publicFooter, pagePath)
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

	srv := &Server{
		DB:                 database,
		Tmpl:               templates,
		Env:                env,
		WaManager:          waMgr,
		WorkCtrl:           workCtrl,
		CSWhatsAppURL:      "https://wa.me/",
		CSTelegramURL:      "https://t.me/feepay_cs",
		ChannelTelegramURL: "https://t.me/goblast_channel",
		ClientIPHeader:     "CF-Connecting-IP",
		LoginIPLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "ip",
			Rate:       rate.Every(config.LoginRateLimitIPWindow / time.Duration(config.LoginRateLimitIPAttempts)),
			Burst:      config.LoginRateLimitIPAttempts,
			Window:     config.LoginRateLimitIPWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: false,
		}),
		LoginIdentLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "ident",
			Rate:       rate.Every(config.LoginRateLimitIdentifierWindow / time.Duration(config.LoginRateLimitIdentifierAttempts)),
			Burst:      config.LoginRateLimitIdentifierAttempts,
			Window:     config.LoginRateLimitIdentifierWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		LoginPairLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "pair",
			Rate:       rate.Every(config.LoginRateLimitPairWindow / time.Duration(config.LoginRateLimitPairAttempts)),
			Burst:      config.LoginRateLimitPairAttempts,
			Window:     config.LoginRateLimitPairWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		LoginGlobalLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "global",
			Rate:       rate.Every(config.LoginRateLimitGlobalWindow / time.Duration(config.LoginRateLimitGlobalAttempts)),
			Burst:      config.LoginRateLimitGlobalAttempts,
			Window:     config.LoginRateLimitGlobalWindow,
			MaxKeys:    10,
			FailClosed: false,
		}),
		RegisterHourlyLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "register_hourly",
			Rate:       rate.Every(config.RegisterRateLimitHourlyWindow / time.Duration(config.RegisterRateLimitHourlyAttempts)),
			Burst:      config.RegisterRateLimitHourlyAttempts,
			Window:     config.RegisterRateLimitHourlyWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: false,
		}),
		RegisterDailyLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "register_daily",
			Rate:       rate.Every(config.RegisterRateLimitDailyWindow / time.Duration(config.RegisterRateLimitDailyAttempts)),
			Burst:      config.RegisterRateLimitDailyAttempts,
			Window:     config.RegisterRateLimitDailyWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: false,
		}),
		AuditLoginFailedLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "audit_login_failed",
			Rate:       rate.Every(config.AuditLoginFailedRateLimitWindow / time.Duration(config.AuditLoginFailedRateLimitAttempts)),
			Burst:      config.AuditLoginFailedRateLimitAttempts,
			Window:     config.AuditLoginFailedRateLimitWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: false,
		}),
		WorkerTicket10MinLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "worker_ticket_10m",
			Rate:       rate.Every(config.WorkerTicket10MinWindow / time.Duration(config.WorkerTicket10MinAttempts)),
			Burst:      config.WorkerTicket10MinAttempts,
			Window:     config.WorkerTicket10MinWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		WorkerTicketDailyLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "worker_ticket_daily",
			Rate:       rate.Every(config.WorkerTicketDailyWindow / time.Duration(config.WorkerTicketDailyAttempts)),
			Burst:      config.WorkerTicketDailyAttempts,
			Window:     config.WorkerTicketDailyWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		WorkerSecurityLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "worker_security",
			Rate:       rate.Every(config.WorkerSecurityWindow / time.Duration(config.WorkerSecurityAttempts)),
			Burst:      config.WorkerSecurityAttempts,
			Window:     config.WorkerSecurityWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		WorkerFinancialLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "worker_financial",
			Rate:       rate.Every(config.WorkerFinancialWindow / time.Duration(config.WorkerFinancialAttempts)),
			Burst:      config.WorkerFinancialAttempts,
			Window:     config.WorkerFinancialWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		WorkerAccountUserLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "worker_account_user",
			Rate:       rate.Every(config.WorkerAccountUserWindow / time.Duration(config.WorkerAccountUserAttempts)),
			Burst:      config.WorkerAccountUserAttempts,
			Window:     config.WorkerAccountUserWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		WorkerAccountIPLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "worker_account_ip",
			Rate:       rate.Every(config.WorkerAccountIPWindow / time.Duration(config.WorkerAccountIPAttempts)),
			Burst:      config.WorkerAccountIPAttempts,
			Window:     config.WorkerAccountIPWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		WorkerWorkCtrlLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "worker_work_ctrl",
			Rate:       rate.Every(config.WorkerWorkCtrlWindow / time.Duration(config.WorkerWorkCtrlAttempts)),
			Burst:      config.WorkerWorkCtrlAttempts,
			Window:     config.WorkerWorkCtrlWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		WorkerAPIPollingLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "worker_api_polling",
			Rate:       rate.Every(config.WorkerAPIPollingWindow / time.Duration(config.WorkerAPIPollingAttempts)),
			Burst:      config.WorkerAPIPollingBurst,
			Window:     config.WorkerAPIPollingWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		PublicRouteLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "public_route",
			Rate:       rate.Every(config.PublicRouteWindow / time.Duration(config.PublicRouteAttempts)),
			Burst:      config.PublicRouteAttempts,
			Window:     config.PublicRouteWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: false,
		}),
		StaticRouteLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "static_route",
			Rate:       rate.Every(config.StaticRouteWindow / time.Duration(config.StaticRouteAttempts)),
			Burst:      config.StaticRouteAttempts,
			Window:     config.StaticRouteWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: false,
		}),
		AdminGlobalLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "admin_global",
			Rate:       rate.Every(config.AdminGlobalWindow / time.Duration(config.AdminGlobalAttempts)),
			Burst:      config.AdminGlobalAttempts,
			Window:     config.AdminGlobalWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		AdminTOTPVerifyLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "admin_totp_verify",
			Rate:       rate.Every(config.AdminTOTPVerifyWindow / time.Duration(config.AdminTOTPVerifyAttempts)),
			Burst:      config.AdminTOTPVerifyAttempts,
			Window:     config.AdminTOTPVerifyWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		AdminCampaignExportNumbersLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "admin_campaign_export_numbers",
			Rate:       rate.Every(config.AdminCampaignExportNumbersWindow / time.Duration(config.AdminCampaignExportNumbersAttempts)),
			Burst:      config.AdminCampaignExportNumbersAttempts,
			Window:     config.AdminCampaignExportNumbersWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		AdminStoragePurgeLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "admin_storage_purge",
			Rate:       rate.Every(config.AdminStoragePurgeWindow / time.Duration(config.AdminStoragePurgeAttempts)),
			Burst:      config.AdminStoragePurgeAttempts,
			Window:     config.AdminStoragePurgeWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
		AdminStorageCleanLimiter: ratelimit.NewLimiter(ratelimit.Config{
			Layer:      "admin_storage_clean",
			Rate:       rate.Every(config.AdminStoragePurgeWindow / time.Duration(config.AdminStoragePurgeAttempts)),
			Burst:      config.AdminStoragePurgeAttempts,
			Window:     config.AdminStoragePurgeWindow,
			MaxKeys:    config.MaxLimiterKeys,
			FailClosed: true,
		}),
	}
	if eng, ok := workCtrl.(*engine.Engine); ok {
		srv.Engine = eng
	}

	if database != nil && database.Pool != nil {
		safe.Go("web-session-cleaner", func() {
			srv.startSessionCleaner()
		})
		safe.Go("audit-log-cleaner", func() {
			srv.startAuditCleaner()
		})
	}
	return srv, nil
}

func (s *Server) startSessionCleaner() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		if s.IsDraining() {
			return
		}
		if s.DB != nil && s.DB.Pool != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, err := s.DB.Pool.Exec(ctx, "DELETE FROM web_sessions WHERE expires_at < CURRENT_TIMESTAMP")
			cancel()
			if err != nil {
				slog.Error("web: gagal membersihkan sesi web kedaluwarsa", "err", err)
			}
		}
	}
}

func (s *Server) startAuditCleaner() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		if s.IsDraining() {
			return
		}
		if s.DB != nil && s.DB.Pool != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, err := s.DB.Pool.Exec(ctx, `
				DELETE FROM audit_logs 
				WHERE action IN ('login_failed', 'login_rejected') 
				  AND created_at < CURRENT_TIMESTAMP - INTERVAL '30 days'
			`)
			cancel()
			if err != nil {
				slog.Error("web: gagal membersihkan audit log lama", "err", err)
			}
		}
	}
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

// WrapGlobalMiddleware membungkus handler utama dengan RecoverMiddleware, SecurityHeadersMiddleware, dan MaxBytes dinamis.
func (s *Server) WrapGlobalMiddleware(next http.Handler) http.Handler {
	return s.RecoverMiddleware(s.SecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isUpload := isUploadRoute(r.URL.Path)
		s.MaxBytesMiddleware(next, isUpload).ServeHTTP(w, r)
	})))
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	handle := func(pattern string, handler http.Handler) {
		isUpload := isUploadRoute(pattern)
		mux.Handle(pattern, s.RecoverMiddleware(s.SecurityHeadersMiddleware(s.MaxBytesMiddleware(handler, isUpload))))
	}
	handleFunc := func(pattern string, fn http.HandlerFunc) {
		isUpload := isUploadRoute(pattern)
		mux.Handle(pattern, s.RecoverMiddleware(s.SecurityHeadersMiddleware(s.MaxBytesMiddleware(fn, isUpload))))
	}

	// Serve static files dengan neutered file system (mencegah directory listing) dan header cache browser
	staticFileServer := http.StripPrefix("/static/", http.FileServer(neuteredFileSystem{fs: http.Dir(filepath.Join("web", "static"))}))
	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		staticFileServer.ServeHTTP(w, r)
	})
	handle("/static/", s.RateLimitStaticMiddleware(staticHandler))

	handleFunc("/healthz", s.handleHealthz)
	handleFunc("GET /healthz", s.handleHealthz)
	handleFunc("/readyz", s.handleReadyz)
	handleFunc("GET /readyz", s.handleReadyz)

	handle("/", s.RateLimitPublicMiddleware(s.CSRFMiddleware(http.HandlerFunc(s.handleIndex))))
	handle("GET /syarat", s.RateLimitPublicMiddleware(s.CSRFMiddleware(http.HandlerFunc(s.handleSyarat))))
	handle("GET /privasi", s.RateLimitPublicMiddleware(s.CSRFMiddleware(http.HandlerFunc(s.handlePrivasi))))
	handle("GET /teknologi", s.RateLimitPublicMiddleware(s.CSRFMiddleware(http.HandlerFunc(s.handleTeknologi))))
	handle("POST /theme", s.RateLimitPublicMiddleware(s.CSRFMiddleware(http.HandlerFunc(s.HandleThemePOST))))
	handle("POST /language", s.RateLimitPublicMiddleware(s.CSRFMiddleware(http.HandlerFunc(s.HandleLanguagePOST))))
	handle("/login", s.RateLimitPublicMiddleware(s.CSRFMiddleware(http.HandlerFunc(s.handleLogin))))
	handle("/register", s.RateLimitPublicMiddleware(s.CSRFMiddleware(http.HandlerFunc(s.handleRegister))))

	handle("GET /login/totp", s.RateLimitPublicMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleLoginTOTPGET, "admin"))))
	handle("POST /login/totp", s.RateLimitPublicMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleLoginTOTPPOST, "admin"))))

	handle("POST /logout", s.RateLimitPublicMiddleware(s.CSRFMiddleware(s.RequireRole(s.handleLogout, "admin", "worker"))))
	handle("GET /logout", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}))
	handle("GET /maintenance", s.RateLimitPublicMiddleware(s.CSRFMiddleware(http.HandlerFunc(s.HandleMaintenancePublicGET))))

	// TOTP Setup & Step-Up Admin
	handle("GET /admin/totp/setup", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminTOTPSetupGET, "admin"))))
	handle("POST /admin/totp/setup", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminTOTPSetupPOST, "admin"))))
	handle("GET /admin/stepup", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminStepUpGET, "admin"))))
	handle("POST /admin/stepup", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminStepUpPOST, "admin"))))

	// Tahap 12: Admin Dashboard, Settings, Wallet, Workers
	handle("GET /admin", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminHome, "admin"))))
	handle("GET /admin/api/stats", s.RateLimitAdminMiddleware(s.RequireRole(s.HandleAdminAPIStats, "admin")))
	handle("GET /admin/api/metrics", s.RateLimitAdminMiddleware(s.RequireRole(s.HandleAdminAPIMetrics, "admin")))
	handle("GET /admin/settings", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminSettingsGET, "admin"))))
	handle("POST /admin/settings", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminSettingsPOST), "admin"))))
	handle("GET /admin/wallet", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminWalletGET, "admin"))))
	handle("POST /admin/wallet", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminWalletTopupPOST), "admin"))))
	handle("POST /admin/wallet/topup", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminWalletTopupPOST), "admin"))))
	handle("GET /admin/withdrawals", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminWithdrawalsGET, "admin"))))
	handle("POST /admin/withdrawals/{id}/view-destination", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminWithdrawalViewDestinationPOST), "admin"))))
	handle("POST /admin/withdrawals/{id}/approve", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminWithdrawalApprovePOST), "admin"))))
	handle("POST /admin/withdrawals/{id}/reject", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminWithdrawalRejectPOST), "admin"))))
	handle("GET /admin/workers", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminWorkers, "admin"))))
	handle("GET /admin/workers/{id}", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminWorkerDetailGET, "admin"))))
	handle("POST /admin/workers/{id}/reset-password", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminWorkerResetPasswordPOST), "admin"))))
	handle("POST /admin/workers/{id}/freeze", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminWorkerFreezePOST), "admin"))))
	handle("POST /admin/workers/{id}/unfreeze", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminWorkerUnfreezePOST), "admin"))))
	handle("POST /admin/workers/{id}/disable", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminWorkerDisablePOST), "admin"))))
	handle("POST /admin/workers/{id}/enable", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminWorkerEnablePOST), "admin"))))
	handle("GET /admin/tickets", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminTicketsGET, "admin"))))
	handle("POST /admin/tickets/{id}/resolve", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminTicketResolvePOST, "admin"))))
	handle("GET /admin/broadcasts", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminBroadcastsGET, "admin"))))
	handle("POST /admin/broadcasts/new", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminBroadcastNewPOST, "admin"))))
	handle("POST /admin/broadcasts/{id}/toggle", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminBroadcastTogglePOST, "admin"))))
	handle("POST /admin/broadcasts/{id}/delete", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminBroadcastDeletePOST, "admin"))))
	handle("GET /admin/maintenance", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminMaintenanceGET, "admin"))))
	handle("POST /admin/maintenance/toggle", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminMaintenanceTogglePOST, "admin"))))
	handle("POST /admin/maintenance/locks", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminMaintenanceLocksPOST, "admin"))))
	handle("POST /admin/maintenance/lock/toggle", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminMaintenanceLockTogglePOST, "admin"))))
	handle("POST /admin/maintenance/restrictions/release", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminMaintenanceRestrictionReleasePOST, "admin"))))
	handle("POST /admin/maintenance/restrictions/release-all", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminMaintenanceRestrictionReleaseAllPOST, "admin"))))

	// Tahap H8: Penyimpanan Data & Pembersihan Server
	handle("GET /admin/storage", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminStorageGET, "admin"))))
	handle("POST /admin/storage/clean-tmp", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminStorageCleanTmpPOST), "admin"))))
	handle("POST /admin/storage/clean-orphans", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminStorageCleanOrphansPOST), "admin"))))
	handle("POST /admin/storage/clean-retention", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminStorageCleanRetentionPOST), "admin"))))
	handle("POST /admin/storage/vacuum", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminStorageVacuumPOST), "admin"))))
	handle("GET /admin/storage/vacuum", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/storage", http.StatusSeeOther)
	}, "admin"))))
	handle("GET /admin/storage/clean-tmp", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/storage", http.StatusSeeOther)
	}, "admin"))))
	handle("GET /admin/storage/clean-orphans", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/storage", http.StatusSeeOther)
	}, "admin"))))
	handle("GET /admin/storage/clean-retention", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/storage", http.StatusSeeOther)
	}, "admin"))))

	// Tahap H9: Backup Aman dan Pemulihan
	handle("GET /admin/backups", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminBackupsGET, "admin"))))
	handle("POST /admin/backups/create", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminBackupCreatePOST, "admin"))))
	handle("POST /admin/backups/{id}/verify", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminBackupVerifyPOST, "admin"))))
	handle("GET /admin/backups/{id}/download", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminBackupDownloadGET), "admin"))))
	handle("POST /admin/backups/{id}/delete", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminBackupDeletePOST), "admin"))))
	handle("POST /admin/backups/{id}/restore", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminBackupRestorePOST), "admin"))))

	handle("/admin/", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, "admin"))))

	// Tahap 8
	handle("GET /admin/campaigns", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaigns, "admin"))))
	handle("GET /admin/campaigns/new", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNewGET, "admin"))))
	handle("POST /admin/campaigns/new", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNewPOST, "admin"))))
	handle("GET /admin/campaigns/{id}", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignDetail, "admin"))))
	handle("POST /admin/campaigns/{id}/start", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignStart, "admin"))))
	handle("POST /admin/campaigns/{id}/pause", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignPause, "admin"))))
	handle("POST /admin/campaigns/{id}/finish", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignFinish, "admin"))))
	handle("POST /admin/campaigns/{id}/delete", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignDelete, "admin"))))
	handle("POST /admin/campaigns/{id}/archive", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignArchive, "admin"))))
	handle("POST /admin/campaigns/{id}/purge", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminCampaignPurgePOST), "admin"))))
	handle("POST /admin/campaigns/{id}/requeue-uncertain", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminCampaignRequeueUncertain), "admin"))))
	handle("GET /admin/campaigns/{id}/edit", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignEditGET, "admin"))))
	handle("POST /admin/campaigns/{id}/edit", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignEditPOST, "admin"))))
	handle("POST /admin/campaigns/preview", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignPreview, "admin"))))
	handle("GET /uploads/{filename}", s.RateLimitAdminMiddleware(s.RequireRole(s.HandleServeUpload, "admin")))

	// Tahap 7
	handle("GET /admin/campaigns/{id}/numbers", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminCampaignNumbersGET), "admin"))))
	handle("GET /admin/campaigns/{id}/numbers/upload", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminCampaignNumbersGET), "admin"))))
	handle("POST /admin/campaigns/{id}/numbers/preview", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminCampaignNumbersPreview), "admin"))))
	handle("POST /admin/campaigns/{id}/numbers/confirm", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminCampaignNumbersConfirm), "admin"))))
	handle("POST /admin/campaigns/{id}/numbers/cancel", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminCampaignNumbersCancel), "admin"))))
	handle("GET /admin/campaigns/{id}/numbers/export", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.RequireStepUp(s.HandleAdminCampaignExportNumbers), "admin"))))
	handle("GET /admin/campaigns/{id}/numbers/rejected/{previewId}", s.RateLimitAdminMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersRejected, "admin"))))

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
	handle("GET /worker/api/notifications", s.RateLimitWorkerAPIPollingMiddleware(s.RequireRole(s.HandleWorkerNotificationsAPI, "worker")))
	handle("GET /worker/api/tickets", s.RateLimitWorkerAPIPollingMiddleware(s.RequireRole(s.HandleWorkerTicketsAPI, "worker")))
	handle("GET /worker/api/stats", s.RateLimitWorkerAPIPollingMiddleware(s.RequireRole(s.HandleWorkerAPIStats, "worker")))
	handle("POST /worker/notifications/read", s.RateLimitWorkerAPIPollingMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleWorkerNotificationsReadAllPOST, "worker"))))
	handle("POST /worker/notifications/{id}/read", s.RateLimitWorkerAPIPollingMiddleware(s.CSRFMiddleware(s.RequireRole(s.HandleWorkerNotificationReadOnePOST, "worker"))))

	// Tahap 9
	handle("GET /worker/accounts", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccounts, "worker")))
	handle("POST /worker/accounts/init", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountInitPOST, "worker")))
	handle("GET /worker/accounts/new", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountNewGET, "worker")))
	handle("POST /worker/accounts/new", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountNewPOST, "worker")))
	handle("GET /worker/accounts/{id}/status", s.RateLimitWorkerAPIPollingMiddleware(s.RequireRole(s.HandleWorkerAccountStatus, "worker")))
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

	// Pengunjung yang sudah punya sesi login valid diarahkan ke panel sesuai peran
	if s.DB != nil && s.DB.Pool != nil {
		cookieName := s.sessionCookieName()
		cookie, err := r.Cookie(cookieName)
		if err != nil && s.isProd() {
			cookie, err = r.Cookie("gb_session")
		}
		if err == nil && cookie.Value != "" {
			hash := auth.HashSessionToken(cookie.Value)
			var role string
			qCtx, qCancel := context.WithTimeout(r.Context(), 2*time.Second)
			err = s.DB.Pool.QueryRow(qCtx, `
				SELECT u.role 
				FROM web_sessions ws
				JOIN users u ON u.id = ws.user_id
				WHERE ws.token_hash = $1 AND ws.expires_at > CURRENT_TIMESTAMP AND u.status = 'active'
			`, hash).Scan(&role)
			qCancel()
			if err == nil {
				if role == "admin" {
					http.Redirect(w, r, "/admin", http.StatusSeeOther)
					return
				} else if role == "worker" {
					http.Redirect(w, r, "/worker", http.StatusSeeOther)
					return
				}
			}
		}
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

	claimable, wageIDR, refIDR, hasWage, hasRef := s.getPublicStats(r.Context())

	var wageStr, refStr string
	if hasWage {
		wageStr = money.FormatIDR(wageIDR)
	}
	if hasRef {
		refStr = money.FormatIDR(refIDR)
	}

	step4Fmt := i18n.T(lang, "public.step4_desc")
	step4Desc := fmt.Sprintf(step4Fmt, money.FormatIDR(config.MinWithdrawIDR))

	var waURL, tgCS, tgCh string
	if isSecureHTTPS(s.CSWhatsAppURL) {
		waURL = s.CSWhatsAppURL
	}
	if isSecureHTTPS(s.CSTelegramURL) {
		tgCS = s.CSTelegramURL
	}
	if isSecureHTTPS(s.ChannelTelegramURL) {
		tgCh = s.ChannelTelegramURL
	}

	data := struct {
		Lang               string
		Title              string
		AppName            string
		AppTagline         string
		Theme              string
		CSRFToken          string
		Nonce              string
		ClaimableCount     int
		WageIDR            string
		ReferralIDR        string
		Step4Desc          string
		CSWhatsAppURL      string
		CSTelegramURL      string
		ChannelTelegramURL string
		T                  func(string) string
	}{
		Lang:               lang,
		Title:              i18n.T(lang, "public.home_title"),
		AppName:            i18n.T(lang, "app.name"),
		AppTagline:         i18n.T(lang, "app.tagline"),
		Theme:              getTheme(r),
		CSRFToken:          getCSRF(r),
		Nonce:              getNonce(r),
		ClaimableCount:     claimable,
		WageIDR:            wageStr,
		ReferralIDR:        refStr,
		Step4Desc:          step4Desc,
		CSWhatsAppURL:      waURL,
		CSTelegramURL:      tgCS,
		ChannelTelegramURL: tgCh,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.Tmpl["index.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) handleSyarat(w http.ResponseWriter, r *http.Request) {
	lang := validateLang(r.URL.Query().Get("lang"))
	if lang == "id" {
		if cookie, err := r.Cookie("lang"); err == nil {
			lang = validateLang(cookie.Value)
		}
	}

	data := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		Nonce     string
		T         func(string) string
	}{
		Lang:      lang,
		Title:     i18n.T(lang, "terms.title"),
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		Nonce:     getNonce(r),
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.Tmpl["syarat.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) handlePrivasi(w http.ResponseWriter, r *http.Request) {
	lang := validateLang(r.URL.Query().Get("lang"))
	if lang == "id" {
		if cookie, err := r.Cookie("lang"); err == nil {
			lang = validateLang(cookie.Value)
		}
	}

	data := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		Nonce     string
		T         func(string) string
	}{
		Lang:      lang,
		Title:     i18n.T(lang, "privacy.title"),
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		Nonce:     getNonce(r),
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.Tmpl["privasi.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) handleTeknologi(w http.ResponseWriter, r *http.Request) {
	lang := validateLang(r.URL.Query().Get("lang"))
	if lang == "id" {
		if cookie, err := r.Cookie("lang"); err == nil {
			lang = validateLang(cookie.Value)
		}
	}

	data := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		Nonce     string
		T         func(string) string
	}{
		Lang:      lang,
		Title:     i18n.T(lang, "tech.title"),
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		Nonce:     getNonce(r),
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	s.Tmpl["teknologi.html"].ExecuteTemplate(w, "base.html", data)
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

func (s *Server) writeRateLimitJSON(w http.ResponseWriter, r *http.Request, actionName string, retryAfter time.Duration) {
	clientIP := s.getClientIP(r)
	var userIDStr string
	if user, ok := currentUser(r); ok {
		userIDStr = strconv.FormatInt(user.ID, 10)
	}
	slog.Warn("Rate limit exceeded (API)", "action", actionName, "ip", clientIP, "user_id", userIDStr, "retry_after", retryAfter)

	retrySecs := int(math.Ceil(retryAfter.Seconds()))
	if retrySecs < 1 {
		retrySecs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retrySecs))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":          false,
		"error":       "rate_limit_exceeded",
		"message":     fmt.Sprintf("Terlalu banyak permintaan. Silakan tunggu %d detik.", retrySecs),
		"retry_after": retrySecs,
	})
}

func (s *Server) redirectRateLimit(w http.ResponseWriter, r *http.Request, targetURL, actionName string, retryAfter time.Duration) {
	clientIP := s.getClientIP(r)
	var userIDStr string
	if user, ok := currentUser(r); ok {
		userIDStr = strconv.FormatInt(user.ID, 10)
	}
	slog.Warn("Rate limit exceeded (Form)", "action", actionName, "ip", clientIP, "user_id", userIDStr, "retry_after", retryAfter)

	retrySecs := int(math.Ceil(retryAfter.Seconds()))
	if retrySecs < 1 {
		retrySecs = 1
	}
	sep := "?"
	if strings.Contains(targetURL, "?") {
		sep = "&"
	}
	msg := fmt.Sprintf("Terlalu banyak permintaan. Silakan tunggu %d detik.", retrySecs)
	http.Redirect(w, r, targetURL+sep+"error="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) RateLimitPublicMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.PublicRouteLimiter != nil {
			clientIP := s.getClientIP(r)
			if allowed, wait := s.PublicRouteLimiter.Take(clientIP); !allowed {
				s.writeRateLimitJSON(w, r, "public_route", wait)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) RateLimitStaticMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.StaticRouteLimiter != nil {
			clientIP := s.getClientIP(r)
			if allowed, wait := s.StaticRouteLimiter.Take(clientIP); !allowed {
				retrySecs := int(math.Ceil(wait.Seconds()))
				if retrySecs < 1 {
					retrySecs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retrySecs))
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) RateLimitAdminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.AdminGlobalLimiter != nil {
			var key string
			if user, ok := currentUser(r); ok {
				key = strconv.FormatInt(user.ID, 10)
			} else {
				key = s.getClientIP(r)
			}
			if allowed, wait := s.AdminGlobalLimiter.Take(key); !allowed {
				if strings.Contains(r.Header.Get("Accept"), "application/json") || strings.HasPrefix(r.URL.Path, "/admin/api/") {
					s.writeRateLimitJSON(w, r, "admin_global", wait)
				} else {
					s.redirectRateLimit(w, r, "/admin", "admin_global", wait)
				}
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) RateLimitWorkerAPIPollingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.WorkerAPIPollingLimiter != nil {
			var key string
			if user, ok := currentUser(r); ok {
				key = strconv.FormatInt(user.ID, 10)
			} else {
				key = s.getClientIP(r)
			}
			if allowed, wait := s.WorkerAPIPollingLimiter.Take(key); !allowed {
				s.writeRateLimitJSON(w, r, "worker_api_polling", wait)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
