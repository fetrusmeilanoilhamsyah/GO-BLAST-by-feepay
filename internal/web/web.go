package web

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"goblast/internal/db"
	"goblast/internal/i18n"
	"goblast/internal/wa"
)

type Server struct {
	DB            *db.DB
	Tmpl          map[string]*template.Template
	Env           string
	WaManager     *wa.Manager
	EncryptionKey []byte
}

func NewServer(database *db.DB, env string, waMgr ...*wa.Manager) (*Server, error) {
	base := filepath.Join("web", "templates", "base.html")
	workerNav := filepath.Join("web", "templates", "worker_nav.html")

	templates := make(map[string]*template.Template)

	workerPages := map[string]bool{
		"worker_home.html":        true,
		"worker_accounts.html":    true,
		"worker_account_new.html": true,
		"worker_withdrawals.html": true,
		"worker_referral.html":    true,
	}

	pages := []string{
		"index.html", "login.html", "register.html",
		"admin_campaigns.html", "admin_campaign_form.html", "admin_campaign_detail.html",
		"worker_accounts.html", "worker_account_new.html",
		"worker_home.html", "worker_withdrawals.html", "worker_referral.html",
	}
	for _, page := range pages {
		var tmpl *template.Template
		var err error
		pagePath := filepath.Join("web", "templates", page)
		if workerPages[page] {
			tmpl, err = template.ParseFiles(base, workerNav, pagePath)
		} else {
			tmpl, err = template.ParseFiles(base, pagePath)
		}
		if err != nil {
			return nil, err
		}
		templates[page] = tmpl
	}

	// Preview template doesn't use base.html
	previewTmpl, err := template.ParseFiles(filepath.Join("web", "templates", "admin_campaign_preview.html"))
	if err == nil {
		templates["admin_campaign_preview.html"] = previewTmpl
	}

	var mgr *wa.Manager
	if len(waMgr) > 0 {
		mgr = waMgr[0]
	}

	return &Server{
		DB:        database,
		Tmpl:      templates,
		Env:       env,
		WaManager: mgr,
	}, nil
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Serve static files
	staticHandler := http.FileServer(http.Dir(filepath.Join("web", "static")))
	mux.Handle("/static/", http.StripPrefix("/static/", staticHandler))

	mux.HandleFunc("/healthz", s.handleHealthz)

	mux.Handle("/", s.CSRFMiddleware(http.HandlerFunc(s.handleIndex)))
	mux.Handle("POST /theme", s.CSRFMiddleware(http.HandlerFunc(s.HandleThemePOST)))
	mux.Handle("POST /language", s.CSRFMiddleware(http.HandlerFunc(s.HandleLanguagePOST)))
	mux.Handle("/login", s.CSRFMiddleware(RateLimitLogin(s.handleLogin)))
	mux.Handle("/register", s.CSRFMiddleware(RateLimitRegister(s.handleRegister)))

	mux.Handle("/logout", s.CSRFMiddleware(s.RequireRole(s.handleLogout, "admin", "worker")))
	mux.Handle("/admin", s.CSRFMiddleware(s.RequireRole(s.handleAdminHome, "admin")))

	// Tahap 8
	mux.Handle("GET /admin/campaigns", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaigns, "admin")))
	mux.Handle("GET /admin/campaigns/new", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNewGET, "admin")))
	mux.Handle("POST /admin/campaigns/new", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNewPOST, "admin")))
	mux.Handle("GET /admin/campaigns/{id}", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignDetail, "admin")))
	mux.Handle("POST /admin/campaigns/{id}/start", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignStart, "admin")))
	mux.Handle("POST /admin/campaigns/{id}/finish", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignFinish, "admin")))
	mux.Handle("GET /admin/campaigns/{id}/edit", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignEditGET, "admin")))
	mux.Handle("POST /admin/campaigns/{id}/edit", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignEditPOST, "admin")))
	mux.Handle("POST /admin/campaigns/preview", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignPreview, "admin")))
	mux.Handle("GET /uploads/{filename}", s.RequireRole(s.HandleServeUpload, "admin")) // simple protection

	// Tahap 7
	mux.Handle("GET /admin/campaigns/{id}/numbers", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersGET, "admin")))
	mux.Handle("GET /admin/campaigns/{id}/numbers/upload", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersGET, "admin")))
	mux.Handle("POST /admin/campaigns/{id}/numbers/preview", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersPreview, "admin")))
	mux.Handle("POST /admin/campaigns/{id}/numbers/confirm", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersConfirm, "admin")))
	mux.Handle("GET /admin/campaigns/{id}/numbers/rejected/{previewId}", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersRejected, "admin")))

	// Tahap 11: Worker Panel
	mux.Handle("GET /worker", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerHome, "worker")))
	mux.Handle("POST /worker/profile", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerProfilePOST, "worker")))
	mux.Handle("POST /worker/security", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerSecurityPOST, "worker")))
	mux.Handle("GET /worker/withdrawals", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerWithdrawalsGET, "worker")))
	mux.Handle("POST /worker/withdrawals", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerWithdrawalsPOST, "worker")))
	mux.Handle("GET /worker/referral", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerReferralGET, "worker")))

	// Tahap 9
	mux.Handle("GET /worker/accounts", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccounts, "worker")))
	mux.Handle("POST /worker/accounts/init", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountInitPOST, "worker")))
	mux.Handle("GET /worker/accounts/new", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountNewGET, "worker")))
	mux.Handle("POST /worker/accounts/new", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountNewPOST, "worker")))
	mux.Handle("GET /worker/accounts/{id}/status", s.RequireRole(s.HandleWorkerAccountStatus, "worker"))
	mux.Handle("POST /worker/accounts/{id}/pair", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountPairPOST, "worker")))
	mux.Handle("POST /worker/accounts/{id}/settings", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountSettingsPOST, "worker")))
	mux.Handle("POST /worker/accounts/{id}/delete", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountDelete, "worker")))
	mux.Handle("POST /worker/accounts/{id}/disconnect", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountDisconnect, "worker")))
	mux.Handle("POST /worker/accounts/{id}/stop", s.CSRFMiddleware(s.RequireRole(s.HandleWorkerAccountStop, "worker")))
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	err := s.DB.Pool.Ping(context.Background())
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"error","db":"unreachable"}`))
		return
	}

	w.Write([]byte(`{"status":"ok","db":"ok"}`))
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	lang := r.URL.Query().Get("lang")
	if lang != "" {
		lang = validateLang(lang)
		http.SetCookie(w, &http.Cookie{
			Name:     "lang",
			Value:    lang,
			Path:     "/",
			Expires:  time.Now().Add(30 * 24 * time.Hour),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	} else {
		cookie, err := r.Cookie("lang")
		if err == nil {
			lang = validateLang(cookie.Value)
		} else {
			lang = "id" // default
		}
	}

	data := struct {
		Lang           string
		Title          string
		AppName        string
		AppTagline     string
		SelectLanguage string
		Theme          string
		CSRFToken      string
		T              func(string) string
	}{
		Lang:           lang,
		Title:          i18n.T(lang, "app.name"),
		AppName:        i18n.T(lang, "app.name"),
		AppTagline:     i18n.T(lang, "app.tagline"),
		SelectLanguage: i18n.T(lang, "common.select_language"),
		Theme:          getTheme(r),
		CSRFToken:      getCSRF(r),
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["index.html"].ExecuteTemplate(w, "base.html", data)
}

func getCSRF(r *http.Request) string {
	if token, ok := r.Context().Value(csrfContextKey).(string); ok && token != "" {
		return token
	}
	if cookie, err := r.Cookie("gb_csrf"); err == nil {
		return cookie.Value
	}
	return ""
}

func getTheme(r *http.Request) string {
	cookie, err := r.Cookie("theme")
	if err == nil {
		switch cookie.Value {
		case "light", "dark":
			return cookie.Value
		}
	}
	return ""
}

func (s *Server) HandleThemePOST(w http.ResponseWriter, r *http.Request) {
	theme := r.FormValue("theme")
	if theme != "auto" && theme != "light" && theme != "dark" {
		http.Error(w, "Invalid theme value", http.StatusBadRequest)
		return
	}

	secure := s.Env != "dev"
	if theme == "auto" {
		http.SetCookie(w, &http.Cookie{
			Name:     "theme",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: false,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
	} else {
		http.SetCookie(w, &http.Cookie{
			Name:     "theme",
			Value:    theme,
			Path:     "/",
			MaxAge:   365 * 24 * 60 * 60,
			HttpOnly: false,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})
	}

	target := "/"
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

func (s *Server) HandleLanguagePOST(w http.ResponseWriter, r *http.Request) {
	lang := validateLang(r.FormValue("lang"))

	secure := s.Env != "dev"
	http.SetCookie(w, &http.Cookie{
		Name:     "lang",
		Value:    lang,
		Path:     "/",
		Expires:  time.Now().Add(30 * 24 * time.Hour),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	if user, ok := currentUser(r); ok {
		_, _ = s.DB.Pool.Exec(r.Context(), "UPDATE users SET language = $1 WHERE id = $2", lang, user.ID)
	}

	target := "/"
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

func validateLang(lang string) string {
	switch lang {
	case "id", "en", "zh":
		return lang
	default:
		return "id"
	}
}
