package web

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"goblast/internal/auth"
	"goblast/internal/i18n"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.renderLogin(w, r, "")
		return
	}

	if r.Method == http.MethodPost {
		identifier := strings.TrimSpace(r.FormValue("identifier"))
		if identifier == "" {
			identifier = strings.TrimSpace(r.FormValue("email"))
		}
		password := r.FormValue("password")

		var id int64
		var role, hash, lang string
		err := s.DB.Pool.QueryRow(context.Background(), `
			SELECT id, role, password_hash, language 
			FROM users 
			WHERE (LOWER(email) = LOWER($1) OR LOWER(username) = LOWER($1)) AND status = 'active'
		`, identifier).Scan(&id, &role, &hash, &lang)

		if err != nil {
			s.logAudit(nil, "login_failed", "users", identifier, "invalid identifier or inactive")
			s.renderLogin(w, r, "Email/Username atau kata sandi salah")
			return
		}

		match, err := auth.VerifyPassword(password, hash)
		if err != nil || !match {
			s.logAudit(nil, "login_failed", "users", identifier, "invalid password")
			s.renderLogin(w, r, "Email/Username atau kata sandi salah")
			return
		}

		// Success login
		rawToken, hashToken, err := auth.GenerateSessionToken()
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		expiresAt := time.Now().Add(7 * 24 * time.Hour)
		if role == "admin" {
			expiresAt = time.Now().Add(24 * time.Hour)
		}

		_, err = s.DB.Pool.Exec(context.Background(), `
			INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
			VALUES ($1, $2, $3, $4, $5)
		`, id, hashToken, expiresAt, getIP(r), r.UserAgent())
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		s.logAudit(&id, "login_success", "users", identifier, "")

		http.SetCookie(w, &http.Cookie{
			Name:     "gb_session",
			Value:    rawToken,
			Path:     "/",
			Expires:  expiresAt,
			HttpOnly: true,
			Secure:   s.Env != "dev",
			SameSite: http.SameSiteLaxMode,
		})

		http.SetCookie(w, &http.Cookie{
			Name:     "lang",
			Value:    lang,
			Path:     "/",
			Expires:  time.Now().Add(30 * 24 * time.Hour),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		if role == "admin" {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/worker", http.StatusSeeOther)
		}
	}
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, errMsg string) {
	lang := validateLang(r.URL.Query().Get("lang"))
	if lang == "id" {
		cookie, err := r.Cookie("lang")
		if err == nil {
			lang = validateLang(cookie.Value)
		}
	}

	csrfToken := getCSRF(r)

	data := map[string]interface{}{
		"Lang":      lang,
		"Title":     i18n.T(lang, "auth.login_title"),
		"Error":     errMsg,
		"CSRFToken": csrfToken,
		"Theme":     getTheme(r),
		"T": func(key string) string {
			return i18n.T(lang, key)
		},
	}
	s.Tmpl["login.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.renderRegister(w, r, "")
		return
	}

	if r.Method == http.MethodPost {
		email := strings.TrimSpace(r.FormValue("email"))
		username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
		password := r.FormValue("password")
		lang := r.FormValue("language")
		lang = validateLang(lang)
		referralCode := strings.TrimSpace(r.FormValue("referral_code"))

		if username == "" {
			parts := strings.Split(email, "@")
			if len(parts) > 0 {
				username = strings.ToLower(parts[0])
			}
		}

		matched, _ := regexp.MatchString(`^[a-zA-Z0-9_]{3,30}$`, username)
		if !matched {
			s.renderRegister(w, r, "Username harus 3-30 karakter (hanya huruf, angka, dan underscore)")
			return
		}

		if len(password) < 10 {
			s.renderRegister(w, r, "Kata sandi minimal 10 karakter")
			return
		}

		hash, err := auth.HashPassword(password)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		var referredBy *int64
		if referralCode != "" {
			var refID int64
			err := s.DB.Pool.QueryRow(context.Background(), "SELECT id FROM users WHERE referral_code = $1", referralCode).Scan(&refID)
			if err == nil {
				referredBy = &refID
			}
		}

		// generate new referral code for this user
		newRefCode, _, _ := auth.GenerateSessionToken()
		newRefCode = newRefCode[:8] // simple short code

		displayName := username

		_, err = s.DB.Pool.Exec(context.Background(), `
			INSERT INTO users (email, username, password_hash, role, display_name, language, currency, referral_code, referred_by)
			VALUES ($1, $2, $3, 'worker', $4, $5, 'IDR', $6, $7)
		`, email, username, hash, displayName, lang, newRefCode, referredBy)

		if err != nil {
			s.renderRegister(w, r, "Email atau Username sudah terdaftar")
			return
		}

		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

func (s *Server) renderRegister(w http.ResponseWriter, r *http.Request, errMsg string) {
	lang := validateLang(r.URL.Query().Get("lang"))
	if lang == "id" {
		cookie, err := r.Cookie("lang")
		if err == nil {
			lang = validateLang(cookie.Value)
		}
	}

	csrfToken := getCSRF(r)

	data := map[string]interface{}{
		"Lang":      lang,
		"Title":     i18n.T(lang, "auth.register_title"),
		"Error":     errMsg,
		"CSRFToken": csrfToken,
		"Theme":     getTheme(r),
		"T": func(key string) string {
			return i18n.T(lang, key)
		},
	}
	s.Tmpl["register.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sessionHash, ok := r.Context().Value(sessionKey).(string)
	if ok && sessionHash != "" {
		_, _ = s.DB.Pool.Exec(context.Background(), "DELETE FROM web_sessions WHERE token_hash = $1", sessionHash)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "gb_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleAdminHome(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	w.Write([]byte("Panel Admin - Tahap 12 akan mengisi halaman ini. Halo " + user.Email))
}

func (s *Server) handleWorkerHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/worker/accounts", http.StatusSeeOther)
}

func (s *Server) logAudit(actorID *int64, action, entity, entityID, meta string) {
	metaJSON := "{}"
	if meta != "" {
		metaJSON = `{"note": "` + meta + `"}`
	}
	_, _ = s.DB.Pool.Exec(context.Background(), `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta)
		VALUES ($1, $2, $3, $4, $5)
	`, actorID, action, entity, entityID, metaJSON)
}
