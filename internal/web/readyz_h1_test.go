package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"goblast/internal/db"
	"goblast/internal/safe"
	"goblast/internal/testutil"
)

func TestHealthzAndReadyz(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}

	srv, err := NewServer(database, "test", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// 1. Uji /healthz selalu 200 OK
	reqHealth := httptest.NewRequest("GET", "/healthz", nil)
	recHealth := httptest.NewRecorder()
	mux.ServeHTTP(recHealth, reqHealth)

	if recHealth.Code != http.StatusOK {
		t.Fatalf("Ekspektasi /healthz 200 OK, didapat %d", recHealth.Code)
	}

	// 2. Uji /readyz 200 OK dalam kondisi normal (DB aktif, belum draining)
	reqReady := httptest.NewRequest("GET", "/readyz", nil)
	recReady := httptest.NewRecorder()
	mux.ServeHTTP(recReady, reqReady)

	if recReady.Code != http.StatusOK {
		t.Fatalf("Ekspektasi /readyz 200 OK dalam kondisi normal, didapat %d (body: %s)", recReady.Code, recReady.Body.String())
	}

	// 3. Uji /readyz 503 saat server draining
	srv.SetDraining(true)
	reqDraining := httptest.NewRequest("GET", "/readyz", nil)
	recDraining := httptest.NewRecorder()
	mux.ServeHTTP(recDraining, reqDraining)

	if recDraining.Code != http.StatusServiceUnavailable {
		t.Fatalf("Ekspektasi /readyz 503 saat draining, didapat %d", recDraining.Code)
	}

	// Pastikan /healthz tetap 200 OK walau draining (karena liveness probe tetap hidup)
	recHealthDraining := httptest.NewRecorder()
	mux.ServeHTTP(recHealthDraining, reqHealth)
	if recHealthDraining.Code != http.StatusOK {
		t.Fatalf("Ekspektasi /healthz tetap 200 saat draining, didapat %d", recHealthDraining.Code)
	}

	// 4. Uji /readyz 503 saat DB bernilai nil
	srvNoDB, err := NewServer(nil, "test", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server tanpa DB: %v", err)
	}
	muxNoDB := http.NewServeMux()
	srvNoDB.RegisterRoutes(muxNoDB)
	recNoDB := httptest.NewRecorder()
	muxNoDB.ServeHTTP(recNoDB, reqReady)

	if recNoDB.Code != http.StatusServiceUnavailable {
		t.Fatalf("Ekspektasi /readyz 503 saat DB nil, didapat %d", recNoDB.Code)
	}
}

func TestHTTPRecoverMiddleware(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	safe.ResetPanicCount()
	initPanics := safe.PanicCount()

	srv, err := NewServer(nil, "test", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server: %v", err)
	}

	// Handler tiruan yang sengaja panic
	panickingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("panic sengaja dalam handler HTTP")
	})

	wrapped := srv.RecoverMiddleware(panickingHandler)

	req := httptest.NewRequest("GET", "/test-panic", nil)
	rec := httptest.NewRecorder()

	// Jalankan request - tidak boleh crash / crash runtime
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Ekspektasi status 500 saat panic di handler, didapat %d", rec.Code)
	}

	if safe.PanicCount() != initPanics+1 {
		t.Fatalf("Ekspektasi safe.PanicCount bertambah 1, didapat %d", safe.PanicCount())
	}
}
