package web

import (
	"context"
	"html/template"
	"net/http"
	"path/filepath"
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
	
	pages := []string{"index.html", "login.html", "register.html"}
	for _, page := range pages {
		tmpl, err := template.ParseFiles(base, filepath.Join("web", "templates", page))
		if err != nil {
			return nil, err
		}
		templates[page] = tmpl
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
	mux.Handle("/login", s.CSRFMiddleware(RateLimitLogin(s.handleLogin)))
	mux.Handle("/register", s.CSRFMiddleware(RateLimitRegister(s.handleRegister)))

	mux.Handle("/logout", s.CSRFMiddleware(s.RequireRole(s.handleLogout, "admin", "worker")))
	mux.Handle("/admin", s.CSRFMiddleware(s.RequireRole(s.handleAdminHome, "admin")))
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
	}{
		Lang:           lang,
		Title:          i18n.T(lang, "app.name"),
		AppName:        i18n.T(lang, "app.name"),
		AppTagline:     i18n.T(lang, "app.tagline"),
		SelectLanguage: i18n.T(lang, "common.select_language"),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl["index.html"].ExecuteTemplate(w, "base.html", data)
}

func validateLang(lang string) string {
	switch lang {
	case "id", "en", "zh":
		return lang
	default:
		return "id"
	}
}
