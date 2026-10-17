package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goblast/internal/db"
	"goblast/internal/testutil"
)

// TestH9_Flaw_BackupEndpointsMissing membuktikan bahwa rute GET /admin/backups
// dan aksi backup saat ini belum ada (mengembalikan 404 Not Found).
func TestH9_Flaw_BackupEndpointsMissing(t *testing.T) {
	srv, pool, adminID, _ := setupAdminTest(t)

	req := createAdminReqWithStepUp(t, srv, pool, adminID, "GET", "/admin/backups", nil)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Cadangan") {
		t.Fatalf("BUKTI TEMUAN P6 (H9): Halaman GET /admin/backups belum diimplementasikan (respons: %d, location: %s)!", rec.Code, rec.Header().Get("Location"))
	}
}

// TestH9_Flaw_MaintenanceBackupHealthCheckMissing membuktikan bahwa fitur RunHealthChecks
// di /admin/maintenance saat ini belum memantau status backup (waktu backup terakhir dan verifikasi).
func TestH9_Flaw_MaintenanceBackupHealthCheckMissing(t *testing.T) {
	srv, _, _, _ := setupAdminTest(t)
	ctx := context.Background()

	results := srv.RunHealthChecks(ctx)
	hasBackupCheck := false
	for _, res := range results {
		if res.Name == "backup_status" || strings.Contains(strings.ToLower(res.Name), "backup") {
			hasBackupCheck = true
			break
		}
	}

	if !hasBackupCheck {
		t.Fatalf("BUKTI TEMUAN P6 (H9): Health check backup belum diimplementasikan di RunHealthChecks!")
	}
}

// TestH9_Flaw_ApplicationNameInDBMissing membuktikan bahwa pool koneksi db.Connect
// menyetel application_name=goblast pada runtime params koneksi.
func TestH9_Flaw_ApplicationNameInDBMissing(t *testing.T) {
	testDSN := testutil.TestDBURL(t)
	database, err := db.Connect(context.Background(), testDSN)
	if err != nil {
		t.Fatalf("Gagal db.Connect: %v", err)
	}
	defer database.Pool.Close()

	var appName string
	err = database.Pool.QueryRow(context.Background(), "SELECT current_setting('application_name')").Scan(&appName)
	if err != nil {
		t.Fatalf("Gagal membaca current_setting(application_name): %v", err)
	}

	if appName != "goblast" {
		t.Fatalf("BUKTI TEMUAN H9: application_name pada koneksi database belum disetel ke 'goblast', saat ini: %q", appName)
	}
}
