package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nyaruka/phonenumbers"
	"goblast/internal/auth"
	"goblast/internal/backup"
	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/engine"
	"goblast/internal/i18n"
	"goblast/internal/money"
	"goblast/internal/numbers"
	"goblast/internal/ratelimit"
	"goblast/internal/timeutil"
	"goblast/internal/withdraw"
)

// --- Structs for Admin Dashboard & API Stats ---

type CampaignStatRow = campaign.CampaignStatRow

type WorkerStatRow struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	SentToday    int    `json:"sent_today"`
	SentTotal    int    `json:"sent_total"`
	EarningsIDR  int64  `json:"earnings_idr"`
	EarningsUSDT int64  `json:"earnings_usdt"`
}

type AccountStatRow struct {
	ID             int64  `json:"id"`
	WorkerID       int64  `json:"worker_id"`
	WorkerUsername string `json:"worker_username"`
	Label          string `json:"label"`
	Speed          string `json:"speed"`
	Status         string `json:"status"`
	IsRunning      bool   `json:"is_running"`
	StopReason     string `json:"stop_reason"`
}

type DailyTrendRow struct {
	Date           string `json:"date"`
	Terkirim       int    `json:"terkirim"`
	Gagal          int    `json:"gagal"`
	TidakTerdaftar int    `json:"tidak_terdaftar"`
	Total          int    `json:"total"`
}

type DashboardData struct {
	OverallCounts map[string]int    `json:"overall_counts"`
	Campaigns     []CampaignStatRow `json:"campaigns"`
	Workers       []WorkerStatRow   `json:"workers"`
	Accounts      []AccountStatRow  `json:"accounts"`
	DailyTrends   []DailyTrendRow   `json:"daily_trends"`
	UpdatedAt     string            `json:"updated_at"`
}

// HandleAdminHome renders the full admin dashboard
func (s *Server) HandleAdminHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}

	data, err := s.fetchDashboardData(r.Context())
	if err != nil {
		slog.Error("Gagal memuat statistik dashboard", "err", err)
		http.Error(w, "Gagal memuat statistik", http.StatusInternalServerError)
		return
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	viewData := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		Nonce     string
		ActiveNav string
		Stats     *DashboardData
		T         func(string) string
	}{
		Lang:      lang,
		Title:     "Dashboard Admin - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		Nonce:     getNonce(r),
		ActiveNav: "dashboard",
		Stats:     data,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["admin_dashboard.html"].ExecuteTemplate(w, "base.html", viewData)
}

// HandleAdminAPIStats serves GET /admin/api/stats for 10s polling (admin-only)
func (s *Server) HandleAdminAPIStats(w http.ResponseWriter, r *http.Request) {
	data, err := s.fetchDashboardData(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to fetch stats"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

// HandleAdminAPIMetrics menyajikan GET /admin/api/metrics (JSON, khusus admin).
// Menyajikan metrik atomik engine, metrik pgx connection pool, dan runtime MemStats.
func (s *Server) HandleAdminAPIMetrics(w http.ResponseWriter, r *http.Request) {
	var engStats engine.EngineStats
	if s.Engine != nil {
		engStats = s.Engine.Stats()
	}

	type dbStatsJSON struct {
		AcquireCount         int64 `json:"acquire_count"`
		AcquiredConns        int32 `json:"acquired_conns"`
		CanceledAcquireCount int64 `json:"canceled_acquire_count"`
		ConstructingConns    int32 `json:"constructing_conns"`
		EmptyAcquireCount    int64 `json:"empty_acquire_count"`
		IdleConns            int32 `json:"idle_conns"`
		MaxConns             int32 `json:"max_conns"`
		TotalConns           int32 `json:"total_conns"`
	}

	var dbJSON dbStatsJSON
	if s.DB != nil && s.DB.Pool != nil {
		stat := s.DB.Pool.Stat()
		if stat != nil {
			dbJSON = dbStatsJSON{
				AcquireCount:         stat.AcquireCount(),
				AcquiredConns:        stat.AcquiredConns(),
				CanceledAcquireCount: stat.CanceledAcquireCount(),
				ConstructingConns:    stat.ConstructingConns(),
				EmptyAcquireCount:    stat.EmptyAcquireCount(),
				IdleConns:            stat.IdleConns(),
				MaxConns:             stat.MaxConns(),
				TotalConns:           stat.TotalConns(),
			}
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	var backupJSON any
	if s.BackupMgr != nil {
		ctx := r.Context()
		totalCount, totalBytes, lastBackupTime, lastVerifiedTime, lastVerifyOK, err := s.BackupMgr.GetStats(ctx)
		freeDisk, _ := backup.GetFreeDiskSpace(s.BackupMgr.Dir())
		var lastBackupStr, lastVerifiedStr any
		if lastBackupTime != nil {
			lastBackupStr = lastBackupTime.Format(time.RFC3339)
		}
		if lastVerifiedTime != nil {
			lastVerifiedStr = lastVerifiedTime.Format(time.RFC3339)
		}
		backupJSON = map[string]any{
			"enabled":          true,
			"total_count":      totalCount,
			"total_bytes":      totalBytes,
			"free_disk_bytes":  freeDisk,
			"last_backup_at":   lastBackupStr,
			"last_verified_at": lastVerifiedStr,
			"last_verify_ok":   lastVerifyOK,
			"stats_error":      err != nil,
		}
	} else {
		backupJSON = map[string]any{
			"enabled": false,
		}
	}

	resp := map[string]any{
		"engine": engStats,
		"db":     dbJSON,
		"backup": backupJSON,
		"memory": map[string]any{
			"alloc_bytes":       m.Alloc,
			"total_alloc_bytes": m.TotalAlloc,
			"sys_bytes":         m.Sys,
			"num_gc":            m.NumGC,
			"num_goroutine":     runtime.NumGoroutine(),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) fetchDashboardData(ctx context.Context) (*DashboardData, error) {
	startOfTodayWIB := timeutil.GetJakartaStartOfDay()

	// 1. Overall counts
	overall := map[string]int{
		"tersedia":        0,
		"dikirim":         0,
		"terkirim":        0,
		"gagal":           0,
		"tidak_terdaftar": 0,
		"tertahan":        0,
	}
	rows, err := s.DB.Pool.Query(ctx, `SELECT status, COUNT(*) FROM campaign_numbers GROUP BY status`)
	if err == nil {
		for rows.Next() {
			var st string
			var count int
			if err := rows.Scan(&st, &count); err == nil {
				overall[st] = count
			}
		}
		rows.Close()
	}

	// Gunakan ClaimableCount untuk nomor tersedia yang benar-benar siap kirim
	if claimable, err := campaign.ClaimableCount(ctx, s.DB.Pool); err == nil {
		overall["tersedia"] = claimable
	}
	// Hitung angka "tertahan" (nomor berstatus tersedia milik campaign yang tidak berjalan atau diarsipkan)
	if tertahan, err := campaign.TertahanCount(ctx, s.DB.Pool); err == nil {
		overall["tertahan"] = tertahan
	}

	// 2. Per campaign counts
	campaigns, _ := campaign.GetCampaignStats(ctx, s.DB.Pool)

	// 3. Worker stats
	var workers []WorkerStatRow
	wRows, err := s.DB.Pool.Query(ctx, `
		SELECT u.id, COALESCE(u.username, u.email), COALESCE(u.email, '-'),
		       COUNT(CASE WHEN le.kind = 'upah' AND le.created_at >= $1 THEN 1 END) AS sent_today,
		       COUNT(CASE WHEN le.kind = 'upah' THEN 1 END) AS sent_total,
		       COALESCE(SUM(CASE WHEN le.currency = 'IDR' AND le.kind IN ('upah', 'referral') THEN le.amount ELSE 0 END), 0) AS earnings_idr,
		       COALESCE(SUM(CASE WHEN le.currency = 'USDT' AND le.kind IN ('upah', 'referral') THEN le.amount ELSE 0 END), 0) AS earnings_usdt
		FROM users u
		LEFT JOIN ledger_entries le ON le.user_id = u.id
		WHERE u.role = 'worker'
		GROUP BY u.id, u.username, u.email
		ORDER BY sent_today DESC, sent_total DESC
	`, startOfTodayWIB)
	if err == nil {
		for wRows.Next() {
			var w WorkerStatRow
			if err := wRows.Scan(&w.ID, &w.Username, &w.Email, &w.SentToday, &w.SentTotal, &w.EarningsIDR, &w.EarningsUSDT); err == nil {
				workers = append(workers, w)
			}
		}
		wRows.Close()
	}

	// 4. WhatsApp Accounts
	var accounts []AccountStatRow
	aRows, err := s.DB.Pool.Query(ctx, `
		SELECT wa.id, wa.worker_id, COALESCE(u.username, u.email), wa.label, wa.speed, wa.status, COALESCE(wa.stop_reason, '')
		FROM wa_accounts wa
		JOIN users u ON u.id = wa.worker_id
		ORDER BY wa.id DESC
	`)
	if err == nil {
		for aRows.Next() {
			var a AccountStatRow
			if err := aRows.Scan(&a.ID, &a.WorkerID, &a.WorkerUsername, &a.Label, &a.Speed, &a.Status, &a.StopReason); err == nil {
				if s.WorkCtrl != nil {
					a.IsRunning = s.WorkCtrl.IsRunning(a.ID)
				}
				accounts = append(accounts, a)
			}
		}
		aRows.Close()
	}

	// 5. 14-Day daily trend (Asia/Jakarta)
	start14Days := startOfTodayWIB.AddDate(0, 0, -13)
	trendMap := make(map[string]*DailyTrendRow)

	// Pre-fill all 14 days
	var orderedDates []string
	for i := 0; i < 14; i++ {
		d := start14Days.AddDate(0, 0, i)
		dStr := d.Format("2006-01-02")
		orderedDates = append(orderedDates, dStr)
		trendMap[dStr] = &DailyTrendRow{
			Date: dStr,
		}
	}

	tRows, err := s.DB.Pool.Query(ctx, `
		SELECT (sent_at AT TIME ZONE 'Asia/Jakarta')::date::text AS day,
		       COUNT(CASE WHEN status = 'terkirim' THEN 1 END) AS count_terkirim,
		       COUNT(CASE WHEN status = 'gagal' THEN 1 END) AS count_gagal,
		       COUNT(CASE WHEN status = 'tidak_terdaftar' THEN 1 END) AS count_tidak_terdaftar
		FROM campaign_numbers
		WHERE sent_at >= $1
		GROUP BY (sent_at AT TIME ZONE 'Asia/Jakarta')::date
	`, start14Days)
	if err == nil {
		for tRows.Next() {
			var day string
			var tk, gg, tt int
			if err := tRows.Scan(&day, &tk, &gg, &tt); err == nil {
				if item, exists := trendMap[day]; exists {
					item.Terkirim = tk
					item.Gagal = gg
					item.TidakTerdaftar = tt
					item.Total = tk + gg + tt
				}
			}
		}
		tRows.Close()
	}

	var dailyTrends []DailyTrendRow
	// Return latest date first
	for i := len(orderedDates) - 1; i >= 0; i-- {
		dStr := orderedDates[i]
		dailyTrends = append(dailyTrends, *trendMap[dStr])
	}

	return &DashboardData{
		OverallCounts: overall,
		Campaigns:     campaigns,
		Workers:       workers,
		Accounts:      accounts,
		DailyTrends:   dailyTrends,
		UpdatedAt:     time.Now().Format(time.RFC3339),
	}, nil
}

// --- Settings Handler ---

type SettingsViewData struct {
	MaxAccountsPerWorker   *int
	RateWageIDR            *int64
	RateReferralIDR        *int64
	RateIDRtoUSDT          *int64
	RateIDRtoUSDTDisplay   string
	RateIDRtoUSDTUpdatedAt string
	DefaultCountry         string
	CountryOptions         []numbers.CountryOption
}

func (s *Server) HandleAdminSettingsGET(w http.ResponseWriter, r *http.Request) {
	settings, err := s.readSettingsFromDB(r.Context())
	if err != nil {
		slog.Error("Gagal membaca pengaturan sistem", "err", err)
		http.Error(w, "Gagal membaca pengaturan", http.StatusInternalServerError)
		return
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	flashMsg := r.URL.Query().Get("msg")
	errMsg := r.URL.Query().Get("err")

	viewData := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		Nonce     string
		ActiveNav string
		Settings  *SettingsViewData
		Message   string
		Error     string
		T         func(string) string
	}{
		Lang:      lang,
		Title:     "Pengaturan Sistem - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		Nonce:     getNonce(r),
		ActiveNav: "settings",
		Settings:  settings,
		Message:   flashMsg,
		Error:     errMsg,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["admin_settings.html"].ExecuteTemplate(w, "base.html", viewData)
}

func parseUSDTInput(str string) (int64, error) {
	str = strings.TrimSpace(str)
	if str == "" {
		return 0, errors.New("input kosong")
	}
	str = strings.ReplaceAll(str, ",", ".")
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return 0, err
	}
	if f < 0 {
		return 0, errors.New("tidak boleh negatif")
	}
	if f < 1000.0 {
		return int64(math.Round(f * 1000000.0)), nil
	}
	return int64(math.Round(f)), nil
}

func (s *Server) HandleAdminSettingsPOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Form tidak valid", http.StatusBadRequest)
		return
	}

	// 1. Validate rate_wage (IDR > 0, required)
	rateWageIDRStr := strings.TrimSpace(r.FormValue("rate_wage_idr"))
	if rateWageIDRStr == "" {
		s.renderSettingsError(w, r, "rate_wage wajib diisi (rupiah)")
		return
	}
	rateWageIDR, err1 := strconv.ParseInt(rateWageIDRStr, 10, 64)
	if err1 != nil || rateWageIDR <= 0 {
		s.renderSettingsError(w, r, "rate_wage harus bernilai lebih dari 0")
		return
	}

	// 2. Validate rate_referral (IDR >= 0)
	rateRefIDRStr := strings.TrimSpace(r.FormValue("rate_referral_idr"))
	var rateRefIDR int64
	if rateRefIDRStr != "" {
		var err2 error
		rateRefIDR, err2 = strconv.ParseInt(rateRefIDRStr, 10, 64)
		if err2 != nil || rateRefIDR < 0 {
			s.renderSettingsError(w, r, "rate_referral tidak boleh negatif")
			return
		}
	}

	// 3. Validate rate_idr_to_usdt (IDR per 1 USDT, > 0)
	rateIDRtoUSDTStr := strings.TrimSpace(r.FormValue("rate_idr_to_usdt"))
	if rateIDRtoUSDTStr == "" {
		s.renderSettingsError(w, r, "rate_idr_to_usdt wajib diisi")
		return
	}
	rateIDRtoUSDT, convErr := strconv.ParseInt(rateIDRtoUSDTStr, 10, 64)
	if convErr != nil || rateIDRtoUSDT <= 0 {
		s.renderSettingsError(w, r, "rate_idr_to_usdt harus bernilai lebih dari 0 (contoh: 15000)")
		return
	}

	// 4. Validate max_accounts_per_worker (1 to 50, required)
	maxAccountsStr := strings.TrimSpace(r.FormValue("max_accounts_per_worker"))
	if maxAccountsStr == "" {
		s.renderSettingsError(w, r, "max_accounts_per_worker wajib diisi")
		return
	}
	maxAccounts, convErr2 := strconv.Atoi(maxAccountsStr)
	if convErr2 != nil || maxAccounts < 1 || maxAccounts > 50 {
		s.renderSettingsError(w, r, "max_accounts_per_worker harus antara 1 sampai 50")
		return
	}

	// Waktu pembaruan/konfirmasi kurs otomatis disegarkan setiap kali admin menyimpan pengaturan
	kursUpdatedAt := time.Now().Format(time.RFC3339)

	// 5. Validate default_country (ISO 3166-1 alpha-2, valid region)
	defaultCountry := strings.ToUpper(strings.TrimSpace(r.FormValue("default_country")))
	if defaultCountry == "" {
		defaultCountry = "ID"
	}
	if !phonenumbers.GetSupportedRegions()[defaultCountry] {
		s.renderSettingsError(w, r, "Negara bawaan tidak valid")
		return
	}

	// Save settings with audit log
	ip := getIP(r)
	updates := []struct {
		key   string
		value interface{}
	}{
		{"rate_wage", rateWageIDR},
		{"rate_referral", rateRefIDR},
		{"rate_idr_to_usdt", rateIDRtoUSDT},
		{"rate_idr_to_usdt_updated_at", kursUpdatedAt},
		{"max_accounts_per_worker", maxAccounts},
		{"default_country", defaultCountry},
	}

	for _, up := range updates {
		if err := s.updateSettingWithAudit(r.Context(), admin.ID, up.key, up.value, ip); err != nil {
			slog.Error("Gagal menyimpan pengaturan sistem", "err", err, "key", up.key)
			s.renderSettingsError(w, r, "Gagal menyimpan pengaturan. Silakan coba lagi.")
			return
		}
	}

	http.Redirect(w, r, "/admin/settings?msg=Pengaturan+berhasil+disimpan", http.StatusSeeOther)
}

func (s *Server) renderSettingsError(w http.ResponseWriter, r *http.Request, errMsg string) {
	settings, _ := s.readSettingsFromDB(r.Context())
	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	viewData := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		ActiveNav string
		Settings  *SettingsViewData
		Message   string
		Error     string
		Nonce     string
		T         func(string) string
	}{
		Lang:      lang,
		Title:     "Pengaturan Sistem - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		Nonce:     getNonce(r),
		ActiveNav: "settings",
		Settings:  settings,
		Message:   "",
		Error:     errMsg,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.WriteHeader(http.StatusBadRequest)
	s.Tmpl["admin_settings.html"].ExecuteTemplate(w, "base.html", viewData)
}

func (s *Server) updateSettingWithAudit(ctx context.Context, adminID int64, key string, newVal interface{}, ip string) error {
	newJSON, err := json.Marshal(newVal)
	if err != nil {
		return err
	}

	var oldVal *string
	err = s.DB.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = $1", key).Scan(&oldVal)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	_, err = s.DB.Pool.Exec(ctx, `
		INSERT INTO settings (key, value, updated_at, updated_by)
		VALUES ($1, $2::jsonb, CURRENT_TIMESTAMP, $3)
		ON CONFLICT (key) DO UPDATE
		SET value = $2::jsonb, updated_at = CURRENT_TIMESTAMP, updated_by = $3
	`, key, string(newJSON), adminID)
	if err != nil {
		return err
	}

	var oldParsed interface{}
	if oldVal != nil {
		_ = json.Unmarshal([]byte(*oldVal), &oldParsed)
	}

	metaObj := map[string]interface{}{
		"old": oldParsed,
		"new": newVal,
	}
	metaBytes, _ := json.Marshal(metaObj)

	_, err = s.DB.Pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta, ip)
		VALUES ($1, 'setting_update', 'settings', $2, $3::jsonb, $4)
	`, adminID, key, string(metaBytes), ip)
	return err
}

func (s *Server) readSettingsFromDB(ctx context.Context) (*SettingsViewData, error) {
	data := &SettingsViewData{}

	rows, err := s.DB.Pool.Query(ctx, "SELECT key, value::text FROM settings WHERE key IN ('rate_wage', 'rate_referral', 'rate_idr_to_usdt', 'rate_idr_to_usdt_updated_at', 'max_accounts_per_worker', 'default_country')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var key, valStr string
		if err := rows.Scan(&key, &valStr); err != nil {
			continue
		}

		switch key {
		case "max_accounts_per_worker":
			var v int
			if err := json.Unmarshal([]byte(valStr), &v); err == nil {
				data.MaxAccountsPerWorker = &v
			}
		case "rate_wage":
			var intVal int64
			if err := json.Unmarshal([]byte(valStr), &intVal); err == nil && intVal > 0 {
				data.RateWageIDR = &intVal
			} else {
				var rw map[string]int64
				if err := json.Unmarshal([]byte(valStr), &rw); err == nil {
					if idr, ok := rw["IDR"]; ok {
						val := idr
						data.RateWageIDR = &val
					}
				}
			}
		case "rate_referral":
			var intVal int64
			if err := json.Unmarshal([]byte(valStr), &intVal); err == nil && intVal >= 0 {
				data.RateReferralIDR = &intVal
			} else {
				var rr map[string]int64
				if err := json.Unmarshal([]byte(valStr), &rr); err == nil {
					if idr, ok := rr["IDR"]; ok {
						val := idr
						data.RateReferralIDR = &val
					}
				}
			}
		case "rate_idr_to_usdt":
			var v int64
			if err := json.Unmarshal([]byte(valStr), &v); err == nil {
				data.RateIDRtoUSDT = &v
			}
		case "rate_idr_to_usdt_updated_at":
			var s string
			if err := json.Unmarshal([]byte(valStr), &s); err == nil {
				data.RateIDRtoUSDTUpdatedAt = s
			} else {
				data.RateIDRtoUSDTUpdatedAt = strings.Trim(valStr, `"`)
			}
		case "default_country":
			var c string
			if err := json.Unmarshal([]byte(valStr), &c); err == nil && c != "" {
				data.DefaultCountry = c
			} else {
				data.DefaultCountry = strings.Trim(valStr, `"`)
			}
		}
	}

	if data.DefaultCountry == "" {
		data.DefaultCountry = "ID"
	}
	data.CountryOptions = numbers.SupportedCountryOptions()

	if data.RateIDRtoUSDT != nil {
		data.RateIDRtoUSDTDisplay = money.FormatIDR(*data.RateIDRtoUSDT)
	}
	if data.RateIDRtoUSDTUpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, data.RateIDRtoUSDTUpdatedAt); err == nil {
			t = t.In(timeutil.JakartaLocation())
			data.RateIDRtoUSDTUpdatedAt = t.Format("02 Jan 2006 15:04 WIB")
		}
	}

	return data, nil
}

// --- Admin Wallet Handler ---

type WalletEntryRow struct {
	ID        int64     `json:"id"`
	Currency  string    `json:"currency"`
	Amount    int64     `json:"amount"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

type WalletViewData struct {
	BalanceIDR  int64
	BalanceUSDT int64
	TopupIDR    int64
	TopupUSDT   int64
	SpentIDR    int64
	SpentUSDT   int64
	Entries     []WalletEntryRow
}

func (s *Server) HandleAdminWalletGET(w http.ResponseWriter, r *http.Request) {
	walletData, err := s.fetchWalletData(r.Context())
	if err != nil {
		slog.Error("Gagal memuat data dompet admin", "err", err)
		http.Error(w, "Gagal memuat dompet", http.StatusInternalServerError)
		return
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	flashMsg := r.URL.Query().Get("msg")
	errMsg := r.URL.Query().Get("err")

	viewData := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		Nonce     string
		ActiveNav string
		Wallet    *WalletViewData
		Message   string
		Error     string
		T         func(string) string
	}{
		Lang:      lang,
		Title:     "Dompet Admin - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		Nonce:     getNonce(r),
		ActiveNav: "wallet",
		Wallet:    walletData,
		Message:   flashMsg,
		Error:     errMsg,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["admin_wallet.html"].ExecuteTemplate(w, "base.html", viewData)
}

func (s *Server) HandleAdminWalletTopupPOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Form tidak valid", http.StatusBadRequest)
		return
	}

	currency := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
	if currency != "IDR" && currency != "USDT" {
		http.Redirect(w, r, "/admin/wallet?err=Mata+uang+tidak+valid", http.StatusSeeOther)
		return
	}

	amountStr := strings.TrimSpace(r.FormValue("amount"))
	amount, err := strconv.ParseInt(amountStr, 10, 64)
	if err != nil || amount <= 0 {
		http.Redirect(w, r, "/admin/wallet?err=Jumlah+harus+lebih+dari+0", http.StatusSeeOther)
		return
	}

	note := strings.TrimSpace(r.FormValue("note"))
	if note == "" {
		http.Redirect(w, r, "/admin/wallet?err=Catatan+wajib+diisi", http.StatusSeeOther)
		return
	}

	var entryID int64
	err = s.DB.Pool.QueryRow(r.Context(), `
		INSERT INTO wallet_entries (currency, amount, note, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, currency, amount, note, admin.ID).Scan(&entryID)
	if err != nil {
		slog.Error("Gagal mencatat isi ulang dompet", "err", err, "admin_id", admin.ID)
		http.Redirect(w, r, "/admin/wallet?err=Gagal+mencatat+isi+ulang", http.StatusSeeOther)
		return
	}

	metaJSON, _ := json.Marshal(map[string]interface{}{
		"currency": currency,
		"amount":   amount,
		"note":     note,
	})
	_, _ = s.DB.Pool.Exec(r.Context(), `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta, ip)
		VALUES ($1, 'wallet_topup', 'wallet_entries', $2, $3::jsonb, $4)
	`, admin.ID, strconv.FormatInt(entryID, 10), string(metaJSON), getIP(r))

	http.Redirect(w, r, "/admin/wallet?msg=Isi+ulang+berhasil+dicatat", http.StatusSeeOther)
}

func (s *Server) fetchWalletData(ctx context.Context) (*WalletViewData, error) {
	data := &WalletViewData{}

	// Query IDR stats
	err := s.DB.Pool.QueryRow(ctx, `
		SELECT 
			COALESCE((SELECT SUM(amount) FROM wallet_entries WHERE currency = 'IDR'), 0) AS topup_idr,
			COALESCE((SELECT SUM(amount) FROM ledger_entries WHERE currency = 'IDR' AND kind IN ('upah', 'referral')), 0) AS spent_idr
	`).Scan(&data.TopupIDR, &data.SpentIDR)
	if err != nil {
		return nil, err
	}
	data.BalanceIDR = data.TopupIDR - data.SpentIDR

	// Query USDT stats
	err = s.DB.Pool.QueryRow(ctx, `
		SELECT 
			COALESCE((SELECT SUM(amount) FROM wallet_entries WHERE currency = 'USDT'), 0) AS topup_usdt,
			COALESCE((SELECT SUM(amount) FROM ledger_entries WHERE currency = 'USDT' AND kind IN ('upah', 'referral')), 0) AS spent_usdt
	`).Scan(&data.TopupUSDT, &data.SpentUSDT)
	if err != nil {
		return nil, err
	}
	data.BalanceUSDT = data.TopupUSDT - data.SpentUSDT

	// Recent entries
	rows, err := s.DB.Pool.Query(ctx, `
		SELECT we.id, we.currency, we.amount, we.note, we.created_at, COALESCE(u.username, u.email)
		FROM wallet_entries we
		JOIN users u ON u.id = we.created_by
		ORDER BY we.id DESC
		LIMIT 50
	`)
	if err == nil {
		for rows.Next() {
			var r WalletEntryRow
			if err := rows.Scan(&r.ID, &r.Currency, &r.Amount, &r.Note, &r.CreatedAt, &r.CreatedBy); err == nil {
				data.Entries = append(data.Entries, r)
			}
		}
		rows.Close()
	}

	return data, nil
}

// --- Admin Withdrawals Handlers ---

type WithdrawalItemView struct {
	ID              int64
	RequestedAt     time.Time
	WorkerUsername  string
	WorkerEmail     string
	Currency        string
	AmountFormatted string
	Method          string
	Destination     string
	DestinationMasked string
	PayoutReference string
	RejectReason    string
	Status          string
	ProcessedAt     *time.Time
}

func formatIDR(n int64) string {
	return money.FormatIDR(n)
}

func formatUSDT(n int64) string {
	return money.FormatUSDT(n)
}

func (s *Server) HandleAdminWithdrawalsGET(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	lang := "id"
	if admin.Language != "" {
		lang = admin.Language
	}

	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	if statusFilter == "" {
		statusFilter = "menunggu"
	}
	viewIDStr := strings.TrimSpace(r.URL.Query().Get("view_id"))
	viewID, _ := strconv.ParseInt(viewIDStr, 10, 64)

	// Pending totals
	var pendingIDRTotal, pendingIDRCount int64
	var pendingUSDTTotal, pendingUSDTCount int64

	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0), COUNT(*) 
		FROM withdrawals 
		WHERE status = 'menunggu' AND currency = 'IDR'
	`).Scan(&pendingIDRTotal, &pendingIDRCount)

	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0), COUNT(*) 
		FROM withdrawals 
		WHERE status = 'menunggu' AND currency = 'USDT'
	`).Scan(&pendingUSDTTotal, &pendingUSDTCount)

	// Build query
	var query string
	var args []interface{}
	if statusFilter == "semua" {
		query = `
			SELECT w.id, w.currency, w.amount, w.method, w.destination_encrypted, w.status, w.requested_at, w.processed_at,
			       COALESCE(u.username, u.email), u.email, COALESCE(w.payout_reference, ''), COALESCE(w.reject_reason, '')
			FROM withdrawals w
			JOIN users u ON u.id = w.user_id
			ORDER BY w.id DESC
			LIMIT 100
		`
	} else {
		query = `
			SELECT w.id, w.currency, w.amount, w.method, w.destination_encrypted, w.status, w.requested_at, w.processed_at,
			       COALESCE(u.username, u.email), u.email, COALESCE(w.payout_reference, ''), COALESCE(w.reject_reason, '')
			FROM withdrawals w
			JOIN users u ON u.id = w.user_id
			WHERE w.status = $1
			ORDER BY w.id DESC
			LIMIT 100
		`
		args = append(args, statusFilter)
	}

	rows, err := s.DB.Pool.Query(ctx, query, args...)
	var items []WithdrawalItemView
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var it WithdrawalItemView
			var destEnc string
			var rawAmount int64
			if err := rows.Scan(&it.ID, &it.Currency, &rawAmount, &it.Method, &destEnc, &it.Status, &it.RequestedAt, &it.ProcessedAt, &it.WorkerUsername, &it.WorkerEmail, &it.PayoutReference, &it.RejectReason); err == nil {
				if it.Currency == "IDR" {
					it.AmountFormatted = formatIDR(rawAmount)
				} else {
					it.AmountFormatted = formatUSDT(rawAmount)
				}

				// Dekripsi hanya jika admin membuka detail spesifik (viewID)
				if viewID > 0 && it.ID == viewID {
					dest, decErr := s.DecryptDestination(destEnc)
					if decErr != nil {
						it.Destination = "tujuan tidak terbaca, minta pekerja mengisi ulang"
					} else {
						it.Destination = dest
					}
					it.DestinationMasked = it.Destination
					s.logAudit(&admin.ID, "view_destination", "withdrawals", strconv.FormatInt(it.ID, 10), "admin opened withdrawal detail")
				} else {
					// Di daftar antrean umum, tujuan disamarkan
					if dec, decErr := s.DecryptDestination(destEnc); decErr == nil {
						it.DestinationMasked = MaskDestination(dec, it.Method)
					} else {
						it.DestinationMasked = "tujuan tidak terbaca, minta pekerja mengisi ulang"
					}
					it.Destination = it.DestinationMasked
				}

				items = append(items, it)
			}
		}
	}

	viewData := struct {
		Lang             string
		Title            string
		Theme            string
		CSRFToken        string
		Nonce            string
		ActiveNav        string
		PendingIDRTotal  string
		PendingIDRCount  int64
		PendingUSDTTotal string
		PendingUSDTCount int64
		CurrentStatus    string
		Message          string
		Error            string
		Items            []WithdrawalItemView
		T                func(string) string
	}{
		Lang:             lang,
		Title:            "Permintaan Penarikan - GO BLAST",
		Theme:            getTheme(r),
		CSRFToken:        getCSRF(r),
		Nonce:            getNonce(r),
		ActiveNav:        "withdrawals",
		PendingIDRTotal:  formatIDR(pendingIDRTotal),
		PendingIDRCount:  pendingIDRCount,
		PendingUSDTTotal: formatUSDT(pendingUSDTTotal),
		PendingUSDTCount: pendingUSDTCount,
		CurrentStatus:    statusFilter,
		Message:          r.URL.Query().Get("msg"),
		Error:            r.URL.Query().Get("err"),
		Items:            items,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["admin_withdrawals.html"].ExecuteTemplate(w, "base.html", viewData)
}

func (s *Server) HandleAdminWithdrawalViewDestinationPOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	withdrawalID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID penarikan tidak valid", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	var destEnc string
	err = s.DB.Pool.QueryRow(ctx, "SELECT destination_encrypted FROM withdrawals WHERE id = $1", withdrawalID).Scan(&destEnc)
	if err != nil {
		http.Error(w, "Data penarikan tidak ditemukan", http.StatusNotFound)
		return
	}

	dest, decErr := s.DecryptDestination(destEnc)
	if decErr != nil {
		dest = "tujuan tidak terbaca, minta pekerja mengisi ulang"
	}

	s.logAudit(&admin.ID, "view_destination", "withdrawals", idStr, "admin revealed withdrawal destination")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "ok",
		"destination": dest,
	})
}

func (s *Server) HandleAdminWithdrawalApprovePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	withdrawalID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=ID+penarikan+tidak+valid", http.StatusSeeOther)
		return
	}

	payoutRef := strings.TrimSpace(r.FormValue("payout_reference"))

	ctx := r.Context()
	var status, currency string
	var amount int64
	err = s.DB.Pool.QueryRow(ctx, "SELECT status, amount, currency FROM withdrawals WHERE id = $1 FOR UPDATE", withdrawalID).Scan(&status, &amount, &currency)
	if err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=Data+penarikan+tidak+ditemukan", http.StatusSeeOther)
		return
	}

	if status != "menunggu" {
		http.Redirect(w, r, "/admin/withdrawals?err=Hanya+penarikan+berstatus+menunggu+yang+dapat+disetujui", http.StatusSeeOther)
		return
	}

	if currency == "USDT" && payoutRef == "" {
		http.Redirect(w, r, "/admin/withdrawals?err=Bukti+transfer+/+TXID+(payout_reference)+wajib+diisi+untuk+penarikan+USDT", http.StatusSeeOther)
		return
	}

	var payoutRefPtr *string
	if payoutRef != "" {
		payoutRefPtr = &payoutRef
	}

	_, err = s.DB.Pool.Exec(ctx, `
		UPDATE withdrawals 
		SET status = 'selesai', payout_reference = $1, processed_at = CURRENT_TIMESTAMP 
		WHERE id = $2
	`, payoutRefPtr, withdrawalID)
	if err != nil {
		slog.Error("Gagal menyetujui penarikan", "err", err, "withdrawal_id", withdrawalID)
		http.Redirect(w, r, "/admin/withdrawals?err=Gagal+menyetujui+penarikan", http.StatusSeeOther)
		return
	}

	meta := fmt.Sprintf("approved %d %s", amount, currency)
	if payoutRef != "" {
		meta += fmt.Sprintf(" (ref: %s)", payoutRef)
	}
	s.logAudit(&admin.ID, "withdrawal_approve", "withdrawals", idStr, meta)
	http.Redirect(w, r, "/admin/withdrawals?msg=Penarikan+#"+idStr+"+berhasil+disetujui", http.StatusSeeOther)
}

func (s *Server) HandleAdminWithdrawalRejectPOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	withdrawalID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=ID+penarikan+tidak+valid", http.StatusSeeOther)
		return
	}

	rejectReason := strings.TrimSpace(r.FormValue("reject_reason"))
	if rejectReason == "" {
		http.Redirect(w, r, "/admin/withdrawals?err=Alasan+penolakan+(reject_reason)+wajib+diisi", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=Kesalahan+sistem", http.StatusSeeOther)
		return
	}
	defer tx.Rollback(ctx)

	var userID, amount int64
	var status, currency string
	err = tx.QueryRow(ctx, "SELECT user_id, status, amount, currency FROM withdrawals WHERE id = $1 FOR UPDATE", withdrawalID).Scan(&userID, &status, &amount, &currency)
	if err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=Data+penarikan+tidak+ditemukan", http.StatusSeeOther)
		return
	}

	if status != "menunggu" {
		http.Redirect(w, r, "/admin/withdrawals?err=Hanya+penarikan+berstatus+menunggu+yang+dapat+ditolak", http.StatusSeeOther)
		return
	}

	// 1. Ubah status jadi ditolak dan simpan reject_reason
	_, err = tx.Exec(ctx, `
		UPDATE withdrawals 
		SET status = 'ditolak', reject_reason = $1, processed_at = CURRENT_TIMESTAMP 
		WHERE id = $2
	`, rejectReason, withdrawalID)
	if err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=Gagal+mengubah+status+penarikan", http.StatusSeeOther)
		return
	}

	// 2. Refund saldo pekerja di ledger_entries (kind = 'koreksi')
	refundNote := fmt.Sprintf("Pengembalian dana penarikan #%d ditolak: %s", withdrawalID, rejectReason)
	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'koreksi', $2, $3, $4)
	`, userID, currency, amount, refundNote)
	if err != nil {
		slog.Error("Gagal mengembalikan saldo penarikan", "err", err, "withdrawal_id", withdrawalID)
		http.Redirect(w, r, "/admin/withdrawals?err=Gagal+mengembalikan+saldo", http.StatusSeeOther)
		return
	}

	// 3. Masukkan notifikasi ke pekerja di tabel notifications
	var formattedAmount string
	if currency == "IDR" {
		formattedAmount = "Rp " + formatIDR(amount)
	} else {
		formattedAmount = formatUSDT(amount) + " USDT"
	}
	notifTitle := "Penarikan Dana Ditolak"
	notifMsg := fmt.Sprintf("Pengajuan penarikan #%d sebesar %s ditolak. Alasan: %s. Saldo telah dikembalikan ke akun Anda.", withdrawalID, formattedAmount, rejectReason)
	_, err = tx.Exec(ctx, `
		INSERT INTO notifications (user_id, title, message, kind, ref_id)
		VALUES ($1, $2, $3, 'system', $4)
	`, userID, notifTitle, notifMsg, withdrawalID)
	if err != nil {
		slog.Error("Gagal membuat notifikasi penolakan penarikan", "err", err, "withdrawal_id", withdrawalID)
		http.Redirect(w, r, "/admin/withdrawals?err=Gagal+membuat+notifikasi", http.StatusSeeOther)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=Gagal+menyelesaikan+transaksi+penolakan", http.StatusSeeOther)
		return
	}

	s.logAudit(&admin.ID, "withdrawal_reject", "withdrawals", idStr, fmt.Sprintf("rejected: %s", rejectReason))
	http.Redirect(w, r, "/admin/withdrawals?msg=Penarikan+#"+idStr+"+ditolak+dan+saldo+telah+dikembalikan", http.StatusSeeOther)
}

// --- Admin Workers Handler ---

type WorkerListItem struct {
	ID                   int64  `json:"id"`
	Username             string `json:"username"`
	Email                string `json:"email"`
	Status               string `json:"status"`
	AccountCount         int    `json:"account_count"`
	ActiveAccountCount   int    `json:"active_account_count"`
	SentCount            int    `json:"sent_count"`
	BalanceIDR          int64  `json:"balance_idr"`
	BalanceUSDT         int64  `json:"balance_usdt"`
	BalanceIDRFormatted  string `json:"balance_idr_formatted"`
	BalanceUSDTFormatted string `json:"balance_usdt_formatted"`
}

func (s *Server) HandleAdminWorkers(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/workers" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	var query string
	var args []interface{}
	if q != "" {
		normQ := withdraw.NormalizeDestination(q)
		destHash := withdraw.HashDestination(normQ, s.EncryptionKey)

		query = `
			SELECT u.id, COALESCE(u.username, u.email), COALESCE(u.email, '-'), u.status,
			       (SELECT COUNT(*) FROM wa_accounts WHERE worker_id = u.id) AS account_count,
			       (SELECT COUNT(*) FROM wa_accounts WHERE worker_id = u.id AND status = 'terhubung') AS active_account_count,
			       (SELECT COUNT(*) FROM ledger_entries WHERE user_id = u.id AND kind = 'upah') AS sent_count,
			       user_balance(u.id, 'IDR') AS balance_idr,
			       user_balance(u.id, 'USDT') AS balance_usdt
			FROM users u
			WHERE u.role = 'worker' AND (u.username ILIKE $1 OR u.email ILIKE $1 OR u.payout_dest_hash = $2)
			ORDER BY u.id DESC
		`
		args = append(args, "%"+q+"%", destHash)
	} else {
		query = `
			SELECT u.id, COALESCE(u.username, u.email), COALESCE(u.email, '-'), u.status,
			       (SELECT COUNT(*) FROM wa_accounts WHERE worker_id = u.id) AS account_count,
			       (SELECT COUNT(*) FROM wa_accounts WHERE worker_id = u.id AND status = 'terhubung') AS active_account_count,
			       (SELECT COUNT(*) FROM ledger_entries WHERE user_id = u.id AND kind = 'upah') AS sent_count,
			       user_balance(u.id, 'IDR') AS balance_idr,
			       user_balance(u.id, 'USDT') AS balance_usdt
			FROM users u
			WHERE u.role = 'worker'
			ORDER BY u.id DESC
		`
	}

	rows, err := s.DB.Pool.Query(ctx, query, args...)
	if err != nil {
		slog.Error("Gagal memuat pekerja dari database", "err", err)
		http.Error(w, "Gagal memuat pekerja", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var workers []WorkerListItem
	for rows.Next() {
		var item WorkerListItem
		if err := rows.Scan(&item.ID, &item.Username, &item.Email, &item.Status, &item.AccountCount, &item.ActiveAccountCount, &item.SentCount, &item.BalanceIDR, &item.BalanceUSDT); err != nil {
			slog.Error("Failed to scan worker row in admin list", "err", err)
			continue
		}
		item.BalanceIDRFormatted = formatIDR(item.BalanceIDR)
		item.BalanceUSDTFormatted = formatUSDT(item.BalanceUSDT)
		workers = append(workers, item)
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	viewData := struct {
		Lang        string
		Title       string
		Theme       string
		CSRFToken   string
		Nonce       string
		ActiveNav   string
		SearchQuery string
		Workers     []WorkerListItem
		T           func(string) string
	}{
		Lang:        lang,
		Title:       "Daftar Pekerja - GO BLAST",
		Theme:       getTheme(r),
		CSRFToken:   getCSRF(r),
		Nonce:       getNonce(r),
		ActiveNav:   "workers",
		SearchQuery: q,
		Workers:     workers,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["admin_workers.html"].ExecuteTemplate(w, "base.html", viewData)
}

func (s *Server) HandleAdminWorkerDisablePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	workerID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	// 1. Verify worker exists
	var role, currentStatus string
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT role, status FROM users WHERE id = $1", workerID).Scan(&role, &currentStatus)
	if err != nil || role != "worker" {
		http.Error(w, "Pekerja tidak ditemukan", http.StatusNotFound)
		return
	}

	// 2. Fetch all WhatsApp accounts for this worker
	rows, err := s.DB.Pool.Query(r.Context(), "SELECT id FROM wa_accounts WHERE worker_id = $1", workerID)
	var accountIDs []int64
	if err == nil {
		for rows.Next() {
			var accID int64
			if err := rows.Scan(&accID); err == nil {
				accountIDs = append(accountIDs, accID)
			}
		}
		rows.Close()
	}

	// 3. Stop each account in engine & Disconnect in manager
	for _, accID := range accountIDs {
		if s.WorkCtrl != nil {
			s.WorkCtrl.StopAccount(accID)
		}
		if s.WaManager != nil {
			_ = s.WaManager.DisconnectAccount(r.Context(), accID)
		}
	}

	// 4. Delete all web_sessions for this worker
	_, _ = s.DB.Pool.Exec(r.Context(), "DELETE FROM web_sessions WHERE user_id = $1", workerID)

	// 5. Update user status to disabled
	_, _ = s.DB.Pool.Exec(r.Context(), "UPDATE users SET status = 'disabled' WHERE id = $1", workerID)

	// 6. Audit log
	metaJSON, _ := json.Marshal(map[string]interface{}{
		"status":          "disabled",
		"stopped_accounts": len(accountIDs),
	})
	_, _ = s.DB.Pool.Exec(r.Context(), `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta, ip)
		VALUES ($1, 'worker_disable', 'users', $2, $3::jsonb, $4)
	`, admin.ID, idStr, string(metaJSON), getIP(r))

	http.Redirect(w, r, "/admin/workers", http.StatusSeeOther)
}

func (s *Server) HandleAdminWorkerEnablePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	workerID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	var role string
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT role FROM users WHERE id = $1", workerID).Scan(&role)
	if err != nil || role != "worker" {
		http.Error(w, "Pekerja tidak ditemukan", http.StatusNotFound)
		return
	}

	_, _ = s.DB.Pool.Exec(r.Context(), "UPDATE users SET status = 'active' WHERE id = $1", workerID)

	metaJSON, _ := json.Marshal(map[string]interface{}{
		"status": "active",
	})
	_, _ = s.DB.Pool.Exec(r.Context(), `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta, ip)
		VALUES ($1, 'worker_enable', 'users', $2, $3::jsonb, $4)
	`, admin.ID, idStr, string(metaJSON), getIP(r))

	http.Redirect(w, r, "/admin/workers", http.StatusSeeOther)
}

// --- Support Tickets & Broadcasts Management ---

type AdminSupportTicketRow struct {
	ID           int64
	UserID       int64
	Username     string
	Email        string
	Category     string
	Message      string
	Status       string
	AdminNotes   string
	ReplyMessage string
	RepliedAt    string
	CreatedAt    string
	ResolvedAt   string
}

type AdminBroadcastRow struct {
	ID        int64
	Title     string
	Message   string
	Badge     string
	IsActive  bool
	CreatedAt string
}

func (s *Server) HandleAdminTicketsGET(w http.ResponseWriter, r *http.Request) {
	adminUser, ok := currentUser(r)
	if !ok || adminUser.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	lang := adminUser.Language
	if lang == "" {
		lang = "id"
	}
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	if statusFilter == "" {
		statusFilter = "all"
	}

	query := `
		SELECT st.id, st.user_id, COALESCE(u.username, ''), COALESCE(u.email, ''),
		       st.category, st.message, st.status, COALESCE(st.admin_notes, ''),
		       st.created_at, st.resolved_at,
		       COALESCE(st.reply_message, ''), st.replied_at
		FROM support_tickets st
		LEFT JOIN users u ON u.id = st.user_id
	`
	if statusFilter == "open" {
		query += " WHERE st.status = 'open'"
	} else if statusFilter == "resolved" {
		query += " WHERE st.status = 'resolved'"
	}
	query += " ORDER BY st.id DESC LIMIT 100"

	rows, err := s.DB.Pool.Query(ctx, query)
	var tickets []AdminSupportTicketRow
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t AdminSupportTicketRow
			var cAt time.Time
			var rAt *time.Time
			var rplAt *time.Time
			if err := rows.Scan(&t.ID, &t.UserID, &t.Username, &t.Email, &t.Category, &t.Message, &t.Status, &t.AdminNotes, &cAt, &rAt, &t.ReplyMessage, &rplAt); err == nil {
				t.CreatedAt = cAt.Format("02 Jan 2006 15:04")
				if rAt != nil {
					t.ResolvedAt = rAt.Format("02 Jan 2006 15:04")
				}
				if rplAt != nil {
					t.RepliedAt = rplAt.Format("02 Jan 2006 15:04")
				}
				tickets = append(tickets, t)
			}
		}
	}

	data := struct {
		Lang         string
		Title        string
		Theme        string
		CSRFToken    string
		ActiveNav    string
		StatusFilter string
		Tickets      []AdminSupportTicketRow
		Success      string
		Error        string
		Nonce        string
		T            func(string) string
	}{
		Lang:         lang,
		Title:        "Laporan Pengguna - Admin",
		Theme:        getTheme(r),
		CSRFToken:    getCSRF(r),
		Nonce:        getNonce(r),
		ActiveNav:    "tickets",
		StatusFilter: statusFilter,
		Tickets:      tickets,
		Success:      r.URL.Query().Get("success"),
		Error:        r.URL.Query().Get("error"),
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["admin_tickets.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleAdminTicketResolvePOST(w http.ResponseWriter, r *http.Request) {
	adminUser, ok := currentUser(r)
	if !ok || adminUser.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin/tickets?error=ID+tiket+tidak+valid", http.StatusSeeOther)
		return
	}

	replyMsg := strings.TrimSpace(r.FormValue("reply_message"))
	if replyMsg == "" {
		replyMsg = "Laporan Anda telah selesai ditindaklanjuti oleh admin."
	}

	now := time.Now()
	var ticketOwnerID int64
	var ticketCat string
	_ = s.DB.Pool.QueryRow(r.Context(), "SELECT user_id, category FROM support_tickets WHERE id = $1", id).Scan(&ticketOwnerID, &ticketCat)

	_, err = s.DB.Pool.Exec(r.Context(), `
		UPDATE support_tickets
		SET status = 'resolved',
		    reply_message = $1,
		    replied_by = $2,
		    replied_at = $3,
		    resolved_at = $3
		WHERE id = $4
	`, replyMsg, adminUser.ID, now, id)
	if err != nil {
		slog.Error("Gagal memperbarui status tiket", "err", err, "ticket_id", id)
		http.Redirect(w, r, "/admin/tickets?error=Gagal+memperbarui+status+tiket", http.StatusSeeOther)
		return
	}

	if ticketOwnerID > 0 {
		notifTitle := fmt.Sprintf("Balasan Laporan Tiket #%d (%s)", id, ticketCat)
		_, _ = s.DB.Pool.Exec(r.Context(), `
			INSERT INTO notifications (user_id, title, message, kind, ref_id)
			VALUES ($1, $2, $3, 'ticket_reply', $4)
		`, ticketOwnerID, notifTitle, replyMsg, id)
	}

	s.logAudit(&adminUser.ID, "resolve_ticket", "support_tickets", strconv.FormatInt(id, 10), "Status diubah menjadi resolved: "+replyMsg)
	http.Redirect(w, r, "/admin/tickets?success=Tiket+berhasil+dibalas+dan+diselesaikan", http.StatusSeeOther)
}

func (s *Server) HandleAdminBroadcastsGET(w http.ResponseWriter, r *http.Request) {
	adminUser, ok := currentUser(r)
	if !ok || adminUser.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	lang := adminUser.Language
	if lang == "" {
		lang = "id"
	}

	rows, err := s.DB.Pool.Query(ctx, `
		SELECT id, title, message, badge, is_active, created_at
		FROM broadcasts
		ORDER BY id DESC
		LIMIT 50
	`)
	var broadcasts []AdminBroadcastRow
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var b AdminBroadcastRow
			var cAt time.Time
			if err := rows.Scan(&b.ID, &b.Title, &b.Message, &b.Badge, &b.IsActive, &cAt); err == nil {
				b.CreatedAt = cAt.Format("02 Jan 2006 15:04")
				broadcasts = append(broadcasts, b)
			}
		}
	}

	data := struct {
		Lang       string
		Title      string
		Theme      string
		CSRFToken  string
		ActiveNav  string
		Broadcasts []AdminBroadcastRow
		Success    string
		Error      string
		Nonce      string
		T          func(string) string
	}{
		Lang:       lang,
		Title:      "Kelola Broadcast - Admin",
		Theme:      getTheme(r),
		CSRFToken:  getCSRF(r),
		Nonce:      getNonce(r),
		ActiveNav:  "broadcasts",
		Broadcasts: broadcasts,
		Success:    r.URL.Query().Get("success"),
		Error:      r.URL.Query().Get("error"),
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["admin_broadcasts.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleAdminBroadcastNewPOST(w http.ResponseWriter, r *http.Request) {
	adminUser, ok := currentUser(r)
	if !ok || adminUser.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	message := strings.TrimSpace(r.FormValue("message"))
	badge := strings.TrimSpace(r.FormValue("badge"))
	isActive := r.FormValue("is_active") == "true" || r.FormValue("is_active") == "on" || r.FormValue("is_active") == "1"

	if title == "" {
		http.Redirect(w, r, "/admin/broadcasts?error=Judul+broadcast+tidak+boleh+kosong", http.StatusSeeOther)
		return
	}
	if message == "" {
		http.Redirect(w, r, "/admin/broadcasts?error=Pesan+broadcast+tidak+boleh+kosong", http.StatusSeeOther)
		return
	}
	if badge == "" {
		badge = "INFO"
	}

	var bID int64
	err := s.DB.Pool.QueryRow(r.Context(), `
		INSERT INTO broadcasts (title, message, badge, is_active, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, title, message, badge, isActive, adminUser.ID).Scan(&bID)
	if err != nil {
		slog.Error("Gagal membuat broadcast", "err", err)
		http.Redirect(w, r, "/admin/broadcasts?error=Gagal+membuat+broadcast", http.StatusSeeOther)
		return
	}

	s.logAudit(&adminUser.ID, "create_broadcast", "broadcasts", strconv.FormatInt(bID, 10), title)

	// Masukkan pengumuman ke sistem notifikasi lonceng seluruh pekerja aktif
	if isActive {
		ctx := r.Context()
		rows, qErr := s.DB.Pool.Query(ctx, "SELECT id FROM users WHERE role = 'worker' AND status = 'active'")
		if qErr == nil {
			defer rows.Close()
			for rows.Next() {
				var wID int64
				if err := rows.Scan(&wID); err == nil {
					_, _ = s.DB.Pool.Exec(ctx, `
						INSERT INTO notifications (user_id, title, message, kind, ref_id)
						VALUES ($1, $2, $3, 'broadcast', $4)
					`, wID, title, message, bID)
				}
			}
		}
	}

	http.Redirect(w, r, "/admin/broadcasts?success=Broadcast+berhasil+diterbitkan", http.StatusSeeOther)
}

func (s *Server) HandleAdminBroadcastTogglePOST(w http.ResponseWriter, r *http.Request) {
	adminUser, ok := currentUser(r)
	if !ok || adminUser.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin/broadcasts?error=ID+broadcast+tidak+valid", http.StatusSeeOther)
		return
	}

	var newActive bool
	var bTitle, bMessage string
	err = s.DB.Pool.QueryRow(r.Context(), `
		UPDATE broadcasts
		SET is_active = NOT is_active
		WHERE id = $1
		RETURNING is_active, title, message
	`, id).Scan(&newActive, &bTitle, &bMessage)
	if err != nil {
		slog.Error("Gagal mengubah status broadcast", "err", err, "broadcast_id", id)
		http.Redirect(w, r, "/admin/broadcasts?error=Gagal+mengubah+status+broadcast", http.StatusSeeOther)
		return
	}

	if newActive {
		ctx := r.Context()
		_, _ = s.DB.Pool.Exec(ctx, `
			INSERT INTO notifications (user_id, title, message, kind, ref_id)
			SELECT u.id, $1, $2, 'broadcast', $3
			FROM users u
			WHERE u.role = 'worker' AND u.status = 'active'
			  AND NOT EXISTS (
			      SELECT 1 FROM notifications n
			      WHERE n.user_id = u.id AND n.kind = 'broadcast' AND n.ref_id = $3
			  )
		`, bTitle, bMessage, id)
	}

	s.logAudit(&adminUser.ID, "toggle_broadcast", "broadcasts", strconv.FormatInt(id, 10), "Status broadcast di-toggle")
	http.Redirect(w, r, "/admin/broadcasts?success=Status+broadcast+berhasil+diperbarui", http.StatusSeeOther)
}

func (s *Server) HandleAdminBroadcastDeletePOST(w http.ResponseWriter, r *http.Request) {
	adminUser, ok := currentUser(r)
	if !ok || adminUser.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin/broadcasts?error=ID+broadcast+tidak+valid", http.StatusSeeOther)
		return
	}

	_, err = s.DB.Pool.Exec(r.Context(), `DELETE FROM broadcasts WHERE id = $1`, id)
	if err != nil {
		slog.Error("Gagal menghapus broadcast", "err", err, "broadcast_id", id)
		http.Redirect(w, r, "/admin/broadcasts?error=Gagal+menghapus+broadcast", http.StatusSeeOther)
		return
	}

	s.logAudit(&adminUser.ID, "delete_broadcast", "broadcasts", strconv.FormatInt(id, 10), "Broadcast dihapus")
	http.Redirect(w, r, "/admin/broadcasts?success=Broadcast+berhasil+dihapus", http.StatusSeeOther)
}

// =====================================================================
// --- Panel Maintenance & Health Check ---
// =====================================================================

type HealthCheckItem struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "peringatan", "gagal"
	Message string `json:"message"`
}

type MaintenanceHealthViewData struct {
	Lang                  string
	Title                 string
	Theme                 string
	CSRFToken             string
	ActiveNav             string
	Message               string
	Error                 string
	MaintenanceMode       bool
	LockWithdrawals       bool
	LockLinking           bool
	LockEngine            bool
	LockMessage           string
	DBOnline              bool
	DBPingLatency         string
	DBAcquiredConns       int32
	DBIdleConns           int32
	DBTotalConns          int32
	DBMaxConns            int32
	ActiveWACount         int64
	DisconnectedWACount   int64
	PendingWithdrawals    int64
	RunningCampaignsCount int64
	AvailableNumbersCount int64
	EngineStatus          string
	RecentAuditLogs       []AuditLogRow
	HealthChecks          []HealthCheckItem
	ActiveRestrictions    []ratelimit.RestrictionEntry
	Nonce                 string
	T                     func(string) string
}

type AuditLogRow struct {
	ID        int64
	ActorName string
	Action    string
	Entity    string
	EntityID  string
	Meta      string
	IP        string
	CreatedAt string
}

// RunHealthChecks menjalankan 9 pemeriksaan kesehatan sistem komprehensif.
func (s *Server) RunHealthChecks(ctx context.Context) []HealthCheckItem {
	var results []HealthCheckItem

	// 1. Database dan latensi
	startPing := time.Now()
	pingErr := s.DB.Pool.Ping(ctx)
	pingLatency := time.Since(startPing)
	if pingErr != nil {
		results = append(results, HealthCheckItem{
			Key:     "db_latency",
			Name:    "Database & Latensi",
			Status:  "gagal",
			Message: fmt.Sprintf("Koneksi database terputus: %s", pingErr.Error()),
		})
	} else if pingLatency > 500*time.Millisecond {
		results = append(results, HealthCheckItem{
			Key:     "db_latency",
			Name:    "Database & Latensi",
			Status:  "peringatan",
			Message: fmt.Sprintf("Latensi database tinggi (%.2f ms)", float64(pingLatency.Microseconds())/1000.0),
		})
	} else {
		results = append(results, HealthCheckItem{
			Key:     "db_latency",
			Name:    "Database & Latensi",
			Status:  "ok",
			Message: fmt.Sprintf("Koneksi online, latensi normal (%.2f ms)", float64(pingLatency.Microseconds())/1000.0),
		})
	}

	// 2. Versi migrasi
	var migrationVer int64
	var isApplied bool
	errMig := s.DB.Pool.QueryRow(ctx, "SELECT version_id, is_applied FROM goose_db_version ORDER BY id DESC LIMIT 1").Scan(&migrationVer, &isApplied)
	if errMig != nil {
		results = append(results, HealthCheckItem{
			Key:     "migration_version",
			Name:    "Versi Migrasi Skema",
			Status:  "gagal",
			Message: "Tabel versi goose_db_version tidak ditemukan",
		})
	} else if !isApplied {
		results = append(results, HealthCheckItem{
			Key:     "migration_version",
			Name:    "Versi Migrasi Skema",
			Status:  "gagal",
			Message: fmt.Sprintf("Migrasi versi %d belum selesai diterapkan", migrationVer),
		})
	} else if migrationVer < 18 {
		results = append(results, HealthCheckItem{
			Key:     "migration_version",
			Name:    "Versi Migrasi Skema",
			Status:  "peringatan",
			Message: fmt.Sprintf("Versi skema saat ini %d (tersedia versi lebih baru)", migrationVer),
		})
	} else {
		results = append(results, HealthCheckItem{
			Key:     "migration_version",
			Name:    "Versi Migrasi Skema",
			Status:  "ok",
			Message: fmt.Sprintf("Skema database versi %d (terbaru)", migrationVer),
		})
	}

	// 3. Mesin kirim (akun bekerja, pengiriman sukses terakhir, persentase gagal 15 menit)
	runningWorkers := 0
	if s.WorkCtrl != nil {
		runningWorkers = s.WorkCtrl.RunningCount()
	}
	var lastSentAt *time.Time
	_ = s.DB.Pool.QueryRow(ctx, "SELECT MAX(sent_at) FROM campaign_numbers WHERE status = 'terkirim'").Scan(&lastSentAt)
	var sent15, fail15 int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COUNT(CASE WHEN status = 'terkirim' THEN 1 END),
		       COUNT(CASE WHEN status = 'gagal' THEN 1 END)
		FROM campaign_numbers
		WHERE sent_at >= NOW() - INTERVAL '15 minutes'
	`).Scan(&sent15, &fail15)
	total15 := sent15 + fail15
	failRate := 0
	if total15 > 0 {
		failRate = int((fail15 * 100) / total15)
	}
	lastSentStr := "belum ada pengiriman"
	if lastSentAt != nil {
		lastSentStr = lastSentAt.Format("02 Jan 15:04:05")
	}
	if total15 >= 5 && failRate > 50 {
		results = append(results, HealthCheckItem{
			Key:     "engine",
			Name:    "Mesin Pengiriman Pesan",
			Status:  "gagal",
			Message: fmt.Sprintf("%d akun aktif, tingkat gagal 15 mnt tinggi: %d%% (%d/%d), sukses terakhir: %s", runningWorkers, failRate, fail15, total15, lastSentStr),
		})
	} else if total15 >= 5 && failRate > 20 {
		results = append(results, HealthCheckItem{
			Key:     "engine",
			Name:    "Mesin Pengiriman Pesan",
			Status:  "peringatan",
			Message: fmt.Sprintf("%d akun aktif, tingkat gagal 15 mnt: %d%% (%d/%d), sukses terakhir: %s", runningWorkers, failRate, fail15, total15, lastSentStr),
		})
	} else if total15 > 0 && fail15 > 0 {
		results = append(results, HealthCheckItem{
			Key:     "engine",
			Name:    "Mesin Pengiriman Pesan",
			Status:  "peringatan",
			Message: fmt.Sprintf("%d akun aktif, %d gagal dari %d pengiriman 15 mnt terakhir, sukses terakhir: %s", runningWorkers, fail15, total15, lastSentStr),
		})
	} else {
		results = append(results, HealthCheckItem{
			Key:     "engine",
			Name:    "Mesin Pengiriman Pesan",
			Status:  "ok",
			Message: fmt.Sprintf("%d akun aktif bekerja, kegagalan 15 mnt: %d%%, sukses terakhir: %s", runningWorkers, failRate, lastSentStr),
		})
	}

	// 4. Sesi WhatsApp (terhubung dibanding terdaftar)
	var totalWA, connectedWA int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*), COUNT(CASE WHEN status = 'terhubung' THEN 1 END) FROM wa_accounts").Scan(&totalWA, &connectedWA)
	if totalWA == 0 {
		results = append(results, HealthCheckItem{
			Key:     "wa_sessions",
			Name:    "Sesi Akun WhatsApp",
			Status:  "ok",
			Message: "Belum ada akun WhatsApp terdaftar",
		})
	} else if connectedWA == 0 {
		results = append(results, HealthCheckItem{
			Key:     "wa_sessions",
			Name:    "Sesi Akun WhatsApp",
			Status:  "peringatan",
			Message: fmt.Sprintf("0 dari %d akun WhatsApp terhubung aktif", totalWA),
		})
	} else {
		results = append(results, HealthCheckItem{
			Key:     "wa_sessions",
			Name:    "Sesi Akun WhatsApp",
			Status:  "ok",
			Message: fmt.Sprintf("%d dari %d akun WhatsApp terhubung aktif", connectedWA, totalWA),
		})
	}

	if s.WaManager != nil && s.WaManager.IsClientOutdated() {
		results = append(results, HealthCheckItem{
			Key:     "wa_client_version",
			Name:    "Versi Pustaka WhatsApp",
			Status:  "gagal",
			Message: "Pustaka WhatsApp kedaluwarsa (WhatsApp menolak koneksi: ClientOutdated). Diperlukan pembaruan modul whatsmeow.",
		})
	}

	// 5. Nomor menggantung (status dikirim lebih lama dari ClaimTimeoutSeconds)
	claimTimeout := config.ClaimTimeoutSeconds
	var staleCount int64
	errStale := s.DB.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_numbers
		WHERE status = 'dikirim' AND claimed_at < NOW() - make_interval(secs => $1)
	`, claimTimeout).Scan(&staleCount)
	if errStale != nil {
		results = append(results, HealthCheckItem{
			Key:     "stale_numbers",
			Name:    "Nomor Menggantung",
			Status:  "gagal",
			Message: "Gagal memeriksa nomor menggantung: " + errStale.Error(),
		})
	} else if staleCount > 0 {
		results = append(results, HealthCheckItem{
			Key:     "stale_numbers",
			Name:    "Nomor Menggantung",
			Status:  "gagal",
			Message: fmt.Sprintf("Ditemukan %d nomor menggantung (dikirim > %d detik)", staleCount, claimTimeout),
		})
	} else {
		results = append(results, HealthCheckItem{
			Key:     "stale_numbers",
			Name:    "Nomor Menggantung",
			Status:  "ok",
			Message: "Tidak ada nomor menggantung (semua klaim aktif valid)",
		})
	}

	// 6. Penarikan menunggu lebih dari 24 jam
	var overdueWithdrawals, totalPendingWithdrawals int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COUNT(CASE WHEN requested_at < NOW() - INTERVAL '24 hours' THEN 1 END),
		       COUNT(*)
		FROM withdrawals
		WHERE status = 'menunggu'
	`).Scan(&overdueWithdrawals, &totalPendingWithdrawals)
	if overdueWithdrawals > 0 {
		results = append(results, HealthCheckItem{
			Key:     "pending_withdrawals",
			Name:    "Antrean Penarikan Dana",
			Status:  "peringatan",
			Message: fmt.Sprintf("%d penarikan menunggu > 24 jam (total antrean: %d)", overdueWithdrawals, totalPendingWithdrawals),
		})
	} else {
		results = append(results, HealthCheckItem{
			Key:     "pending_withdrawals",
			Name:    "Antrean Penarikan Dana",
			Status:  "ok",
			Message: fmt.Sprintf("Semua penarikan tepat waktu (total antrean: %d)", totalPendingWithdrawals),
		})
	}

	// 7. Kurs USDT basi
	var rateVal int64
	var rateRaw, updatedRaw string
	_ = s.DB.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'rate_idr_to_usdt'").Scan(&rateRaw)
	_ = json.Unmarshal([]byte(rateRaw), &rateVal)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'rate_idr_to_usdt_updated_at'").Scan(&updatedRaw)
	updatedRaw = strings.Trim(updatedRaw, `"`)
	if rateVal <= 0 {
		results = append(results, HealthCheckItem{
			Key:     "usdt_rate",
			Name:    "Kurs Konversi USDT",
			Status:  "gagal",
			Message: "Kurs USDT belum diatur admin",
		})
	} else if updatedRaw == "" || updatedRaw == "null" {
		results = append(results, HealthCheckItem{
			Key:     "usdt_rate",
			Name:    "Kurs Konversi USDT",
			Status:  "peringatan",
			Message: "Timestamp pembaruan kurs USDT belum tercatat",
		})
	} else {
		tUpdated, errT := time.Parse(time.RFC3339, updatedRaw)
		if errT != nil {
			results = append(results, HealthCheckItem{
				Key:     "usdt_rate",
				Name:    "Kurs Konversi USDT",
				Status:  "peringatan",
				Message: "Format waktu kurs tidak valid",
			})
		} else {
			ageHours := time.Since(tUpdated).Hours()
			if ageHours > float64(config.KursMaxAgeHours) {
				results = append(results, HealthCheckItem{
					Key:     "usdt_rate",
					Name:    "Kurs Konversi USDT",
					Status:  "peringatan",
					Message: fmt.Sprintf("Kurs USDT kedaluwarsa (diperbarui %.1f jam lalu, batas: %d jam)", ageHours, config.KursMaxAgeHours),
				})
			} else {
				results = append(results, HealthCheckItem{
					Key:     "usdt_rate",
					Name:    "Kurs Konversi USDT",
					Status:  "ok",
					Message: fmt.Sprintf("Kurs Rp %s/USDT segar (diperbarui %.1f jam lalu)", money.FormatIDR(rateVal), ageHours),
				})
			}
		}
	}

	// 8. Uji enkripsi lalu dekripsi dengan kunci aktif
	probe := "health_check_probe"
	cipher, errEnc := withdraw.EncryptDestination(probe, s.EncryptionKey)
	if errEnc != nil {
		results = append(results, HealthCheckItem{
			Key:     "encryption",
			Name:    "Kunci Enkripsi Data",
			Status:  "gagal",
			Message: "Uji enkripsi AES-256-GCM gagal: " + errEnc.Error(),
		})
	} else {
		plain, errDec := withdraw.DecryptDestination(cipher, s.EncryptionKey)
		if errDec != nil {
			results = append(results, HealthCheckItem{
				Key:     "encryption",
				Name:    "Kunci Enkripsi Data",
				Status:  "gagal",
				Message: "Uji dekripsi AES-256-GCM gagal: " + errDec.Error(),
			})
		} else if plain != probe {
			results = append(results, HealthCheckItem{
				Key:     "encryption",
				Name:    "Kunci Enkripsi Data",
				Status:  "gagal",
				Message: "Hasil dekripsi tidak sesuai dengan teks awal",
			})
		} else {
			results = append(results, HealthCheckItem{
				Key:     "encryption",
				Name:    "Kunci Enkripsi Data",
				Status:  "ok",
				Message: "Kunci aktif AES-256-GCM valid dan berhasil diverifikasi",
			})
		}
	}

	// 9. Rekonsiliasi buku besar
	var sentCount, wageCount int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE status = 'terkirim'").Scan(&sentCount)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM ledger_entries WHERE kind = 'upah'").Scan(&wageCount)
	diffWage := sentCount != wageCount

	var negBalanceCount int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM (SELECT user_id, currency, SUM(amount) AS bal FROM ledger_entries GROUP BY user_id, currency HAVING SUM(amount) < 0) sub").Scan(&negBalanceCount)

	var globalSumIDR, usersSumIDR int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE currency = 'IDR'").Scan(&globalSumIDR)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(sub.s), 0) FROM (SELECT SUM(amount) AS s FROM ledger_entries WHERE currency = 'IDR' GROUP BY user_id) sub").Scan(&usersSumIDR)

	var globalSumUSDT, usersSumUSDT int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE currency = 'USDT'").Scan(&globalSumUSDT)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(sub.s), 0) FROM (SELECT SUM(amount) AS s FROM ledger_entries WHERE currency = 'USDT' GROUP BY user_id) sub").Scan(&usersSumUSDT)
	diffSum := (globalSumIDR != usersSumIDR) || (globalSumUSDT != usersSumUSDT)

	if negBalanceCount > 0 {
		results = append(results, HealthCheckItem{
			Key:     "ledger_reconciliation",
			Name:    "Rekonsiliasi Buku Besar",
			Status:  "gagal",
			Message: fmt.Sprintf("Terdeteksi %d akun pekerja bersaldo negatif", negBalanceCount),
		})
	} else if diffWage {
		results = append(results, HealthCheckItem{
			Key:     "ledger_reconciliation",
			Name:    "Rekonsiliasi Buku Besar",
			Status:  "gagal",
			Message: fmt.Sprintf("Selisih nomor terkirim vs upah (%d terkirim != %d upah)", sentCount, wageCount),
		})
	} else if diffSum {
		results = append(results, HealthCheckItem{
			Key:     "ledger_reconciliation",
			Name:    "Rekonsiliasi Buku Besar",
			Status:  "gagal",
			Message: "Total buku besar global tidak cocok dengan akumulasi per pekerja",
		})
	} else {
		results = append(results, HealthCheckItem{
			Key:     "ledger_reconciliation",
			Name:    "Rekonsiliasi Buku Besar",
			Status:  "ok",
			Message: fmt.Sprintf("Buku besar seimbang: %d upah cocok terkirim, 0 saldo negatif, total mutasi konsisten", wageCount),
		})
	}

	// 10. Pengingat retensi arsip campaign > 90 hari (Tahap H8)
	var oldArchivedCount int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaigns
		WHERE archived_at IS NOT NULL AND purged_at IS NULL AND archived_at < NOW() - INTERVAL '90 days'
	`).Scan(&oldArchivedCount)
	if oldArchivedCount > 0 {
		results = append(results, HealthCheckItem{
			Key:     "archived_campaigns_retention",
			Name:    "Retensi Kampanye Terarsip",
			Status:  "peringatan",
			Message: fmt.Sprintf("Ada %d kampanye terarsip > 90 hari yang belum dibersihkan (pertimbangkan pembersihan di menu Penyimpanan)", oldArchivedCount),
		})
	} else {
		results = append(results, HealthCheckItem{
			Key:     "archived_campaigns_retention",
			Name:    "Retensi Kampanye Terarsip",
			Status:  "ok",
			Message: "Semua kampanye terarsip dalam batas retensi wajar atau sudah dibersihkan",
		})
	}

	// 11. Status Cadangan Database & Verifikasi (Tahap H9)
	if s.BackupMgr == nil {
		results = append(results, HealthCheckItem{
			Key:     "backup_status",
			Name:    "Status Cadangan Database (Backup)",
			Status:  "peringatan",
			Message: "Cadangan database belum diaktifkan (BACKUP_KEY belum dikonfigurasi)",
		})
	} else {
		totalCount, totalBytes, lastBackupTime, lastVerifiedTime, lastVerifyOK, errStats := s.BackupMgr.GetStats(ctx)
		freeDisk, _ := backup.GetFreeDiskSpace(s.BackupMgr.Dir())
		freeDiskPretty := FormatBytes(int64(freeDisk))
		totalSizePretty := FormatBytes(totalBytes)

		if errStats != nil {
			results = append(results, HealthCheckItem{
				Key:     "backup_status",
				Name:    "Status Cadangan Database (Backup)",
				Status:  "gagal",
				Message: fmt.Sprintf("Gagal membaca status cadangan: %v", errStats),
			})
		} else if totalCount == 0 || lastBackupTime == nil {
			results = append(results, HealthCheckItem{
				Key:     "backup_status",
				Name:    "Status Cadangan Database (Backup)",
				Status:  "gagal",
				Message: fmt.Sprintf("Belum ada cadangan terbuat! (Sisa disk: %s)", freeDiskPretty),
			})
		} else {
			hoursSinceLast := time.Since(*lastBackupTime).Hours()
			if hoursSinceLast > 36 {
				results = append(results, HealthCheckItem{
					Key:     "backup_status",
					Name:    "Status Cadangan Database (Backup)",
					Status:  "gagal",
					Message: fmt.Sprintf("Cadangan terakhir dibuat %.1f jam lalu (> 36 jam)! Total: %d berkas (%s), Sisa disk: %s", hoursSinceLast, totalCount, totalSizePretty, freeDiskPretty),
				})
			} else if lastVerifiedTime == nil || !lastVerifyOK {
				results = append(results, HealthCheckItem{
					Key:     "backup_status",
					Name:    "Status Cadangan Database (Backup)",
					Status:  "peringatan",
					Message: fmt.Sprintf("Cadangan terakhir %.1f jam lalu tetapi belum ada cadangan terverifikasi valid! Total: %d berkas (%s), Sisa disk: %s", hoursSinceLast, totalCount, totalSizePretty, freeDiskPretty),
				})
			} else {
				results = append(results, HealthCheckItem{
					Key:     "backup_status",
					Name:    "Status Cadangan Database (Backup)",
					Status:  "ok",
					Message: fmt.Sprintf("Cadangan sehat (terakhir %.1f jam lalu, diverifikasi: %s). Total: %d berkas (%s), Sisa disk: %s", hoursSinceLast, lastVerifiedTime.Format("02/01 15:04"), totalCount, totalSizePretty, freeDiskPretty),
				})
			}
		}
	}

	// 12. Pemeriksaan Nomor WhatsApp (Tahap H13)
	var consecutiveUnreg uint64
	if s.Engine != nil {
		consecutiveUnreg = s.Engine.ConsecutiveTidakTerdaftar()
	}
	if consecutiveUnreg >= 20 {
		results = append(results, HealthCheckItem{
			Key:     "wa_check_status",
			Name:    "Pemeriksaan Nomor WhatsApp",
			Status:  "peringatan",
			Message: fmt.Sprintf("Terdeteksi %d hasil tidak terdaftar berturut-turut di seluruh engine tanpa nomor terdaftar. Disarankan untuk memeriksa format nomor dan kondisi sesi WhatsApp.", consecutiveUnreg),
		})
	} else {
		results = append(results, HealthCheckItem{
			Key:     "wa_check_status",
			Name:    "Pemeriksaan Nomor WhatsApp",
			Status:  "ok",
			Message: fmt.Sprintf("Normal (%d tidak terdaftar berturut-turut, batas peringatan: 20)", consecutiveUnreg),
		})
	}

	return results
}

func (s *Server) HandleAdminMaintenanceGET(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	lang := "id"
	if admin.Language != "" {
		lang = admin.Language
	}

	// 1. Ukur latensi database ping
	startPing := time.Now()
	pingErr := s.DB.Pool.Ping(ctx)
	pingLatency := fmt.Sprintf("%.2f ms", float64(time.Since(startPing).Microseconds())/1000.0)
	dbOnline := pingErr == nil

	// 2. Stat database pool
	stat := s.DB.Pool.Stat()

	// 3. Hitung statistik operasional
	var activeWA, disconnectedWA, pendingWithdrawals, runningCamps, availNums int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM wa_accounts WHERE status = 'terhubung'").Scan(&activeWA)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM wa_accounts WHERE status != 'terhubung'").Scan(&disconnectedWA)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM withdrawals WHERE status = 'menunggu'").Scan(&pendingWithdrawals)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaigns WHERE status = 'berjalan'").Scan(&runningCamps)
	if claimable, err := campaign.ClaimableCount(ctx, s.DB.Pool); err == nil {
		availNums = int64(claimable)
	}

	// 4. Status Mesin
	engineStatus := "Siap / Aktif"
	if s.WorkCtrl == nil {
		engineStatus = "Nonaktif (Standby)"
	}

	// 5. 20 Audit Log Terbaru
	var auditLogs []AuditLogRow
	logRows, err := s.DB.Pool.Query(ctx, `
		SELECT a.id, COALESCE(u.display_name, u.username, u.email, 'Sistem'),
		       a.action, a.entity, COALESCE(a.entity_id, ''), COALESCE(a.meta::text, ''),
		       a.ip, a.created_at
		FROM audit_logs a
		LEFT JOIN users u ON u.id = a.actor_id
		ORDER BY a.id DESC
		LIMIT 20
	`)
	if err == nil {
		defer logRows.Close()
		for logRows.Next() {
			var l AuditLogRow
			var t time.Time
			if err := logRows.Scan(&l.ID, &l.ActorName, &l.Action, &l.Entity, &l.EntityID, &l.Meta, &l.IP, &t); err == nil {
				l.CreatedAt = t.Format("02 Jan 2006 15:04:05")
				auditLogs = append(auditLogs, l)
			}
		}
	}

	// 6. Kunci Sistem & Hasil Pemeriksaan Kesehatan
	locks := s.GetSystemLocks(ctx)
	healthChecks := s.RunHealthChecks(ctx)

	// 7. Pembatasan Laju Aktif (Tahap H5a)
	var activeRestrictions []ratelimit.RestrictionEntry
	if s.LoginIPLimiter != nil {
		activeRestrictions = append(activeRestrictions, s.LoginIPLimiter.ActiveRestrictions()...)
	}
	if s.LoginIdentLimiter != nil {
		activeRestrictions = append(activeRestrictions, s.LoginIdentLimiter.ActiveRestrictions()...)
	}
	if s.LoginPairLimiter != nil {
		activeRestrictions = append(activeRestrictions, s.LoginPairLimiter.ActiveRestrictions()...)
	}
	if s.RegisterHourlyLimiter != nil {
		activeRestrictions = append(activeRestrictions, s.RegisterHourlyLimiter.ActiveRestrictions()...)
	}
	if s.RegisterDailyLimiter != nil {
		activeRestrictions = append(activeRestrictions, s.RegisterDailyLimiter.ActiveRestrictions()...)
	}

	viewData := MaintenanceHealthViewData{
		Lang:                  lang,
		Title:                 "Pemeliharaan & Diagnostik Sistem - GO BLAST",
		Theme:                 getTheme(r),
		CSRFToken:             getCSRF(r),
		Nonce:                 getNonce(r),
		ActiveNav:             "maintenance",
		Message:               r.URL.Query().Get("msg"),
		Error:                 r.URL.Query().Get("err"),
		MaintenanceMode:       s.IsMaintenanceMode(ctx),
		LockWithdrawals:       locks.LockWithdrawals,
		LockLinking:           locks.LockLinking,
		LockEngine:            locks.LockEngine,
		LockMessage:           locks.LockMessage,
		DBOnline:              dbOnline,
		DBPingLatency:         pingLatency,
		DBAcquiredConns:       stat.AcquiredConns(),
		DBIdleConns:           stat.IdleConns(),
		DBTotalConns:          stat.TotalConns(),
		DBMaxConns:            stat.MaxConns(),
		ActiveWACount:         activeWA,
		DisconnectedWACount:   disconnectedWA,
		PendingWithdrawals:    pendingWithdrawals,
		RunningCampaignsCount: runningCamps,
		AvailableNumbersCount: availNums,
		EngineStatus:          engineStatus,
		RecentAuditLogs:       auditLogs,
		HealthChecks:          healthChecks,
		ActiveRestrictions:    activeRestrictions,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["admin_maintenance.html"].ExecuteTemplate(w, "base.html", viewData)
}

func (s *Server) HandleAdminMaintenanceRestrictionReleasePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	key := strings.TrimSpace(r.FormValue("key"))
	if key != "" {
		if s.LoginIPLimiter != nil {
			s.LoginIPLimiter.Reset(key)
		}
		if s.LoginIdentLimiter != nil {
			s.LoginIdentLimiter.Reset(key)
		}
		if s.LoginPairLimiter != nil {
			s.LoginPairLimiter.Reset(key)
		}
		if s.RegisterHourlyLimiter != nil {
			s.RegisterHourlyLimiter.Reset(key)
		}
		if s.RegisterDailyLimiter != nil {
			s.RegisterDailyLimiter.Reset(key)
		}
		s.logAudit(&admin.ID, "ratelimit_release", "system", key, "manual release by admin")
	}

	http.Redirect(w, r, "/admin/maintenance?msg=Pembatasan+berhasil+dilepas", http.StatusSeeOther)
}

func (s *Server) HandleAdminMaintenanceRestrictionReleaseAllPOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.LoginIPLimiter != nil {
		s.LoginIPLimiter.ResetAll()
	}
	if s.LoginIdentLimiter != nil {
		s.LoginIdentLimiter.ResetAll()
	}
	if s.LoginPairLimiter != nil {
		s.LoginPairLimiter.ResetAll()
	}
	if s.LoginGlobalLimiter != nil {
		s.LoginGlobalLimiter.ResetAll()
	}
	if s.RegisterHourlyLimiter != nil {
		s.RegisterHourlyLimiter.ResetAll()
	}
	if s.RegisterDailyLimiter != nil {
		s.RegisterDailyLimiter.ResetAll()
	}

	s.logAudit(&admin.ID, "ratelimit_release_all", "system", "all", "manual release all by admin")
	http.Redirect(w, r, "/admin/maintenance?msg=Semua+pembatasan+laju+berhasil+dikosongkan", http.StatusSeeOther)
}

func (s *Server) HandleAdminMaintenanceTogglePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	current := s.IsMaintenanceMode(ctx)
	newMode := !current

	if err := s.updateSettingWithAudit(ctx, admin.ID, "maintenance_mode", newMode, getIP(r)); err != nil {
		slog.Error("Gagal mengubah mode pemeliharaan", "err", err)
		http.Redirect(w, r, "/admin/maintenance?err=Gagal+mengubah+mode+pemeliharaan", http.StatusSeeOther)
		return
	}

	s.InvalidateSystemLockCache()

	statusMsg := "Mode+pemeliharaan+berhasil+diaktifkan+(pekerja+dialihkan)"
	if !newMode {
		statusMsg = "Mode+pemeliharaan+berhasil+dinonaktifkan+(layanan+normal)"
	}
	http.Redirect(w, r, "/admin/maintenance?msg="+statusMsg, http.StatusSeeOther)
}

func (s *Server) HandleAdminMaintenanceLocksPOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	ip := getIP(r)

	lockWithdrawals := r.FormValue("lock_withdrawals") == "1" || r.FormValue("lock_withdrawals") == "true" || r.FormValue("lock_withdrawals") == "on"
	lockLinking := r.FormValue("lock_linking") == "1" || r.FormValue("lock_linking") == "true" || r.FormValue("lock_linking") == "on"
	lockEngine := r.FormValue("lock_engine") == "1" || r.FormValue("lock_engine") == "true" || r.FormValue("lock_engine") == "on"
	lockMessage := strings.TrimSpace(r.FormValue("lock_message"))

	updates := []struct {
		key string
		val interface{}
	}{
		{"lock_withdrawals", lockWithdrawals},
		{"lock_linking", lockLinking},
		{"lock_engine", lockEngine},
		{"lock_message", lockMessage},
	}

	for _, u := range updates {
		if err := s.updateSettingWithAudit(ctx, admin.ID, u.key, u.val, ip); err != nil {
			slog.Error("Gagal menyimpan kunci pemeliharaan", "err", err, "key", u.key)
			http.Redirect(w, r, "/admin/maintenance?err=Gagal+menyimpan+kunci", http.StatusSeeOther)
			return
		}
	}

	s.InvalidateSystemLockCache()
	http.Redirect(w, r, "/admin/maintenance?msg=Kunci+sistem+dan+pesan+berhasil+diperbarui", http.StatusSeeOther)
}

func (s *Server) HandleAdminMaintenanceLockTogglePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	ip := getIP(r)
	key := strings.TrimSpace(r.FormValue("key"))

	switch key {
	case "lock_withdrawals", "lock_linking", "lock_engine":
	default:
		http.Redirect(w, r, "/admin/maintenance?err=Kunci+tidak+valid", http.StatusSeeOther)
		return
	}

	locks := s.GetSystemLocks(ctx)
	var current bool
	switch key {
	case "lock_withdrawals":
		current = locks.LockWithdrawals
	case "lock_linking":
		current = locks.LockLinking
	case "lock_engine":
		current = locks.LockEngine
	}
	newVal := !current

	if err := s.updateSettingWithAudit(ctx, admin.ID, key, newVal, ip); err != nil {
		slog.Error("Gagal mengubah kunci pemeliharaan", "err", err, "key", key)
		http.Redirect(w, r, "/admin/maintenance?err=Gagal+mengubah+kunci", http.StatusSeeOther)
		return
	}

	s.InvalidateSystemLockCache()
	statusText := "diaktifkan"
	if !newVal {
		statusText = "dinonaktifkan"
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/maintenance?msg=Kunci+%s+berhasil+%s", key, statusText), http.StatusSeeOther)
}

func (s *Server) HandleMaintenancePublicGET(w http.ResponseWriter, r *http.Request) {
	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	} else if lCookie, err := r.Cookie("lang"); err == nil && lCookie.Value != "" {
		lang = validateLang(lCookie.Value)
	}

	viewData := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		Nonce     string
		IsActive  bool
		T         func(string) string
	}{
		Lang:      lang,
		Title:     "Pemeliharaan Sistem - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		Nonce:     getNonce(r),
		IsActive:  s.IsMaintenanceMode(r.Context()),
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["maintenance.html"].ExecuteTemplate(w, "base.html", viewData)
}

// =====================================================================
// --- Panel Detail Pekerja & Reset Password ---
// =====================================================================

type AdminWorkerAccountItem struct {
	ID         int64
	Label      string
	Speed      string
	Status     string
	IsWorking  bool
	StopReason string
	SentTotal  int64
	CreatedAt  string
}

type AdminWorkerWithdrawalItem struct {
	ID              int64
	Currency        string
	AmountFormatted string
	Method          string
	Status          string
	CreatedAt       string
	ProcessedAt     string
}

type AdminWorkerLedgerItem struct {
	ID              int64
	Kind            string
	Currency        string
	AmountFormatted string
	RateSnapshot    int64
	Note            string
	CreatedAt       string
}

type WorkerDetailInfo struct {
	ID           int64
	Username     string
	Email        string
	DisplayName  string
	Status       string
	Role         string
	Language     string
	Currency     string
	ReferralCode string
	FreezeReason string
	CreatedAt    string
}

type AdminWorkerRefereeItem struct {
	ID                   int64
	Username             string
	Status               string
	SentCount            int64
	BonusEarned          int64
	BonusEarnedFormatted string
	SamePayoutDest       bool
}

type AdminWorkerSameDestItem struct {
	ID        int64
	Username  string
	Role      string
	Status    string
	CreatedAt string
}

type AdminWorkerDetailViewData struct {
	Lang                    string
	Title                   string
	Theme                   string
	CSRFToken               string
	ActiveNav               string
	Message                 string
	Error                   string
	ResetPasswordNotice     string
	Worker                  WorkerDetailInfo
	ReferrerID              int64
	ReferrerUsername        string
	Referees                []AdminWorkerRefereeItem
	TotalRefereeBonusFormatted string
	SameDestWorkers         []AdminWorkerSameDestItem
	BalanceIDRFormatted     string
	BalanceUSDTFormatted    string
	TotalWageIDRFormatted   string
	TotalWithdrawnIDR       string
	TotalWithdrawnUSDT      string
	PendingWithdrawalsCount int64
	PayoutMethod            string
	PayoutAccountName       string
	PayoutDestMasked        string
	PayoutDestDecrypted     string
	Accounts                []AdminWorkerAccountItem
	Withdrawals             []AdminWorkerWithdrawalItem
	LedgerEntries           []AdminWorkerLedgerItem
	Nonce                   string
	T                       func(string) string
}

func (s *Server) HandleAdminWorkerDetailGET(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	workerID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID pekerja tidak valid", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 1. Ambil data profil pekerja
	var wInfo WorkerDetailInfo
	var payoutMethod, payoutAccName, payoutDestEnc *string
	var createdAt time.Time
	var emailPtr *string
	var referredBy *int64
	var payoutDestHash string

	err = s.DB.Pool.QueryRow(ctx, `
		SELECT id, email, COALESCE(username, ''), COALESCE(display_name, ''),
		       role, status, language, referral_code,
		       payout_method, payout_account_name, payout_dest_encrypted, created_at,
		       COALESCE(freeze_reason, ''), referred_by, COALESCE(payout_dest_hash, '')
		FROM users
		WHERE id = $1 AND role = 'worker'
	`, workerID).Scan(
		&wInfo.ID, &emailPtr, &wInfo.Username, &wInfo.DisplayName,
		&wInfo.Role, &wInfo.Status, &wInfo.Language, &wInfo.ReferralCode,
		&payoutMethod, &payoutAccName, &payoutDestEnc, &createdAt,
		&wInfo.FreezeReason, &referredBy, &payoutDestHash,
	)
	wInfo.Currency = "IDR"
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Pekerja tidak ditemukan", http.StatusNotFound)
			return
		}
		slog.Error("Kesalahan database saat memuat profil pekerja", "err", err, "worker_id", workerID)
		http.Error(w, "Terjadi kesalahan sistem", http.StatusInternalServerError)
		return
	}
	if emailPtr != nil {
		wInfo.Email = *emailPtr
	}
	wInfo.CreatedAt = createdAt.Format("02 Jan 2006 15:04")

	// 2. Data Finansial & Saldo
	var balanceIDR, balanceUSDT, totalWageIDR, totalWithdrawnIDR, totalWithdrawnUSDT, pendingWithdrawals int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, 'IDR')", workerID).Scan(&balanceIDR)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, 'USDT')", workerID).Scan(&balanceUSDT)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE user_id = $1 AND kind = 'upah'", workerID).Scan(&totalWageIDR)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(amount), 0) FROM withdrawals WHERE user_id = $1 AND status = 'selesai' AND currency = 'IDR'", workerID).Scan(&totalWithdrawnIDR)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(amount), 0) FROM withdrawals WHERE user_id = $1 AND status = 'selesai' AND currency = 'USDT'", workerID).Scan(&totalWithdrawnUSDT)
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM withdrawals WHERE user_id = $1 AND status = 'menunggu'", workerID).Scan(&pendingWithdrawals)

	// 3. Dekripsi Rekening Tujuan
	var pMethod, pAccName, pDestMasked, pDestDecrypted string
	if payoutMethod != nil {
		pMethod = *payoutMethod
	}
	if payoutAccName != nil {
		pAccName = *payoutAccName
	}
	if payoutDestEnc != nil && *payoutDestEnc != "" {
		dec, decErr := s.DecryptDestination(*payoutDestEnc)
		if decErr == nil {
			pDestDecrypted = dec
			pDestMasked = MaskDestination(dec, pMethod)
		} else {
			pDestDecrypted = "tujuan tidak terbaca, minta pekerja mengisi ulang"
			pDestMasked = "(enkripsi lama)"
		}
	}

	// 4. Daftar Akun WhatsApp Milik Pekerja
	var accounts []AdminWorkerAccountItem
	accRows, err := s.DB.Pool.Query(ctx, `
		SELECT wa.id, wa.label, wa.speed, wa.status, COALESCE(wa.stop_reason, ''), wa.created_at,
		       (SELECT COUNT(*) FROM campaign_numbers WHERE claimed_account_id = wa.id AND status = 'terkirim') AS sent_total
		FROM wa_accounts wa
		WHERE wa.worker_id = $1
		ORDER BY wa.id DESC
	`, workerID)
	if err == nil {
		defer accRows.Close()
		for accRows.Next() {
			var acc AdminWorkerAccountItem
			var t time.Time
			if err := accRows.Scan(&acc.ID, &acc.Label, &acc.Speed, &acc.Status, &acc.StopReason, &t, &acc.SentTotal); err == nil {
				acc.CreatedAt = t.Format("02 Jan 2006")
				if s.WorkCtrl != nil {
					acc.IsWorking = s.WorkCtrl.IsRunning(acc.ID)
				}
				accounts = append(accounts, acc)
			}
		}
	}

	// 5. 10 Penarikan Terbaru Milik Pekerja
	var withdrawals []AdminWorkerWithdrawalItem
	wRows, err := s.DB.Pool.Query(ctx, `
		SELECT id, currency, amount, method, status, created_at, processed_at
		FROM withdrawals
		WHERE user_id = $1
		ORDER BY id DESC
		LIMIT 10
	`, workerID)
	if err == nil {
		defer wRows.Close()
		for wRows.Next() {
			var wItem AdminWorkerWithdrawalItem
			var t time.Time
			var pTime *time.Time
			var rawAmount int64
			if err := wRows.Scan(&wItem.ID, &wItem.Currency, &rawAmount, &wItem.Method, &wItem.Status, &t, &pTime); err == nil {
				wItem.CreatedAt = t.Format("02 Jan 2006 15:04")
				if pTime != nil {
					wItem.ProcessedAt = pTime.Format("02 Jan 2006 15:04")
				}
				if wItem.Currency == "USDT" {
					wItem.AmountFormatted = formatUSDT(rawAmount) + " USDT"
				} else {
					wItem.AmountFormatted = "Rp " + formatIDR(rawAmount)
				}
				withdrawals = append(withdrawals, wItem)
			}
		}
	}

	// 6. 15 Mutasi Buku Besar Terakhir
	var ledgerEntries []AdminWorkerLedgerItem
	lRows, err := s.DB.Pool.Query(ctx, `
		SELECT id, kind, currency, amount, COALESCE(rate_snapshot, 0), COALESCE(note, ''), created_at
		FROM ledger_entries
		WHERE user_id = $1
		ORDER BY id DESC
		LIMIT 15
	`, workerID)
	if err == nil {
		defer lRows.Close()
		for lRows.Next() {
			var lItem AdminWorkerLedgerItem
			var t time.Time
			var rawAmount int64
			if err := lRows.Scan(&lItem.ID, &lItem.Kind, &lItem.Currency, &rawAmount, &lItem.RateSnapshot, &lItem.Note, &t); err == nil {
				lItem.CreatedAt = t.Format("02 Jan 2006 15:04")
				if lItem.Currency == "USDT" {
					lItem.AmountFormatted = formatUSDT(rawAmount) + " USDT"
				} else {
					lItem.AmountFormatted = "Rp " + formatIDR(rawAmount)
				}
				ledgerEntries = append(ledgerEntries, lItem)
			}
		}
	}

	// 7. Data Pengundang & Rekan Referral
	var referrerID int64
	var referrerUsername string
	if referredBy != nil && *referredBy > 0 {
		referrerID = *referredBy
		_ = s.DB.Pool.QueryRow(ctx, "SELECT username FROM users WHERE id = $1", referrerID).Scan(&referrerUsername)
	}

	refRows, err := s.DB.Pool.Query(ctx, `
		SELECT 
			u.id, 
			u.username, 
			u.status,
			COALESCE(sent.cnt, 0) AS sent_count,
			COALESCE(ref_bonus.total_bonus, 0) AS bonus_earned,
			(u.payout_dest_hash IS NOT NULL AND u.payout_dest_hash != '' AND u.payout_dest_hash = $2) AS same_payout_dest
		FROM users u
		LEFT JOIN (
			SELECT user_id, COUNT(*) AS cnt 
			FROM ledger_entries 
			WHERE kind = 'upah' 
			GROUP BY user_id
		) sent ON sent.user_id = u.id
		LEFT JOIN (
			SELECT source_user_id, SUM(amount) AS total_bonus 
			FROM ledger_entries 
			WHERE user_id = $1 AND kind = 'referral' AND source_user_id IS NOT NULL 
			GROUP BY source_user_id
		) ref_bonus ON ref_bonus.source_user_id = u.id
		WHERE u.referred_by = $1
		ORDER BY u.id DESC
	`, workerID, payoutDestHash)

	var referees []AdminWorkerRefereeItem
	var totalRefBonus int64
	if err == nil {
		defer refRows.Close()
		for refRows.Next() {
			var rItem AdminWorkerRefereeItem
			if err := refRows.Scan(&rItem.ID, &rItem.Username, &rItem.Status, &rItem.SentCount, &rItem.BonusEarned, &rItem.SamePayoutDest); err == nil {
				rItem.BonusEarnedFormatted = formatIDR(rItem.BonusEarned)
				totalRefBonus += rItem.BonusEarned
				referees = append(referees, rItem)
			}
		}
	}

	// 8. Kelompok Multi-Akun (Rekening Sama Berdasarkan Payout Dest Hash)
	var sameDestWorkers []AdminWorkerSameDestItem
	if payoutDestHash != "" {
		sdRows, err := s.DB.Pool.Query(ctx, `
			SELECT id, username, role, status, created_at
			FROM users
			WHERE payout_dest_hash = $1 AND id != $2
			ORDER BY id ASC
		`, payoutDestHash, workerID)
		if err == nil {
			defer sdRows.Close()
			for sdRows.Next() {
				var sd AdminWorkerSameDestItem
				var t time.Time
				if err := sdRows.Scan(&sd.ID, &sd.Username, &sd.Role, &sd.Status, &t); err == nil {
					sd.CreatedAt = t.Format("02 Jan 2006")
					sameDestWorkers = append(sameDestWorkers, sd)
				}
			}
		}
	}

	lang := "id"
	if admin.Language != "" {
		lang = admin.Language
	}

	viewData := AdminWorkerDetailViewData{
		Lang:                       lang,
		Title:                      fmt.Sprintf("Detail Pekerja %s - GO BLAST", wInfo.Username),
		Theme:                      getTheme(r),
		CSRFToken:                  getCSRF(r),
		Nonce:                      getNonce(r),
		ActiveNav:                  "workers",
		Message:                    r.URL.Query().Get("msg"),
		Error:                      r.URL.Query().Get("err"),
		ResetPasswordNotice:        r.URL.Query().Get("reset_pwd"),
		Worker:                     wInfo,
		ReferrerID:                 referrerID,
		ReferrerUsername:           referrerUsername,
		Referees:                   referees,
		TotalRefereeBonusFormatted: formatIDR(totalRefBonus),
		SameDestWorkers:            sameDestWorkers,
		BalanceIDRFormatted:        formatIDR(balanceIDR),
		BalanceUSDTFormatted:       formatUSDT(balanceUSDT),
		TotalWageIDRFormatted:      formatIDR(totalWageIDR),
		TotalWithdrawnIDR:          formatIDR(totalWithdrawnIDR),
		TotalWithdrawnUSDT:         formatUSDT(totalWithdrawnUSDT),
		PendingWithdrawalsCount:    pendingWithdrawals,
		PayoutMethod:               pMethod,
		PayoutAccountName:          pAccName,
		PayoutDestMasked:           pDestMasked,
		PayoutDestDecrypted:        pDestDecrypted,
		Accounts:                   accounts,
		Withdrawals:                withdrawals,
		LedgerEntries:              ledgerEntries,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["admin_worker_detail.html"].ExecuteTemplate(w, "base.html", viewData)
}

func (s *Server) HandleAdminWorkerResetPasswordPOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	workerID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID pekerja tidak valid", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 1. Verifikasi pekerja ada
	var username string
	err = s.DB.Pool.QueryRow(ctx, "SELECT username FROM users WHERE id = $1 AND role = 'worker'", workerID).Scan(&username)
	if err != nil {
		http.Redirect(w, r, "/admin/workers?err=Pekerja+tidak+ditemukan", http.StatusSeeOther)
		return
	}

	// 2. Baca input password baru atau buat temporary password
	plainPassword := strings.TrimSpace(r.FormValue("new_password"))
	if plainPassword == "" {
		plainPassword = generateTemporaryPassword()
	} else if len(plainPassword) < 10 {
		http.Redirect(w, r, fmt.Sprintf("/admin/workers/%d?err=Kata+sandi+baru+minimal+10+karakter", workerID), http.StatusSeeOther)
		return
	}

	// 3. Hash kata sandi dengan argon2id
	hash, err := auth.HashPassword(plainPassword)
	if err != nil {
		slog.Error("Gagal membuat hash kata sandi", "err", err, "worker_id", workerID)
		http.Redirect(w, r, fmt.Sprintf("/admin/workers/%d?err=Gagal+membuat+hash+kata+sandi", workerID), http.StatusSeeOther)
		return
	}

	// 4. Update kata sandi pengguna, set must_change_password = true, withdraw_locked_until = now() + 24h
	_, err = s.DB.Pool.Exec(ctx, `
		UPDATE users 
		SET password_hash = $1, 
		    must_change_password = TRUE, 
		    withdraw_locked_until = NOW() + make_interval(hours => $2) 
		WHERE id = $3
	`, hash, config.WithdrawLockAfterChangeHours, workerID)
	if err != nil {
		slog.Error("Gagal memperbarui kata sandi pekerja", "err", err, "worker_id", workerID)
		http.Redirect(w, r, fmt.Sprintf("/admin/workers/%d?err=Gagal+memperbarui+kata+sandi", workerID), http.StatusSeeOther)
		return
	}

	// 5. Putuskan semua sesi web aktif milik pekerja
	_, _ = s.DB.Pool.Exec(ctx, "DELETE FROM web_sessions WHERE user_id = $1", workerID)

	// 6. Catat audit log
	metaJSON, _ := json.Marshal(map[string]interface{}{
		"worker_id":     workerID,
		"username":      username,
		"forced_change": true,
		"locked_hours":  config.WithdrawLockAfterChangeHours,
	})
	_, _ = s.DB.Pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta, ip)
		VALUES ($1, 'worker_reset_password', 'users', $2, $3::jsonb, $4)
	`, admin.ID, strconv.FormatInt(workerID, 10), string(metaJSON), getIP(r))

	http.Redirect(w, r, fmt.Sprintf("/admin/workers/%d?msg=Kata+sandi+pekerja+berhasil+direset&reset_pwd=%s", workerID, url.QueryEscape(plainPassword)), http.StatusSeeOther)
}

func generateTemporaryPassword() string {
	const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

func (s *Server) HandleAdminWorkerFreezePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	workerID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	freezeReason := strings.TrimSpace(r.FormValue("freeze_reason"))
	if freezeReason == "" {
		http.Redirect(w, r, fmt.Sprintf("/admin/workers/%d?err=Alasan+pembekuan+akun+wajib+diisi", workerID), http.StatusSeeOther)
		return
	}

	ctx := r.Context()

	// 1. Verifikasi pekerja ada
	var username string
	err = s.DB.Pool.QueryRow(ctx, "SELECT username FROM users WHERE id = $1 AND role = 'worker'", workerID).Scan(&username)
	if err != nil {
		http.Redirect(w, r, "/admin/workers?err=Pekerja+tidak+ditemukan", http.StatusSeeOther)
		return
	}

	// 2. Ambil semua akun WhatsApp milik pekerja
	rows, err := s.DB.Pool.Query(ctx, "SELECT id FROM wa_accounts WHERE worker_id = $1", workerID)
	var accountIDs []int64
	if err == nil {
		for rows.Next() {
			var accID int64
			if err := rows.Scan(&accID); err == nil {
				accountIDs = append(accountIDs, accID)
			}
		}
		rows.Close()
	}

	// 3. Hentikan engine loop & putuskan koneksi WhatsApp untuk setiap akun
	for _, accID := range accountIDs {
		if s.WorkCtrl != nil {
			s.WorkCtrl.StopAccount(accID)
		}
		if s.WaManager != nil {
			_ = s.WaManager.DisconnectAccount(ctx, accID)
		}
		_, _ = s.DB.Pool.Exec(ctx, "UPDATE wa_accounts SET status = 'terputus', stop_reason = 'Akun pekerja dibekukan' WHERE id = $1", accID)
	}

	// 4. Hapus semua sesi web aktif milik pekerja
	_, _ = s.DB.Pool.Exec(ctx, "DELETE FROM web_sessions WHERE user_id = $1", workerID)

	// 5. Update user status ke 'frozen' dan simpan freeze_reason
	_, err = s.DB.Pool.Exec(ctx, "UPDATE users SET status = 'frozen', freeze_reason = $1 WHERE id = $2 AND role = 'worker'", freezeReason, workerID)
	if err != nil {
		slog.Error("Gagal membekukan akun pekerja", "err", err, "worker_id", workerID)
		http.Redirect(w, r, fmt.Sprintf("/admin/workers/%d?err=Gagal+membekukan+akun", workerID), http.StatusSeeOther)
		return
	}

	// 6. Catat audit log
	metaJSON, _ := json.Marshal(map[string]interface{}{
		"worker_id":        workerID,
		"username":         username,
		"freeze_reason":    freezeReason,
		"stopped_accounts": len(accountIDs),
	})
	_, _ = s.DB.Pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta, ip)
		VALUES ($1, 'worker_freeze', 'users', $2, $3::jsonb, $4)
	`, admin.ID, idStr, string(metaJSON), getIP(r))

	http.Redirect(w, r, fmt.Sprintf("/admin/workers/%d?msg=Akun+pekerja+berhasil+dibekukan", workerID), http.StatusSeeOther)
}

func (s *Server) HandleAdminWorkerUnfreezePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	workerID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	var username string
	err = s.DB.Pool.QueryRow(ctx, "SELECT username FROM users WHERE id = $1 AND role = 'worker'", workerID).Scan(&username)
	if err != nil {
		http.Redirect(w, r, "/admin/workers?err=Pekerja+tidak+ditemukan", http.StatusSeeOther)
		return
	}

	_, err = s.DB.Pool.Exec(ctx, "UPDATE users SET status = 'active', freeze_reason = NULL WHERE id = $1 AND role = 'worker'", workerID)
	if err != nil {
		slog.Error("Gagal mengaktifkan akun pekerja", "err", err, "worker_id", workerID)
		http.Redirect(w, r, fmt.Sprintf("/admin/workers/%d?err=Gagal+mengaktifkan+akun", workerID), http.StatusSeeOther)
		return
	}

	metaJSON, _ := json.Marshal(map[string]interface{}{
		"worker_id": workerID,
		"username":  username,
		"action":    "unfreeze",
	})
	_, _ = s.DB.Pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta, ip)
		VALUES ($1, 'worker_unfreeze', 'users', $2, $3::jsonb, $4)
	`, admin.ID, idStr, string(metaJSON), getIP(r))

	http.Redirect(w, r, fmt.Sprintf("/admin/workers/%d?msg=Pembekuan+akun+pekerja+berhasil+dibuka", workerID), http.StatusSeeOther)
}


