package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"goblast/internal/auth"
	"goblast/internal/backup"
	"goblast/internal/testutil"
)

func createAdminReqWithoutStepUp(t *testing.T, srv *Server, pool *pgxpool.Pool, adminID int64, method, path string, body url.Values) *http.Request {
	t.Helper()
	rawToken, hashToken, _ := auth.GenerateSessionToken()
	now := time.Now()
	expiredStepUp := now.Add(-10 * time.Minute)
	_, _ = pool.Exec(context.Background(), `
		UPDATE users SET totp_enabled_at = $1 WHERE id = $2
	`, now, adminID)
	_, _ = pool.Exec(context.Background(), `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, stepup_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, '127.0.0.1', 'test-agent')
	`, adminID, hashToken, now.Add(1*time.Hour), expiredStepUp)

	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}

	req.AddCookie(&http.Cookie{
		Name:  srv.sessionCookieName(),
		Value: rawToken,
	})
	req.AddCookie(&http.Cookie{
		Name:  srv.csrfCookieName(),
		Value: "test-csrf",
	})
	req.Header.Set("X-CSRF-Token", "test-csrf")

	ctx := context.WithValue(req.Context(), userContextKey, &UserSession{
		ID:            adminID,
		Role:          "admin",
		Language:      "id",
		TOTPEnabledAt: &now,
		StepUpAt:      &expiredStepUp,
	})
	ctx = context.WithValue(ctx, sessionKey, hashToken)
	return req.WithContext(ctx)
}

func createWorkerTestReq(t *testing.T, srv *Server, pool *pgxpool.Pool, workerID int64, method, path string) *http.Request {
	t.Helper()
	rawToken, hashToken, _ := auth.GenerateSessionToken()
	now := time.Now()
	_, _ = pool.Exec(context.Background(), `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, '127.0.0.1', 'test-agent')
	`, workerID, hashToken, now.Add(1*time.Hour))

	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{
		Name:  srv.sessionCookieName(),
		Value: rawToken,
	})
	ctx := context.WithValue(req.Context(), userContextKey, &UserSession{
		ID:       workerID,
		Role:     "worker",
		Language: "id",
	})
	ctx = context.WithValue(ctx, sessionKey, hashToken)
	return req.WithContext(ctx)
}

func TestAdminBackups_GET(t *testing.T) {
	srv, pool, adminID, workerID := setupAdminTest(t)
	backupDir := t.TempDir()
	key := testutil.TestEncryptionKey()

	srv.BackupMgr = backup.NewManager(pool, backupDir, key, nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// 1. Worker tidak berhak akses -> 403 Forbidden
	reqWorker := createWorkerTestReq(t, srv, pool, workerID, "GET", "/admin/backups")
	recWorker := httptest.NewRecorder()
	mux.ServeHTTP(recWorker, reqWorker)
	if recWorker.Code != http.StatusForbidden {
		t.Fatalf("Worker harus menerima 403 Forbidden, didapat: %d", recWorker.Code)
	}

	// 2. Admin berhak akses
	reqAdmin := createAdminReqWithStepUp(t, srv, pool, adminID, "GET", "/admin/backups", nil)
	recAdmin := httptest.NewRecorder()
	mux.ServeHTTP(recAdmin, reqAdmin)
	if recAdmin.Code != http.StatusOK {
		t.Fatalf("Admin GET /admin/backups harus 200 OK, didapat: %d", recAdmin.Code)
	}
	body := recAdmin.Body.String()
	if !strings.Contains(body, "Cadangan Database") {
		t.Fatalf("Halaman cadangan harus memuat judul 'Cadangan Database'")
	}
}

func TestAdminBackup_CreateAndVerifyPOST(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	backupDir := t.TempDir()
	key := testutil.TestEncryptionKey()

	srv.BackupMgr = backup.NewManager(pool, backupDir, key, nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// 1. Buat cadangan manual
	form := url.Values{}
	form.Set("note", "Uji Cadangan Manual Web")

	reqCreate := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", "/admin/backups/create", form)
	recCreate := httptest.NewRecorder()
	mux.ServeHTTP(recCreate, reqCreate)

	if recCreate.Code != http.StatusSeeOther {
		t.Fatalf("Create backup POST harus redirect 303, didapat: %d", recCreate.Code)
	}

	// Periksa database
	var backupID int64
	var filename string
	var verifyOK *bool
	err := pool.QueryRow(context.Background(), `
		SELECT id, filename, verify_ok 
		FROM backups 
		WHERE note = 'Uji Cadangan Manual Web' 
		ORDER BY id DESC LIMIT 1
	`).Scan(&backupID, &filename, &verifyOK)
	if err != nil {
		t.Fatalf("Gagal menemukan rekaman backup di database: %v", err)
	}

	if verifyOK == nil || !*verifyOK {
		t.Fatalf("Backup harus otomatis diverifikasi saat pembuatan, didapat: %v", verifyOK)
	}

	// Pastikan berkas fisik ada di disk
	fullPath := filepath.Join(backupDir, filename)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Fatalf("Berkas cadangan fisik tidak ditemukan di: %s", fullPath)
	}

	// 2. Uji Verifikasi Manual POST
	verifyPath := fmt.Sprintf("/admin/backups/%d/verify", backupID)
	reqVerify := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", verifyPath, nil)
	recVerify := httptest.NewRecorder()
	mux.ServeHTTP(recVerify, reqVerify)

	if recVerify.Code != http.StatusSeeOther {
		t.Fatalf("Verify backup POST harus redirect 303, didapat: %d", recVerify.Code)
	}
}

func TestAdminBackup_DownloadGET(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	backupDir := t.TempDir()
	key := testutil.TestEncryptionKey()

	srv.BackupMgr = backup.NewManager(pool, backupDir, key, nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Buat 1 backup
	rec, err := srv.BackupMgr.CreateManual(context.Background(), adminID, "Uji Unduh")
	if err != nil {
		t.Fatalf("Gagal membuat backup untuk unduhan: %v", err)
	}

	downloadPath := fmt.Sprintf("/admin/backups/%d/download", rec.ID)

	// 1. Tanpa Step-Up -> diarahkan ke /admin/stepup
	reqNoStepUp := createAdminReqWithoutStepUp(t, srv, pool, adminID, "GET", downloadPath, nil)
	recNoStepUp := httptest.NewRecorder()
	mux.ServeHTTP(recNoStepUp, reqNoStepUp)
	if recNoStepUp.Code != http.StatusSeeOther || !strings.Contains(recNoStepUp.Header().Get("Location"), "/admin/stepup") {
		t.Fatalf("Unduhan tanpa step-up harus diarahkan ke /admin/stepup, didapat %d ke %s", recNoStepUp.Code, recNoStepUp.Header().Get("Location"))
	}

	// 2. Dengan Step-Up -> streaming berkas
	reqWithStepUp := createAdminReqWithStepUp(t, srv, pool, adminID, "GET", downloadPath, nil)
	recWithStepUp := httptest.NewRecorder()
	mux.ServeHTTP(recWithStepUp, reqWithStepUp)

	if recWithStepUp.Code != http.StatusOK {
		t.Fatalf("Unduhan dengan step-up harus 200 OK, didapat: %d", recWithStepUp.Code)
	}

	if recWithStepUp.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("Content-Type harus application/octet-stream, didapat: %s", recWithStepUp.Header().Get("Content-Type"))
	}

	if !strings.Contains(recWithStepUp.Header().Get("Content-Disposition"), "attachment; filename=") {
		t.Fatalf("Content-Disposition harus berupa attachment, didapat: %s", recWithStepUp.Header().Get("Content-Disposition"))
	}

	if recWithStepUp.Body.Len() == 0 {
		t.Fatalf("Badan berkas unduhan tidak boleh kosong")
	}
}

func TestAdminBackup_DeleteAndRetentionPOST(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	backupDir := t.TempDir()
	key := testutil.TestEncryptionKey()

	srv.BackupMgr = backup.NewManager(pool, backupDir, key, nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	ctx := context.Background()

	// Buat backup pertama (terverifikasi)
	b1, err := srv.BackupMgr.CreateManual(ctx, adminID, "Backup Pertama")
	if err != nil {
		t.Fatalf("Gagal b1: %v", err)
	}

	// 1. Mencoba menghapus backup satu-satunya yang terverifikasi -> HARUS DITOLAK
	delPath1 := fmt.Sprintf("/admin/backups/%d/delete", b1.ID)
	reqDel1 := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", delPath1, nil)
	recDel1 := httptest.NewRecorder()
	mux.ServeHTTP(recDel1, reqDel1)

	// Harus redirect dengan pesan error
	if !strings.Contains(recDel1.Header().Get("Location"), "err=") {
		t.Fatalf("Penghapusan cadangan terakhir terverifikasi harus ditolak dengan error di URL")
	}

	// Pastikan b1 masih ada di database
	var count int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM backups WHERE id = $1", b1.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("Backup b1 tidak boleh terhapus dari database!")
	}

	// 2. Buat backup kedua (sekarang b2 jadi yang terbaru terverifikasi)
	b2, err := srv.BackupMgr.CreateManual(ctx, adminID, "Backup Kedua")
	if err != nil {
		t.Fatalf("Gagal b2: %v", err)
	}
	_ = b2

	// Sekarang menghapus b1 harus diizinkan karena b2 adalah cadangan terverifikasi aktif
	reqDel1Again := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", delPath1, nil)
	recDel1Again := httptest.NewRecorder()
	mux.ServeHTTP(recDel1Again, reqDel1Again)

	if !strings.Contains(recDel1Again.Header().Get("Location"), "msg=") {
		t.Fatalf("Penghapusan b1 harus berhasil saat b2 ada, didapat: %s", recDel1Again.Header().Get("Location"))
	}

	// Pastikan b1 sudah terhapus
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM backups WHERE id = $1", b1.ID).Scan(&count)
	if count != 0 {
		t.Fatalf("Backup b1 seharusnya sudah terhapus dari database!")
	}
}

func TestAdminBackup_RestorePOST(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	backupDir := t.TempDir()
	key := testutil.TestEncryptionKey()

	srv.BackupMgr = backup.NewManager(pool, backupDir, key, nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	ctx := context.Background()

	// Buat backup untuk pengujian pemulihan
	b, err := srv.BackupMgr.CreateManual(ctx, adminID, "Backup Untuk Restore")
	if err != nil {
		t.Fatalf("Gagal membuat backup: %v", err)
	}

	restorePath := fmt.Sprintf("/admin/backups/%d/restore", b.ID)

	// 1. Ketika ALLOW_WEB_RESTORE=false (bawaan) -> DITOLAK
	srv.AllowWebRestore = false
	reqDisabled := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", restorePath, nil)
	recDisabled := httptest.NewRecorder()
	mux.ServeHTTP(recDisabled, reqDisabled)

	if !strings.Contains(recDisabled.Header().Get("Location"), "err=") {
		t.Fatalf("Web restore saat dinonaktifkan harus ditolak")
	}

	// 2. Ketika ALLOW_WEB_RESTORE=true, tapi di luar mode pemeliharaan -> DITOLAK
	srv.AllowWebRestore = true
	_, _ = pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ('maintenance_mode', 'false'::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = 'false'::jsonb
	`)
	srv.InvalidateSystemLockCache()

	form := url.Values{}
	form.Set("confirm_word", "RESTORE")
	reqNoMaint := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", restorePath, form)
	recNoMaint := httptest.NewRecorder()
	mux.ServeHTTP(recNoMaint, reqNoMaint)

	if !strings.Contains(recNoMaint.Header().Get("Location"), "err=") {
		t.Fatalf("Web restore di luar mode pemeliharaan harus ditolak")
	}

	// 3. Ketika dalam mode pemeliharaan, tapi kata konfirmasi salah -> DITOLAK
	_, _ = pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ('maintenance_mode', 'true'::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = 'true'::jsonb
	`)
	srv.InvalidateSystemLockCache()

	formWrong := url.Values{}
	formWrong.Set("confirm_word", "SALAH")
	reqWrongWord := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", restorePath, formWrong)
	recWrongWord := httptest.NewRecorder()
	mux.ServeHTTP(recWrongWord, reqWrongWord)

	if !strings.Contains(recWrongWord.Header().Get("Location"), "err=") {
		t.Fatalf("Web restore dengan kata konfirmasi salah harus ditolak")
	}

	// 4. Ketika valid: mode pemeliharaan aktif, confirm_word = 'RESTORE' -> SUKSES
	reqValid := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", restorePath, form)
	recValid := httptest.NewRecorder()
	mux.ServeHTTP(recValid, reqValid)

	if !strings.Contains(recValid.Header().Get("Location"), "msg=") {
		t.Fatalf("Web restore valid harus berhasil, didapat: %s", recValid.Header().Get("Location"))
	}

	// Periksa bahwa berkas fisik cadangan 'sebelum_restore' otomatis dibuat di disk sebelum pemulihan
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("Gagal membaca folder backup: %v", err)
	}
	var foundPreRestoreFile bool
	for _, e := range entries {
		if strings.Contains(e.Name(), "sebelum_restore") {
			foundPreRestoreFile = true
			break
		}
	}
	if !foundPreRestoreFile {
		t.Fatalf("Berkas fisik cadangan sebelum_restore harus dibuat di disk sebelum operasi restore!")
	}
}

func TestAdminAPIMetrics_BackupBlock(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	backupDir := t.TempDir()
	key := testutil.TestEncryptionKey()

	srv.BackupMgr = backup.NewManager(pool, backupDir, key, nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Buat 1 backup
	_, err := srv.BackupMgr.CreateManual(context.Background(), adminID, "Uji Metrik")
	if err != nil {
		t.Fatalf("Gagal buat backup: %v", err)
	}

	req := createAdminReqWithStepUp(t, srv, pool, adminID, "GET", "/admin/api/metrics", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("API metrics harus 200 OK, didapat: %d", rec.Code)
	}

	var data map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("Gagal parse respons JSON metrics: %v", err)
	}

	backupBlock, ok := data["backup"].(map[string]any)
	if !ok {
		t.Fatalf("Respons metrics harus memuat field 'backup' berupa map, didapat: %v", data["backup"])
	}

	if enabled, _ := backupBlock["enabled"].(bool); !enabled {
		t.Fatalf("backup.enabled harus true")
	}

	if totalCount, _ := backupBlock["total_count"].(float64); totalCount < 1 {
		t.Fatalf("backup.total_count harus minimal 1, didapat: %v", totalCount)
	}
}

func TestHealthChecks_BackupHealthItem(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	backupDir := t.TempDir()
	key := testutil.TestEncryptionKey()

	srv.BackupMgr = backup.NewManager(pool, backupDir, key, nil)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, "TRUNCATE TABLE backups")

	// 1. Sebelum ada backup -> Health check berstatus 'gagal'
	results := srv.RunHealthChecks(ctx)
	var backupItem *HealthCheckItem
	for i := range results {
		if results[i].Key == "backup_status" {
			backupItem = &results[i]
			break
		}
	}
	if backupItem == nil {
		t.Fatalf("Health check item backup_status harus ada")
	}
	if backupItem.Status != "gagal" {
		t.Fatalf("Sebelum ada backup, status harus gagal, didapat: %s", backupItem.Status)
	}

	// 2. Buat backup terverifikasi
	_, err := srv.BackupMgr.CreateManual(ctx, adminID, "Uji Health")
	if err != nil {
		t.Fatalf("Gagal buat backup: %v", err)
	}

	// 3. Setelah ada backup terverifikasi segar -> Health check berstatus 'ok'
	resultsAfter := srv.RunHealthChecks(ctx)
	var backupItemAfter *HealthCheckItem
	for i := range resultsAfter {
		if resultsAfter[i].Key == "backup_status" {
			backupItemAfter = &resultsAfter[i]
			break
		}
	}
	if backupItemAfter == nil || backupItemAfter.Status != "ok" {
		t.Fatalf("Setelah ada backup terverifikasi segar, status harus ok, didapat: %+v", backupItemAfter)
	}
}
