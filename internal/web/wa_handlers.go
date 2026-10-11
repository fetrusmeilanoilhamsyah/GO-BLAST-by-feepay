package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"goblast/internal/i18n"

	qrcode "github.com/skip2/go-qrcode"
)

type WAAccountView struct {
	ID        int64
	Label     string
	Speed     string
	Status    string
	CreatedAt string
}

func (s *Server) HandleWorkerAccounts(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	rows, err := s.DB.Pool.Query(r.Context(),
		`SELECT id, label, speed, status, created_at FROM wa_accounts WHERE worker_id = $1 ORDER BY id DESC`,
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
		if err := rows.Scan(&a.ID, &a.Label, &a.Speed, &a.Status, &t); err == nil {
			accounts = append(accounts, a)
		}
	}

	lang := user.Language
	if lang == "" {
		lang = "id"
	}

	errMsg := r.URL.Query().Get("error")

	data := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		Accounts  []WAAccountView
		Error     string
		T         func(string) string
	}{
		Lang:      lang,
		Title:     i18n.T(lang, "wa.accounts_title"),
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		Accounts:  accounts,
		Error:     errMsg,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["worker_accounts.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleWorkerAccountNewGET(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	lang := user.Language
	if lang == "" {
		lang = "id"
	}

	// Check if viewing an existing connecting account
	idStr := r.URL.Query().Get("id")
	var activeAccountID int64
	var label string
	var qrImageB64 string
	pairingMode := "qr"
	pairingCode := ""

	if idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err == nil {
			// Verify ownership
			var ownerID int64
			var status string
			err = s.DB.Pool.QueryRow(r.Context(),
				`SELECT worker_id, label, status FROM wa_accounts WHERE id = $1`, id,
			).Scan(&ownerID, &label, &status)

			if err == nil && ownerID == user.ID {
				activeAccountID = id
				// Try get QR code from manager
				if s.WaManager != nil {
					if code, ok := s.WaManager.GetQRCode(id); ok {
						if pngBytes, err := qrcode.Encode(code, qrcode.Medium, 256); err == nil {
							qrImageB64 = base64.StdEncoding.EncodeToString(pngBytes)
						}
					}
				}
			}
		}
	}

	data := struct {
		Lang            string
		Title           string
		Theme           string
		CSRFToken       string
		ActiveAccountID int64
		AccountLabel    string
		PairingMode     string
		PairingCode     string
		QRImageB64      string
		ResumeID        string
		FormLabel       string
		Error           string
		T               func(string) string
	}{
		Lang:            lang,
		Title:           i18n.T(lang, "wa.new_account_title"),
		Theme:           getTheme(r),
		CSRFToken:       getCSRF(r),
		ActiveAccountID: activeAccountID,
		AccountLabel:    label,
		PairingMode:     pairingMode,
		PairingCode:     pairingCode,
		QRImageB64:      qrImageB64,
		ResumeID:        r.URL.Query().Get("resume_id"),
		FormLabel:       "",
		Error:           r.URL.Query().Get("error"),
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["worker_account_new.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleWorkerAccountNewPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	label := strings.TrimSpace(r.FormValue("label"))
	speed := strings.TrimSpace(r.FormValue("speed"))
	mode := strings.TrimSpace(r.FormValue("mode"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	resumeIDStr := strings.TrimSpace(r.FormValue("resume_id"))

	if label == "" {
		label = "WhatsApp"
	}
	switch speed {
	case "fast", "medium", "slow", "over_slow":
	default:
		speed = "medium"
	}

	var accountID int64
	if resumeIDStr != "" {
		resID, err := strconv.ParseInt(resumeIDStr, 10, 64)
		if err == nil {
			var ownerID int64
			err = s.DB.Pool.QueryRow(r.Context(),
				`SELECT worker_id FROM wa_accounts WHERE id = $1`, resID,
			).Scan(&ownerID)
			if err == nil && ownerID == user.ID {
				accountID = resID
				_, _ = s.DB.Pool.Exec(r.Context(),
					`UPDATE wa_accounts SET label = $1, speed = $2, status = 'menunggu_scan' WHERE id = $3`,
					label, speed, accountID)
			}
		}
	}

	if accountID == 0 {
		dummyHash := hex.EncodeToString(sha256.New().Sum([]byte(fmt.Sprintf("%d-%s-%d", user.ID, label, timeNowUnix()))))
		err := s.DB.Pool.QueryRow(r.Context(),
			`INSERT INTO wa_accounts (worker_id, label, jid_hash, speed, status)
			 VALUES ($1, $2, $3, $4, 'menunggu_scan')
			 RETURNING id`,
			user.ID, label, dummyHash, speed,
		).Scan(&accountID)
		if err != nil {
			http.Redirect(w, r, "/worker/accounts?error=Gagal+membuat+akun+di+database", http.StatusSeeOther)
			return
		}
	}

	if s.WaManager == nil {
		http.Redirect(w, r, "/worker/accounts?error=WhatsApp+manager+tidak+tersedia", http.StatusSeeOther)
		return
	}

	qrChan, pairFn, err := s.WaManager.Connect(r.Context(), accountID, user.ID)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/worker/accounts?error=%s", urlQueryEscape(err.Error())), http.StatusSeeOther)
		return
	}

	lang := user.Language
	if lang == "" {
		lang = "id"
	}

	var qrImageB64 string
	var pairingCode string

	if mode == "pairing_code" && phone != "" {
		code, err := pairFn(phone)
		if err != nil {
			http.Redirect(w, r, fmt.Sprintf("/worker/accounts?error=%s", urlQueryEscape(err.Error())), http.StatusSeeOther)
			return
		}
		pairingCode = code
	} else {
		// Wait brief moment for first QR code
		select {
		case code := <-qrChan:
			if pngBytes, err := qrcode.Encode(code, qrcode.Medium, 256); err == nil {
				qrImageB64 = base64.StdEncoding.EncodeToString(pngBytes)
			}
		default:
		}
	}

	data := struct {
		Lang            string
		Title           string
		Theme           string
		CSRFToken       string
		ActiveAccountID int64
		AccountLabel    string
		PairingMode     string
		PairingCode     string
		QRImageB64      string
		ResumeID        string
		FormLabel       string
		Error           string
		T               func(string) string
	}{
		Lang:            lang,
		Title:           i18n.T(lang, "wa.new_account_title"),
		Theme:           getTheme(r),
		CSRFToken:       getCSRF(r),
		ActiveAccountID: accountID,
		AccountLabel:    label,
		PairingMode:     mode,
		PairingCode:     pairingCode,
		QRImageB64:      qrImageB64,
		ResumeID:        "",
		FormLabel:       label,
		Error:           "",
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["worker_account_new.html"].ExecuteTemplate(w, "base.html", data)
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
			if code, ok := s.WaManager.GetQRCode(id); ok {
				if pngBytes, err := qrcode.Encode(code, qrcode.Medium, 256); err == nil {
					qrImageB64 = base64.StdEncoding.EncodeToString(pngBytes)
				}
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

func timeNowUnix() int64 {
	return 1700000000
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, " ", "+"), ":", "%3A")
}
