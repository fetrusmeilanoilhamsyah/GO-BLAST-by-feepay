package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"goblast/internal/campaign"
	"goblast/internal/i18n"
)

type WAAccountView struct {
	ID         int64
	Label      string
	Speed      string
	Status     string
	IsWorking  bool
	StopReason string
	CreatedAt  string
}

func (s *Server) HandleWorkerAccounts(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	rows, err := s.DB.Pool.Query(r.Context(),
		`SELECT id, label, speed, status, COALESCE(stop_reason, ''), created_at FROM wa_accounts WHERE worker_id = $1 ORDER BY id DESC`,
		user.ID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var accounts []WAAccountView
	for rows.Next() {
		var a WAAccountView
		var t interface{}
		if err := rows.Scan(&a.ID, &a.Label, &a.Speed, &a.Status, &a.StopReason, &t); err == nil {
			if s.WorkCtrl != nil {
				a.IsWorking = s.WorkCtrl.IsRunning(a.ID)
			}
			accounts = append(accounts, a)
		}
	}

	lang := user.Language
	if lang == "" {
		lang = "id"
	}

	errMsg := r.URL.Query().Get("error")

	var availableNumbers int64
	if count, err := campaign.ClaimableCount(r.Context(), s.DB.Pool); err == nil {
		availableNumbers = int64(count)
	}

	var runningAccountsGlobal int
	if s.WorkCtrl != nil {
		runningAccountsGlobal = s.WorkCtrl.RunningCount()
	}

	var connectedAccounts int64
	_ = s.DB.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM wa_accounts WHERE worker_id = $1 AND status = 'terhubung'", user.ID).Scan(&connectedAccounts)

	data := struct {
		CSLinks
		Lang                  string
		Title                 string
		Theme                 string
		CSRFToken             string
		ActiveNav             string
		User                  UserSession
		Accounts              []WAAccountView
		AvailableNumbers      int64
		RunningAccountsGlobal int
		ConnectedAccounts     int64
		Error                 string
		T                     func(string) string
	}{
		CSLinks:               s.csLinks(r.Context()),
		Lang:                  lang,
		Title:                 i18n.T(lang, "wa.accounts_title"),
		Theme:                 getTheme(r),
		CSRFToken:             getCSRF(r),
		ActiveNav:             "accounts",
		User:                  user,
		Accounts:              accounts,
		AvailableNumbers:      availableNumbers,
		RunningAccountsGlobal: runningAccountsGlobal,
		ConnectedAccounts: connectedAccounts,
		Error:             errMsg,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["worker_accounts.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleWorkerAccountInitPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if s.IsLinkingLocked(r.Context()) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Penautan akun WhatsApp sedang dikunci oleh admin",
		})
		return
	}

	var activeAccountID int64
	var label, speed string
	idStr := r.URL.Query().Get("id")
	if idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			var ownerID int64
			_ = s.DB.Pool.QueryRow(r.Context(),
				`SELECT worker_id, label, speed FROM wa_accounts WHERE id = $1`, id,
			).Scan(&ownerID, &label, &speed)
			if ownerID == user.ID {
				activeAccountID = id
			}
		}
	}

	if activeAccountID == 0 {
		_ = s.DB.Pool.QueryRow(r.Context(),
			`SELECT id, label, speed FROM wa_accounts 
			 WHERE worker_id = $1 AND status = 'menunggu_scan' 
			 ORDER BY id DESC LIMIT 1`, user.ID,
		).Scan(&activeAccountID, &label, &speed)
	}

	if activeAccountID == 0 {
		var countExisting int
		_ = s.DB.Pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM wa_accounts WHERE worker_id = $1`, user.ID,
		).Scan(&countExisting)
		label = fmt.Sprintf("WhatsApp %d", countExisting+1)
		speed = "medium"

		err := s.DB.Pool.QueryRow(r.Context(),
			`INSERT INTO wa_accounts (worker_id, label, speed, status)
			 VALUES ($1, $2, $3, 'menunggu_scan')
			 RETURNING id`,
			user.ID, label, speed,
		).Scan(&activeAccountID)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":    false,
				"error": "Gagal membuat akun: " + err.Error(),
			})
			return
		}
	}

	var qrImageB64 string
	if s.WaManager != nil && activeAccountID > 0 {
		sess := s.WaManager.GetSession(activeAccountID)
		if sess == nil {
			_, _, _ = s.WaManager.Connect(r.Context(), activeAccountID, user.ID)
		}
		if b64, ok := s.WaManager.GetQRImageB64(activeAccountID); ok {
			qrImageB64 = b64
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":           true,
		"account_id":   activeAccountID,
		"label":        label,
		"speed":        speed,
		"qr_image_b64": qrImageB64,
	})
}

func (s *Server) HandleWorkerAccountNewGET(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = user

	idStr := r.URL.Query().Get("id")
	if idStr != "" {
		http.Redirect(w, r, fmt.Sprintf("/worker/accounts?open=%s", urlQueryEscape(idStr)), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/worker/accounts?open=add", http.StatusSeeOther)
}

func (s *Server) HandleWorkerAccountNewPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.IsLinkingLocked(r.Context()) {
		http.Redirect(w, r, "/worker/accounts?err=Penautan+akun+WhatsApp+sedang+dikunci+oleh+admin", http.StatusSeeOther)
		return
	}

	idStr := strings.TrimSpace(r.FormValue("id"))
	label := strings.TrimSpace(r.FormValue("label"))
	speed := strings.TrimSpace(r.FormValue("speed"))

	if label == "" {
		label = "WhatsApp"
	}
	switch speed {
	case "fast", "medium", "slow", "over_slow":
	default:
		speed = "medium"
	}

	if idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			_, _ = s.DB.Pool.Exec(r.Context(),
				`UPDATE wa_accounts SET label = $1, speed = $2 WHERE id = $3 AND worker_id = $4`,
				label, speed, id, user.ID)
			http.Redirect(w, r, "/worker/accounts", http.StatusSeeOther)
			return
		}
	}

	http.Redirect(w, r, "/worker/accounts", http.StatusSeeOther)
}

func (s *Server) HandleWorkerAccountSettingsPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	label := strings.TrimSpace(r.FormValue("label"))
	speed := strings.TrimSpace(r.FormValue("speed"))

	if label == "" {
		label = "WhatsApp"
	}
	switch speed {
	case "fast", "medium", "slow", "over_slow":
	default:
		speed = "medium"
	}

	_, err = s.DB.Pool.Exec(r.Context(),
		`UPDATE wa_accounts SET label = $1, speed = $2 WHERE id = $3 AND worker_id = $4`,
		label, speed, id, user.ID)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "label": label, "speed": speed})
}

func (s *Server) HandleWorkerAccountPairPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.IsLinkingLocked(r.Context()) {
		if strings.Contains(r.Header.Get("Accept"), "application/json") || r.FormValue("format") == "json" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Penautan akun WhatsApp sedang dikunci oleh admin",
			})
			return
		}
		http.Redirect(w, r, "/worker/accounts?err=Penautan+akun+WhatsApp+sedang+dikunci+oleh+admin", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	phone := strings.TrimSpace(r.FormValue("phone"))
	phone = strings.ReplaceAll(phone, "+", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.ReplaceAll(phone, " ", "")
	if strings.HasPrefix(phone, "0") {
		phone = "62" + phone[1:]
	}

	isJSON := strings.Contains(r.Header.Get("Accept"), "application/json") || r.FormValue("format") == "json"

	if phone == "" {
		if isJSON {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "Nomor HP wajib diisi"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/worker/accounts?open=%d&error=%s", id, urlQueryEscape("Nomor HP wajib diisi")), http.StatusSeeOther)
		return
	}

	if s.WaManager == nil {
		if isJSON {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "WhatsApp manager tidak tersedia"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/worker/accounts?open=%d&error=%s", id, urlQueryEscape("WhatsApp manager tidak tersedia")), http.StatusSeeOther)
		return
	}

	// Pastikan sesi aktif berjalan
	sess := s.WaManager.GetSession(id)
	if sess == nil {
		_, _, err = s.WaManager.Connect(r.Context(), id, user.ID)
		if err != nil {
			if isJSON {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/worker/accounts?open=%d&error=%s", id, urlQueryEscape(err.Error())), http.StatusSeeOther)
			return
		}
	}

	code, err := s.WaManager.PairPhone(id, phone)
	if err != nil {
		if isJSON {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/worker/accounts?open=%d&error=%s", id, urlQueryEscape(err.Error())), http.StatusSeeOther)
		return
	}

	if isJSON {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "pairing_code": code})
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/worker/accounts?open=%d&pairing_code=%s", id, urlQueryEscape(code)), http.StatusSeeOther)
}

func (s *Server) HandleWorkerAccountDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	isJSON := strings.Contains(r.Header.Get("Accept"), "application/json") || r.FormValue("format") == "json"

	var delErr error
	if s.WaManager != nil {
		delErr = s.WaManager.Delete(r.Context(), id, user.ID)
	} else {
		_, _ = s.DB.Pool.Exec(r.Context(),
			`UPDATE campaign_numbers SET status = 'gagal', sent_at = now(), last_error = 'hasil_tidak_pasti'
			 WHERE status = 'dikirim' AND claimed_account_id = $1`, id)
		_, delErr = s.DB.Pool.Exec(r.Context(), `DELETE FROM wa_accounts WHERE id = $1 AND worker_id = $2`, id, user.ID)
	}

	if isJSON {
		w.Header().Set("Content-Type", "application/json")
		if delErr != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": delErr.Error()})
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		}
		return
	}

	if delErr != nil {
		http.Redirect(w, r, fmt.Sprintf("/worker/accounts?error=%s", urlQueryEscape(delErr.Error())), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/worker/accounts", http.StatusSeeOther)
}

func (s *Server) HandleWorkerAccountStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	// Ownership check
	var ownerID int64
	var status, stopReason string
	err = s.DB.Pool.QueryRow(r.Context(),
		`SELECT worker_id, status, COALESCE(stop_reason, '') FROM wa_accounts WHERE id = $1`, id,
	).Scan(&ownerID, &status, &stopReason)
	if err != nil || ownerID != user.ID {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	var qrImageB64 string
	if s.WaManager != nil {
		if liveStatus, err := s.WaManager.GetStatusForAccount(r.Context(), id); err == nil && liveStatus != "" {
			status = liveStatus
		}
		if status == "menunggu_scan" {
			if b64, ok := s.WaManager.GetQRImageB64(id); ok {
				qrImageB64 = b64
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"account_id":   id,
		"status":       status,
		"stop_reason":  stopReason,
		"qr_image_b64": qrImageB64,
	})
}

func (s *Server) HandleWorkerAccountDisconnect(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	if s.WaManager != nil {
		if err := s.WaManager.Disconnect(r.Context(), id, user.ID); err != nil {
			http.Redirect(w, r, fmt.Sprintf("/worker/accounts?error=%s", urlQueryEscape(err.Error())), http.StatusSeeOther)
			return
		}
	}

	http.Redirect(w, r, "/worker/accounts", http.StatusSeeOther)
}

func (s *Server) HandleWorkerAccountStop(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	if s.WaManager != nil {
		if err := s.WaManager.Stop(r.Context(), id, user.ID, "user_stop"); err != nil {
			http.Redirect(w, r, fmt.Sprintf("/worker/accounts?error=%s", urlQueryEscape(err.Error())), http.StatusSeeOther)
			return
		}
	}

	http.Redirect(w, r, "/worker/accounts", http.StatusSeeOther)
}

func (s *Server) HandleWorkerAccountWorkStart(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	// Cek kepemilikan dan status akun
	var ownerID int64
	var status string
	err = s.DB.Pool.QueryRow(r.Context(),
		`SELECT worker_id, status FROM wa_accounts WHERE id = $1`, id,
	).Scan(&ownerID, &status)
	if err != nil || ownerID != user.ID {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Start ditolak kalau status akun bukan terhubung
	if status != "terhubung" {
		http.Error(w, "Akun harus berstatus terhubung untuk memulai kerja", http.StatusBadRequest)
		return
	}

	if s.WorkCtrl != nil {
		s.WorkCtrl.StartAccount(id, user.ID)
	}

	target := "/worker/accounts"
	if referer := r.Header.Get("Referer"); referer != "" {
		if refURL, err := url.Parse(referer); err == nil && (refURL.Path == "/worker" || refURL.Path == "/worker/accounts") {
			target = refURL.RequestURI()
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) HandleWorkerAccountWorkStop(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	// Cek kepemilikan akun
	var ownerID int64
	err = s.DB.Pool.QueryRow(r.Context(),
		`SELECT worker_id FROM wa_accounts WHERE id = $1`, id,
	).Scan(&ownerID)
	if err != nil || ownerID != user.ID {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if s.WorkCtrl != nil {
		s.WorkCtrl.StopAccount(id)
	}

	target := "/worker/accounts"
	if referer := r.Header.Get("Referer"); referer != "" {
		if refURL, err := url.Parse(referer); err == nil && (refURL.Path == "/worker" || refURL.Path == "/worker/accounts") {
			target = refURL.RequestURI()
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func timeNowUnix() int64 {
	return 1700000000
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, " ", "+"), ":", "%3A")
}
