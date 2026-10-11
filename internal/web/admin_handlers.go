package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nyaruka/phonenumbers"
	"goblast/internal/engine"
	"goblast/internal/i18n"
	"goblast/internal/withdraw"
)

// --- Structs for Admin Dashboard & API Stats ---

type CampaignStatRow struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	Tersedia       int    `json:"tersedia"`
	Dikirim        int    `json:"dikirim"`
	Terkirim       int    `json:"terkirim"`
	Gagal          int    `json:"gagal"`
	TidakTerdaftar int    `json:"tidak_terdaftar"`
}

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
		http.Error(w, "Gagal memuat statistik: "+err.Error(), http.StatusInternalServerError)
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
		ActiveNav string
		Stats     *DashboardData
		T         func(string) string
	}{
		Lang:      lang,
		Title:     "Dashboard Admin - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
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

func (s *Server) fetchDashboardData(ctx context.Context) (*DashboardData, error) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("Asia/Jakarta", 7*3600)
	}
	nowWIB := time.Now().In(loc)
	startOfTodayWIB := time.Date(nowWIB.Year(), nowWIB.Month(), nowWIB.Day(), 0, 0, 0, 0, loc)

	// 1. Overall counts
	overall := map[string]int{
		"tersedia":        0,
		"dikirim":         0,
		"terkirim":        0,
		"gagal":           0,
		"tidak_terdaftar": 0,
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

	// 2. Per campaign counts
	var campaigns []CampaignStatRow
	cRows, err := s.DB.Pool.Query(ctx, `
		SELECT c.id, c.name, c.status,
		       COUNT(CASE WHEN cn.status = 'tersedia' THEN 1 END) AS count_tersedia,
		       COUNT(CASE WHEN cn.status = 'dikirim' THEN 1 END) AS count_dikirim,
		       COUNT(CASE WHEN cn.status = 'terkirim' THEN 1 END) AS count_terkirim,
		       COUNT(CASE WHEN cn.status = 'gagal' THEN 1 END) AS count_gagal,
		       COUNT(CASE WHEN cn.status = 'tidak_terdaftar' THEN 1 END) AS count_tidak_terdaftar
		FROM campaigns c
		LEFT JOIN campaign_numbers cn ON cn.campaign_id = c.id
		GROUP BY c.id, c.name, c.status
		ORDER BY c.id DESC
	`)
	if err == nil {
		for cRows.Next() {
			var c CampaignStatRow
			if err := cRows.Scan(&c.ID, &c.Name, &c.Status, &c.Tersedia, &c.Dikirim, &c.Terkirim, &c.Gagal, &c.TidakTerdaftar); err == nil {
				campaigns = append(campaigns, c)
			}
		}
		cRows.Close()
	}

	// 3. Worker stats
	var workers []WorkerStatRow
	wRows, err := s.DB.Pool.Query(ctx, `
		SELECT u.id, COALESCE(u.username, u.email), u.email,
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
	SpeedDelays            map[string]int
	DailyLimits            map[string]*int
	MaxAttempts            *int
	MaxConsecutiveFailures *int
	ClaimTimeoutSeconds    int
	MaxAccountsPerWorker   *int
	MaxActiveSessions      *int
	DefaultCountryCode     string
	RateWageIDR            *int64
	RateWageUSDT           *int64
	RateWageUSDTDisplay    string
	RateReferralIDR        *int64
	RateReferralUSDT       *int64
	RateReferralUSDTDisplay string
}

func (s *Server) HandleAdminSettingsGET(w http.ResponseWriter, r *http.Request) {
	settings, err := s.readSettingsFromDB(r.Context())
	if err != nil {
		http.Error(w, "Gagal membaca pengaturan: "+err.Error(), http.StatusInternalServerError)
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

	// 1. Validate speed_delays (1 to 600)
	speedDelays := make(map[string]int)
	for _, k := range []string{"fast", "medium", "slow", "over_slow"} {
		valStr := strings.TrimSpace(r.FormValue("speed_delay_" + k))
		val, convErr := strconv.Atoi(valStr)
		if convErr != nil || val < 1 || val > 600 {
			s.renderSettingsError(w, r, fmt.Sprintf("speed_delays (%s) harus antara 1 sampai 600 detik", k))
			return
		}
		speedDelays[k] = val
	}

	// 2. Validate daily_limits (0 to 10000000, 0 = unlimited)
	dailyLimits := make(map[string]int)
	for _, k := range []string{"fast", "medium", "slow", "over_slow"} {
		valStr := strings.TrimSpace(r.FormValue("daily_limit_" + k))
		if valStr == "" {
			s.renderSettingsError(w, r, fmt.Sprintf("daily_limits (%s) wajib diisi", k))
			return
		}
		val, convErr := strconv.Atoi(valStr)
		if convErr != nil || val < 0 || val > 10000000 {
			s.renderSettingsError(w, r, fmt.Sprintf("daily_limits (%s) harus antara 0 (tanpa batas) sampai 10.000.000 pesan per hari", k))
			return
		}
		dailyLimits[k] = val
	}

	// 3. Validate max_attempts (1 to 10, required)
	maxAttemptsStr := strings.TrimSpace(r.FormValue("max_attempts"))
	if maxAttemptsStr == "" {
		s.renderSettingsError(w, r, "max_attempts wajib diisi")
		return
	}
	maxAttempts, convErr := strconv.Atoi(maxAttemptsStr)
	if convErr != nil || maxAttempts < 1 || maxAttempts > 10 {
		s.renderSettingsError(w, r, "max_attempts harus antara 1 sampai 10")
		return
	}

	// 4. Validate max_consecutive_failures (1 to 50, required)
	maxConsecutiveFailuresStr := strings.TrimSpace(r.FormValue("max_consecutive_failures"))
	if maxConsecutiveFailuresStr == "" {
		s.renderSettingsError(w, r, "max_consecutive_failures wajib diisi")
		return
	}
	maxConsecutiveFailures, convErr := strconv.Atoi(maxConsecutiveFailuresStr)
	if convErr != nil || maxConsecutiveFailures < 1 || maxConsecutiveFailures > 50 {
		s.renderSettingsError(w, r, "max_consecutive_failures harus antara 1 sampai 50")
		return
	}

	// 5. Validate claim_timeout_seconds (120 to 3600, > timeout kirim engine 60s)
	claimTimeoutStr := strings.TrimSpace(r.FormValue("claim_timeout_seconds"))
	claimTimeout, convErr := strconv.Atoi(claimTimeoutStr)
	engineSendTimeoutSec := int(engine.SendTimeout.Seconds())
	if convErr != nil || claimTimeout < 120 || claimTimeout > 3600 || claimTimeout <= engineSendTimeoutSec {
		s.renderSettingsError(w, r, fmt.Sprintf("claim_timeout_seconds harus antara 120 sampai 3600 dan lebih besar dari timeout engine (%d detik)", engineSendTimeoutSec))
		return
	}

	// 6. Validate max_accounts_per_worker (1 to 50, required)
	maxAccountsStr := strings.TrimSpace(r.FormValue("max_accounts_per_worker"))
	if maxAccountsStr == "" {
		s.renderSettingsError(w, r, "max_accounts_per_worker wajib diisi")
		return
	}
	maxAccounts, convErr := strconv.Atoi(maxAccountsStr)
	if convErr != nil || maxAccounts < 1 || maxAccounts > 50 {
		s.renderSettingsError(w, r, "max_accounts_per_worker harus antara 1 sampai 50")
		return
	}

	// 7. Validate max_active_sessions (1 to 500, required)
	maxSessionsStr := strings.TrimSpace(r.FormValue("max_active_sessions"))
	if maxSessionsStr == "" {
		s.renderSettingsError(w, r, "max_active_sessions wajib diisi")
		return
	}
	maxSessions, convErr := strconv.Atoi(maxSessionsStr)
	if convErr != nil || maxSessions < 1 || maxSessions > 500 {
		s.renderSettingsError(w, r, "max_active_sessions harus antara 1 sampai 500")
		return
	}

	// 8. Validate default_country_code (recognized by phonenumbers)
	defaultCC := strings.TrimSpace(r.FormValue("default_country_code"))
	ccInt, convErr := strconv.Atoi(defaultCC)
	if convErr != nil || ccInt <= 0 {
		s.renderSettingsError(w, r, "default_country_code harus berupa angka kode negara yang valid")
		return
	}
	region := phonenumbers.GetRegionCodeForCountryCode(ccInt)
	if region == "ZZ" || region == "" {
		s.renderSettingsError(w, r, "default_country_code tidak dikenal oleh pustaka phonenumbers")
		return
	}

	// 9. Validate rate_wage (IDR > 0, USDT > 0, required)
	rateWageIDRStr := strings.TrimSpace(r.FormValue("rate_wage_idr"))
	rateWageUSDTStr := strings.TrimSpace(r.FormValue("rate_wage_usdt"))
	if rateWageIDRStr == "" || rateWageUSDTStr == "" {
		s.renderSettingsError(w, r, "rate_wage wajib diisi (IDR dan USDT)")
		return
	}
	rateWageIDR, err1 := strconv.ParseInt(rateWageIDRStr, 10, 64)
	rateWageUSDT, err2 := parseUSDTInput(rateWageUSDTStr)
	if err1 != nil || err2 != nil || rateWageIDR <= 0 || rateWageUSDT <= 0 {
		s.renderSettingsError(w, r, "rate_wage harus bernilai lebih dari 0")
		return
	}
	rateWage := map[string]int64{
		"IDR":  rateWageIDR,
		"USDT": rateWageUSDT,
	}

	// 10. Validate rate_referral (IDR >= 0, USDT >= 0)
	rateRefIDRStr := strings.TrimSpace(r.FormValue("rate_referral_idr"))
	rateRefUSDTStr := strings.TrimSpace(r.FormValue("rate_referral_usdt"))
	var rateRefIDR, rateRefUSDT int64
	if rateRefIDRStr != "" {
		rateRefIDR, _ = strconv.ParseInt(rateRefIDRStr, 10, 64)
	}
	if rateRefUSDTStr != "" {
		rateRefUSDT, err2 = parseUSDTInput(rateRefUSDTStr)
		if err2 != nil {
			s.renderSettingsError(w, r, "rate_referral USDT tidak valid: "+err2.Error())
			return
		}
	}
	if rateRefIDR < 0 || rateRefUSDT < 0 {
		s.renderSettingsError(w, r, "rate_referral tidak boleh negatif")
		return
	}
	rateReferral := map[string]int64{
		"IDR":  rateRefIDR,
		"USDT": rateRefUSDT,
	}

	// Save all settings with audit log
	ip := getIP(r)
	updates := []struct {
		key   string
		value interface{}
	}{
		{"speed_delays", speedDelays},
		{"daily_limits", dailyLimits},
		{"max_attempts", maxAttempts},
		{"max_consecutive_failures", maxConsecutiveFailures},
		{"claim_timeout_seconds", claimTimeout},
		{"max_accounts_per_worker", maxAccounts},
		{"max_active_sessions", maxSessions},
		{"default_country_code", defaultCC},
		{"rate_wage", rateWage},
		{"rate_referral", rateReferral},
	}

	for _, up := range updates {
		if err := s.updateSettingWithAudit(r.Context(), admin.ID, up.key, up.value, ip); err != nil {
			s.renderSettingsError(w, r, "Gagal menyimpan pengaturan: "+err.Error())
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
		T         func(string) string
	}{
		Lang:      lang,
		Title:     "Pengaturan Sistem - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
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
	data := &SettingsViewData{
		SpeedDelays:        make(map[string]int),
		DailyLimits:        make(map[string]*int),
		DefaultCountryCode: "62",
	}

	rows, err := s.DB.Pool.Query(ctx, "SELECT key, value::text FROM settings")
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
		case "speed_delays":
			_ = json.Unmarshal([]byte(valStr), &data.SpeedDelays)
		case "daily_limits":
			var dl map[string]int
			if err := json.Unmarshal([]byte(valStr), &dl); err == nil {
				for k, v := range dl {
					val := v
					data.DailyLimits[k] = &val
				}
			}
		case "max_attempts":
			var v int
			if err := json.Unmarshal([]byte(valStr), &v); err == nil {
				data.MaxAttempts = &v
			}
		case "max_consecutive_failures":
			var v int
			if err := json.Unmarshal([]byte(valStr), &v); err == nil {
				data.MaxConsecutiveFailures = &v
			}
		case "claim_timeout_seconds":
			var v int
			if err := json.Unmarshal([]byte(valStr), &v); err == nil {
				data.ClaimTimeoutSeconds = v
			}
		case "max_accounts_per_worker":
			var v int
			if err := json.Unmarshal([]byte(valStr), &v); err == nil {
				data.MaxAccountsPerWorker = &v
			}
		case "max_active_sessions":
			var v int
			if err := json.Unmarshal([]byte(valStr), &v); err == nil {
				data.MaxActiveSessions = &v
			}
		case "default_country_code":
			var s string
			if err := json.Unmarshal([]byte(valStr), &s); err == nil {
				data.DefaultCountryCode = s
			} else {
				data.DefaultCountryCode = strings.Trim(valStr, `"`)
			}
		case "rate_wage":
			var rw map[string]int64
			if err := json.Unmarshal([]byte(valStr), &rw); err == nil {
				if idr, ok := rw["IDR"]; ok {
					val := idr
					data.RateWageIDR = &val
				}
				if usdt, ok := rw["USDT"]; ok {
					val := usdt
					data.RateWageUSDT = &val
				}
			}
		case "rate_referral":
			var rr map[string]int64
			if err := json.Unmarshal([]byte(valStr), &rr); err == nil {
				if idr, ok := rr["IDR"]; ok {
					val := idr
					data.RateReferralIDR = &val
				}
				if usdt, ok := rr["USDT"]; ok {
					val := usdt
					data.RateReferralUSDT = &val
				}
			}
		}
	}

	if data.RateWageUSDT != nil {
		f := float64(*data.RateWageUSDT) / 1000000.0
		data.RateWageUSDTDisplay = strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
	}
	if data.RateReferralUSDT != nil {
		f := float64(*data.RateReferralUSDT) / 1000000.0
		data.RateReferralUSDTDisplay = strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
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
		http.Error(w, "Gagal memuat dompet: "+err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "Gagal mencatat isi ulang: "+err.Error(), http.StatusInternalServerError)
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
	Status          string
	ProcessedAt     *time.Time
}

func formatIDR(n int64) string {
	in := strconv.FormatInt(n, 10)
	var out []byte
	l := len(in)
	for i, c := range in {
		if i > 0 && (l-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func formatUSDT(n int64) string {
	f := float64(n) / 1000000.0
	res := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
	if res == "" {
		return "0"
	}
	return res
}

func (s *Server) HandleAdminWithdrawalsGET(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	if statusFilter == "" {
		statusFilter = "menunggu"
	}

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
			       COALESCE(u.username, u.email), u.email
			FROM withdrawals w
			JOIN users u ON u.id = w.user_id
			ORDER BY w.id DESC
			LIMIT 100
		`
	} else {
		query = `
			SELECT w.id, w.currency, w.amount, w.method, w.destination_encrypted, w.status, w.requested_at, w.processed_at,
			       COALESCE(u.username, u.email), u.email
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
			if err := rows.Scan(&it.ID, &it.Currency, &rawAmount, &it.Method, &destEnc, &it.Status, &it.RequestedAt, &it.ProcessedAt, &it.WorkerUsername, &it.WorkerEmail); err == nil {
				if it.Currency == "IDR" {
					it.AmountFormatted = formatIDR(rawAmount)
				} else {
					it.AmountFormatted = formatUSDT(rawAmount)
				}

				dest, decErr := withdraw.DecryptDestination(destEnc, s.EncryptionKey)
				if decErr != nil {
					it.Destination = "(gagal dekripsi)"
				} else {
					it.Destination = dest
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

	_, err = s.DB.Pool.Exec(ctx, "UPDATE withdrawals SET status = 'selesai', processed_at = CURRENT_TIMESTAMP WHERE id = $1", withdrawalID)
	if err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=Gagal+menyetujui+penarikan: "+err.Error(), http.StatusSeeOther)
		return
	}

	s.logAudit(&admin.ID, "withdrawal_approve", "withdrawals", idStr, fmt.Sprintf("approved %d %s", amount, currency))
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

	// 1. Ubah status jadi ditolak
	_, err = tx.Exec(ctx, "UPDATE withdrawals SET status = 'ditolak', processed_at = CURRENT_TIMESTAMP WHERE id = $1", withdrawalID)
	if err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=Gagal+mengubah+status+penarikan", http.StatusSeeOther)
		return
	}

	// 2. Refund saldo pekerja di ledger_entries (kind = 'koreksi')
	refundNote := fmt.Sprintf("Pengembalian dana penarikan #%d ditolak", withdrawalID)
	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'koreksi', $2, $3, $4)
	`, userID, currency, amount, refundNote)
	if err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=Gagal+mengembalikan+saldo: "+err.Error(), http.StatusSeeOther)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Redirect(w, r, "/admin/withdrawals?err=Gagal+menyelesaikan+transaksi+penolakan", http.StatusSeeOther)
		return
	}

	s.logAudit(&admin.ID, "withdrawal_reject", "withdrawals", idStr, fmt.Sprintf("rejected %d %s (refunded)", amount, currency))
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
		query = `
			SELECT u.id, COALESCE(u.username, u.email), u.email, u.status,
			       (SELECT COUNT(*) FROM wa_accounts WHERE worker_id = u.id) AS account_count,
			       (SELECT COUNT(*) FROM wa_accounts WHERE worker_id = u.id AND status = 'terhubung') AS active_account_count,
			       (SELECT COUNT(*) FROM ledger_entries WHERE user_id = u.id AND kind = 'upah') AS sent_count,
			       user_balance(u.id, 'IDR') AS balance_idr,
			       user_balance(u.id, 'USDT') AS balance_usdt
			FROM users u
			WHERE u.role = 'worker' AND (u.username ILIKE $1 OR u.email ILIKE $1)
			ORDER BY u.id DESC
		`
		args = append(args, "%"+q+"%")
	} else {
		query = `
			SELECT u.id, COALESCE(u.username, u.email), u.email, u.status,
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
		http.Error(w, "Gagal memuat pekerja: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var workers []WorkerListItem
	for rows.Next() {
		var item WorkerListItem
		if err := rows.Scan(&item.ID, &item.Username, &item.Email, &item.Status, &item.AccountCount, &item.ActiveAccountCount, &item.SentCount, &item.BalanceIDR, &item.BalanceUSDT); err == nil {
			item.BalanceIDRFormatted = formatIDR(item.BalanceIDR)
			item.BalanceUSDTFormatted = formatUSDT(item.BalanceUSDT)
			workers = append(workers, item)
		}
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
		ActiveNav   string
		SearchQuery string
		Workers     []WorkerListItem
		T           func(string) string
	}{
		Lang:        lang,
		Title:       "Daftar Pekerja - GO BLAST",
		Theme:       getTheme(r),
		CSRFToken:   getCSRF(r),
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
