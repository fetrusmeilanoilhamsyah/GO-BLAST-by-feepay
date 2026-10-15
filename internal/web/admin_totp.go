package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"goblast/internal/auth"
	"goblast/internal/config"
	"goblast/internal/i18n"
	"goblast/internal/withdraw"
)

func (s *Server) signSetupToken(secret string, hashedCodes []string) string {
	payload := secret + ":" + strings.Join(hashedCodes, ",")
	mac := hmac.New(sha256.New, []byte(s.SessionSecret))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

func (s *Server) verifySetupToken(token string) (string, []string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", nil, false
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", nil, false
	}
	mac := hmac.New(sha256.New, []byte(s.SessionSecret))
	mac.Write(rawPayload)
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expectedSig)) != 1 {
		return "", nil, false
	}
	payloadParts := strings.SplitN(string(rawPayload), ":", 2)
	if len(payloadParts) != 2 {
		return "", nil, false
	}
	secret := payloadParts[0]
	hashedCodes := strings.Split(payloadParts[1], ",")
	return secret, hashedCodes, true
}

func (s *Server) HandleAdminTOTPSetupGET(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if user.TOTPEnabledAt != nil {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		http.Error(w, "Gagal membuat kunci TOTP", http.StatusInternalServerError)
		return
	}

	plainCodes, hashedCodes, err := auth.GenerateRecoveryCodes(10)
	if err != nil {
		http.Error(w, "Gagal membuat kode cadangan", http.StatusInternalServerError)
		return
	}

	authURI := auth.TOTPAuthURI(user.Username, "GO BLAST", secret)
	setupToken := s.signSetupToken(secret, hashedCodes)

	lang := validateLang(r.URL.Query().Get("lang"))
	data := map[string]interface{}{
		"Lang":          lang,
		"Title":         "Setup TOTP Admin",
		"SecretBase32":  secret,
		"AuthURI":       authURI,
		"RecoveryCodes": plainCodes,
		"SetupToken":    setupToken,
		"CSRFToken":     getCSRF(r),
		"Nonce":         getNonce(r),
		"Theme":         getTheme(r),
		"Error":         r.URL.Query().Get("error"),
		"T": func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["admin_totp_setup.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleAdminTOTPSetupPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	setupToken := r.FormValue("setup_token")
	totpCode := strings.TrimSpace(r.FormValue("totp_code"))

	secret, hashedCodes, okToken := s.verifySetupToken(setupToken)
	if !okToken {
		http.Redirect(w, r, "/admin/totp/setup?error="+url.QueryEscape("Sesi setup kedaluwarsa. Silakan mulai ulang."), http.StatusSeeOther)
		return
	}

	valid, step, _ := auth.ValidateTOTP(secret, totpCode, time.Now(), 0)
	if !valid {
		lang := validateLang(r.URL.Query().Get("lang"))
		data := map[string]interface{}{
			"Lang":          lang,
			"Title":         "Setup TOTP Admin",
			"SecretBase32":  secret,
			"AuthURI":       auth.TOTPAuthURI(user.Username, "GO BLAST", secret),
			"RecoveryCodes": nil, // plain codes tidak ditampilkan ulang setelah submit
			"SetupToken":    setupToken,
			"CSRFToken":     getCSRF(r),
			"Nonce":         getNonce(r),
			"Theme":         getTheme(r),
			"Error":         "Kode TOTP tidak valid. Pastikan waktu pada perangkat Anda tepat.",
			"T": func(key string) string {
				return i18n.T(lang, key)
			},
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = s.Tmpl["admin_totp_setup.html"].ExecuteTemplate(w, "base.html", data)
		return
	}

	encSecret, err := withdraw.EncryptDestination(secret, s.EncryptionKey)
	if err != nil {
		slog.Error("Gagal mengenkripsi secret TOTP", "err", err, "user_id", user.ID)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		slog.Error("Gagal memulai transaksi setup TOTP", "err", err, "user_id", user.ID)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE users 
		SET totp_secret_encrypted = $1, totp_enabled_at = CURRENT_TIMESTAMP, totp_last_step = $2
		WHERE id = $3
	`, encSecret, step, user.ID)
	if err != nil {
		slog.Error("Gagal menyimpan data TOTP pengguna", "err", err, "user_id", user.ID)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	_, _ = tx.Exec(ctx, "DELETE FROM admin_recovery_codes WHERE user_id = $1", user.ID)
	for _, h := range hashedCodes {
		_, err = tx.Exec(ctx, `
			INSERT INTO admin_recovery_codes (user_id, code_hash)
			VALUES ($1, $2)
		`, user.ID, h)
		if err != nil {
			slog.Error("Gagal menyimpan recovery code", "err", err, "user_id", user.ID)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	sessionHash, _ := r.Context().Value(sessionKey).(string)
	if sessionHash != "" {
		_, _ = tx.Exec(ctx, "UPDATE web_sessions SET stepup_at = CURRENT_TIMESTAMP WHERE token_hash = $1", sessionHash)
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("Gagal commit setup TOTP", "err", err, "user_id", user.ID)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logAudit(&user.ID, "totp_setup_completed", "users", strconv.FormatInt(user.ID, 10), "TOTP enabled and recovery codes stored")
	http.Redirect(w, r, "/admin?success="+url.QueryEscape("Autentikasi dua langkah (TOTP) berhasil diaktifkan."), http.StatusSeeOther)
}

func (s *Server) HandleLoginTOTPGET(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if user.TOTPEnabledAt == nil {
		http.Redirect(w, r, "/admin/totp/setup", http.StatusSeeOther)
		return
	}

	if user.StepUpAt != nil {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	lang := validateLang(r.URL.Query().Get("lang"))
	data := map[string]interface{}{
		"Lang":      lang,
		"Title":     "Verifikasi Dua Langkah",
		"CSRFToken": getCSRF(r),
		"Nonce":     getNonce(r),
		"Theme":     getTheme(r),
		"Error":     r.URL.Query().Get("error"),
		"T": func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["login_totp.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleLoginTOTPPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	userIDStr := strconv.FormatInt(user.ID, 10)
	if s.AdminTOTPVerifyLimiter != nil {
		if allowed, wait := s.AdminTOTPVerifyLimiter.Take(userIDStr); !allowed {
			s.redirectRateLimit(w, r, "/login/totp", "admin_totp_verify", wait)
			return
		}
	}

	totpCode := strings.TrimSpace(r.FormValue("totp_code"))
	if totpCode == "" {
		http.Redirect(w, r, "/login/totp?error="+url.QueryEscape("Kode verifikasi wajib diisi"), http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	sessionHash, _ := ctx.Value(sessionKey).(string)

	// Cek apakah input berupa kode cadangan (format XXXXX-XXXXX)
	if strings.Contains(totpCode, "-") || len(totpCode) == 11 {
		codeHash := auth.HashRecoveryCode(totpCode)
		var recoveryID int64
		err := s.DB.Pool.QueryRow(ctx, `
			SELECT id FROM admin_recovery_codes
			WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL
		`, user.ID, codeHash).Scan(&recoveryID)

		if err == nil && recoveryID > 0 {
			_, _ = s.DB.Pool.Exec(ctx, "UPDATE admin_recovery_codes SET used_at = CURRENT_TIMESTAMP WHERE id = $1", recoveryID)
			if sessionHash != "" {
				_, _ = s.DB.Pool.Exec(ctx, "UPDATE web_sessions SET stepup_at = CURRENT_TIMESTAMP WHERE token_hash = $1", sessionHash)
			}
			s.logAudit(&user.ID, "totp_login_recovery_code_used", "admin_recovery_codes", strconv.FormatInt(recoveryID, 10), "")
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		s.logAudit(&user.ID, "totp_login_failed", "users", userIDStr, "invalid recovery code")
		http.Redirect(w, r, "/login/totp?error="+url.QueryEscape("Kode cadangan tidak valid atau sudah pernah digunakan."), http.StatusSeeOther)
		return
	}

	// Verifikasi 6 digit TOTP
	var encSecret *string
	var lastStep int64
	err := s.DB.Pool.QueryRow(ctx, `
		SELECT totp_secret_encrypted, COALESCE(totp_last_step, 0)
		FROM users WHERE id = $1
	`, user.ID).Scan(&encSecret, &lastStep)

	if err != nil || encSecret == nil || *encSecret == "" {
		http.Redirect(w, r, "/admin/totp/setup", http.StatusSeeOther)
		return
	}

	secret, err := withdraw.DecryptDestination(*encSecret, s.EncryptionKey)
	if err != nil {
		slog.Error("Gagal mendekripsi secret TOTP admin", "err", err, "user_id", user.ID)
		http.Redirect(w, r, "/login/totp?error="+url.QueryEscape("Terjadi kesalahan sistem"), http.StatusSeeOther)
		return
	}

	valid, step, _ := auth.ValidateTOTP(secret, totpCode, time.Now(), lastStep)
	if !valid {
		s.logAudit(&user.ID, "totp_login_failed", "users", userIDStr, "invalid totp code")
		http.Redirect(w, r, "/login/totp?error="+url.QueryEscape("Kode TOTP salah atau sudah kedaluwarsa."), http.StatusSeeOther)
		return
	}

	// Update last step (anti-replay) dan aktifkan stepup sesi
	_, _ = s.DB.Pool.Exec(ctx, "UPDATE users SET totp_last_step = $1 WHERE id = $2", step, user.ID)
	if sessionHash != "" {
		_, _ = s.DB.Pool.Exec(ctx, "UPDATE web_sessions SET stepup_at = CURRENT_TIMESTAMP WHERE token_hash = $1", sessionHash)
	}

	s.logAudit(&user.ID, "totp_login_success", "users", userIDStr, "")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) HandleAdminStepUpGET(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	redirectURL := r.URL.Query().Get("redirect")
	if redirectURL == "" || !strings.HasPrefix(redirectURL, "/") || strings.HasPrefix(redirectURL, "//") {
		redirectURL = "/admin"
	}

	lang := validateLang(r.URL.Query().Get("lang"))
	data := map[string]interface{}{
		"Lang":        lang,
		"Title":       "Verifikasi Keamanan (Step-Up)",
		"RedirectURL": redirectURL,
		"CSRFToken":   getCSRF(r),
		"Nonce":       getNonce(r),
		"Theme":       getTheme(r),
		"Error":       r.URL.Query().Get("error"),
		"T": func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["admin_stepup.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleAdminStepUpPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	userIDStr := strconv.FormatInt(user.ID, 10)
	if s.AdminTOTPVerifyLimiter != nil {
		if allowed, wait := s.AdminTOTPVerifyLimiter.Take(userIDStr); !allowed {
			s.redirectRateLimit(w, r, "/admin/stepup", "admin_stepup", wait)
			return
		}
	}

	redirectURL := r.FormValue("redirect")
	if redirectURL == "" || !strings.HasPrefix(redirectURL, "/") || strings.HasPrefix(redirectURL, "//") {
		redirectURL = "/admin"
	}

	password := r.FormValue("password")
	totpCode := strings.TrimSpace(r.FormValue("totp_code"))

	ctx := r.Context()
	sessionHash, _ := ctx.Value(sessionKey).(string)
	var verified bool

	if totpCode != "" {
		var encSecret *string
		var lastStep int64
		err := s.DB.Pool.QueryRow(ctx, `
			SELECT totp_secret_encrypted, COALESCE(totp_last_step, 0)
			FROM users WHERE id = $1
		`, user.ID).Scan(&encSecret, &lastStep)

		if err == nil && encSecret != nil && *encSecret != "" {
			secret, decErr := withdraw.DecryptDestination(*encSecret, s.EncryptionKey)
			if decErr == nil {
				valid, step, _ := auth.ValidateTOTP(secret, totpCode, time.Now(), lastStep)
				if valid {
					verified = true
					_, _ = s.DB.Pool.Exec(ctx, "UPDATE users SET totp_last_step = $1 WHERE id = $2", step, user.ID)
					s.logAudit(&user.ID, "admin_stepup_success", "web_sessions", "", "via_totp")
				}
			}
		}
	}

	if !verified && password != "" {
		var currentHash string
		err := s.DB.Pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1", user.ID).Scan(&currentHash)
		if err == nil {
			match, matchErr := auth.VerifyPasswordGated(ctx, password, currentHash)
			if matchErr == nil && match {
				verified = true
				s.logAudit(&user.ID, "admin_stepup_success", "web_sessions", "", "via_password")
			}
		}
	}

	if verified {
		if sessionHash != "" {
			_, _ = s.DB.Pool.Exec(ctx, "UPDATE web_sessions SET stepup_at = CURRENT_TIMESTAMP WHERE token_hash = $1", sessionHash)
		}
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "admin_stepup_failed", "web_sessions", "", "invalid credentials")
	http.Redirect(w, r, fmt.Sprintf("/admin/stepup?redirect=%s&error=%s", url.QueryEscape(redirectURL), url.QueryEscape("Kata sandi atau kode TOTP tidak valid.")), http.StatusSeeOther)
}

// RequireStepUp memeriksa apakah sesi admin telah melakukan verifikasi sensitif dalam waktu 5 menit terakhir.
func (s *Server) RequireStepUp(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := currentUser(r)
		if !ok || user.Role != "admin" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		sessionHash, _ := r.Context().Value(sessionKey).(string)
		var stepupAt *time.Time
		if sessionHash != "" {
			_ = s.DB.Pool.QueryRow(r.Context(), "SELECT stepup_at FROM web_sessions WHERE token_hash = $1", sessionHash).Scan(&stepupAt)
		}

		if stepupAt == nil || time.Since(*stepupAt) > config.AdminStepUpDuration {
			if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"ok":         false,
					"error":      "stepup_required",
					"message":    "Verifikasi keamanan (step-up) diperlukan untuk aksi administratif sensitif ini.",
					"stepup_url": "/admin/stepup?redirect=" + url.QueryEscape(r.RequestURI),
				})
				return
			}
			http.Redirect(w, r, "/admin/stepup?redirect="+url.QueryEscape(r.RequestURI), http.StatusSeeOther)
			return
		}

		next(w, r)
	}
}

// ResetAdminTOTP mereset status TOTP admin berdasarkan email, menghapus kode pemulihan, dan mencatat audit log.
func ResetAdminTOTP(ctx context.Context, pool *pgxpool.Pool, email string) (int64, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return 0, errors.New("email tidak boleh kosong")
	}

	var adminID int64
	var role string
	err := pool.QueryRow(ctx, "SELECT id, role FROM users WHERE LOWER(email) = LOWER($1)", email).Scan(&adminID, &role)
	if err != nil {
		return 0, fmt.Errorf("admin dengan email %q tidak ditemukan: %w", email, err)
	}

	if role != "admin" {
		return 0, fmt.Errorf("pengguna %q bukan akun admin (role: %s)", email, role)
	}

	_, err = pool.Exec(ctx, `
		UPDATE users 
		SET totp_secret_encrypted = NULL, totp_enabled_at = NULL, totp_last_step = NULL
		WHERE id = $1
	`, adminID)
	if err != nil {
		return 0, fmt.Errorf("gagal mereset kolom TOTP: %w", err)
	}

	_, _ = pool.Exec(ctx, "DELETE FROM admin_recovery_codes WHERE user_id = $1", adminID)

	_, _ = pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta)
		VALUES ($1, 'reset_totp_cli', 'users', $2, '{"note":"Emergency CLI reset TOTP"}')
	`, adminID, strconv.FormatInt(adminID, 10))

	return adminID, nil
}
