package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"goblast/internal/auth"
	"goblast/internal/i18n"
	"goblast/internal/withdraw"
)

type WorkerActivityView struct {
	ID        int64
	Kind      string
	Amount    int64
	Currency  string
	CreatedAt string
	Note      string
}

type WorkerWithdrawalView struct {
	ID          int64
	Currency    string
	Amount      int64
	Method      string
	Status      string
	RequestedAt string
	ProcessedAt string
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

	// 1. Saldo saat ini
	var balance int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, $2)", user.ID, user.Currency).Scan(&balance)

	// 2. Total upah terkumpul
	var totalWage int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'upah' AND currency = $2
	`, user.ID, user.Currency).Scan(&totalWage)

	// 3. Total bonus referral
	var totalReferral int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'referral' AND currency = $2
	`, user.ID, user.Currency).Scan(&totalReferral)

	// 4. Total telah ditarik
	var totalWithdrawn int64
	_ = s.DB.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(ABS(amount)), 0)
		FROM ledger_entries
		WHERE user_id = $1 AND kind = 'penarikan' AND currency = $2
	`, user.ID, user.Currency).Scan(&totalWithdrawn)

	// 5. Statistik Akun WhatsApp
	var totalAccounts int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM wa_accounts WHERE worker_id = $1", user.ID).Scan(&totalAccounts)

	var connectedAccounts int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM wa_accounts WHERE worker_id = $1 AND status = 'terhubung'", user.ID).Scan(&connectedAccounts)

	// 6. Total pesan terkirim
	var totalSent int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'upah'", user.ID).Scan(&totalSent)

	// 7. Aktivitas saldo terkini (5 teratas)
	rows, err := s.DB.Pool.Query(ctx, `
		SELECT id, kind, amount, currency, created_at, COALESCE(note, '')
		FROM ledger_entries
		WHERE user_id = $1
		ORDER BY id DESC
		LIMIT 5
	`, user.ID)

	var activities []WorkerActivityView
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var act WorkerActivityView
			var t time.Time
			if err := rows.Scan(&act.ID, &act.Kind, &act.Amount, &act.Currency, &t, &act.Note); err == nil {
				act.CreatedAt = t.Format("02 Jan 2006 15:04")
				activities = append(activities, act)
			}
		}
	}

	// 8. Sisa nomor tersedia (angka agregat sistem)
	var availableNumbers int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE status = 'tersedia'").Scan(&availableNumbers)

	// 9. Daftar akun WhatsApp milik pekerja
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

	// 10. Ringkasan penghasilan per mata uang
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

	data := struct {
		Lang              string
		Title             string
		Theme             string
		CSRFToken         string
		ActiveNav         string
		User              UserSession
		Balance           int64
		TotalWage         int64
		TotalReferral     int64
		TotalWithdrawn    int64
		TotalAccounts     int64
		ConnectedAccounts int64
		TotalSent         int64
		AvailableNumbers  int64
		Accounts          []WorkerAccountSummaryView
		Incomes           []CurrencyIncomeView
		Activities        []WorkerActivityView
		T                 func(string) string
	}{
		Lang:              lang,
		Title:             i18n.T(lang, "worker.home_title"),
		Theme:             getTheme(r),
		CSRFToken:         getCSRF(r),
		ActiveNav:         "home",
		User:              user,
		Balance:           balance,
		TotalWage:         totalWage,
		TotalReferral:     totalReferral,
		TotalWithdrawn:    totalWithdrawn,
		TotalAccounts:     totalAccounts,
		ConnectedAccounts: connectedAccounts,
		TotalSent:         totalSent,
		AvailableNumbers:  availableNumbers,
		Accounts:          accounts,
		Incomes:           incomes,
		Activities:        activities,
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
		T            func(string) string
	}{
		Lang:         lang,
		Title:        i18n.T(lang, "nav.history"),
		Theme:        getTheme(r),
		CSRFToken:    getCSRF(r),
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
	var balance int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, $2)", user.ID, user.Currency).Scan(&balance)

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
				list = append(list, wRow)
			}
		}
	}

	errMsg := r.URL.Query().Get("error")
	successMsg := r.URL.Query().Get("success")
	if successMsg == "1" {
		successMsg = i18n.T(lang, "worker.withdraw_success")
	}

	data := struct {
		Lang        string
		Title       string
		Theme       string
		CSRFToken   string
		ActiveNav   string
		User        UserSession
		Balance     int64
		Withdrawals []WorkerWithdrawalView
		Error       string
		Success     string
		T           func(string) string
	}{
		Lang:        lang,
		Title:       i18n.T(lang, "nav.withdrawals"),
		Theme:       getTheme(r),
		CSRFToken:   getCSRF(r),
		ActiveNav:   "withdrawals",
		User:        user,
		Balance:     balance,
		Withdrawals: list,
		Error:       errMsg,
		Success:     successMsg,
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
	amountStr := strings.TrimSpace(r.FormValue("amount"))
	method := strings.ToLower(strings.TrimSpace(r.FormValue("method")))
	dest := strings.TrimSpace(r.FormValue("destination"))
	accountName := strings.TrimSpace(r.FormValue("account_name"))

	amount, err := strconv.ParseInt(amountStr, 10, 64)
	if err != nil || amount <= 0 {
		http.Redirect(w, r, "/worker/withdrawals?error=Nominal+penarikan+tidak+valid", http.StatusSeeOther)
		return
	}

	var payoutMethod string
	var encryptedDest string

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

		// Simpan ke profil user agar selanjutnya tidak perlu isi ulang
		_, _ = s.DB.Pool.Exec(ctx, `
			UPDATE users
			SET payout_method = $1, payout_dest_encrypted = $2, payout_account_name = $3
			WHERE id = $4
		`, method, enc, accountName, user.ID)
	} else {
		// Gunakan rekening yang sudah tersimpan di akun user
		var dbMethod, dbDestEnc, dbAccName *string
		err = s.DB.Pool.QueryRow(ctx, `
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

	// Cek saldo
	var currentBalance int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, $2)", user.ID, user.Currency).Scan(&currentBalance)
	if currentBalance < amount {
		http.Redirect(w, r, "/worker/withdrawals?error=Saldo+Anda+tidak+mencukupi+untuk+penarikan+ini", http.StatusSeeOther)
		return
	}

	// Transaksi atomik: Insert ke withdrawals dan kurangi saldo di ledger_entries
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Terjadi+kesalahan+sistem", http.StatusSeeOther)
		return
	}
	defer tx.Rollback(ctx)

	var withdrawalID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO withdrawals (user_id, currency, amount, method, destination_encrypted, status)
		VALUES ($1, $2, $3, $4, $5, 'menunggu')
		RETURNING id
	`, user.ID, user.Currency, amount, payoutMethod, encryptedDest).Scan(&withdrawalID)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Gagal+membuat+pengajuan+penarikan", http.StatusSeeOther)
		return
	}

	// Catat di buku besar (nominal negatif)
	note := fmt.Sprintf("Pengajuan penarikan #%d", withdrawalID)
	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'penarikan', $2, $3, $4)
	`, user.ID, user.Currency, -amount, note)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Saldo+tidak+mencukupi+atau+transaksi+batal", http.StatusSeeOther)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Gagal+menyimpan+transaksi+penarikan", http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "request_withdrawal", "withdrawals", strconv.FormatInt(withdrawalID, 10), fmt.Sprintf("%d %s (%s)", amount, user.Currency, payoutMethod))
	http.Redirect(w, r, "/worker/withdrawals?success=1", http.StatusSeeOther)
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

	_, err = s.DB.Pool.Exec(r.Context(), `
		UPDATE users
		SET payout_method = $1, payout_dest_encrypted = $2, payout_account_name = $3
		WHERE id = $4
	`, method, encryptedDest, accountName, user.ID)
	if err != nil {
		http.Redirect(w, r, target+sep+"error=Gagal+menyimpan+pengaturan+rekening", http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "update_payout_settings", "users", strconv.FormatInt(user.ID, 10), fmt.Sprintf("method: %s", method))
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
		WHERE user_id = $1 AND kind = 'referral' AND currency = $2
	`, user.ID, user.Currency).Scan(&totalBonus)

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
		T            func(string) string
	}{
		Lang:         lang,
		Title:        i18n.T(lang, "nav.referral"),
		Theme:        getTheme(r),
		CSRFToken:    getCSRF(r),
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

	match, err := auth.VerifyPassword(oldPass, currentHash)
	if err != nil || !match {
		http.Redirect(w, r, target+sep+"error=Kata+sandi+lama+salah", http.StatusSeeOther)
		return
	}

	newHash, err := auth.HashPassword(newPass)
	if err != nil {
		http.Redirect(w, r, target+sep+"error=Gagal+memproses+kata+sandi+baru", http.StatusSeeOther)
		return
	}

	_, _ = s.DB.Pool.Exec(r.Context(), "UPDATE users SET password_hash = $1 WHERE id = $2", newHash, user.ID)
	s.logAudit(&user.ID, "change_password", "users", strconv.FormatInt(user.ID, 10), "password updated")
	http.Redirect(w, r, target+sep+"success=Kata+sandi+berhasil+diperbarui", http.StatusSeeOther)
}
