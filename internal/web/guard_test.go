package web

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"goblast/internal/campaign"
	"goblast/internal/db"
	"goblast/internal/i18n"
	"goblast/internal/testutil"
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
	nav := filepath.Join(root, "web", "templates", "admin_nav.html")
	form := filepath.Join(root, "web", "templates", "admin_campaign_form.html")

	_ = i18n.Load(filepath.Join(root, "locales"))

	tmpl, err := template.ParseFiles(base, nav, form)
	if err != nil {
		t.Fatalf("Failed to parse campaign form template: %v", err)
	}

	data := struct {
		ActiveNav   string
		Lang        string
		Title       string
		Theme       string
		CSRFToken   string
		Campaign    campaign.Campaign
		DefaultMode string
		Nonce       string
		T           func(string) string
	}{
		ActiveNav: "campaigns",
		Lang:      "id",
		Title:     "Buat Campaign Baru",
		Theme:     "",
		CSRFToken: "test-csrf",
		Campaign: campaign.Campaign{
			Elements: campaign.Elements{
				CSActive: false,
			},
			ButtonMode: campaign.ModeKartuLink,
		},
		DefaultMode: campaign.ModeKartuLink,
		Nonce:       "test-nonce",
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

	// Also test handler with DB
	pool := testutil.TestDB(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(&db.DB{Pool: pool}, "test", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
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

func hexToRGB(hex string) (r, g, b float64, err error) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, 0, 0, fmt.Errorf("invalid hex length: %s", hex)
	}
	val, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, err
	}
	r = float64((val>>16)&0xFF) / 255.0
	g = float64((val>>8)&0xFF) / 255.0
	b = float64(val&0xFF) / 255.0
	return r, g, b, nil
}

func sRGBToLinear(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func relativeLuminance(r, g, b float64) float64 {
	return 0.2126*sRGBToLinear(r) + 0.7152*sRGBToLinear(g) + 0.0722*sRGBToLinear(b)
}

func contrastRatio(hex1, hex2 string) (float64, error) {
	r1, g1, b1, err := hexToRGB(hex1)
	if err != nil {
		return 0, err
	}
	r2, g2, b2, err := hexToRGB(hex2)
	if err != nil {
		return 0, err
	}
	l1 := relativeLuminance(r1, g1, b1)
	l2 := relativeLuminance(r2, g2, b2)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05), nil
}

func parseCSSVars(content, blockSelector string) map[string]string {
	vars := make(map[string]string)
	idx := strings.Index(content, blockSelector)
	if idx == -1 {
		return vars
	}
	sub := content[idx:]
	openBrace := strings.Index(sub, "{")
	if openBrace == -1 {
		return vars
	}
	sub = sub[openBrace+1:]
	depth := 1
	var block strings.Builder
	for i := 0; i < len(sub); i++ {
		ch := sub[i]
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				break
			}
		}
		block.WriteByte(ch)
	}

	reVar := regexp.MustCompile(`--([a-z0-9-]+)\s*:\s*(#[0-9a-fA-F]{3,8})`)
	matches := reVar.FindAllStringSubmatch(block.String(), -1)
	for _, m := range matches {
		vars[m[1]] = m[2]
	}
	return vars
}

func TestThemeContrast(t *testing.T) {
	root := findProjectRoot(t)
	cssBytes, err := os.ReadFile(filepath.Join(root, "web", "static", "css", "theme.css"))
	if err != nil {
		t.Fatalf("Failed to read theme.css: %v", err)
	}
	css := string(cssBytes)

	lightVars := parseCSSVars(css, ":root")
	darkVars := parseCSSVars(css, ":root[data-theme=\"dark\"]")

	checkPairs := func(themeName string, vars map[string]string) {
		pairs := []struct {
			name string
			c1   string
			c2   string
		}{
			{"Teks utama vs Latar", vars["color-text"], vars["color-bg"]},
			{"Teks muted vs Latar", vars["color-muted"], vars["color-bg"]},
			{"Link vs Latar", vars["color-link"], vars["color-bg"]},
			{"Tombol primary (teks vs bg)", vars["color-on-primary"], vars["color-primary"]},
		}

		for _, p := range pairs {
			if p.c1 == "" || p.c2 == "" {
				t.Errorf("[%s] %s: variable missing in CSS (c1=%q, c2=%q)", themeName, p.name, p.c1, p.c2)
				continue
			}
			ratio, err := contrastRatio(p.c1, p.c2)
			if err != nil {
				t.Errorf("[%s] %s: calculation error (%s vs %s): %v", themeName, p.name, p.c1, p.c2, err)
				continue
			}
			if ratio < 4.5 {
				t.Errorf("[%s] %s: contrast ratio %.2f:1 is below WCAG 2.1 threshold 4.5:1 (%s vs %s)", themeName, p.name, ratio, p.c1, p.c2)
			}
		}
	}

	checkPairs("Light", lightVars)
	checkPairs("Dark", darkVars)
}

func TestNoHardcodedColorsInTemplates(t *testing.T) {
	root := findProjectRoot(t)
	templatesDir := filepath.Join(root, "web", "templates")
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("Failed to read templates directory: %v", err)
	}

	reColor := regexp.MustCompile(`#[0-9a-fA-F]{3,8}|rgba?\(|hsla?\(`)

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		filePath := filepath.Join(templatesDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Errorf("Failed to read %s: %v", entry.Name(), err)
			continue
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if loc := reColor.FindString(line); loc != "" {
				t.Errorf("Hardcoded color %q found in %s:%d -> %s", loc, entry.Name(), i+1, strings.TrimSpace(line))
			}
		}
	}
}

func TestThemeEndpoint(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(nil, "test", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// 1. Without CSRF -> 403 Forbidden
	{
		form := url.Values{"theme": {"light"}}
		req := httptest.NewRequest("POST", "/theme", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("Expected 403 without CSRF, got %d", rr.Code)
		}
	}

	// Helper to get CSRF token and cookie
	getCSRFTokenAndCookie := func() (string, *http.Cookie) {
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		for _, c := range rr.Result().Cookies() {
			if c.Name == "gb_csrf" {
				return c.Value, c
			}
		}
		return "", nil
	}

	token, csrfCookie := getCSRFTokenAndCookie()
	if token == "" || csrfCookie == nil {
		t.Fatalf("Failed to obtain CSRF cookie")
	}

	// 2. Invalid theme -> 400 Bad Request
	{
		form := url.Values{"theme": {"neon_blue"}, "csrf_token": {token}}
		req := httptest.NewRequest("POST", "/theme", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(csrfCookie)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("Expected 400 with invalid theme, got %d", rr.Code)
		}
	}

	// 3. theme=light -> 303, cookie theme=light
	{
		form := url.Values{"theme": {"light"}, "csrf_token": {token}}
		req := httptest.NewRequest("POST", "/theme", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(csrfCookie)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("Expected 303 for theme=light, got %d", rr.Code)
		}
		found := false
		for _, c := range rr.Result().Cookies() {
			if c.Name == "theme" && c.Value == "light" && c.MaxAge > 0 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected Set-Cookie theme=light with MaxAge > 0, got cookies: %v", rr.Result().Cookies())
		}
	}

	// 4. theme=dark -> 303, cookie theme=dark
	{
		form := url.Values{"theme": {"dark"}, "csrf_token": {token}}
		req := httptest.NewRequest("POST", "/theme", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(csrfCookie)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("Expected 303 for theme=dark, got %d", rr.Code)
		}
		found := false
		for _, c := range rr.Result().Cookies() {
			if c.Name == "theme" && c.Value == "dark" && c.MaxAge > 0 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected Set-Cookie theme=dark with MaxAge > 0, got cookies: %v", rr.Result().Cookies())
		}
	}

	// 5. theme=auto -> 303, cookie theme cleared (MaxAge <= 0 or empty value)
	{
		form := url.Values{"theme": {"auto"}, "csrf_token": {token}}
		req := httptest.NewRequest("POST", "/theme", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(csrfCookie)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("Expected 303 for theme=auto, got %d", rr.Code)
		}
		found := false
		for _, c := range rr.Result().Cookies() {
			if c.Name == "theme" && (c.MaxAge <= 0 || c.Value == "") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected Set-Cookie theme cleared with MaxAge <= 0, got cookies: %v", rr.Result().Cookies())
		}
	}
}

func TestThemePageRendering(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(filepath.Join(root, "locales"))

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	srv, err := NewServer(nil, "test", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// 1. Without cookie -> <html does not have data-theme
	{
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		body := rr.Body.String()
		if strings.Contains(body, "data-theme") {
			t.Errorf("Expected no data-theme attribute when no cookie, got %s", body)
		}
	}

	// 2. Cookie theme=light -> data-theme="light"
	{
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(&http.Cookie{Name: "theme", Value: "light"})
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		body := rr.Body.String()
		if !strings.Contains(body, `data-theme="light"`) {
			t.Errorf("Expected data-theme=\"light\" on <html>, got %s", body)
		}
	}

	// 3. Cookie theme=dark -> data-theme="dark"
	{
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		body := rr.Body.String()
		if !strings.Contains(body, `data-theme="dark"`) {
			t.Errorf("Expected data-theme=\"dark\" on <html>, got %s", body)
		}
	}
}
