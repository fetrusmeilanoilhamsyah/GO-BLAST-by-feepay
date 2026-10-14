package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStaticNoDirectoryListing membuktikan bahwa endpoint /static/ tidak boleh menampilkan daftar isi direktori.
func TestStaticNoDirectoryListing(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(nil, "test", nil, nil)
	if err != nil {
		t.Fatalf("Gagal inisialisasi server: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Uji 1: GET /static/
	req := httptest.NewRequest("GET", "/static/", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code == http.StatusOK {
		body := rr.Body.String()
		// Jika status 200 dan berisi tag <pre> atau daftar file css/js, berarti directory listing bocor
		if strings.Contains(body, "<pre>") || strings.Contains(body, "theme.css") {
			t.Fatalf("Temuan K5 terbukti: /static/ mengembalikan daftar isi direktori (status 200) bukannya menolak. Body:\n%s", body)
		}
	}

	// Uji 2: Pastikan file yang sah tetap bisa diakses (misal css/theme.css)
	themePath := filepath.Join("web", "static", "css", "theme.css")
	if _, err := os.Stat(themePath); err == nil {
		reqValid := httptest.NewRequest("GET", "/static/css/theme.css", nil)
		rrValid := httptest.NewRecorder()
		mux.ServeHTTP(rrValid, reqValid)
		if rrValid.Code != http.StatusOK {
			t.Fatalf("File statis sah harus tetap bisa diakses (200), got %d", rrValid.Code)
		}
	}
}
