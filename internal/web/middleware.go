package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"goblast/internal/auth"

	"golang.org/x/time/rate"
)

type contextKey string

const (
	userContextKey  contextKey = "user"
	sessionKey      contextKey = "session"
	csrfContextKey  contextKey = "csrf"
	nonceContextKey contextKey = "nonce"
)

func getNonce(r *http.Request) string {
	if val := r.Context().Value(nonceContextKey); val != nil {
		if n, ok := val.(string); ok {
			return n
		}
	}
	return ""
}

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

func (s *Server) SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceBytes := make([]byte, 16)
		if _, err := rand.Read(nonceBytes); err != nil {
			copy(nonceBytes, []byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		}
		nonce := base64.StdEncoding.EncodeToString(nonceBytes)

		// Content-Security-Policy
		csp := fmt.Sprintf("default-src 'self'; script-src 'self' 'nonce-%s' https://challenges.cloudflare.com; frame-src https://challenges.cloudflare.com; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'", nonce)
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		w.Header().Set("X-Frame-Options", "DENY")

		if s.isProd() || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		}

		if strings.HasPrefix(r.URL.Path, "/worker") || strings.HasPrefix(r.URL.Path, "/admin") || strings.HasPrefix(r.URL.Path, "/api") {
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
			w.Header().Set("Pragma", "no-cache")
		}

		ctx := context.WithValue(r.Context(), nonceContextKey, nonce)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func parseHost(remoteAddr string) (string, net.IP) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return host, ip
}

func getIP(r *http.Request) string {
	return getIPWithTrustedProxies(r, nil, "CF-Connecting-IP")
}

func (s *Server) getClientIP(r *http.Request) string {
	if s == nil {
		return getIP(r)
	}
	return getIPWithTrustedProxies(r, s.TrustedProxyCIDRs, s.ClientIPHeader)
}

func getIPWithTrustedProxies(r *http.Request, trustedCIDRs []*net.IPNet, headerName string) string {
	host, remoteIP := parseHost(r.RemoteAddr)
	if remoteIP == nil {
		return host
	}

	trusted := false
	if len(trustedCIDRs) == 0 {
		if remoteIP.IsLoopback() {
			trusted = true
		}
	} else {
		for _, cidr := range trustedCIDRs {
			if cidr.Contains(remoteIP) {
				trusted = true
				break
			}
		}
	}

	if !trusted {
		return host
	}

	if headerName == "" {
		headerName = "CF-Connecting-IP"
	}

	rawHeader := strings.TrimSpace(r.Header.Get(headerName))
	if rawHeader == "" {
		rawHeader = strings.TrimSpace(r.Header.Get("X-Real-IP"))
	}

	if rawHeader != "" {
		if idx := strings.Index(rawHeader, ","); idx != -1 {
			rawHeader = strings.TrimSpace(rawHeader[:idx])
		}
		if parsed := net.ParseIP(rawHeader); parsed != nil {
			return parsed.String()
		}
	}

	return host
}

func (s *Server) RequireRole(next http.HandlerFunc, roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookieName := s.sessionCookieName()
		cookie, err := r.Cookie(cookieName)
		if err != nil && s.isProd() {
			cookie, err = r.Cookie("gb_session")
		}
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		hash := auth.HashSessionToken(cookie.Value)

		var user UserSession
		var expiresAt time.Time
		var payoutMethod, payoutAccName *string

		qCtx, qCancel := context.WithTimeout(r.Context(), 10*time.Second)
		err = s.DB.Pool.QueryRow(qCtx, `
			SELECT u.id, u.role, COALESCE(u.email, ''), COALESCE(u.username, ''), COALESCE(u.display_name, u.username, ''), u.language,
			       u.payout_method, u.payout_account_name, ws.expires_at, COALESCE(u.must_change_password, FALSE)
			FROM web_sessions ws
			JOIN users u ON u.id = ws.user_id
			WHERE ws.token_hash = $1 AND u.status = 'active'
		`, hash).Scan(&user.ID, &user.Role, &user.Email, &user.Username, &user.DisplayName, &user.Language,
			&payoutMethod, &payoutAccName, &expiresAt, &user.MustChangePassword)
		qCancel()

		if payoutMethod != nil {
			user.PayoutMethod = *payoutMethod
		}
		if payoutAccName != nil {
			user.PayoutAccountName = *payoutAccName
		}
		// H5a: Dekripsi tujuan penarikan dihapus dari RequireRole (dilakukan hanya saat halaman penarikan/profil dibuka)

		if err != nil || time.Now().After(expiresAt) {
			// Clean up invalid cookie
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
				Secure:   s.isProd(),
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

		// H5a: Update last_seen_at hanya jika aktivitas sebelumnya lebih tua dari 1 menit (mencegah write amplification)
		updCtx, updCancel := context.WithTimeout(r.Context(), 5*time.Second)
		_, _ = s.DB.Pool.Exec(updCtx, `
			UPDATE web_sessions 
			SET last_seen_at = CURRENT_TIMESTAMP 
			WHERE token_hash = $1 AND last_seen_at < CURRENT_TIMESTAMP - INTERVAL '1 minute'
		`, hash)
		updCancel()

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
		cookieName := s.csrfCookieName()
		var csrfToken string
		cookie, err := r.Cookie(cookieName)
		if err != nil && s.isProd() {
			cookie, err = r.Cookie("gb_csrf")
		}
		if err == nil && cookie.Value != "" {
			csrfToken = cookie.Value
		}

		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			// 1. Verifikasi Sec-Fetch-Site jika ada
			if secFetchSite := r.Header.Get("Sec-Fetch-Site"); secFetchSite == "cross-site" {
				http.Error(w, "Cross-site request rejected", http.StatusForbidden)
				return
			}

			// 2. Verifikasi Origin jika ada
			if origin := r.Header.Get("Origin"); origin != "" {
				if originURL, err := url.Parse(origin); err == nil {
					if originURL.Host != "" && !strings.EqualFold(originURL.Host, r.Host) {
						http.Error(w, "Origin mismatch", http.StatusForbidden)
						return
					}
				}
			}

			var maxBytesErr *http.MaxBytesError
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				if err := r.ParseMultipartForm(6 << 20); err != nil && errors.As(err, &maxBytesErr) {
					http.Error(w, "Ukuran request melebihi batas", http.StatusRequestEntityTooLarge)
					return
				}
			} else {
				if err := r.ParseForm(); err != nil && errors.As(err, &maxBytesErr) {
					http.Error(w, "Ukuran request melebihi batas", http.StatusRequestEntityTooLarge)
					return
				}
			}

			sentToken := r.FormValue("csrf_token")
			if sentToken == "" {
				sentToken = r.Header.Get("X-CSRF-Token")
			}

			if csrfToken == "" || sentToken == "" || subtle.ConstantTimeCompare([]byte(sentToken), []byte(csrfToken)) != 1 {
				http.Error(w, "Invalid CSRF token", http.StatusForbidden)
				return
			}

			// Jika server memiliki SessionSecret terkonfigurasi, validasi signature token
			if len(s.SessionSecret) > 0 {
				if _, ok := auth.VerifySignedCSRFToken(sentToken, s.SessionSecret); !ok {
					http.Error(w, "Invalid CSRF token signature", http.StatusForbidden)
					return
				}
			}
		}

		// Set CSRF cookie for GET requests if not exists
		if r.Method == http.MethodGet && csrfToken == "" {
			token, _ := auth.GenerateCSRFToken()
			if len(s.SessionSecret) > 0 {
				token = auth.SignCSRFToken(token, s.SessionSecret)
			}
			csrfToken = token
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName,
				Value:    token,
				Path:     "/",
				HttpOnly: false,
				Secure:   s.isProd(),
				SameSite: http.SameSiteLaxMode,
			})
		}

		ctx := context.WithValue(r.Context(), csrfContextKey, csrfToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
