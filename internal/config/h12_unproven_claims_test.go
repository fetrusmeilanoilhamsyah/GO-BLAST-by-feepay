package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"goblast/internal/testutil"
)

// TestH12_ProveUnprovenClaims verifies that unproven claims identified in Stage H12
// have been properly corrected across .env.example, DOC_KONSEP.md, DOC_SECURITY_DEPLOY.md, and DOC_PROGRES.md.
func TestH12_ProveUnprovenClaims(t *testing.T) {
	rootDir := testutil.RootDir(t)

	// 1. Periksa .env.example
	t.Run("EnvExample", func(t *testing.T) {
		path := filepath.Join(rootDir, ".env.example")
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("gagal membaca %s: %v", path, err)
		}
		content := string(contentBytes)

		if strings.Contains(content, "diukur empiris di H10: 75-80 sesi aman") {
			t.Errorf(".env.example masih mengklaim 'diukur empiris di H10: 75-80 sesi aman'")
		}
		if !strings.Contains(content, "MAX_ACTIVE_SESSIONS=20") {
			t.Errorf(".env.example harus menyetel default konservatif MAX_ACTIVE_SESSIONS=20")
		}
		if !strings.Contains(content, "nilai awal sementara") {
			t.Errorf(".env.example harus menjelaskan bahwa MAX_ACTIVE_SESSIONS=20 adalah nilai awal sementara")
		}
	})

	// 2. Periksa DOC_KONSEP.md
	t.Run("DocKonsep", func(t *testing.T) {
		path := filepath.Join(rootDir, "docs", "DOC_KONSEP.md")
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("gagal membaca %s: %v", path, err)
		}
		content := string(contentBytes)

		if strings.Contains(content, "Hasil Ukur Empiris Tahap H10") {
			t.Errorf("DOC_KONSEP.md masih memuat judul 'Hasil Ukur Empiris Tahap H10'")
		}
		if strings.Contains(content, "75–80 sesi WhatsApp aktif") || strings.Contains(content, "75-80 sesi WhatsApp aktif") {
			t.Errorf("DOC_KONSEP.md masih mengklaim 75-80 sesi WhatsApp aktif sebagai hasil ukur/batas aman terverifikasi")
		}
		// Keputusan terbuka #6 harus terbuka kembali: - [ ] 6.
		reDec6 := regexp.MustCompile(`-\s*\[\s*\]\s*6\.\s*Nilai awal batas`)
		if !reDec6.MatchString(content) {
			t.Errorf("DOC_KONSEP.md keputusan #6 harus kembali terbuka (- [ ] 6.)")
		}
	})

	// 3. Periksa DOC_SECURITY_DEPLOY.md
	t.Run("DocSecurityDeploy", func(t *testing.T) {
		path := filepath.Join(rootDir, "docs", "DOC_SECURITY_DEPLOY.md")
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("gagal membaca %s: %v", path, err)
		}
		content := string(contentBytes)

		if strings.Contains(content, "hasil pengukuran empiris beban 75–80 sesi") {
			t.Errorf("DOC_SECURITY_DEPLOY.md B1 masih mengklaim 'hasil pengukuran empiris beban 75–80 sesi'")
		}
		if strings.Contains(content, "hasil ukur H10: 75–80") {
			t.Errorf("DOC_SECURITY_DEPLOY.md B4 masih mengklaim 'hasil ukur H10: 75–80'")
		}
		if !strings.Contains(content, "| `MAX_ACTIVE_SESSIONS` | Batas maksimal sesi WhatsApp aktif simultan | `20`") {
			t.Errorf("DOC_SECURITY_DEPLOY.md B4 harus mencantumkan nilai awal sementara 20 untuk MAX_ACTIVE_SESSIONS")
		}

		// Checklist item 11 (MemoryMax) harus "Perlu Tindakan" dan penanggung jawab "Pemilik"
		reItem11 := regexp.MustCompile(`\|\s*11\s*\|\s*MemoryMax[^|]*\|\s*Infrastruktur\s*\|\s*Perlu Tindakan\s*\|[^|]*\|\s*Pemilik\s*\|`)
		if !reItem11.MatchString(content) {
			t.Errorf("DOC_SECURITY_DEPLOY.md Checklist butir 11 harus berstatus 'Perlu Tindakan' dengan penanggung jawab 'Pemilik'")
		}

		// Checklist item baru go test -race
		if !strings.Contains(content, "go test -race lulus untuk paket engine, wa, web, campaign, backup") {
			t.Errorf("DOC_SECURITY_DEPLOY.md Checklist harus memuat butir baru 'go test -race lulus untuk paket engine, wa, web, campaign, backup'")
		}

		// Prosedur pengukuran memori per sesi harus ada
		if !strings.Contains(content, "Prosedur Pengukuran Memori Riil per Sesi WhatsApp") {
			t.Errorf("DOC_SECURITY_DEPLOY.md harus memuat prosedur pengukuran memori riil per sesi")
		}
	})

	// 4. Periksa DOC_PROGRES.md
	t.Run("DocProgres", func(t *testing.T) {
		path := filepath.Join(rootDir, "docs", "DOC_PROGRES.md")
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("gagal membaca %s: %v", path, err)
		}
		content := string(contentBytes)

		// Tahap 15 harus Sebagian
		reTahap15 := regexp.MustCompile(`\|\s*15\s*\|\s*Review keamanan dan uji beban\s*\|\s*Sebagian\s*\|`)
		if !reTahap15.MatchString(content) {
			t.Errorf("DOC_PROGRES.md Tahap 15 harus berstatus 'Sebagian'")
		}

		// Keputusan terbuka #6 harus terbuka
		reDec6 := regexp.MustCompile(`-\s*\[\s*\]\s*6\.\s*Nilai awal batas`)
		if !reDec6.MatchString(content) {
			t.Errorf("DOC_PROGRES.md keputusan #6 harus terbuka (- [ ] 6.)")
		}

		// Prosedur pengukuran memori per sesi harus ada
		if !strings.Contains(content, "Prosedur Pengukuran Memori Riil per Sesi WhatsApp") {
			t.Errorf("DOC_PROGRES.md harus memuat prosedur pengukuran memori riil per sesi")
		}
	})
}
