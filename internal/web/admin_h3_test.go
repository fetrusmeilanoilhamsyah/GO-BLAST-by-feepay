package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa/watest"
)

func TestAdminAPIMetrics_Handler(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	ctx := context.Background()

	var adminID int64
	err := pool.QueryRow(ctx,
		"INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'hash', 'admin', $2, 'Admin Metrics') RETURNING id",
		fmt.Sprintf("admin_metrics_%d@test.com", time.Now().UnixNano()),
		fmt.Sprintf("REF_METRICS_%d", time.Now().UnixNano()),
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
		DB     map[string]any     `json:"db"`
		Memory map[string]any     `json:"memory"`
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode JSON metrics response: %v", err)
	}

	if resp.DB == nil {
		t.Errorf("expected 'db' stats in metrics response, got nil")
	}
	if resp.Memory == nil {
		t.Errorf("expected 'memory' stats in metrics response, got nil")
	}
	if resp.Engine.ClassCounts == nil {
		t.Errorf("expected 'engine.class_counts' in metrics response, got nil")
	}
	t.Logf("Endpoint /admin/api/metrics sukses menyajikan data metrik")
}

func TestAdminCampaignRequeueUncertain_Handler(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	ctx := context.Background()

	var adminID int64
	err := pool.QueryRow(ctx,
		"INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'hash', 'admin', $2, 'Admin Requeue') RETURNING id",
		fmt.Sprintf("admin_req_%d@test.com", time.Now().UnixNano()),
		fmt.Sprintf("REF_REQ_%d", time.Now().UnixNano()),
	).Scan(&adminID)
	if err != nil {
		t.Fatal(err)
	}

	var workerID int64
	err = pool.QueryRow(ctx,
		"INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'hash', 'worker', $2, 'Worker User') RETURNING id",
		fmt.Sprintf("worker_req_%d@test.com", time.Now().UnixNano()),
		fmt.Sprintf("REF_WORKER_REQ_%d", time.Now().UnixNano()),
	).Scan(&workerID)
	if err != nil {
		t.Fatal(err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign Requeue Test', 'berjalan', 'kartu_link', 'Halo', '{}', $1)
		 RETURNING id`,
		adminID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID, workerID}, []int64{campaignID})
	})

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'req.csv', $2, 3, 3) RETURNING id`,
		campaignID, adminID,
	).Scan(&batchID)
	if err != nil {
		t.Fatal(err)
	}

	// Buat 2 nomor gagal hasil_tidak_pasti dan 1 nomor gagal nomor_tidak_valid
	var n1, n2, n3 int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, last_error, attempts)
		 VALUES ($1, $2, '62899995501', '62', 'gagal', 'hasil_tidak_pasti', 2) RETURNING id`,
		campaignID, batchID,
	).Scan(&n1)
	if err != nil {
		t.Fatal(err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, last_error, attempts)
		 VALUES ($1, $2, '62899995502', '62', 'gagal', 'hasil_tidak_pasti', 1) RETURNING id`,
		campaignID, batchID,
	).Scan(&n2)
	if err != nil {
		t.Fatal(err)
	}

	err = pool.QueryRow(ctx,
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, last_error, attempts)
		 VALUES ($1, $2, '62899995503', '62', 'gagal', 'nomor_tidak_valid', 3) RETURNING id`,
		campaignID, batchID,
	).Scan(&n3)
	if err != nil {
		t.Fatal(err)
	}

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, nil)

	s, err := NewServer(database, "test", nil, eng)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// 1. Uji akses oleh non-admin (worker): harus ditolak
	reqWorker := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/requeue-uncertain", campaignID), nil)
	reqWorker.SetPathValue("id", strconv.FormatInt(campaignID, 10))
	ctxWorker := context.WithValue(reqWorker.Context(), userContextKey, &UserSession{ID: workerID, Role: "worker"})
	reqWorker = reqWorker.WithContext(ctxWorker)
	rrWorker := httptest.NewRecorder()
	s.HandleAdminCampaignRequeueUncertain(rrWorker, reqWorker)

	if rrWorker.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for worker role, got %d", rrWorker.Code)
	}

	// 2. Uji akses oleh admin: berhasil mengubah status 2 nomor tidak pasti
	reqAdmin := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/requeue-uncertain", campaignID), nil)
	reqAdmin.SetPathValue("id", strconv.FormatInt(campaignID, 10))
	ctxAdmin := context.WithValue(reqAdmin.Context(), userContextKey, &UserSession{ID: adminID, Role: "admin"})
	reqAdmin = reqAdmin.WithContext(ctxAdmin)
	rrAdmin := httptest.NewRecorder()
	s.HandleAdminCampaignRequeueUncertain(rrAdmin, reqAdmin)

	if rrAdmin.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect StatusSeeOther (303), got %d: %s", rrAdmin.Code, rrAdmin.Body.String())
	}

	// Verifikasi nomor n1 & n2 kembali 'tersedia' dengan attempts = 0 dan last_error = NULL
	for _, id := range []int64{n1, n2} {
		var st string
		var attempts int
		var lastErr *string
		err = pool.QueryRow(ctx, `SELECT status, attempts, last_error FROM campaign_numbers WHERE id = $1`, id).Scan(&st, &attempts, &lastErr)
		if err != nil {
			t.Fatalf("query status gagal: %v", err)
		}
		if st != "tersedia" {
			t.Errorf("number %d: expected status 'tersedia', got %q", id, st)
		}
		if attempts != 0 {
			t.Errorf("number %d: expected attempts reset to 0, got %d", id, attempts)
		}
		if lastErr != nil {
			t.Errorf("number %d: expected last_error NULL, got %q", id, *lastErr)
		}
	}

	// Verifikasi nomor n3 (nomor_tidak_valid) TIDAK berubah
	var st3 string
	var lastErr3 string
	err = pool.QueryRow(ctx, `SELECT status, COALESCE(last_error, '') FROM campaign_numbers WHERE id = $1`, n3).Scan(&st3, &lastErr3)
	if err != nil {
		t.Fatalf("query n3 gagal: %v", err)
	}
	if st3 != "gagal" || lastErr3 != "nomor_tidak_valid" {
		t.Errorf("number n3 seharusnya tidak tersentuh: status=%s, last_error=%s", st3, lastErr3)
	}

	// Verifikasi audit log tertulis
	var auditCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs 
		WHERE actor_id = $1 AND action = 'campaign_requeue_uncertain' AND entity_id = $2
	`, adminID, strconv.FormatInt(campaignID, 10)).Scan(&auditCount)
	if err != nil {
		t.Fatalf("query audit_logs gagal: %v", err)
	}
	if auditCount < 1 {
		t.Errorf("audit log untuk campaign_requeue_uncertain tidak ditemukan")
	}
}
