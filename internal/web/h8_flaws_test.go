package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"goblast/internal/auth"

	"github.com/jackc/pgx/v5/pgxpool"
)

func createAdminReqWithStepUp(t *testing.T, srv *Server, pool *pgxpool.Pool, adminID int64, method, path string, body url.Values) *http.Request {
	t.Helper()
	rawToken, hashToken, _ := auth.GenerateSessionToken()
	now := time.Now()
	_, _ = pool.Exec(context.Background(), `
		UPDATE users SET totp_enabled_at = $1 WHERE id = $2
	`, now, adminID)
	_, _ = pool.Exec(context.Background(), `
		INSERT INTO web_sessions (user_id, token_hash, expires_at, stepup_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, '127.0.0.1', 'test-agent')
	`, adminID, hashToken, now.Add(1*time.Hour), now)

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
		ID:       adminID,
		Role:     "admin",
		Language: "id",
	})
	ctx = context.WithValue(ctx, sessionKey, hashToken)
	return req.WithContext(ctx)
}

// TestH8_Flaw_PurgeEndpoint404 membuktikan bahwa endpoint POST /admin/campaigns/{id}/purge
// saat ini belum ada dan mengembalikan status 404 (harus gagal sebelum H8 dikerjakan).
func TestH8_Flaw_PurgeEndpoint404(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	ctx := context.Background()

	var cID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Campaign to Purge', 'selesai', 'kartu_link', 'Halo', '{}', $1)
		RETURNING id
	`, adminID).Scan(&cID)
	if err != nil {
		t.Fatalf("Failed to create campaign: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE campaign_id = $1", cID)
		_, _ = pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cID)
	})

	form := url.Values{}
	form.Set("csrf_token", "test-csrf")
	form.Set("confirm_name", "Campaign to Purge")

	req := createAdminReqWithStepUp(t, srv, pool, adminID, "POST", fmt.Sprintf("/admin/campaigns/%d/purge", cID), form)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Harapannya: endpoint purge harus memproses dan mengisi purged_at pada campaign
	var purgedAt *time.Time
	err = pool.QueryRow(ctx, "SELECT purged_at FROM campaigns WHERE id = $1", cID).Scan(&purgedAt)
	if err != nil || purgedAt == nil {
		t.Fatalf("BUKTI TEMUAN H8: Endpoint POST /admin/campaigns/%d/purge belum diimplementasikan (purged_at tetap nil, respons: %d, location: %s)!", cID, rec.Code, rec.Header().Get("Location"))
	}
}

// TestH8_Flaw_StoragePage404 membuktikan bahwa halaman GET /admin/storage saat ini belum ada.
func TestH8_Flaw_StoragePage404(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)

	req := createAdminReqWithStepUp(t, srv, pool, adminID, "GET", "/admin/storage", nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Harapannya: halaman /admin/storage harus merender halaman penyimpanan data (200 OK dengan konten 'Penyimpanan')
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Penyimpanan") {
		t.Fatalf("BUKTI TEMUAN H8: Halaman GET /admin/storage belum diimplementasikan (respons: %d, redirect ke: %s)!", rec.Code, rec.Header().Get("Location"))
	}
}

// TestH8_Flaw_MaintenanceArchivedReminder membuktikan bahwa campaign terarsip > 90 hari
// yang belum dibersihkan (purged_at IS NULL) saat ini belum memiliki pengingat di RunHealthChecks.
func TestH8_Flaw_MaintenanceArchivedReminder(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)
	ctx := context.Background()

	// Buat campaign terarsip 100 hari lalu dan belum dibersihkan
	var cID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, archived_at)
		VALUES ('Old Archived Camp', 'selesai', 'kartu_link', 'Halo', '{}', $1, NOW() - INTERVAL '100 days')
		RETURNING id
	`, adminID).Scan(&cID)
	if err != nil {
		t.Fatalf("Failed to create old archived campaign: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cID)
	})

	checks := srv.RunHealthChecks(ctx)
	foundReminder := false
	for _, check := range checks {
		if check.Key == "archived_campaigns_retention" || strings.Contains(strings.ToLower(check.Name), "arsip") || strings.Contains(strings.ToLower(check.Message), "terarsip") {
			foundReminder = true
			if check.Status != "peringatan" {
				t.Errorf("Ekspektasi status peringatan untuk arsip > 90 hari, dapat %s", check.Status)
			}
			break
		}
	}

	if !foundReminder {
		t.Fatalf("BUKTI TEMUAN H8: RunHealthChecks belum memiliki pemeriksaan pengingat untuk campaign terarsip > 90 hari yang belum dibersihkan!")
	}
}
