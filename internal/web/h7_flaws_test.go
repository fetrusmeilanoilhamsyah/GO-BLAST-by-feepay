package web

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"goblast/internal/auth"
	"goblast/internal/testutil"
)

func createH7TestAdmin(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	nonce := time.Now().UnixNano()
	var adminID int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO users (username, email, password_hash, role, referral_code, display_name, status, totp_enabled_at)
		VALUES ($1, $2, 'hash', 'admin', $3, 'Admin H7', 'active', CURRENT_TIMESTAMP)
		RETURNING id
	`, fmt.Sprintf("%s_%d", name, nonce), fmt.Sprintf("%s_%d@test.com", name, nonce), fmt.Sprintf("REF_%s_%d", name, nonce)).Scan(&adminID)
	if err != nil {
		t.Fatalf("create test admin failed: %v", err)
	}
	return adminID
}

// TestFinding_P1_WebPreviewImageUploadIgnored membuktikan bahwa handler pratinjau
// POST /admin/campaigns/preview saat ini mengabaikan file image dari multipart
// dan respons HTML tidak memuat tag <img> atau data URI gambar.
func TestFinding_P1_WebPreviewImageUploadIgnored(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	setupWebH5aTest(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// Buat gambar PNG uji di memori
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, img); err != nil {
		t.Fatalf("encode png failed: %v", err)
	}

	adminID := createH7TestAdmin(t, pool, "prevadm")
	rawToken, tokenHash, _ := auth.GenerateSessionToken()
	_, err = pool.Exec(context.Background(), `
		INSERT INTO web_sessions (user_id, token_hash, ip, user_agent, expires_at, stepup_at)
		VALUES ($1, $2, '127.0.0.1', 'test-ua', $3, CURRENT_TIMESTAMP)
	`, adminID, tokenHash, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("insert session failed: %v", err)
	}
	defer func() {
		testutil.CleanupTestData(pool, []int64{adminID}, nil)
	}()

	csrfToken := auth.SignCSRFToken(rawToken, srv.SessionSecret)

	bodyBuf := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuf)
	_ = writer.WriteField("csrf_token", csrfToken)
	_ = writer.WriteField("name", "Uji Pratinjau")
	_ = writer.WriteField("button_mode", "kartu_link")
	_ = writer.WriteField("body", "Halo ini pratinjau dengan gambar")

	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="image"; filename="test.png"`)
	partHeader.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("create part failed: %v", err)
	}
	_, _ = part.Write(imgBuf.Bytes())
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/admin/campaigns/preview", bodyBuf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: srv.sessionCookieName(), Value: rawToken})
	req.AddCookie(&http.Cookie{Name: srv.csrfCookieName(), Value: csrfToken})
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	respBody := w.Body.String()
	if !strings.Contains(respBody, "<img") || !strings.Contains(respBody, "data:image/") {
		t.Errorf("BUG P1 TERBUKTI: Pratinjau tidak memuat tag <img> atau data URI saat file gambar diunggah! Respons:\n%s", respBody)
	}
}

// TestFinding_P4a_CampaignPausedDisplay membuktikan bahwa campaign draf yang memiliki
// started_at terisi (dijeda) saat ini masih ditampilkan sebagai 'draf' bukan 'Dijeda'.
func TestFinding_P4a_CampaignPausedDisplay(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	setupWebH5aTest(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	adminID := createH7TestAdmin(t, pool, "pauseadm")
	defer func() {
		testutil.CleanupTestData(pool, []int64{adminID}, nil)
	}()

	now := time.Now()
	var campaignID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, started_at)
		VALUES ('Campaign Uji Jeda', 'draf', 'kartu_link', 'Isi', '{}', $1, $2)
		RETURNING id
	`, adminID, now).Scan(&campaignID)
	if err != nil {
		t.Fatalf("insert campaign failed: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", campaignID)
	}()

	rawToken, tokenHash, _ := auth.GenerateSessionToken()
	_, err = pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, ip, user_agent, expires_at, stepup_at)
		VALUES ($1, $2, '127.0.0.1', 'test-ua', $3, CURRENT_TIMESTAMP)
	`, adminID, tokenHash, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("insert session failed: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/admin/campaigns", nil)
	req.AddCookie(&http.Cookie{Name: srv.sessionCookieName(), Value: rawToken})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	respList := w.Body.String()
	if !strings.Contains(respList, "Dijeda") {
		t.Errorf("BUG P4a TERBUKTI: Campaign dijeda belum ditampilkan sebagai 'Dijeda' di /admin/campaigns!")
	}

	reqDetail := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/campaigns/%d", campaignID), nil)
	reqDetail.AddCookie(&http.Cookie{Name: srv.sessionCookieName(), Value: rawToken})
	wDetail := httptest.NewRecorder()
	mux.ServeHTTP(wDetail, reqDetail)

	respDetail := wDetail.Body.String()
	if !strings.Contains(respDetail, "Dijeda") {
		t.Errorf("BUG P4a TERBUKTI: Campaign dijeda belum ditampilkan sebagai 'Dijeda' di /admin/campaigns/%d!", campaignID)
	}
}

// TestFinding_P4b_ExportNumbersNotFound membuktikan bahwa aksi ekspor sisa nomor
// GET /admin/campaigns/{id}/numbers/export saat ini belum ada (mengembalikan 404).
func TestFinding_P4b_ExportNumbersNotFound(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	setupWebH5aTest(t)
	pool := testutil.TestDB(t)
	database := testutil.TestDBInstance(t)
	ctx := context.Background()

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	adminID := createH7TestAdmin(t, pool, "expadm")
	defer func() {
		testutil.CleanupTestData(pool, []int64{adminID}, nil)
	}()

	var campaignID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Campaign Export Test', 'draf', 'kartu_link', 'Isi', '{}', $1)
		RETURNING id
	`, adminID).Scan(&campaignID)
	if err != nil {
		t.Fatalf("insert campaign failed: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", campaignID)
	}()

	rawToken, tokenHash, _ := auth.GenerateSessionToken()
	_, err = pool.Exec(ctx, `
		INSERT INTO web_sessions (user_id, token_hash, ip, user_agent, expires_at, stepup_at)
		VALUES ($1, $2, '127.0.0.1', 'test-ua', $3, $4)
	`, adminID, tokenHash, time.Now().Add(1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("insert session failed: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/campaigns/%d/numbers/export", campaignID), nil)
	req.AddCookie(&http.Cookie{Name: srv.sessionCookieName(), Value: rawToken})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("BUG P4b TERBUKTI: Endpoint GET /admin/campaigns/{id}/numbers/export belum diimplementasikan! Status: %d, Body: %s", w.Code, w.Body.String())
	}
}
