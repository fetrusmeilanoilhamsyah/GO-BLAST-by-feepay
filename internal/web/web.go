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
	Tmpl *template.Template
}

func NewServer(database *db.DB) (*Server, error) {
	tmpl, err := template.ParseFiles(
		filepath.Join("web", "templates", "base.html"),
		filepath.Join("web", "templates", "index.html"),
	)
	if err != nil {
		return nil, err
	}

	return &Server{
		DB:   database,
		Tmpl: tmpl,
	}, nil
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Serve static files
	staticHandler := http.FileServer(http.Dir(filepath.Join("web", "static")))
	mux.Handle("/static/", http.StripPrefix("/static/", staticHandler))

	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/", s.handleIndex)
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
		http.SetCookie(w, &http.Cookie{
			Name:     "lang",
			Value:    lang,
			Path:     "/",
			Expires:  time.Now().Add(30 * 24 * time.Hour),
			HttpOnly: true,
		})
	} else {
		cookie, err := r.Cookie("lang")
		if err == nil {
			lang = cookie.Value
		} else {
			lang = "id" // default
		}
	}

	data := struct {
		Lang       string
		Title      string
		AppName    string
		AppTagline string
	}{
		Lang:       lang,
		Title:      i18n.T(lang, "app.name"),
		AppName:    i18n.T(lang, "app.name"),
		AppTagline: i18n.T(lang, "app.tagline"),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Tmpl.ExecuteTemplate(w, "base.html", data)
}
