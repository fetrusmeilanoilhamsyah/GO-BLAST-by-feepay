package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
		Activities:        activities,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["worker_home.html"].ExecuteTemplate(w, "base.html", data)
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
	successMsg := ""
	if r.URL.Query().Get("success") == "1" {
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
	method := strings.TrimSpace(r.FormValue("method"))
	dest := strings.TrimSpace(r.FormValue("destination"))
	accountName := strings.TrimSpace(r.FormValue("account_name"))

	amount, err := strconv.ParseInt(amountStr, 10, 64)
	if err != nil || amount <= 0 {
		http.Redirect(w, r, "/worker/withdrawals?error=Nominal+penarikan+tidak+valid", http.StatusSeeOther)
		return
	}

	// Validasi method
	switch method {
	case "dana", "seabank", "bank", "usdt_bep20":
	default:
		http.Redirect(w, r, "/worker/withdrawals?error=Metode+penarikan+tidak+didukung", http.StatusSeeOther)
		return
	}

	if dest == "" || accountName == "" {
		http.Redirect(w, r, "/worker/withdrawals?error=Nomor+rekening+dan+nama+pemilik+wajib+diisi", http.StatusSeeOther)
		return
	}

	// Cek saldo
	var currentBalance int64
	_ = s.DB.Pool.QueryRow(ctx, "SELECT user_balance($1, $2)", user.ID, user.Currency).Scan(&currentBalance)
	if currentBalance < amount {
		http.Redirect(w, r, "/worker/withdrawals?error=Saldo+Anda+tidak+mencukupi+untuk+penarikan+ini", http.StatusSeeOther)
		return
	}

	// Enkripsi tujuan penarikan
	fullDest := fmt.Sprintf("%s (%s)", dest, accountName)
	encryptedDest, err := withdraw.EncryptDestination(fullDest, s.EncryptionKey)
	if err != nil {
		http.Redirect(w, r, "/worker/withdrawals?error=Gagal+mengenkripsi+data+tujuan", http.StatusSeeOther)
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
	`, user.ID, user.Currency, amount, method, encryptedDest).Scan(&withdrawalID)
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

	s.logAudit(&user.ID, "request_withdrawal", "withdrawals", strconv.FormatInt(withdrawalID, 10), fmt.Sprintf("%d %s", amount, user.Currency))
	http.Redirect(w, r, "/worker/withdrawals?success=1", http.StatusSeeOther)
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
