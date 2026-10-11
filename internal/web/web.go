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
)

type Server struct {
	DB   *db.DB
	Tmpl map[string]*template.Template
	Env  string
}

func NewServer(database *db.DB, env string) (*Server, error) {
	base := filepath.Join("web", "templates", "base.html")

	templates := make(map[string]*template.Template)

	pages := []string{"index.html", "login.html", "register.html", "admin_campaigns.html", "admin_campaign_form.html"}
	for _, page := range pages {
		tmpl, err := template.ParseFiles(base, filepath.Join("web", "templates", page))
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

	return &Server{
		DB:   database,
		Tmpl: templates,
		Env:  env,
	}, nil
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Serve static files
	staticHandler := http.FileServer(http.Dir(filepath.Join("web", "static")))
	mux.Handle("/static/", http.StripPrefix("/static/", staticHandler))

	mux.HandleFunc("/healthz", s.handleHealthz)

	mux.Handle("/", s.CSRFMiddleware(http.HandlerFunc(s.handleIndex)))
	mux.Handle("POST /theme", s.CSRFMiddleware(http.HandlerFunc(s.HandleThemePOST)))
	mux.Handle("/login", s.CSRFMiddleware(RateLimitLogin(s.handleLogin)))
	mux.Handle("/register", s.CSRFMiddleware(RateLimitRegister(s.handleRegister)))

	mux.Handle("/logout", s.CSRFMiddleware(s.RequireRole(s.handleLogout, "admin", "worker")))
	mux.Handle("/admin", s.CSRFMiddleware(s.RequireRole(s.handleAdminHome, "admin")))

	// Tahap 8
	mux.Handle("GET /admin/campaigns", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaigns, "admin")))
	mux.Handle("GET /admin/campaigns/new", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNewGET, "admin")))
	mux.Handle("POST /admin/campaigns/new", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNewPOST, "admin")))
	mux.Handle("GET /admin/campaigns/{id}/edit", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignEditGET, "admin")))
	mux.Handle("POST /admin/campaigns/{id}/edit", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignEditPOST, "admin")))
	mux.Handle("POST /admin/campaigns/preview", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignPreview, "admin")))
	mux.Handle("GET /uploads/{filename}", s.RequireRole(s.HandleServeUpload, "admin")) // simple protection

	// Tahap 7
	mux.Handle("GET /admin/campaigns/{id}/numbers", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersGET, "admin")))
	mux.Handle("POST /admin/campaigns/{id}/numbers/preview", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersPreview, "admin")))
	mux.Handle("POST /admin/campaigns/{id}/numbers/confirm", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersConfirm, "admin")))
	mux.Handle("GET /admin/campaigns/{id}/numbers/rejected/{previewId}", s.CSRFMiddleware(s.RequireRole(s.HandleAdminCampaignNumbersRejected, "admin")))

	mux.Handle("/worker", s.CSRFMiddleware(s.RequireRole(s.handleWorkerHome, "worker")))
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

func validateLang(lang string) string {
	switch lang {
	case "id", "en", "zh":
		return lang
	default:
		return "id"
	}
}
