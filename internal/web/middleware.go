package web

import (
	"context"
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
	ID          int64
	Role        string
	Email       string
	Username    string
	DisplayName string
	Language    string
	Currency    string
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

		err = s.DB.Pool.QueryRow(context.Background(), `
			SELECT u.id, u.role, u.email, COALESCE(u.username, ''), COALESCE(u.display_name, u.email), u.language, u.currency, ws.expires_at
			FROM web_sessions ws
			JOIN users u ON u.id = ws.user_id
			WHERE ws.token_hash = $1
		`, hash).Scan(&user.ID, &user.Role, &user.Email, &user.Username, &user.DisplayName, &user.Language, &user.Currency, &expiresAt)

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

		// Update last seen
		_, _ = s.DB.Pool.Exec(context.Background(), `
			UPDATE web_sessions SET last_seen_at = CURRENT_TIMESTAMP WHERE token_hash = $1
		`, hash)

		ctx := context.WithValue(r.Context(), userContextKey, user)
		ctx = context.WithValue(ctx, sessionKey, hash)

		next(w, r.WithContext(ctx))
	}
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
