package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestRequestBodyLimits membuktikan temuan K5: belum ada pembatasan MaxBytesReader
// sehingga payload >1MB di rute umum tidak ditolak 413.
func TestRequestBodyLimits(t *testing.T) {
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

	// Kirim payload 2 MB ke /login
	largeBody := bytes.Repeat([]byte("a"), 2*1024*1024)
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	// Harus ditolak dengan status 413 (StatusRequestEntityTooLarge)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Temuan K5 terbukti: request 2MB ke /login tidak ditolak dengan 413, tetapi got status %d", rr.Code)
	}
}
