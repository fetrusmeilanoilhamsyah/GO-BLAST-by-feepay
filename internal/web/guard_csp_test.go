package web_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"goblast/internal/testutil"
)

// TestGuard_CSP_TemplatesStaticCheck memindai semua template HTML di web/templates/
// dan memastikan:
// 1. Setiap tag <script> wajib memiliki atribut nonce (nonce=...).
// 2. Tidak boleh ada inline event handler (onclick=, onchange=, onsubmit=, oninput=, dll.).
func TestGuard_CSP_TemplatesStaticCheck(t *testing.T) {
	root := testutil.RootDir(t)
	templatesDir := filepath.Join(root, "web", "templates")

	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("gagal membaca folder templates: %v", err)
	}

	reScript := regexp.MustCompile(`(?i)<script\b([^>]*)>`)
	reEventHandler := regexp.MustCompile(`(?i)\s(on[a-z]+)\s*=`)

	var scriptViolations []string
	var eventHandlerViolations []string

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}

		path := filepath.Join(templatesDir, entry.Name())
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("gagal membaca file %s: %v", entry.Name(), err)
		}
		content := string(contentBytes)

		// 1. Cek semua tag <script>
		scriptMatches := reScript.FindAllStringSubmatch(content, -1)
		for _, sm := range scriptMatches {
			attrs := sm[1]
			if !strings.Contains(attrs, "nonce=") {
				scriptViolations = append(scriptViolations, fmtViolation(entry.Name(), sm[0]))
			}
		}

		// 2. Cek semua inline event handler on*=
		lines := strings.Split(content, "\n")
		for lineIdx, line := range lines {
			// Lewati baris komentar Go template atau baris string JS murni
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "//") {
				continue
			}
			ehMatches := reEventHandler.FindAllStringSubmatch(line, -1)
			for _, ehm := range ehMatches {
				// Abaikan jika bagian dari string literal JS (misal addEventListener('onclick'...) bukan tag HTML)
				if isHTMLTagAttribute(line, ehm[1]) {
					eventHandlerViolations = append(eventHandlerViolations,
						fmtLineViolation(entry.Name(), lineIdx+1, ehm[1], trimmed))
				}
			}
		}
	}

	if len(scriptViolations) > 0 {
		t.Errorf("Ditemukan tag <script> tanpa atribut nonce:\n%s", strings.Join(scriptViolations, "\n"))
	}
	if len(eventHandlerViolations) > 0 {
		t.Errorf("Ditemukan inline event handler (on*=) di template HTML (harus dipindah ke script ber-nonce):\n%s",
			strings.Join(eventHandlerViolations, "\n"))
	}
}

func fmtViolation(file, tag string) string {
	return "  " + file + ": " + tag
}

func fmtLineViolation(file string, lineNo int, attr, line string) string {
	if len(line) > 80 {
		line = line[:80] + "..."
	}
	return "  " + file + ":" + string(rune('0'+lineNo/1000%10)) + string(rune('0'+lineNo/100%10)) + string(rune('0'+lineNo/10%10)) + string(rune('0'+lineNo%10)) + " [" + attr + "] " + line
}

// isHTMLTagAttribute memeriksa secara sederhana apakah on*= berada dalam tag HTML, bukan dalam string JS.
func isHTMLTagAttribute(line, attr string) bool {
	lower := strings.ToLower(line)
	// Jika baris berisi return '<div onclick atau var str = "...", abaikan jika bukan tag HTML template
	if strings.Contains(lower, "return '") || strings.Contains(lower, "return \"") {
		return true // Ini HTML yang di-render JS, tetap harus dibersihkan dari on*=
	}
	// Periksa apakah ada pola <... on...=
	return strings.Contains(lower, attr+"=")
}
