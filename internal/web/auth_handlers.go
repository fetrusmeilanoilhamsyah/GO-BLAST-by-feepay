package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"goblast/internal/auth"
	"goblast/internal/config"
	"goblast/internal/i18n"
	"goblast/internal/turnstile"
)

var validIdentifierRegex = regexp.MustCompile(`^[A-Za-z0-9_.@+-]{1,64}$`)

func sanitizeEntityID(entity, entityID string) string {
	if entity == "users" && !validIdentifierRegex.MatchString(entityID) {
		return "[tidak valid]"
	}
	return entityID
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.renderLogin(w, r, "")
		return
	}

	if r.Method == http.MethodPost {
		clientIP := s.getClientIP(r)

		// 1. Honeypot check: field website_hp tak terlihat
		if r.FormValue("website_hp") != "" {
			time.Sleep(1 * time.Second)
			s.renderLogin(w, r, "Username/Email atau kata sandi salah")
			return
		}

		// 2. Form timing token check (min 2s, max 2h)
		formToken := r.FormValue("form_token")
		if len(s.SessionSecret) > 0 && formToken != "" {
			if !auth.VerifyFormTimingToken(formToken, s.SessionSecret, time.Now(), 2*time.Second, 2*time.Hour) {
				s.renderLogin(w, r, "Sesi form telah kedaluwarsa atau dikirim terlalu cepat, silakan muat ulang halaman")
				return
			}
		}

		identifier := strings.TrimSpace(r.FormValue("identifier"))
		if identifier == "" {
			identifier = strings.TrimSpace(r.FormValue("email"))
		}
		password := r.FormValue("password")

		identNormalized := strings.ToLower(identifier)
		pairKey := clientIP + ":" + identNormalized

		// 3. Multi-layer Rate Limiting
		// Global
		if s.LoginGlobalLimiter != nil {
			if allowed, wait := s.LoginGlobalLimiter.Allow("global"); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
				s.logAuditWithRateLimit(r, nil, "login_rate_limited", "users", identifier, "global limit exceeded")
				http.Error(w, "Server sedang menerima terlalu banyak percobaan login. Silakan tunggu beberapa saat.", http.StatusTooManyRequests)
				return
			}
		}

		// IP
		if s.LoginIPLimiter != nil {
			if allowed, wait := s.LoginIPLimiter.Allow(clientIP); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
				s.logAuditWithRateLimit(r, nil, "login_rate_limited", "users", identifier, "ip limit exceeded")
				http.Error(w, fmt.Sprintf("Terlalu banyak percobaan login dari IP Anda. Coba lagi dalam %d detik.", int(wait.Seconds())), http.StatusTooManyRequests)
				return
			}
		}

		// Identifier
		if s.LoginIdentLimiter != nil {
			if allowed, wait := s.LoginIdentLimiter.Allow(identNormalized); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
				s.logAuditWithRateLimit(r, nil, "login_rate_limited", "users", identifier, "identifier limit exceeded")
				http.Error(w, fmt.Sprintf("Terlalu banyak percobaan login untuk akun ini. Coba lagi dalam %d detik.", int(wait.Seconds())), http.StatusTooManyRequests)
				return
			}
		}

		// Pair (IP, identifier)
		if s.LoginPairLimiter != nil {
			if allowed, wait := s.LoginPairLimiter.Allow(pairKey); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
				s.logAuditWithRateLimit(r, nil, "login_rate_limited", "users", identifier, "pair limit exceeded")
				http.Error(w, fmt.Sprintf("Terlalu banyak percobaan salah untuk kombinasi ini. Coba lagi dalam %d detik.", int(wait.Seconds())), http.StatusTooManyRequests)
				return
			}
		}

		// 4. Cloudflare Turnstile verification (jika dikonfigurasi)
		if s.Turnstile != nil && s.Turnstile.IsEnabled() {
			cfToken := r.FormValue("cf-turnstile-response")
			success, err := s.Turnstile.Verify(r.Context(), cfToken, "login", clientIP)
			if err != nil || !success {
				if errors.Is(err, turnstile.ErrServiceOutage) {
					s.logAudit(nil, "turnstile_outage", "system", clientIP, "login allowed during outage")
				} else {
					s.renderLogin(w, r, "Verifikasi keamanan bot gagal. Silakan coba lagi.")
					return
				}
			}
		}

		recordLoginFailure := func() {
			if s.LoginGlobalLimiter != nil {
				_, _, _ = s.LoginGlobalLimiter.RecordFailure("global")
			}
			if s.LoginIPLimiter != nil {
				_, _, _ = s.LoginIPLimiter.RecordFailure(clientIP)
			}
			if s.LoginIdentLimiter != nil {
				_, _, _ = s.LoginIdentLimiter.RecordFailure(identNormalized)
			}
			if s.LoginPairLimiter != nil {
				_, _, _ = s.LoginPairLimiter.RecordFailure(pairKey)
			}
		}

		// 5. Query pengguna: pisahkan kueri jika berformat email (@) agar memakai indeks masing-masing
		var id int64
		var role, hash, lang, userStatus string
		var queryErr error

		if strings.Contains(identifier, "@") {
			queryErr = s.DB.Pool.QueryRow(r.Context(), `
				SELECT id, role, password_hash, language, status 
				FROM users 
				WHERE email IS NOT NULL AND LOWER(email) = LOWER($1)
			`, identifier).Scan(&id, &role, &hash, &lang, &userStatus)
		} else {
			queryErr = s.DB.Pool.QueryRow(r.Context(), `
				SELECT id, role, password_hash, language, status 
				FROM users 
				WHERE LOWER(username) = LOWER($1)
			`, identifier).Scan(&id, &role, &hash, &lang, &userStatus)
		}

		// Jika user tidak ditemukan
		if queryErr != nil {
			// Mitigasi Timing Attack: hitung komputasi argon2 dengan ShadowHash
			_, _ = auth.VerifyPasswordGated(r.Context(), password, auth.ShadowHash)
			recordLoginFailure()
			s.logAuditWithRateLimit(r, nil, "login_failed", "users", identifier, "invalid identifier")
			s.renderLogin(w, r, "Username/Email atau kata sandi salah")
			return
		}

		// Jika user ditemukan: verifikasi kata sandi DAHULU dengan ArgonGate
		match, verifyErr := auth.VerifyPasswordGated(r.Context(), password, hash)
		if errors.Is(verifyErr, auth.ErrServerBusy) {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "Server sedang sibuk memproses verifikasi, silakan coba 2 detik lagi.", http.StatusServiceUnavailable)
			return
		}

		if verifyErr != nil || !match {
			recordLoginFailure()
			s.logAuditWithRateLimit(r, nil, "login_failed", "users", identifier, "invalid password")
			s.renderLogin(w, r, "Username/Email atau kata sandi salah")
			return
		}

		// K3: Status akun HANYA diperiksa SETELAH password dipastikan cocok!
		if userStatus == "frozen" {
			s.logAudit(&id, "login_rejected", "users", identifier, "account frozen")
			s.renderLogin(w, r, "Akun Anda sedang dibekukan oleh administrator")
			return
		}
		if userStatus != "active" {
			s.logAudit(&id, "login_rejected", "users", identifier, "account inactive")
			s.renderLogin(w, r, "Akun Anda tidak aktif")
			return
		}

		// Login Berhasil!
		// Reset kuota limiter pasangan untuk pengguna ini agar tidak terkunci akibat typo sebelumnya
		if s.LoginPairLimiter != nil {
			s.LoginPairLimiter.Reset(pairKey)
		}

		// B3 & K8: Hapus sesi kedaluwarsa & batasi sesi aktif maksimal 5 per pengguna
		_, _ = s.DB.Pool.Exec(r.Context(), "DELETE FROM web_sessions WHERE expires_at < CURRENT_TIMESTAMP")
		_, _ = s.DB.Pool.Exec(r.Context(), `
			DELETE FROM web_sessions 
			WHERE user_id = $1 
			  AND id NOT IN (
				  SELECT id FROM web_sessions WHERE user_id = $1 ORDER BY created_at DESC LIMIT 4
			  )
		`, id)

		rawToken, hashToken, err := auth.GenerateSessionToken()
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		expiresAt := time.Now().Add(7 * 24 * time.Hour)
		if role == "admin" {
			expiresAt = time.Now().Add(24 * time.Hour)
		}

		_, err = s.DB.Pool.Exec(r.Context(), `
			INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
			VALUES ($1, $2, $3, $4, $5)
		`, id, hashToken, expiresAt, clientIP, r.UserAgent())
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		s.logAudit(&id, "login_success", "users", identifier, "")

		http.SetCookie(w, &http.Cookie{
			Name:     s.sessionCookieName(),
			Value:    rawToken,
			Path:     "/",
			Expires:  expiresAt,
			HttpOnly: true,
			Secure:   s.isProd(),
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
	formToken := ""
	if len(s.SessionSecret) > 0 {
		formToken = auth.SignFormTimingToken(time.Now(), s.SessionSecret)
	}

	var turnstileSiteKey string
	if s.Turnstile != nil && s.Turnstile.IsEnabled() {
		turnstileSiteKey = s.Turnstile.SiteKey
	}

	data := map[string]interface{}{
		"Lang":             lang,
		"Title":            i18n.T(lang, "auth.login_title"),
		"Error":            errMsg,
		"CSRFToken":        csrfToken,
		"FormToken":        formToken,
		"TurnstileSiteKey": turnstileSiteKey,
		"Nonce":            getNonce(r),
		"Theme":            getTheme(r),
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
		clientIP := s.getClientIP(r)

		// 1. Honeypot check
		if r.FormValue("website_hp") != "" {
			time.Sleep(1 * time.Second)
			s.renderRegister(w, r, "Gagal memproses pendaftaran")
			return
		}

		// 2. Form timing token check (min 2s, max 2h)
		formToken := r.FormValue("form_token")
		if len(s.SessionSecret) > 0 && formToken != "" {
			if !auth.VerifyFormTimingToken(formToken, s.SessionSecret, time.Now(), 2*time.Second, 2*time.Hour) {
				s.renderRegister(w, r, "Sesi form telah kedaluwarsa atau dikirim terlalu cepat, silakan muat ulang halaman")
				return
			}
		}

		// 3. Register rate limiting (10/h dan 100/day)
		if s.RegisterHourlyLimiter != nil {
			if allowed, wait := s.RegisterHourlyLimiter.Allow(clientIP); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
				http.Error(w, fmt.Sprintf("Batas pendaftaran per jam tercapai. Coba lagi dalam %d detik.", int(wait.Seconds())), http.StatusTooManyRequests)
				return
			}
		}
		if s.RegisterDailyLimiter != nil {
			if allowed, wait := s.RegisterDailyLimiter.Allow(clientIP); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
				http.Error(w, fmt.Sprintf("Batas pendaftaran harian tercapai. Coba lagi dalam %d detik.", int(wait.Seconds())), http.StatusTooManyRequests)
				return
			}
		}

		// 4. Cloudflare Turnstile verification (selalu fail-closed untuk register)
		if s.Turnstile != nil && s.Turnstile.IsEnabled() {
			cfToken := r.FormValue("cf-turnstile-response")
			success, err := s.Turnstile.Verify(r.Context(), cfToken, "register", clientIP)
			if err != nil || !success {
				s.renderRegister(w, r, "Verifikasi keamanan bot gagal. Silakan coba lagi.")
				return
			}
		}

		email := strings.TrimSpace(r.FormValue("email"))
		username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
		password := r.FormValue("password")
		lang := r.FormValue("language")
		lang = validateLang(lang)
		referralCode := strings.TrimSpace(r.FormValue("referral_code"))
		agreeTerms := r.FormValue("agree_terms")

		if username == "" {
			s.renderRegister(w, r, "Username wajib diisi")
			return
		}

		matched, _ := regexp.MatchString(`^[a-z0-9_]{3,20}$`, username)
		if !matched {
			s.renderRegister(w, r, "Username harus 3-20 karakter (huruf kecil a-z, angka 0-9, dan garis bawah)")
			return
		}

		if len(password) < 10 {
			s.renderRegister(w, r, "Kata sandi minimal 10 karakter")
			return
		}
		if len(password) > config.MaxPasswordBytes {
			s.renderRegister(w, r, fmt.Sprintf("Kata sandi maksimal %d karakter", config.MaxPasswordBytes))
			return
		}

		if agreeTerms != "true" && agreeTerms != "1" && agreeTerms != "on" {
			s.renderRegister(w, r, i18n.T(lang, "auth.terms_required"))
			return
		}

		// Catat konsumsi rate limit pendaftaran
		if s.RegisterHourlyLimiter != nil {
			_, _, _ = s.RegisterHourlyLimiter.RecordFailure(clientIP)
		}
		if s.RegisterDailyLimiter != nil {
			_, _, _ = s.RegisterDailyLimiter.RecordFailure(clientIP)
		}

		// Kueri duplikat username atau email SEBELUM hashing agar hemat komputasi
		var existingCount int
		if email != "" {
			_ = s.DB.Pool.QueryRow(r.Context(), `
				SELECT COUNT(*) FROM users 
				WHERE LOWER(username) = LOWER($1) OR (email IS NOT NULL AND LOWER(email) = LOWER($2))
			`, username, email).Scan(&existingCount)
		} else {
			_ = s.DB.Pool.QueryRow(r.Context(), `
				SELECT COUNT(*) FROM users 
				WHERE LOWER(username) = LOWER($1)
			`, username).Scan(&existingCount)
		}
		if existingCount > 0 {
			s.renderRegister(w, r, "Username atau email sudah terdaftar. Silakan gunakan yang lain.")
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
			err := s.DB.Pool.QueryRow(r.Context(), "SELECT id FROM users WHERE referral_code = $1", referralCode).Scan(&refID)
			if err == nil {
				referredBy = &refID
			}
		}

		displayName := username
		var emailVal *string
		if email != "" {
			emailVal = &email
		}

		// Retry referral code hingga 5 kali jika terjadi bentrok unik
		var insertErr error
		for attempt := 0; attempt < 5; attempt++ {
			newRefCode, _, _ := auth.GenerateSessionToken()
			newRefCode = newRefCode[:8]

			_, insertErr = s.DB.Pool.Exec(r.Context(), `
				INSERT INTO users (email, username, password_hash, role, display_name, language, referral_code, referred_by, terms_accepted_at, terms_version)
				VALUES ($1, $2, $3, 'worker', $4, $5, $6, $7, CURRENT_TIMESTAMP, 'v1.0')
			`, emailVal, username, hash, displayName, lang, newRefCode, referredBy)

			if insertErr == nil {
				break
			}
			if !strings.Contains(strings.ToLower(insertErr.Error()), "referral_code") {
				break
			}
		}

		if insertErr != nil {
			var emailStr string
			if emailVal != nil {
				emailStr = *emailVal
			}
			slog.Error("Registration failed in DB", "err", insertErr, "username", username, "email", emailStr)
			errStr := strings.ToLower(insertErr.Error())
			if strings.Contains(errStr, "email") {
				s.renderRegister(w, r, "Email sudah terdaftar. Gunakan email lain atau kosongkan email.")
			} else if strings.Contains(errStr, "username") {
				s.renderRegister(w, r, "Username sudah terdaftar. Coba username lain.")
			} else {
				s.renderRegister(w, r, "Gagal mendaftar: "+insertErr.Error())
			}
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
	formToken := ""
	if len(s.SessionSecret) > 0 {
		formToken = auth.SignFormTimingToken(time.Now(), s.SessionSecret)
	}

	var turnstileSiteKey string
	if s.Turnstile != nil && s.Turnstile.IsEnabled() {
		turnstileSiteKey = s.Turnstile.SiteKey
	}

	data := map[string]interface{}{
		"Lang":             lang,
		"Title":            i18n.T(lang, "auth.register_title"),
		"Error":            errMsg,
		"CSRFToken":        csrfToken,
		"FormToken":        formToken,
		"TurnstileSiteKey": turnstileSiteKey,
		"Nonce":            getNonce(r),
		"Theme":            getTheme(r),
		"T": func(key string) string {
			return i18n.T(lang, key)
		},
	}
	s.Tmpl["register.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sessionHash, ok := r.Context().Value(sessionKey).(string)
	if ok && sessionHash != "" {
		if s.DB != nil && s.DB.Pool != nil {
			_, _ = s.DB.Pool.Exec(context.Background(), "DELETE FROM web_sessions WHERE token_hash = $1", sessionHash)
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.isProd(),
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleWorkerHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/worker/accounts", http.StatusSeeOther)
}

func (s *Server) logAudit(actorID *int64, action, entity, entityID, meta string) {
	if action == "login_failed" {
		entityID = sanitizeEntityID(entity, entityID)
	}
	var metaJSON []byte
	if meta == "" {
		metaJSON = []byte("{}")
	} else if json.Valid([]byte(meta)) {
		metaJSON = []byte(meta)
	} else {
		b, err := json.Marshal(map[string]string{"note": meta})
		if err == nil {
			metaJSON = b
		} else {
			metaJSON = []byte("{}")
		}
	}
	if s.DB != nil && s.DB.Pool != nil {
		_, _ = s.DB.Pool.Exec(context.Background(), `
			INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta)
			VALUES ($1, $2, $3, $4, $5)
		`, actorID, action, entity, entityID, metaJSON)
	}
}

func (s *Server) logAuditWithRateLimit(r *http.Request, actorID *int64, action, entity, entityID, meta string) {
	if action == "login_failed" && s.AuditLoginFailedLimiter != nil {
		clientIP := s.getClientIP(r)
		if allowed, _ := s.AuditLoginFailedLimiter.Allow(clientIP); !allowed {
			return
		}
		_, _, _ = s.AuditLoginFailedLimiter.RecordFailure(clientIP)
	}
	s.logAudit(actorID, action, entity, entityID, meta)
}
