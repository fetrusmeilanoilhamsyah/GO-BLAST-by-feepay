package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa"
	"goblast/internal/wa/watest"
)

func TestAdminAPIMetrics_H13CheckCounts(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	ctx := context.Background()

	var adminID int64
	err := pool.QueryRow(ctx,
		"INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'hash', 'admin', $2, 'Admin H13 Metrics') RETURNING id",
		fmt.Sprintf("admin_h13_%d@test.com", time.Now().UnixNano()),
		fmt.Sprintf("REF_H13_M_%d", time.Now().UnixNano()),
	).Scan(&adminID)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID}, nil)
	})

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, nil)

	// Tambahkan metrik pemeriksaan (TidakTerdaftar diikuti Terdaftar agar ter-reset ke 0)
	eng.Metrics().IncCheckResult(wa.CheckResultTidakTerdaftar)
	eng.Metrics().IncCheckResult(wa.CheckResultTidakPasti)
	eng.Metrics().IncCheckResult(wa.CheckResultTerdaftar)

	s, err := NewServer(database, "test", nil, eng)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	req := httptest.NewRequest("GET", "/admin/api/metrics", nil)
	ctxWithUser := context.WithValue(req.Context(), userContextKey, &UserSession{ID: adminID, Role: "admin"})
	req = req.WithContext(ctxWithUser)

	rr := httptest.NewRecorder()
	s.HandleAdminAPIMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Engine engine.EngineStats `json:"engine"`
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	if resp.Engine.CheckCounts == nil {
		t.Fatalf("expected 'check_counts' in engine metrics, got nil")
	}

	if resp.Engine.CheckCounts["terdaftar"] != 1 {
		t.Errorf("expected check_counts.terdaftar == 1, got %d", resp.Engine.CheckCounts["terdaftar"])
	}
	if resp.Engine.CheckCounts["tidak_terdaftar"] != 1 {
		t.Errorf("expected check_counts.tidak_terdaftar == 1, got %d", resp.Engine.CheckCounts["tidak_terdaftar"])
	}
	if resp.Engine.CheckCounts["tidak_pasti"] != 1 {
		t.Errorf("expected check_counts.tidak_pasti == 1, got %d", resp.Engine.CheckCounts["tidak_pasti"])
	}
	if resp.Engine.ConsecutiveTidakTerdaftarCount != 0 {
		t.Errorf("expected consecutive_tidak_terdaftar_count == 0 (reset by terdaftar), got %d", resp.Engine.ConsecutiveTidakTerdaftarCount)
	}
}

func TestAdminMaintenance_WaCheckStatusHealthCheck(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	ctx := context.Background()

	var adminID int64
	err := pool.QueryRow(ctx,
		"INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'hash', 'admin', $2, 'Admin H13 Maint') RETURNING id",
		fmt.Sprintf("admin_h13_maint_%d@test.com", time.Now().UnixNano()),
		fmt.Sprintf("REF_H13_MAINT_%d", time.Now().UnixNano()),
	).Scan(&adminID)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID}, nil)
	})

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, nil)

	s, err := NewServer(database, "test", nil, eng)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	t.Run("Status OK jika tidak_terdaftar berturut-turut < 20", func(t *testing.T) {
		// Simulasikan 10 tidak_terdaftar
		for i := 0; i < 10; i++ {
			eng.Metrics().IncCheckResult(wa.CheckResultTidakTerdaftar)
		}

		checks := s.RunHealthChecks(ctx)
		var waCheck *HealthCheckItem
		for i := range checks {
			if checks[i].Key == "wa_check_status" {
				waCheck = &checks[i]
				break
			}
		}

		if waCheck == nil {
			t.Fatalf("health check 'wa_check_status' tidak ditemukan")
		}
		if waCheck.Status != "ok" {
			t.Errorf("expected status 'ok', got %q (%s)", waCheck.Status, waCheck.Message)
		}
	})

	t.Run("Status Peringatan (kuning) jika 20 tidak_terdaftar berturut-turut", func(t *testing.T) {
		// Tambah 10 lagi sehingga total 20 berturut-turut
		for i := 0; i < 10; i++ {
			eng.Metrics().IncCheckResult(wa.CheckResultTidakTerdaftar)
		}

		checks := s.RunHealthChecks(ctx)
		var waCheck *HealthCheckItem
		for i := range checks {
			if checks[i].Key == "wa_check_status" {
				waCheck = &checks[i]
				break
			}
		}

		if waCheck == nil {
			t.Fatalf("health check 'wa_check_status' tidak ditemukan")
		}
		if waCheck.Status != "peringatan" {
			t.Fatalf("expected status 'peringatan' saat 20 tidak_terdaftar berturut-turut, got %q (%s)", waCheck.Status, waCheck.Message)
		}
		if !strings.Contains(waCheck.Message, "format nomor") || !strings.Contains(waCheck.Message, "kondisi sesi") {
			t.Errorf("pesan peringatan harus menyarankan memeriksa format nomor dan kondisi sesi, got: %s", waCheck.Message)
		}

		// Uji halaman GET /admin/maintenance render sukses
		req := httptest.NewRequest("GET", "/admin/maintenance", nil)
		ctxWithUser := context.WithValue(req.Context(), userContextKey, &UserSession{ID: adminID, Role: "admin"})
		req = req.WithContext(ctxWithUser)
		rr := httptest.NewRecorder()
		s.HandleAdminMaintenanceGET(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200 OK from GET /admin/maintenance, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "wa_check_status") && !strings.Contains(rr.Body.String(), "Pemeriksaan Nomor WhatsApp") {
			t.Errorf("halaman maintenance tidak menampilkan 'Pemeriksaan Nomor WhatsApp'")
		}
	})
}
