package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"goblast/internal/auth"

	"golang.org/x/time/rate"
)

type contextKey string

const (
	userContextKey contextKey = "user"
	sessionKey     contextKey = "session"
	csrfContextKey contextKey = "csrf"
)

type UserSession struct {
	ID                int64
	Role              string
	Email             string
	Username          string
	DisplayName       string
	Language          string
	PayoutMethod      string
	PayoutAccountName string
	PayoutDestMasked   string
	MustChangePassword bool
}

// MaskDestination menyamarkan nomor rekening, nomor e-wallet, atau alamat crypto untuk privasi dan keamanan di UI.
func MaskDestination(dest, method string) string {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return ""
	}
	if method == "usdt_bep20" {
		if len(dest) > 10 {
			return dest[:6] + "..." + dest[len(dest)-4:]
		}
		return dest
	}
	if len(dest) <= 4 {
		return dest
	}
	if len(dest) <= 8 {
		return dest[:2] + strings.Repeat("*", len(dest)-4) + dest[len(dest)-2:]
	}
	prefixLen := 4
	suffixLen := 4
	maskLen := len(dest) - prefixLen - suffixLen
	if maskLen < 3 {
		maskLen = 3
	}
	return dest[:prefixLen] + strings.Repeat("*", maskLen) + dest[len(dest)-suffixLen:]
}

func currentUser(r *http.Request) (UserSession, bool) {
	if val := r.Context().Value(userContextKey); val != nil {
		if u, ok := val.(UserSession); ok {
			return u, true
		}
		if uPtr, ok := val.(*UserSession); ok && uPtr != nil {
			return *uPtr, true
		}
	}
	return UserSession{}, false
}

type limiterStore struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

func newLimiterStore() *limiterStore {
	return &limiterStore{
		limiters: make(map[string]*rate.Limiter),
	}
}

func (s *limiterStore) getVisitor(key string, r rate.Limit, b int) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()

	limiter, exists := s.limiters[key]
	if !exists {
		limiter = rate.NewLimiter(r, b)
		s.limiters[key] = limiter
	}

	return limiter
}

var (
	loginLimiter    = newLimiterStore()
	registerLimiter = newLimiterStore()
)

// RateLimitLogin limits 5 attempts per 15 minutes per IP+email
// We wrap the HTTP handler. We can extract email from r.FormValue("email").
func RateLimitLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			ip := getIP(r)
			identifier := strings.TrimSpace(r.FormValue("identifier"))
			if identifier == "" {
				identifier = strings.TrimSpace(r.FormValue("email"))
			}
			key := ip + ":" + identifier

			// 5 attempts per 15 minutes = 1 attempt per 3 minutes (0.0055/s)
			// We can use rate.Every(15*time.Minute/5), 5 burst.
			limiter := loginLimiter.getVisitor(key, rate.Every(15*time.Minute/5), 5)

			if !limiter.Allow() {
				http.Error(w, "Too many requests", http.StatusTooManyRequests)
				return
			}
		}
		next(w, r)
	}
}

// RateLimitRegister limits 10 per hour per IP
func RateLimitRegister(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			ip := getIP(r)

			// 10 attempts per hour = 1 attempt per 6 minutes
			limiter := registerLimiter.getVisitor(ip, rate.Every(time.Hour/10), 10)

			if !limiter.Allow() {
				http.Error(w, "Too many requests", http.StatusTooManyRequests)
				return
			}
		}
		next(w, r)
	}
}

func getIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "127.0.0.1" || host == "::1" {
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}
	return host
}

func (s *Server) RequireRole(next http.HandlerFunc, roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("gb_session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		hash := auth.HashSessionToken(cookie.Value)

		var user UserSession
		var expiresAt time.Time
		var payoutMethod, payoutDestEncrypted, payoutAccName *string

		err = s.DB.Pool.QueryRow(context.Background(), `
			SELECT u.id, u.role, COALESCE(u.email, ''), COALESCE(u.username, ''), COALESCE(u.display_name, u.username, ''), u.language,
			       u.payout_method, u.payout_dest_encrypted, u.payout_account_name, ws.expires_at, COALESCE(u.must_change_password, FALSE)
			FROM web_sessions ws
			JOIN users u ON u.id = ws.user_id
			WHERE ws.token_hash = $1 AND u.status = 'active'
		`, hash).Scan(&user.ID, &user.Role, &user.Email, &user.Username, &user.DisplayName, &user.Language,
			&payoutMethod, &payoutDestEncrypted, &payoutAccName, &expiresAt, &user.MustChangePassword)

		if payoutMethod != nil {
			user.PayoutMethod = *payoutMethod
		}
		if payoutAccName != nil {
			user.PayoutAccountName = *payoutAccName
		}
		if payoutDestEncrypted != nil && *payoutDestEncrypted != "" {
			if dec, err := s.DecryptDestination(*payoutDestEncrypted); err == nil {
				user.PayoutDestMasked = MaskDestination(dec, user.PayoutMethod)
			}
		}

		if err != nil || time.Now().After(expiresAt) {
			// Clean up invalid cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "gb_session",
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		if lCookie, err := r.Cookie("lang"); err == nil && lCookie.Value != "" {
			user.Language = validateLang(lCookie.Value)
		}

		roleAllowed := false
		for _, role := range roles {
			if user.Role == role {
				roleAllowed = true
				break
			}
		}

		if !roleAllowed {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		if user.Role == "worker" && s.IsMaintenanceMode(r.Context()) {
			http.Redirect(w, r, "/maintenance", http.StatusSeeOther)
			return
		}

		if user.Role == "worker" && user.MustChangePassword {
			if r.URL.Path != "/worker" && r.URL.Path != "/worker/security" && r.URL.Path != "/logout" {
				http.Redirect(w, r, "/worker?error=Kata+sandi+Anda+telah+direset+oleh+admin.+Silakan+ganti+kata+sandi+Anda+terlebih+dahulu.", http.StatusSeeOther)
				return
			}
		}

		// Update last seen
		_, _ = s.DB.Pool.Exec(context.Background(), `
			UPDATE web_sessions SET last_seen_at = CURRENT_TIMESTAMP WHERE token_hash = $1
		`, hash)

		ctx := context.WithValue(r.Context(), userContextKey, user)
		ctx = context.WithValue(ctx, sessionKey, hash)

		next(w, r.WithContext(ctx))
	}
}

// IsMaintenanceMode memeriksa apakah mode pemeliharaan sistem sedang diaktifkan.
func (s *Server) IsMaintenanceMode(ctx context.Context) bool {
	if s == nil || s.DB == nil || s.DB.Pool == nil {
		return false
	}
	var valStr string
	err := s.DB.Pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'maintenance_mode'").Scan(&valStr)
	if err != nil {
		return false
	}
	var enabled bool
	_ = json.Unmarshal([]byte(valStr), &enabled)
	return enabled
}

func (s *Server) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var csrfToken string
		cookie, err := r.Cookie("gb_csrf")
		if err == nil && cookie.Value != "" {
			csrfToken = cookie.Value
		}

		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			sentToken := r.FormValue("csrf_token")
			if sentToken == "" {
				sentToken = r.Header.Get("X-CSRF-Token")
			}
			if csrfToken == "" || sentToken != csrfToken {
				http.Error(w, "Invalid CSRF token", http.StatusForbidden)
				return
			}
		}

		// Set CSRF cookie for GET requests if not exists
		if r.Method == http.MethodGet && csrfToken == "" {
			token, _ := auth.GenerateCSRFToken()
			csrfToken = token
			secure := s.Env != "dev"
			http.SetCookie(w, &http.Cookie{
				Name:     "gb_csrf",
				Value:    token,
				Path:     "/",
				HttpOnly: false, // Must be readable by JS if needed, or we just put it in templates
				Secure:   secure,
				SameSite: http.SameSiteLaxMode,
			})
		}

		ctx := context.WithValue(r.Context(), csrfContextKey, csrfToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// We need Env in server to check secure cookie
