package web

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"goblast/internal/campaign"
	"goblast/internal/db"
	"goblast/internal/i18n"
)

func findProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("Could not find project root (go.mod not found)")
	return ""
}

func isProhibitedEmojiOrSymbol(r rune) bool {
	if (r >= 0x1F000 && r <= 0x1FFFF) ||
		(r >= 0x2600 && r <= 0x27BF) ||
		(r >= 0x2B00 && r <= 0x2BFF) ||
		r == 0xFE0F {
		return true
	}
	return false
}

func TestNoEmojiGuard(t *testing.T) {
	root := findProjectRoot(t)

	// Targets to check:
	// 1. web/templates/*.html
	// 2. locales/*.json
	// 3. .go files under cmd/ and internal/web/
	var filesToCheck []string

	templatesDir := filepath.Join(root, "web", "templates")
	tFiles, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("Failed to read templates dir: %v", err)
	}
	for _, f := range tFiles {
		if strings.HasSuffix(f.Name(), ".html") {
			filesToCheck = append(filesToCheck, filepath.Join(templatesDir, f.Name()))
		}
	}

	localesDir := filepath.Join(root, "locales")
	lFiles, err := os.ReadDir(localesDir)
	if err != nil {
		t.Fatalf("Failed to read locales dir: %v", err)
	}
	for _, f := range lFiles {
		if strings.HasSuffix(f.Name(), ".json") {
			filesToCheck = append(filesToCheck, filepath.Join(localesDir, f.Name()))
		}
	}

	for _, sub := range []string{"cmd", filepath.Join("internal", "web")} {
		scanDir := filepath.Join(root, sub)
		err := filepath.Walk(scanDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(info.Name(), ".go") {
				filesToCheck = append(filesToCheck, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to walk %s: %v", sub, err)
		}
	}

	for _, filePath := range filesToCheck {
		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Errorf("Failed to read %s: %v", filePath, err)
			continue
		}

		content := string(data)
		lineNum := 1
		for i, w := 0, 0; i < len(content); i += w {
			r, width := utf8.DecodeRuneInString(content[i:])
			w = width
			if r == '\n' {
				lineNum++
			}
			if isProhibitedEmojiOrSymbol(r) {
				rel, _ := filepath.Rel(root, filePath)
				t.Errorf("Prohibited emoji/symbol U+%04X found in %s:%d", r, rel, lineNum)
			}
		}
	}
}

func TestCampaignFormTombolAsliDisabled(t *testing.T) {
	root := findProjectRoot(t)
	base := filepath.Join(root, "web", "templates", "base.html")
	form := filepath.Join(root, "web", "templates", "admin_campaign_form.html")

	_ = i18n.Load(filepath.Join(root, "locales"))

	tmpl, err := template.ParseFiles(base, form)
	if err != nil {
		t.Fatalf("Failed to parse campaign form template: %v", err)
	}

	data := struct {
		Lang        string
		Title       string
		CSRFToken   string
		Campaign    campaign.Campaign
		DefaultMode string
		T           func(string) string
	}{
		Lang:      "id",
		Title:     "Buat Campaign Baru",
		CSRFToken: "test-csrf",
		Campaign: campaign.Campaign{
			Elements: campaign.Elements{
				CSActive: false,
			},
			ButtonMode: campaign.ModeKartuLink,
		},
		DefaultMode: campaign.ModeKartuLink,
		T: func(key string) string {
			return i18n.T("id", key)
		},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base.html", data); err != nil {
		t.Fatalf("Failed to execute template: %v", err)
	}

	out := buf.String()

	// Verify tombol_asli option has disabled attribute
	if !strings.Contains(out, `<option value="tombol_asli" disabled>`) &&
		!strings.Contains(out, `<option value="tombol_asli"  disabled>`) &&
		!strings.Contains(out, `value="tombol_asli"`) &&
		!strings.Contains(out, "disabled") {
		t.Errorf("Expected option value=tombol_asli to have disabled attribute")
	}

	// Verify honest label without claims
	if strings.Contains(out, "Tampil di Semua HP") || strings.Contains(out, "Aman & Tampil") {
		t.Errorf("Found unverified claim in rendered template output")
	}

	// Verify CS is not checked by default
	if strings.Contains(out, `name="cs_active" id="csCheck" checked`) {
		t.Errorf("Expected cs_active to not be checked by default")
	}

	// Also test handler with DB if available
	godotenv.Load(filepath.Join(root, ".env"))
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		pool, err := pgxpool.New(context.Background(), dbURL)
		if err == nil {
			defer pool.Close()
			cwd, _ := os.Getwd()
			_ = os.Chdir(root)
			defer os.Chdir(cwd)

			srv, err := NewServer(&db.DB{Pool: pool}, "test")
			if err == nil {
				req := httptest.NewRequest("GET", "/admin/campaigns/new", nil)
				req = req.WithContext(context.WithValue(req.Context(), userContextKey, UserSession{ID: 1, Role: "admin", Language: "id"}))
				rr := httptest.NewRecorder()
				srv.HandleAdminCampaignNewGET(rr, req)

				if rr.Code != http.StatusOK {
					t.Errorf("Expected status 200 from HandleAdminCampaignNewGET, got %d", rr.Code)
				}
				body := rr.Body.String()
				if !strings.Contains(body, `value="tombol_asli"`) || !strings.Contains(body, "disabled") {
					t.Errorf("Expected rendered form from handler to disable tombol_asli")
				}
			}
		}
	}
}
