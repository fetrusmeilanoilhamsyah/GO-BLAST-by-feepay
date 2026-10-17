package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goblast/internal/testutil"
)

func TestAdminStorage_GET(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	ctx := context.Background()

	// Masukkan campaign terarsip dan selesai
	var camp1ID, camp2ID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, archived_at)
		VALUES ('Storage Archived Camp', 'selesai', 'kartu_link', 'Pesan', '{}', $1, NOW() - INTERVAL '10 days')
		RETURNING id
	`, adminID).Scan(&camp1ID)

	_ = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, finished_at)
		VALUES ('Storage Finished Camp', 'selesai', 'kartu_link', 'Pesan', '{}', $1, NOW() - INTERVAL '2 days')
		RETURNING id
	`, adminID).Scan(&camp2ID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID}, []int64{camp1ID, camp2ID})
	})

	req := createAdminReqWithStepUp(t, srv, pool, adminID, "GET", "/admin/storage", nil)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Ekspektasi HTTP 200 untuk GET /admin/storage, dapat %d (Location: %s)", rec.Code, rec.Header().Get("Location"))
	}

	body := rec.Body.String()
	expectedContents := []string{
		"Penyimpanan &amp; Retensi Data",
		"campaign_numbers",
		"ledger_entries",
		"audit_logs",
		"web_sessions",
		"uploads/",
		"data/tmp/",
		"backups/",
		"Storage Archived Camp",
		"Storage Finished Camp",
	}

	for _, expected := range expectedContents {
		if !strings.Contains(body, expected) {
			t.Errorf("Konten yang diharapkan tidak ditemukan di halaman /admin/storage: %s", expected)
		}
	}
}

func TestAdminStorage_CleanTmp(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)

	// Buat folder data/tmp jika belum ada
	_ = os.MkdirAll("data/tmp", 0755)

	// File kedaluwarsa (> 2 jam lalu)
	expiredFile := filepath.Join("data/tmp", fmt.Sprintf("test_old_%d_meta.json", time.Now().UnixNano()))
	if err := os.WriteFile(expiredFile, []byte("{}"), 0644); err != nil {
		t.Fatalf("Failed to write old tmp file: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(expiredFile, oldTime, oldTime)

	// File segar (< 5 menit lalu)
	freshFile := filepath.Join("data/tmp", fmt.Sprintf("test_fresh_%d_meta.json", time.Now().UnixNano()))
	if err := os.WriteFile(freshFile, []byte("{}"), 0644); err != nil {
		t.Fatalf("Failed to write fresh tmp file: %v", err)
	}

	defer func() {
		_ = os.Remove(expiredFile)
		_ = os.Remove(freshFile)
	}()

	form := url.Values{}
	form.Set("csrf_token", "test-csrf")

	req := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", "/admin/storage/clean-tmp", form)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("Ekspektasi HTTP 303 SeeOther, dapat %d", rec.Code)
	}

	// File kedaluwarsa harus terhapus
	if _, err := os.Stat(expiredFile); !os.IsNotExist(err) {
		t.Errorf("File kedaluwarsa tidak terhapus: %s", expiredFile)
	}

	// File segar harus tetap ada
	if _, err := os.Stat(freshFile); os.IsNotExist(err) {
		t.Errorf("File segar tidak sengaja terhapus: %s", freshFile)
	}

	// Cek audit log
	var auditCount int
	_ = pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM audit_logs WHERE actor_id = $1 AND action = 'storage_clean_tmp'", adminID).Scan(&auditCount)
	if auditCount == 0 {
		t.Errorf("Audit log storage_clean_tmp tidak tercatat")
	}
}

func TestAdminStorage_CleanOrphans(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	ctx := context.Background()

	_ = os.MkdirAll("uploads", 0755)

	// 1. File terpakai oleh campaign aktif
	usedFileName := fmt.Sprintf("test_used_%d.jpg", time.Now().UnixNano())
	usedFilePath := filepath.Join("uploads", usedFileName)
	_ = os.WriteFile(usedFilePath, []byte("active image"), 0644)

	var campID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, image_path, elements, created_by)
		VALUES ('Active Image Camp', 'berjalan', 'kartu_link', 'Halo', $1, '{}', $2)
		RETURNING id
	`, filepath.ToSlash(usedFilePath), adminID).Scan(&campID)

	// 2. File yatim (tidak dirujuk campaign mana pun)
	orphanFileName := fmt.Sprintf("test_orphan_%d.jpg", time.Now().UnixNano())
	orphanFilePath := filepath.Join("uploads", orphanFileName)
	_ = os.WriteFile(orphanFilePath, []byte("orphan image"), 0644)

	defer func() {
		_ = os.Remove(usedFilePath)
		_ = os.Remove(orphanFilePath)
		testutil.CleanupTestData(pool, []int64{adminID}, []int64{campID})
	}()

	form := url.Values{}
	form.Set("csrf_token", "test-csrf")

	req := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", "/admin/storage/clean-orphans", form)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("Ekspektasi HTTP 303 SeeOther, dapat %d", rec.Code)
	}

	// File yatim harus terhapus
	if _, err := os.Stat(orphanFilePath); !os.IsNotExist(err) {
		t.Errorf("File yatim tidak terhapus: %s", orphanFilePath)
	}

	// File aktif harus tetap ada
	if _, err := os.Stat(usedFilePath); os.IsNotExist(err) {
		t.Errorf("File aktif tidak sengaja terhapus: %s", usedFilePath)
	}

	// Cek audit log
	var auditCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs WHERE actor_id = $1 AND action = 'storage_clean_orphans'", adminID).Scan(&auditCount)
	if auditCount == 0 {
		t.Errorf("Audit log storage_clean_orphans tidak tercatat")
	}
}

func TestAdminStorage_CleanRetention(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	ctx := context.Background()

	// 1. Sesi kedaluwarsa & sesi segar
	_, _ = pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, 'hash_expired_ret', NOW() - INTERVAL '2 hours', '127.0.0.1', 'agent')
	`, adminID)

	_, _ = pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, 'hash_fresh_ret', NOW() + INTERVAL '2 hours', '127.0.0.1', 'agent')
	`, adminID)

	// 2. Audit log login_failed lama (> 35 hari) & segar (< 2 hari)
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, created_at)
		VALUES ($1, 'login_failed', 'users', 'testuser', NOW() - INTERVAL '40 days')
	`, adminID); err != nil {
		t.Fatalf("Failed to insert old audit log: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, created_at)
		VALUES ($1, 'login_failed', 'users', 'testuser', NOW() - INTERVAL '2 days')
	`, adminID); err != nil {
		t.Fatalf("Failed to insert recent audit log: %v", err)
	}

	// 3. Notifikasi terbaca lama (> 95 hari), terbaca segar (< 10 hari), & belum dibaca
	if _, err := pool.Exec(ctx, `
		INSERT INTO notifications (user_id, title, message, kind, read_at, created_at)
		VALUES ($1, 'Old Read Notif', 'Pesan lama', 'system', NOW() - INTERVAL '100 days', NOW() - INTERVAL '100 days')
	`, adminID); err != nil {
		t.Fatalf("Failed to insert old notif: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO notifications (user_id, title, message, kind, read_at, created_at)
		VALUES ($1, 'Recent Read Notif', 'Pesan baru', 'system', NOW() - INTERVAL '5 days', NOW() - INTERVAL '5 days')
	`, adminID); err != nil {
		t.Fatalf("Failed to insert recent notif: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO notifications (user_id, title, message, kind, read_at, created_at)
		VALUES ($1, 'Unread Notif', 'Pesan unread', 'system', NULL, NOW() - INTERVAL '120 days')
	`, adminID); err != nil {
		t.Fatalf("Failed to insert unread notif: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM web_sessions WHERE user_id = $1", adminID)
		_, _ = pool.Exec(ctx, "DELETE FROM notifications WHERE user_id = $1", adminID)
		_, _ = pool.Exec(ctx, "DELETE FROM audit_logs WHERE actor_id = $1", adminID)
	})

	form := url.Values{}
	form.Set("csrf_token", "test-csrf")

	req := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", "/admin/storage/clean-retention", form)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("Ekspektasi HTTP 303 SeeOther, dapat %d", rec.Code)
	}

	// Sesi kedaluwarsa harus terhapus, sesi segar harus ada
	var expiredSessionCount, freshSessionCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM web_sessions WHERE token_hash = 'hash_expired_ret'").Scan(&expiredSessionCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM web_sessions WHERE token_hash = 'hash_fresh_ret'").Scan(&freshSessionCount)
	if expiredSessionCount != 0 {
		t.Errorf("Sesi kedaluwarsa tidak terhapus")
	}
	if freshSessionCount != 1 {
		t.Errorf("Sesi segar tidak sengaja terhapus")
	}

	// Audit lama harus terhapus, audit segar harus ada
	var oldAuditCount, recentAuditCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs WHERE actor_id = $1 AND action = 'login_failed' AND created_at < NOW() - INTERVAL '30 days'", adminID).Scan(&oldAuditCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs WHERE actor_id = $1 AND action = 'login_failed' AND created_at > NOW() - INTERVAL '10 days'", adminID).Scan(&recentAuditCount)
	if oldAuditCount != 0 {
		t.Errorf("Audit log gagal lama tidak terhapus")
	}
	if recentAuditCount != 1 {
		t.Errorf("Audit log gagal segar tidak sengaja terhapus: got %d", recentAuditCount)
	}

	// Notifikasi terbaca lama harus terhapus, notifikasi segar dan belum dibaca harus ada
	var oldReadNotifCount, recentReadNotifCount, unreadNotifCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND title = 'Old Read Notif'", adminID).Scan(&oldReadNotifCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND title = 'Recent Read Notif'", adminID).Scan(&recentReadNotifCount)
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND title = 'Unread Notif'", adminID).Scan(&unreadNotifCount)
	if oldReadNotifCount != 0 {
		t.Errorf("Notifikasi terbaca lama tidak terhapus")
	}
	if recentReadNotifCount != 1 {
		t.Errorf("Notifikasi terbaca baru tidak sengaja terhapus: got %d", recentReadNotifCount)
	}
	if unreadNotifCount != 1 {
		t.Errorf("Notifikasi belum dibaca tidak sengaja terhapus: got %d", unreadNotifCount)
	}
}

func TestAdminStorage_Vacuum(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	ctx := context.Background()

	form := url.Values{}
	form.Set("csrf_token", "test-csrf")

	req := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", "/admin/storage/vacuum", form)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("Ekspektasi HTTP 303 SeeOther, dapat %d", rec.Code)
	}

	// Cek audit log
	var auditCount int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs WHERE actor_id = $1 AND action = 'storage_vacuum'", adminID).Scan(&auditCount)
	if auditCount == 0 {
		t.Errorf("Audit log storage_vacuum tidak tercatat")
	}
}

func TestAdminStorage_RateLimiting(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)

	form := url.Values{}
	form.Set("csrf_token", "test-csrf")

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Lakukan 5 kali pembersihan (batas maksimal)
	for i := 0; i < 5; i++ {
		req := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", "/admin/storage/clean-tmp", form)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("Percobaan pembersihan %d gagal: code %d", i+1, rec.Code)
		}
	}

	// Percobaan ke-6 harus ditolak oleh rate limiter (Retry-After header terpasang atau pesan err)
	req := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", "/admin/storage/clean-tmp", form)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "err=") {
		t.Errorf("Percobaan ke-6 harus ditolak dengan pesan error di URL, location: %s", loc)
	}
}
