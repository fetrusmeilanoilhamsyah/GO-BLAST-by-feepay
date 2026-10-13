package campaign_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func findProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Gagal mendapatkan working directory: %v", err)
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
	t.Fatal("Akar proyek tidak ditemukan (go.mod tidak ada)")
	return ""
}

// TestTersediaQueryGuard memindai seluruh kode sumber produksi (.go) dan gagal
// jika ditemukan query untuk status 'tersedia' di luar claimable.go dan engine.go.
func TestTersediaQueryGuard(t *testing.T) {
	root := findProjectRoot(t)

	// Pola yang mendeteksi query SQL untuk status 'tersedia':
	// 1. WHERE / AND / OR dengan status = 'tersedia' (dengan atau tanpa alias tabel seperti cn.status)
	// 2. WHERE ... status IN (... 'tersedia' ...)
	// 3. CASE WHEN ... status = 'tersedia'
	queryPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(where|and|or)\s+([a-z0-9_]+\.)?status\s*=\s*['"]tersedia['"]`),
		regexp.MustCompile(`(?i)status\s*=\s*['"]tersedia['"]`),
		regexp.MustCompile(`(?i)status\s+in\s*\([^)]*['"]tersedia['"][^)]*\)`),
	}

	allowedFiles := map[string]bool{
		"claimable.go": true,
		"engine.go":    true,
	}

	var violations []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Abaikan direktori tersembunyi, vendor, scratch, git
		if info.IsDir() {
			base := info.Name()
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "scratch" || base == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}

		// Hanya periksa file Go produksi (bukan file tes _test.go)
		if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}

		// File yang diizinkan
		if allowedFiles[info.Name()] {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()

			// Abaikan baris komentar biasa
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
				continue
			}

			// Abaikan baris UPDATE (contoh: UPDATE ... SET status = 'tersedia' bukan query pencarian/filter)
			if strings.Contains(strings.ToUpper(line), "SET STATUS = 'TERSEDIA'") || strings.Contains(strings.ToUpper(line), "SET STATUS='TERSEDIA'") {
				continue
			}

			// Abaikan baris INSERT (contoh: VALUES (..., 'tersedia') bukan query pencarian/filter)
			if strings.Contains(strings.ToUpper(line), "'TERSEDIA'") && (strings.Contains(strings.ToUpper(line), "VALUES") || strings.Contains(strings.ToUpper(line), "SELECT $1, $2")) {
				continue
			}

			for _, pattern := range queryPatterns {
				if pattern.MatchString(line) {
					relPath, _ := filepath.Rel(root, path)
					violations = append(violations, relPath+":"+strings.TrimSpace(line))
					break
				}
			}
		}
		return scanner.Err()
	})

	if err != nil {
		t.Fatalf("Gagal memindai kode sumber: %v", err)
	}

	if len(violations) > 0 {
		t.Errorf("Ditemukan %d query untuk status 'tersedia' di luar claimable.go dan engine.go:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}
