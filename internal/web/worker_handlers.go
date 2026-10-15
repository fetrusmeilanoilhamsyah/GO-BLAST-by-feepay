package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"goblast/internal/auth"
	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/i18n"
	"goblast/internal/money"
	"goblast/internal/timeutil"
	"goblast/internal/withdraw"
)

type WorkerActivityView struct {
	ID              int64
	Kind            string
	Amount          int64
	AmountFormatted string
	IsCredit        bool
	Currency        string
	CreatedAt       string
	Note            string
}

type WorkerWithdrawalView struct {
	ID              int64
	Currency        string
	Amount          int64
	AmountFormatted string
	Method          string
	Status          string
	RequestedAt     string
	ProcessedAt     string
}

type WorkerAccountSummaryView struct {
	ID         int64
	Label      string
	Speed      string
	Status     string
	IsWorking  bool
	StopReason string
	CreatedAt  string
}

type WorkerHistoryView struct {
	SentDate     string
	CampaignName string
	MessageCount int64
	TotalWage    int64
	Currency     string
}

type CurrencyIncomeView struct {
	Currency  string
	TotalWage int64
	Balance   int64
}

type WorkerBroadcastView struct {
	ID        int64
	Title     string
	Message   string
	Badge     string
	CreatedAt string
}

type CSLinks struct {
	CSWhatsAppURL      string
	CSTelegramURL      string
	ChannelTelegramURL string
	SystemLocks        SystemLocksViewData
}

func (s *Server) csLinks(ctx ...context.Context) CSLinks {
	waURL := s.CSWhatsAppURL
	if waURL == "" || waURL == "https://wa.me/" {
		waURL = "https://wa.me/6281200000000"
	}
	tgCS := s.CSTelegramURL
	if tgCS == "" {
		tgCS = "https://t.me/feepay_cs"
	}
	tgCh := s.ChannelTelegramURL
	if tgCh == "" {
		tgCh = "https://t.me/goblast_channel"
	}
	var locks SystemLocksViewData
	if len(ctx) > 0 && ctx[0] != nil {
		locks = s.GetSystemLocks(ctx[0])
	}
	return CSLinks{
		CSWhatsAppURL:      waURL,
		CSTelegramURL:      tgCS,
		ChannelTelegramURL: tgCh,
		SystemLocks:        locks,
	}
}

func (s *Server) HandleWorkerHome(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	lang := user.Language
	if lang == "" {
		lang = "id"
	}

	// 1. Saldo saat ini (IDR dan USDT terpisah)
	var balanceIDR int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, 'IDR')", user.ID).Scan(&balanceIDR)

	var balanceUSDT int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, 'USDT')", user.ID).Scan(&balanceUSDT)

	var mainBalance int64 = balanceIDR

	startOfTodayWIB := timeutil.GetJakartaStartOfDay()

	// 2. Pendapatan hari ini (upah + referral hari ini, Asia/Jakarta)
	var earnedTodayIDR int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind IN ('upah', 'referral') AND currency = 'IDR' AND created_at >= $2
	`, user.ID, startOfTodayWIB).Scan(&earnedTodayIDR)

	var earnedTodayUSDT int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind IN ('upah', 'referral') AND currency = 'USDT' AND created_at >= $2
	`, user.ID, startOfTodayWIB).Scan(&earnedTodayUSDT)

	// 3. Total upah terkumpul all-time
	var totalWageIDR int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'upah' AND currency = 'IDR'
	`, user.ID).Scan(&totalWageIDR)

	var totalWageUSDT int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'upah' AND currency = 'USDT'
	`, user.ID).Scan(&totalWageUSDT)

	// 4. Total bonus referral all-time
	var totalReferralIDR int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'referral' AND currency = 'IDR'
	`, user.ID).Scan(&totalReferralIDR)

	var totalReferralUSDT int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'referral' AND currency = 'USDT'
	`, user.ID).Scan(&totalReferralUSDT)

	totalEarnedIDR := totalWageIDR + totalReferralIDR
	totalEarnedUSDT := totalWageUSDT + totalReferralUSDT

	// 5. Total telah ditarik
	var totalWithdrawnIDR int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(ABS(amount)), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'penarikan' AND currency = 'IDR'
	`, user.ID).Scan(&totalWithdrawnIDR)

	// 6. Statistik Akun WhatsApp
	var totalAccounts int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM wa_accounts WHERE worker_id = $1", user.ID).Scan(&totalAccounts)

	var connectedAccounts int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM wa_accounts WHERE worker_id = $1 AND status = 'terhubung'", user.ID).Scan(&connectedAccounts)

	// 7. Total pesan terkirim
	var totalSent int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'upah'", user.ID).Scan(&totalSent)

	// 8. Aktivitas saldo terkini (10 teratas) dengan format aman
	rows, err := s.DB.Pool.Query(ctx, `
		SELECT id, kind, amount, currency, created_at, COALESCE(note, '')
		FROM ledger_entries
		WHERE user_id = $1
		ORDER BY id DESC
		LIMIT 10
	`, user.ID)

	var activities []WorkerActivityView
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var act WorkerActivityView
			var t time.Time
			if err := rows.Scan(&act.ID, &act.Kind, &act.Amount, &act.Currency, &t, &act.Note); err == nil {
				act.CreatedAt = t.Format("02 Jan 2006 15:04")
				absAmount := act.Amount
				if absAmount < 0 {
					absAmount = -absAmount
				}
				if act.Currency == "IDR" {
					if act.Amount < 0 {
						act.AmountFormatted = "-Rp " + formatIDR(absAmount)
						act.IsCredit = false
					} else {
						act.AmountFormatted = "+Rp " + formatIDR(absAmount)
						act.IsCredit = true
					}
				} else {
					if act.Amount < 0 {
						act.AmountFormatted = "-" + formatUSDT(absAmount) + " USDT"
						act.IsCredit = false
					} else {
						act.AmountFormatted = "+" + formatUSDT(absAmount) + " USDT"
						act.IsCredit = true
					}
				}
				activities = append(activities, act)
			}
		}
	}

	// 9. Sisa nomor tersedia — hanya kampanye berstatus 'berjalan' dan tidak diarsipkan (ClaimableCount)
	var availableNumbers int64
	if count, err := campaign.ClaimableCount(ctx, s.DB.Pool); err == nil {
		availableNumbers = int64(count)
	}

	var runningAccountsGlobal int
	if s.WorkCtrl != nil {
		runningAccountsGlobal = s.WorkCtrl.RunningCount()
	}

	// 9b. Rate upah dari settings DB
	var rateWageIDR int64 = 25
	var rwJSON string
	if err := s.DB.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'rate_wage'").Scan(&rwJSON); err == nil {
		var intVal int64
		if err := json.Unmarshal([]byte(rwJSON), &intVal); err == nil && intVal > 0 {
			rateWageIDR = intVal
		} else {
			var rw map[string]int64
			if err := json.Unmarshal([]byte(rwJSON), &rw); err == nil {
				if idr, ok := rw["IDR"]; ok {
					rateWageIDR = idr
				}
			}
		}
	}

	var rateIDRtoUSDT int64
	var kursJSON string
	_ = s.DB.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'rate_idr_to_usdt'").Scan(&kursJSON)
	_ = json.Unmarshal([]byte(kursJSON), &rateIDRtoUSDT)

	var kursUpdatedAt string
	_ = s.DB.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'rate_idr_to_usdt_updated_at'").Scan(&kursUpdatedAt)
	kursUpdatedAt = strings.Trim(kursUpdatedAt, `"`)

	var rateWageUSDTApprox string
	if rateIDRtoUSDT > 0 && kursUpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, kursUpdatedAt); err == nil {
			if time.Since(t).Hours() <= float64(config.KursMaxAgeHours) {
				micro, errCalc := money.UsdtMicroFromIDR(rateWageIDR, rateIDRtoUSDT)
				if errCalc == nil && micro > 0 {
					rateWageUSDTApprox = money.FormatUSDT(micro)
				}
			}
		}
	}

	// 10. Daftar akun WhatsApp milik pekerja
	var accounts []WorkerAccountSummaryView
	accRows, err := s.DB.Pool.Query(ctx, `
		SELECT id, label, speed, status, COALESCE(stop_reason, ''), created_at
		FROM wa_accounts
		WHERE worker_id = $1
		ORDER BY id ASC
	`, user.ID)
	if err == nil {
		defer accRows.Close()
		for accRows.Next() {
			var acc WorkerAccountSummaryView
			var t time.Time
			if err := accRows.Scan(&acc.ID, &acc.Label, &acc.Speed, &acc.Status, &acc.StopReason, &t); err == nil {
				acc.CreatedAt = t.Format("02 Jan 2006")
				if s.WorkCtrl != nil {
					acc.IsWorking = s.WorkCtrl.IsRunning(acc.ID)
				}
				accounts = append(accounts, acc)
			}
		}
	}

	// 11. Ringkasan penghasilan per mata uang
	var incomes []CurrencyIncomeView
	incRows, err := s.DB.Pool.Query(ctx, `
		SELECT currency, COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'upah'
		GROUP BY currency
		ORDER BY currency ASC
	`, user.ID)
	if err == nil {
		defer incRows.Close()
		for incRows.Next() {
			var inc CurrencyIncomeView
			if err := incRows.Scan(&inc.Currency, &inc.TotalWage); err == nil {
				_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, $2)", user.ID, inc.Currency).Scan(&inc.Balance)
				incomes = append(incomes, inc)
			}
		}
	}

	// 12. Broadcast pengumuman aktif dari admin
	var activeBroadcast *WorkerBroadcastView
	var bID int64
	var bTitle, bMessage, bBadge string
	var bCreatedAt time.Time
	if err := s.DB.Pool.QueryRow(ctx, `
		SELECT id, title, message, badge, created_at
		FROM broadcasts
		WHERE is_active = true
		ORDER BY id DESC
		LIMIT 1
	`).Scan(&bID, &bTitle, &bMessage, &bBadge, &bCreatedAt); err == nil {
		activeBroadcast = &WorkerBroadcastView{
			ID:        bID,
			Title:     bTitle,
			Message:   bMessage,
			Badge:     bBadge,
			CreatedAt: bCreatedAt.Format("02 Jan 2006 15:04"),
		}
	}

	data := struct {
		CSLinks
		Lang                     string
		Title                    string
		Theme                    string
		CSRFToken                string
		ActiveNav                string
		User                     UserSession
		Success                  string
		Error                    string
		Balance                  int64
		BalanceIDR               int64
		BalanceUSDT              int64
		BalanceIDRFormatted      string
		BalanceUSDTFormatted     string
		EarnedTodayIDR           int64
		EarnedTodayUSDT          int64
		EarnedTodayIDRFormatted  string
		EarnedTodayUSDTFormatted string
		TotalEarnedIDR           int64
		TotalEarnedUSDT          int64
		TotalEarnedIDRFormatted  string
		TotalEarnedUSDTFormatted string
		TotalWage                 int64
		TotalReferral             int64
		TotalWithdrawn            int64
		RateWageIDR               int64
		RateWageUSDTApprox        string
		RateWageIDRFormatted      string
		RateWageUSDTFormatted     string
		TotalReferralIDR          int64
		TotalReferralUSDT         int64
		TotalReferralIDRFormatted string
		TotalReferralUSDTFormatted string
		TotalAccounts             int64
		ConnectedAccounts         int64
		TotalSent                 int64
		AvailableNumbers          int64
		AvailableNumbersFormatted string
		RunningAccountsGlobal     int
		Broadcast                 *WorkerBroadcastView
		Accounts                  []WorkerAccountSummaryView
		Incomes                   []CurrencyIncomeView
		Activities                []WorkerActivityView
		Nonce                     string
		T                         func(string) string
	}{
		CSLinks:                   s.csLinks(r.Context()),
		Lang:                      lang,
		Title:                     i18n.T(lang, "worker.home_title"),
		Theme:                     getTheme(r),
		CSRFToken:                 getCSRF(r),
		Nonce:                     getNonce(r),
		ActiveNav:                 "home",
		User:                      user,
		Success:                   r.URL.Query().Get("success"),
		Error:                     r.URL.Query().Get("error"),
		Balance:                   mainBalance,
		BalanceIDR:               balanceIDR,
		BalanceUSDT:              balanceUSDT,
		BalanceIDRFormatted:      formatIDR(balanceIDR),
		BalanceUSDTFormatted:     formatUSDT(balanceUSDT),
		EarnedTodayIDR:           earnedTodayIDR,
		EarnedTodayUSDT:          earnedTodayUSDT,
		EarnedTodayIDRFormatted:  formatIDR(earnedTodayIDR),
		EarnedTodayUSDTFormatted: formatUSDT(earnedTodayUSDT),
		TotalEarnedIDR:           totalEarnedIDR,
		TotalEarnedUSDT:          totalEarnedUSDT,
		TotalEarnedIDRFormatted:  formatIDR(totalEarnedIDR),
		TotalEarnedUSDTFormatted: formatUSDT(totalEarnedUSDT),
		TotalWage:                totalWageIDR,
		TotalReferral:            totalReferralIDR,
		TotalWithdrawn:           totalWithdrawnIDR,
		RateWageIDR:               rateWageIDR,
		RateWageUSDTApprox:        rateWageUSDTApprox,
		RateWageIDRFormatted:      money.FormatIDR(rateWageIDR),
		RateWageUSDTFormatted:     rateWageUSDTApprox,
		TotalReferralIDR:          totalReferralIDR,
		TotalReferralUSDT:         totalReferralUSDT,
		TotalReferralIDRFormatted: formatIDR(totalReferralIDR),
		TotalReferralUSDTFormatted: formatUSDT(totalReferralUSDT),
		TotalAccounts:            totalAccounts,
		ConnectedAccounts:        connectedAccounts,
		TotalSent:                totalSent,
		AvailableNumbers:         availableNumbers,
		AvailableNumbersFormatted: formatIDR(availableNumbers),
		RunningAccountsGlobal:     runningAccountsGlobal,
		Broadcast:                activeBroadcast,
		Accounts:                 accounts,
		Incomes:                  incomes,
		Activities:               activities,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["worker_home.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleWorkerHistoryGET(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	lang := user.Language
	if lang == "" {
		lang = "id"
	}

	pageStr := r.URL.Query().Get("page")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}
	limit := 20
	offset := (page - 1) * limit

	// Query riwayat agregat per tanggal & kampanye
	// CATATAN KEAMANAN & PRIVASI: Tidak ada kolom nomor telepon customer yang diseleksi sama sekali
	rows, err := s.DB.Pool.Query(ctx, `
		SELECT 
			DATE(le.created_at) as sent_date,
			COALESCE(c.name, 'Kampanye #' || COALESCE(c.id::text, '1')) as campaign_name,
			COUNT(le.id) as message_count,
			SUM(le.amount) as total_wage,
			le.currency
		FROM ledger_entries le
		LEFT JOIN campaign_numbers cn ON le.campaign_number_id = cn.id
		LEFT JOIN campaigns c ON cn.campaign_id = c.id
		WHERE le.user_id = $1 AND le.kind = 'upah'
		GROUP BY DATE(le.created_at), c.id, c.name, le.currency
		ORDER BY sent_date DESC
		LIMIT $2 OFFSET $3
	`, user.ID, limit+1, offset)

	var historyItems []WorkerHistoryView
	hasMore := false
	if err == nil {
		defer rows.Close()
		count := 0
		for rows.Next() {
			count++
			if count > limit {
				hasMore = true
				break
			}
			var item WorkerHistoryView
			var d time.Time
			if err := rows.Scan(&d, &item.CampaignName, &item.MessageCount, &item.TotalWage, &item.Currency); err == nil {
				item.SentDate = d.Format("02 Jan 2006")
				historyItems = append(historyItems, item)
			}
		}
	}

	data := struct {
		CSLinks
		Lang         string
		Title        string
		Theme        string
		CSRFToken    string
		ActiveNav    string
		User         UserSession
		HistoryItems []WorkerHistoryView
		Page         int
		PrevPage     int
		NextPage     int
		HasMore      bool
		Nonce        string
		T            func(string) string
	}{
		CSLinks:      s.csLinks(r.Context()),
		Lang:         lang,
		Title:        i18n.T(lang, "nav.history"),
		Theme:        getTheme(r),
		CSRFToken:    getCSRF(r),
		Nonce:        getNonce(r),
		ActiveNav:    "history",
		User:         user,
		HistoryItems: historyItems,
		Page:         page,
		PrevPage:     page - 1,
		NextPage:     page + 1,
		HasMore:      hasMore,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["worker_history.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleWorkerWithdrawalsGET(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	lang := user.Language
	if lang == "" {
		lang = "id"
	}

	// 1. Saldo dapat ditarik
	var balanceIDR int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, 'IDR')", user.ID).Scan(&balanceIDR)

	var balanceUSDT int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, 'USDT')", user.ID).Scan(&balanceUSDT)

	// Baca rate_idr_to_usdt dari settings
	var rateJSON string
	var rateIDRtoUSDT int64 = 15000
	if err := s.DB.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'rate_idr_to_usdt'").Scan(&rateJSON); err == nil {
		_ = json.Unmarshal([]byte(rateJSON), &rateIDRtoUSDT)
	}
	if rateIDRtoUSDT <= 0 {
		rateIDRtoUSDT = 15000
	}

	// Segarkan info wallet user dari DB jika ada
	var dbMethod, dbDestEnc, dbAccName *string
	if err := s.DB.Pool.QueryRow(ctx, `
		SELECT payout_method, payout_dest_encrypted, payout_account_name
		FROM users
		WHERE id = $1
	`, user.ID).Scan(&dbMethod, &dbDestEnc, &dbAccName); err == nil {
		if dbMethod != nil {
			user.PayoutMethod = *dbMethod
		}
		if dbAccName != nil {
			user.PayoutAccountName = *dbAccName
		}
		if dbDestEnc != nil && *dbDestEnc != "" {
			if dec, err := withdraw.DecryptDestination(*dbDestEnc, s.EncryptionKey); err == nil {
				// Bila format dec memiliki " (Nama)", bersihkan untuk masking
				cleanDest := dec
				if idx := strings.Index(cleanDest, " ("); idx != -1 {
					cleanDest = cleanDest[:idx]
				}
				user.PayoutDestMasked = MaskDestination(cleanDest, user.PayoutMethod)
			}
		}
	}

	// 2. Riwayat penarikan
	rows, err := s.DB.Pool.Query(ctx, `
		SELECT id, currency, amount, method, status, requested_at, processed_at
		FROM withdrawals
		WHERE user_id = $1
		ORDER BY id DESC
		LIMIT 50
	`, user.ID)

	var list []WorkerWithdrawalView
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var wRow WorkerWithdrawalView
			var reqAt time.Time
			var procAt *time.Time
			if err := rows.Scan(&wRow.ID, &wRow.Currency, &wRow.Amount, &wRow.Method, &wRow.Status, &reqAt, &procAt); err == nil {
				wRow.RequestedAt = reqAt.Format("02 Jan 2006 15:04")
				if procAt != nil {
					wRow.ProcessedAt = procAt.Format("02 Jan 2006 15:04")
				} else {
					wRow.ProcessedAt = "-"
				}
				if wRow.Currency == "IDR" {
					wRow.AmountFormatted = formatIDR(wRow.Amount)
				} else {
					wRow.AmountFormatted = formatUSDT(wRow.Amount)
				}
				list = append(list, wRow)
			}
		}
	}

	errMsg := r.URL.Query().Get("error")
	successMsg := r.URL.Query().Get("success")
	if successMsg == "1" {
		successMsg = i18n.T(lang, "worker.withdraw_success")
	}

	convertRequestToken, _, _ := auth.GenerateSessionToken()
	withdrawRequestToken, _, _ := auth.GenerateSessionToken()

	var withdrawLockedUntil *time.Time
	_ = s.DB.Pool.QueryRow(ctx, "SELECT withdraw_locked_until FROM users WHERE id = $1", user.ID).Scan(&withdrawLockedUntil)

	isWithdrawLocked := false
	var withdrawLockedMessage string
	if withdrawLockedUntil != nil && withdrawLockedUntil.After(time.Now()) {
		isWithdrawLocked = true
		remainingDuration := time.Until(*withdrawLockedUntil)
		hours := int(remainingDuration.Hours())
		mins := int(remainingDuration.Minutes()) % 60
		if hours > 0 {
			withdrawLockedMessage = fmt.Sprintf("Penarikan dana sedang ditahan sementara demi keamanan akun setelah perubahan rekening/kata sandi (sisa waktu: %d jam %d menit)", hours, mins)
		} else {
			withdrawLockedMessage = fmt.Sprintf("Penarikan dana sedang ditahan sementara demi keamanan akun setelah perubahan rekening/kata sandi (sisa waktu: %d menit)", mins)
		}
	}

	data := struct {
		CSLinks
		Lang                    string
		Title                   string
		Theme                   string
		CSRFToken               string
		ActiveNav               string
		User                    UserSession
		Balance                 int64
		BalanceIDR              int64
		BalanceIDRFormatted     string
		BalanceUSDT             int64
		BalanceUSDTFormatted    string
		RateIDRtoUSDT           int64
		RateIDRtoUSDTDisplay    string
		ConvertRequestToken     string
		MinConvertIDRFormatted  string
		WithdrawRequestToken    string
		IsWithdrawLocked        bool
		WithdrawLockedMessage   string
		MinWithdrawIDRFormatted string
		MinWithdrawUSDTFormatted string
		Withdrawals             []WorkerWithdrawalView
		Error                   string
		Success                 string
		Nonce                   string
		T                       func(string) string
	}{
		CSLinks:                 s.csLinks(r.Context()),
		Lang:                    lang,
		Title:                   i18n.T(lang, "nav.withdrawals"),
		Theme:                   getTheme(r),
		CSRFToken:               getCSRF(r),
		Nonce:                   getNonce(r),
		ActiveNav:               "withdrawals",
		User:                    user,
		Balance:                 balanceIDR,
		BalanceIDR:              balanceIDR,
		BalanceIDRFormatted:     money.FormatIDR(balanceIDR),
		BalanceUSDT:             balanceUSDT,
		BalanceUSDTFormatted:    money.FormatUSDT(balanceUSDT),
		RateIDRtoUSDT:           rateIDRtoUSDT,
		RateIDRtoUSDTDisplay:    money.FormatIDR(rateIDRtoUSDT),
		ConvertRequestToken:     convertRequestToken,
		MinConvertIDRFormatted:  money.FormatIDR(config.MinConvertIDR),
		WithdrawRequestToken:    withdrawRequestToken,
		IsWithdrawLocked:        isWithdrawLocked,
		WithdrawLockedMessage:   withdrawLockedMessage,
		MinWithdrawIDRFormatted: money.FormatIDR(config.MinWithdrawIDR),
		MinWithdrawUSDTFormatted: money.FormatUSDT(config.MinWithdrawUSDTMicro),
		Withdrawals:             list,
		Error:                   errMsg,
		Success:                 successMsg,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["worker_withdrawals.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleWorkerWithdrawalsPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	if s.IsWithdrawalsLocked(ctx) {
		http.Redirect(w, r, "/worker/withdrawals?error=Fitur+penarikan+dana+sedang+dikunci+sementara", http.StatusSeeOther)
		return
	}
	amountStr := strings.TrimSpace(r.FormValue("amount"))
	method := strings.ToLower(strings.TrimSpace(r.FormValue("method")))
	dest := strings.TrimSpace(r.FormValue("destination"))
	accountName := strings.TrimSpace(r.FormValue("account_name"))
	requestToken := strings.TrimSpace(r.FormValue("request_token"))
	if requestToken == "" {
		rnd, _, _ := auth.GenerateSessionToken()
		requestToken = rnd
	}

	var payoutMethod string
	var encryptedDest string
	var directDestHash string

	// Jika dest diisi langsung di form (backward compatible / tes)
	if dest != "" {
		switch method {
		case "dana", "seabank", "bca", "bank", "usdt_bep20":
		default:
			http.Redirect(w, r, "/worker/withdrawals?error=Metode+penarikan+tidak+didukung", http.StatusSeeOther)
			return
		}

		if method != "usdt_bep20" && accountName == "" {
			http.Redirect(w, r, "/worker/withdrawals?error=Nomor+rekening+dan+nama+pemilik+wajib+diisi", http.StatusSeeOther)
			return
		}

		fullDest := dest
		if accountName != "" {
			fullDest = fmt.Sprintf("%s (%s)", dest, accountName)
		}
		enc, err := withdraw.EncryptDestination(fullDest, s.EncryptionKey)
		if err != nil {
			http.Redirect(w, r, "/worker/withdrawals?error=Gagal+mengenkripsi+data+tujuan", http.StatusSeeOther)
			return
		}
		payoutMethod = method
		encryptedDest = enc
		directDestHash = withdraw.HashDestination(withdraw.NormalizeDestination(dest), s.EncryptionKey)
	} else {
		// Gunakan rekening yang sudah tersimpan di akun user
		var dbMethod, dbDestEnc, dbAccName *string
		err := s.DB.Pool.QueryRow(ctx, `
			SELECT payout_method, payout_dest_encrypted, payout_account_name
			FROM users
			WHERE id = $1
		`, user.ID).Scan(&dbMethod, &dbDestEnc, &dbAccName)
		if err != nil || dbMethod == nil || dbDestEnc == nil || *dbMethod == "" || *dbDestEnc == "" {
			http.Redirect(w, r, "/worker/withdrawals?error=Silakan+atur+rekening+atau+wallet+tujuan+di+pengaturan+akun+terlebih+dahulu", http.StatusSeeOther)
			return
		}
		payoutMethod = *dbMethod
		encryptedDest = *dbDestEnc
	}

	var currency string
	var amount int64
	if payoutMethod == "usdt_bep20" {
		currency = "USDT"
		parsed, pErr := parseUSDTInput(amountStr)
		if pErr != nil || parsed <= 0 {
			http.Redirect(w, r, "/worker/withdrawals?error=Nominal+penarikan+tidak+valid", http.StatusSeeOther)
			return
		}
		if parsed < config.MinWithdrawUSDTMicro {
			http.Redirect(w, r, fmt.Sprintf("/worker/withdrawals?error=Jumlah+minimal+penarikan+USDT+adalah+%s+USDT", money.FormatUSDT(config.MinWithdrawUSDTMicro)), http.StatusSeeOther)
			return
		}
		amount = parsed
	} else {
		currency = "IDR"
		parsed, pErr := strconv.ParseInt(amountStr, 10, 64)
		if pErr != nil || parsed <= 0 {
			http.Redirect(w, r, "/worker/withdrawals?error=Nominal+penarikan+tidak+valid", http.StatusSeeOther)
			return
		}
		if parsed < config.MinWithdrawIDR {
			http.Redirect(w, r, fmt.Sprintf("/worker/withdrawals?error=Jumlah+minimal+penarikan+adalah+Rp+%s", money.FormatIDR(config.MinWithdrawIDR)), http.StatusSeeOther)
			return
		}
		amount = parsed
	}

	// Transaksi atomik tunggal
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Terjadi+kesalahan+sistem", http.StatusSeeOther)
		return
	}
	defer tx.Rollback(ctx)

	// 1. Kunci baris user (FOR UPDATE) dan periksa status & penahanan penarikan
	var lockedUserID int64
	var userStatus string
	var withdrawLockedUntil *time.Time
	err = tx.QueryRow(ctx, "SELECT id, status, withdraw_locked_until FROM users WHERE id = $1 FOR UPDATE", user.ID).Scan(&lockedUserID, &userStatus, &withdrawLockedUntil)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Terjadi+kesalahan+sistem", http.StatusSeeOther)
		return
	}
	if userStatus != "active" {
		_ = tx.Rollback(ctx)
		http.Redirect(w, r, "/worker/withdrawals?error=Akun+Anda+sedang+dibekukan+atau+tidak+aktif", http.StatusSeeOther)
		return
	}
	if withdrawLockedUntil != nil && withdrawLockedUntil.After(time.Now()) {
		_ = tx.Rollback(ctx)
		http.Redirect(w, r, "/worker/withdrawals?error=Penarikan+dana+sedang+ditahan+sementara+demi+keamanan+akun+setelah+perubahan+rekening+atau+kata+sandi", http.StatusSeeOther)
		return
	}

	// 2. Cek token ganda idempoten
	var existingID int64
	errExist := tx.QueryRow(ctx, `
		SELECT id FROM withdrawals WHERE user_id = $1 AND request_token = $2
	`, user.ID, requestToken).Scan(&existingID)
	if errExist == nil {
		_ = tx.Rollback(ctx)
		http.Redirect(w, r, "/worker/withdrawals?success=1", http.StatusSeeOther)
		return
	}

	// 3. Cek jumlah permintaan menunggu (MaxPendingWithdrawals = 1)
	var pendingCount int
	err = tx.QueryRow(ctx, "SELECT COUNT(*) FROM withdrawals WHERE user_id = $1 AND status = 'menunggu'", user.ID).Scan(&pendingCount)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Terjadi+kesalahan+sistem", http.StatusSeeOther)
		return
	}
	if pendingCount >= config.MaxPendingWithdrawals {
		_ = tx.Rollback(ctx)
		http.Redirect(w, r, "/worker/withdrawals?error=Anda+masih+memiliki+pengajuan+penarikan+yang+sedang+menunggu+diproses.+Harap+tunggu+hingga+selesai.", http.StatusSeeOther)
		return
	}

	// 4. Cek saldo sesuai mata uang di dalam transaksi
	var currentBalance int64
	err = tx.QueryRow(ctx, "SELECT user_balance($1, $2)", user.ID, currency).Scan(&currentBalance)
	if err != nil || currentBalance < amount {
		_ = tx.Rollback(ctx)
		http.Redirect(w, r, "/worker/withdrawals?error=Saldo+Anda+tidak+mencukupi+untuk+penarikan+ini", http.StatusSeeOther)
		return
	}

	// 5. Update rekening pengguna jika diisi langsung di form
	if dest != "" {
		_, _ = tx.Exec(ctx, `
			UPDATE users
			SET payout_method = $1, payout_dest_encrypted = $2, payout_account_name = $3, payout_dest_hash = $4
			WHERE id = $5
		`, payoutMethod, encryptedDest, accountName, directDestHash, user.ID)
	}

	// 6. Insert ke withdrawals
	var withdrawalID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO withdrawals (user_id, currency, amount, method, destination_encrypted, request_token, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'menunggu')
		RETURNING id
	`, user.ID, currency, amount, payoutMethod, encryptedDest, requestToken).Scan(&withdrawalID)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Gagal+membuat+pengajuan+penarikan", http.StatusSeeOther)
		return
	}

	// 7. Catat di buku besar (nominal negatif)
	note := fmt.Sprintf("Pengajuan penarikan #%d", withdrawalID)
	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'penarikan', $2, $3, $4)
	`, user.ID, currency, -amount, note)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Saldo+tidak+mencukupi+atau+transaksi+batal", http.StatusSeeOther)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Gagal+menyimpan+transaksi+penarikan", http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "request_withdrawal", "withdrawals", strconv.FormatInt(withdrawalID, 10), fmt.Sprintf("%d %s (%s)", amount, currency, payoutMethod))
	http.Redirect(w, r, "/worker/withdrawals?success=1", http.StatusSeeOther)
}

func (s *Server) HandleWorkerConvertPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	if s.IsWithdrawalsLocked(ctx) {
		http.Redirect(w, r, "/worker/withdrawals?error=Fitur+konversi+saldo+sedang+dikunci+sementara", http.StatusSeeOther)
		return
	}
	token := strings.TrimSpace(r.FormValue("request_token"))
	if token == "" {
		http.Redirect(w, r, "/worker/withdrawals?error=Permintaan+tidak+valid+(token+kosong)", http.StatusSeeOther)
		return
	}

	amountIDRStr := strings.TrimSpace(r.FormValue("amount_idr"))
	amountIDR, err := strconv.ParseInt(amountIDRStr, 10, 64)
	if err != nil || amountIDR <= 0 {
		http.Redirect(w, r, "/worker/withdrawals?error=Nominal+konversi+IDR+tidak+valid", http.StatusSeeOther)
		return
	}

	if amountIDR < config.MinConvertIDR {
		http.Redirect(w, r, fmt.Sprintf("/worker/withdrawals?error=Jumlah+minimal+konversi+adalah+Rp+%s", money.FormatIDR(config.MinConvertIDR)), http.StatusSeeOther)
		return
	}

	// Baca kurs rate_idr_to_usdt dari settings DB
	var rateJSON string
	err = s.DB.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'rate_idr_to_usdt'").Scan(&rateJSON)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Kurs+konversi+belum+diatur+admin", http.StatusSeeOther)
		return
	}

	var rateIDRtoUSDT int64
	if err := json.Unmarshal([]byte(rateJSON), &rateIDRtoUSDT); err != nil || rateIDRtoUSDT <= 0 {
		http.Redirect(w, r, "/worker/withdrawals?error=Kurs+konversi+belum+diatur+admin", http.StatusSeeOther)
		return
	}

	// Periksa usia kurs (KursMaxAgeHours)
	var kursUpdatedAt string
	_ = s.DB.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'rate_idr_to_usdt_updated_at'").Scan(&kursUpdatedAt)
	kursUpdatedAt = strings.Trim(kursUpdatedAt, `"`)
	if kursUpdatedAt == "" {
		http.Redirect(w, r, "/worker/withdrawals?error=Kurs+konversi+belum+divalidasi+admin", http.StatusSeeOther)
		return
	}
	tUpdated, errTime := time.Parse(time.RFC3339, kursUpdatedAt)
	if errTime != nil || time.Since(tUpdated).Hours() > float64(config.KursMaxAgeHours) {
		http.Redirect(w, r, "/worker/withdrawals?error=Kurs+konversi+telah+kedaluwarsa+(lebih+dari+24+jam),+silakan+hubungi+admin", http.StatusSeeOther)
		return
	}

	// Hitung mikro-USDT dengan pembulatan ke bawah (semua bilangan bulat murni)
	usdtAmount, errCalc := money.UsdtMicroFromIDR(amountIDR, rateIDRtoUSDT)
	if errCalc != nil || usdtAmount <= 0 {
		http.Redirect(w, r, "/worker/withdrawals?error=Jumlah+terlalu+kecil+untuk+dikonversi", http.StatusSeeOther)
		return
	}

	// Transaksi atomik tunggal
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Terjadi+kesalahan+sistem", http.StatusSeeOther)
		return
	}
	defer tx.Rollback(ctx)

	// 1. Kunci baris user (FOR UPDATE)
	var lockedUserID int64
	var userStatus string
	err = tx.QueryRow(ctx, "SELECT id, status FROM users WHERE id = $1 FOR UPDATE", user.ID).Scan(&lockedUserID, &userStatus)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Terjadi+kesalahan+sistem", http.StatusSeeOther)
		return
	}
	if userStatus != "active" {
		_ = tx.Rollback(ctx)
		http.Redirect(w, r, "/worker/withdrawals?error=Akun+Anda+sedang+dibekukan+atau+tidak+aktif", http.StatusSeeOther)
		return
	}

	// 2. Cek apakah token ganda sudah pernah dipakai
	var existingID, existingIDR, existingUSDT int64
	errExist := tx.QueryRow(ctx, `
		SELECT id, idr_amount, usdt_micro
		FROM currency_conversions
		WHERE user_id = $1 AND request_token = $2
	`, user.ID, token).Scan(&existingID, &existingIDR, &existingUSDT)
	if errExist == nil {
		_ = tx.Rollback(ctx)
		msg := fmt.Sprintf("Konversi sebelumnya telah berhasil (Rp %s menjadi %s USDT)", money.FormatIDR(existingIDR), money.FormatUSDT(existingUSDT))
		http.Redirect(w, r, fmt.Sprintf("/worker/withdrawals?success=%s", url.QueryEscape(msg)), http.StatusSeeOther)
		return
	}

	// 3. Cek kecukupan saldo rupiah
	var balanceIDR int64
	_ = tx.QueryRow(ctx, "SELECT user_balance($1, 'IDR')", user.ID).Scan(&balanceIDR)
	if balanceIDR < amountIDR {
		http.Redirect(w, r, "/worker/withdrawals?error=Saldo+IDR+Anda+tidak+mencukupi+untuk+konversi+ini", http.StatusSeeOther)
		return
	}

	// 4. Insert currency_conversions
	var conversionID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO currency_conversions (user_id, idr_amount, usdt_micro, kurs, request_token)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, user.ID, amountIDR, usdtAmount, rateIDRtoUSDT, token).Scan(&conversionID)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Gagal+mencatat+konversi", http.StatusSeeOther)
		return
	}

	// 5. Insert dua baris ledger_entries kind 'konversi' dengan conversion_id
	noteIDR := fmt.Sprintf("Konversi ke USDT (kurs Rp %s/USDT)", money.FormatIDR(rateIDRtoUSDT))
	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, conversion_id, note)
		VALUES ($1, 'konversi', 'IDR', $2, $3, $4)
	`, user.ID, -amountIDR, conversionID, noteIDR)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Gagal+mengurangi+saldo+IDR", http.StatusSeeOther)
		return
	}

	noteUSDT := fmt.Sprintf("Hasil konversi dari IDR (Rp %s)", money.FormatIDR(amountIDR))
	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, conversion_id, note)
		VALUES ($1, 'konversi', 'USDT', $2, $3, $4)
	`, user.ID, usdtAmount, conversionID, noteUSDT)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Gagal+menambah+saldo+USDT", http.StatusSeeOther)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Gagal+menyelesaikan+transaksi+konversi", http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "convert_idr_to_usdt", "currency_conversions", strconv.FormatInt(conversionID, 10),
		fmt.Sprintf("converted %d IDR to %d micro-USDT at rate %d", amountIDR, usdtAmount, rateIDRtoUSDT))

	successMsg := fmt.Sprintf("Berhasil menukar Rp %s menjadi %s USDT", money.FormatIDR(amountIDR), money.FormatUSDT(usdtAmount))
	http.Redirect(w, r, fmt.Sprintf("/worker/withdrawals?success=%s", url.QueryEscape(successMsg)), http.StatusSeeOther)
}

func (s *Server) HandleWorkerWalletPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	target := "/worker"
	referer := r.Header.Get("Referer")
	if referer != "" {
		if refURL, err := url.Parse(referer); err == nil {
			if (refURL.Host == "" || refURL.Host == r.Host) && strings.HasPrefix(refURL.Path, "/") && !strings.HasPrefix(refURL.Path, "//") {
				target = refURL.RequestURI()
			}
		}
	}
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}

	method := strings.ToLower(strings.TrimSpace(r.FormValue("method")))
	destination := strings.TrimSpace(r.FormValue("destination"))
	accountName := strings.TrimSpace(r.FormValue("account_name"))

	switch method {
	case "seabank", "bca", "dana":
		if accountName == "" {
			http.Redirect(w, r, target+sep+"error=Nama+pemilik+rekening+wajib+diisi", http.StatusSeeOther)
			return
		}
	case "usdt_bep20":
		if accountName == "" {
			accountName = "USDT BEP20"
		}
	default:
		http.Redirect(w, r, target+sep+"error=Metode+penarikan+tidak+didukung", http.StatusSeeOther)
		return
	}

	if len(destination) < 3 || len(destination) > 100 {
		http.Redirect(w, r, target+sep+"error=Nomor+rekening+atau+alamat+wallet+tidak+valid", http.StatusSeeOther)
		return
	}

	fullDest := destination
	if accountName != "" && method != "usdt_bep20" {
		fullDest = fmt.Sprintf("%s (%s)", destination, accountName)
	}

	encryptedDest, err := withdraw.EncryptDestination(fullDest, s.EncryptionKey)
	if err != nil {
		http.Redirect(w, r, target+sep+"error=Gagal+mengenkripsi+data+rekening", http.StatusSeeOther)
		return
	}

	normDest := withdraw.NormalizeDestination(destination)
	destHash := withdraw.HashDestination(normDest, s.EncryptionKey)

	_, err = s.DB.Pool.Exec(r.Context(), `
		UPDATE users
		SET payout_method = $1, payout_dest_encrypted = $2, payout_account_name = $3, payout_dest_hash = $4,
		    withdraw_locked_until = NOW() + make_interval(hours => $5)
		WHERE id = $6
	`, method, encryptedDest, accountName, destHash, config.WithdrawLockAfterChangeHours, user.ID)
	if err != nil {
		http.Redirect(w, r, target+sep+"error=Gagal+menyimpan+pengaturan+rekening", http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "update_payout_settings", "users", strconv.FormatInt(user.ID, 10), fmt.Sprintf("method: %s (penarikan ditahan %d jam)", method, config.WithdrawLockAfterChangeHours))
	http.Redirect(w, r, target+sep+"success=Rekening+tujuan+penarikan+berhasil+disimpan", http.StatusSeeOther)
}

func (s *Server) HandleWorkerReferralGET(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	lang := user.Language
	if lang == "" {
		lang = "id"
	}

	// 1. Ambil kode referral pengguna
	var referralCode string
	_ = s.DB.Pool.QueryRow(ctx, "SELECT referral_code FROM users WHERE id = $1", user.ID).Scan(&referralCode)

	// Susun link referral lengkap
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	referralLink := fmt.Sprintf("%s://%s/register?ref=%s", scheme, r.Host, referralCode)

	// 2. Jumlah rekan bergabung
	var friendsCount int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM users WHERE referred_by = $1", user.ID).Scan(&friendsCount)

	// 3. Total bonus referral didapat
	var totalBonus int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'referral' AND currency = 'IDR'
	`, user.ID).Scan(&totalBonus)

	// 4. Riwayat komisi referral (20 entri terakhir)
	rows, err := s.DB.Pool.Query(ctx, `
		SELECT id, kind, amount, currency, created_at, COALESCE(note, '')
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'referral'
		ORDER BY id DESC
		LIMIT 20
	`, user.ID)

	var bonusList []WorkerActivityView
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var item WorkerActivityView
			var t time.Time
			if err := rows.Scan(&item.ID, &item.Kind, &item.Amount, &item.Currency, &t, &item.Note); err == nil {
				item.CreatedAt = t.Format("02 Jan 2006 15:04")
				bonusList = append(bonusList, item)
			}
		}
	}

	data := struct {
		CSLinks
		Lang         string
		Title        string
		Theme        string
		CSRFToken    string
		ActiveNav    string
		User         UserSession
		ReferralCode string
		ReferralLink string
		FriendsCount int64
		TotalBonus   int64
		BonusList    []WorkerActivityView
		Nonce        string
		T            func(string) string
	}{
		CSLinks:      s.csLinks(r.Context()),
		Lang:         lang,
		Title:        i18n.T(lang, "nav.referral"),
		Theme:        getTheme(r),
		CSRFToken:    getCSRF(r),
		Nonce:        getNonce(r),
		ActiveNav:    "referral",
		User:         user,
		ReferralCode: referralCode,
		ReferralLink: referralLink,
		FriendsCount: friendsCount,
		TotalBonus:   totalBonus,
		BonusList:    bonusList,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["worker_referral.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleWorkerProfilePOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.FormValue("display_name"))
	if len(name) > 0 && len(name) <= 100 {
		_, _ = s.DB.Pool.Exec(r.Context(), "UPDATE users SET display_name = $1 WHERE id = $2", name, user.ID)
		s.logAudit(&user.ID, "update_profile", "users", strconv.FormatInt(user.ID, 10), "display name updated")
	}

	target := "/worker"
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

func (s *Server) HandleWorkerSecurityPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	oldPass := r.FormValue("old_password")
	newPass := r.FormValue("new_password")
	confirmPass := r.FormValue("confirm_password")

	target := "/worker"
	referer := r.Header.Get("Referer")
	if referer != "" {
		if refURL, err := url.Parse(referer); err == nil {
			if (refURL.Host == "" || refURL.Host == r.Host) && strings.HasPrefix(refURL.Path, "/") && !strings.HasPrefix(refURL.Path, "//") {
				target = refURL.RequestURI()
			}
		}
	}

	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}

	if len(newPass) < 10 {
		http.Redirect(w, r, target+sep+"error=Kata+sandi+baru+minimal+10+karakter", http.StatusSeeOther)
		return
	}
	if len(newPass) > config.MaxPasswordBytes {
		http.Redirect(w, r, target+sep+"error=Kata+sandi+baru+terlalu+panjang+(maksimal+128+karakter)", http.StatusSeeOther)
		return
	}
	if newPass != confirmPass {
		http.Redirect(w, r, target+sep+"error=Konfirmasi+kata+sandi+tidak+cocok", http.StatusSeeOther)
		return
	}

	var currentHash string
	err := s.DB.Pool.QueryRow(r.Context(), "SELECT password_hash FROM users WHERE id = $1", user.ID).Scan(&currentHash)
	if err != nil {
		http.Redirect(w, r, target+sep+"error=Gagal+mengambil+data+pengguna", http.StatusSeeOther)
		return
	}

	match, err := auth.VerifyPasswordGated(r.Context(), oldPass, currentHash)
	if err != nil || !match {
		http.Redirect(w, r, target+sep+"error=Kata+sandi+lama+salah", http.StatusSeeOther)
		return
	}

	newHash, err := auth.HashPassword(newPass)
	if err != nil {
		http.Redirect(w, r, target+sep+"error=Gagal+memproses+kata+sandi+baru", http.StatusSeeOther)
		return
	}

	_, _ = s.DB.Pool.Exec(r.Context(), "UPDATE users SET password_hash = $1, must_change_password = FALSE WHERE id = $2", newHash, user.ID)

	// H5a: Batalkan semua sesi lain pengguna itu kecuali sesi yang sedang dipakai saat ini
	currentSessionHash, _ := r.Context().Value(sessionKey).(string)
	if currentSessionHash != "" {
		_, _ = s.DB.Pool.Exec(r.Context(), "DELETE FROM web_sessions WHERE user_id = $1 AND token_hash != $2", user.ID, currentSessionHash)
	}

	s.logAudit(&user.ID, "change_password", "users", strconv.FormatInt(user.ID, 10), "password updated")
	http.Redirect(w, r, target+sep+"success=Kata+sandi+berhasil+diperbarui", http.StatusSeeOther)
}

func (s *Server) HandleWorkerTicketPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "worker" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	target := "/worker"
	referer := r.Header.Get("Referer")
	if referer != "" {
		if refURL, err := url.Parse(referer); err == nil {
			if (refURL.Host == "" || refURL.Host == r.Host) && strings.HasPrefix(refURL.Path, "/") && !strings.HasPrefix(refURL.Path, "//") {
				target = refURL.RequestURI()
			}
		}
	}
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}

	category := strings.TrimSpace(r.FormValue("category"))
	message := strings.TrimSpace(r.FormValue("message"))

	if category == "" {
		category = "Lainnya"
	}
	if len(message) < 5 {
		http.Redirect(w, r, target+sep+"error=Pesan+laporan+terlalu+pendek+(minimal+5+karakter)", http.StatusSeeOther)
		return
	}
	if len(message) > 2000 {
		http.Redirect(w, r, target+sep+"error=Pesan+laporan+maksimal+2000+karakter", http.StatusSeeOther)
		return
	}

	var ticketID int64
	err := s.DB.Pool.QueryRow(r.Context(), `
		INSERT INTO support_tickets (user_id, category, message, status)
		VALUES ($1, $2, $3, 'open')
		RETURNING id
	`, user.ID, category, message).Scan(&ticketID)
	if err != nil {
		http.Redirect(w, r, target+sep+"error=Gagal+mengirim+laporan: "+err.Error(), http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "submit_ticket", "support_tickets", strconv.FormatInt(ticketID, 10), category)
	http.Redirect(w, r, target+sep+"success=Laporan+berhasil+dikirim+ke+tim+CS", http.StatusSeeOther)
}

// --- Worker Support Tickets API ---

type WorkerTicketItem struct {
	ID           int64      `json:"id"`
	Category     string     `json:"category"`
	Message      string     `json:"message"`
	Status       string     `json:"status"`
	ReplyMessage string     `json:"reply_message"`
	CreatedAt    time.Time  `json:"created_at"`
	RepliedAt    *time.Time `json:"replied_at"`
}

func (s *Server) HandleWorkerTicketsAPI(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	rows, err := s.DB.Pool.Query(ctx, `
		SELECT id, category, message, status, COALESCE(reply_message, ''), created_at, replied_at
		FROM support_tickets
		WHERE user_id = $1
		ORDER BY id DESC
		LIMIT 20
	`, user.ID)

	var tickets []WorkerTicketItem
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t WorkerTicketItem
			if err := rows.Scan(&t.ID, &t.Category, &t.Message, &t.Status, &t.ReplyMessage, &t.CreatedAt, &t.RepliedAt); err == nil {
				tickets = append(tickets, t)
			}
		}
	}
	if tickets == nil {
		tickets = []WorkerTicketItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tickets)
}

// --- Worker Notifications Handlers ---

type WorkerNotificationItem struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	Message   string     `json:"message"`
	Kind      string     `json:"kind"`
	RefID     *int64     `json:"ref_id"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type WorkerNotificationsResponse struct {
	Notifications []WorkerNotificationItem `json:"notifications"`
	UnreadCount   int                      `json:"unread_count"`
}

func (s *Server) HandleWorkerNotificationsAPI(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()

	// 1. Auto-sync broadcast aktif ke notifikasi pekerja jika belum tercatat
	_, _ = s.DB.Pool.Exec(ctx, `
		INSERT INTO notifications (user_id, title, message, kind, ref_id)
		SELECT $1, b.title, b.message, 'broadcast', b.id
		FROM broadcasts b
		WHERE b.is_active = true
		  AND NOT EXISTS (
		      SELECT 1 FROM notifications n
		      WHERE n.user_id = $1 AND n.kind = 'broadcast' AND n.ref_id = b.id
		  )
	`, user.ID)

	var unreadCount int
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL", user.ID).Scan(&unreadCount)

	rows, err := s.DB.Pool.Query(ctx, `
		SELECT id, title, message, kind, ref_id, read_at, created_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY (read_at IS NULL) DESC, created_at DESC
		LIMIT 20
	`, user.ID)

	var list []WorkerNotificationItem
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var item WorkerNotificationItem
			if err := rows.Scan(&item.ID, &item.Title, &item.Message, &item.Kind, &item.RefID, &item.ReadAt, &item.CreatedAt); err == nil {
				list = append(list, item)
			}
		}
	}

	if list == nil {
		list = []WorkerNotificationItem{}
	}

	resp := WorkerNotificationsResponse{
		Notifications: list,
		UnreadCount:   unreadCount,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) HandleWorkerNotificationsReadAllPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	_, _ = s.DB.Pool.Exec(r.Context(), "UPDATE notifications SET read_at = CURRENT_TIMESTAMP WHERE user_id = $1 AND read_at IS NULL", user.ID)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (s *Server) HandleWorkerNotificationReadOnePOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err == nil {
		_, _ = s.DB.Pool.Exec(r.Context(), "UPDATE notifications SET read_at = CURRENT_TIMESTAMP WHERE id = $1 AND user_id = $2 AND read_at IS NULL", id, user.ID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

type WorkerAPIStatsResponse struct {
	DatabaseTersedia           int    `json:"database_tersedia"`
	DatabaseTersediaFormatted  string `json:"database_tersedia_formatted"`
	AkunBekerjaGlobal          int    `json:"akun_bekerja_global"`
	PendapatanHariIni          int64  `json:"pendapatan_hari_ini"`
	PendapatanHariIniFormatted string `json:"pendapatan_hari_ini_formatted"`
	// Aliases untuk integrasi fleksibel:
	AvailableNumbers           int    `json:"available_numbers"`
	RunningAccountsGlobal      int    `json:"running_accounts_global"`
	EarnedTodayIDR             int64  `json:"earned_today_idr"`
}

func (s *Server) HandleWorkerAPIStats(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	claimable, running, err := s.getGlobalStats(ctx)
	if err != nil {
		http.Error(w, `{"error":"gagal mengambil statistik global"}`, http.StatusInternalServerError)
		return
	}

	startOfTodayWIB := timeutil.GetJakartaStartOfDay()

	var earnedTodayIDR int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind IN ('upah', 'referral') AND currency = 'IDR' AND created_at >= $2
	`, user.ID, startOfTodayWIB).Scan(&earnedTodayIDR)

	resp := WorkerAPIStatsResponse{
		DatabaseTersedia:           claimable,
		DatabaseTersediaFormatted:  formatIDR(int64(claimable)),
		AkunBekerjaGlobal:          running,
		PendapatanHariIni:          earnedTodayIDR,
		PendapatanHariIniFormatted: "Rp " + formatIDR(earnedTodayIDR),
		AvailableNumbers:           claimable,
		RunningAccountsGlobal:      running,
		EarnedTodayIDR:             earnedTodayIDR,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
