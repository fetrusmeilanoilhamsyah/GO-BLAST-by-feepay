package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"goblast/internal/testutil"
)

// TestGuard_EnvVarsSync verifies bidirectional synchronization between:
// 1. All environment variables read by config.go (os.Getenv)
// 2. All environment variables listed in .env.example
// 3. All environment variables documented in Table B4 of docs/DOC_SECURITY_DEPLOY.md
func TestGuard_EnvVarsSync(t *testing.T) {
	rootDir := testutil.RootDir(t)

	// 1. Extract os.Getenv keys from internal/config/config.go
	configPath := filepath.Join(rootDir, "internal", "config", "config.go")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("gagal membaca %s: %v", configPath, err)
	}

	getenvRegex := regexp.MustCompile(`os\.Getenv\("([A-Z0-9_]+)"\)`)
	configMatches := getenvRegex.FindAllStringSubmatch(string(configBytes), -1)
	configEnvSet := make(map[string]bool)
	for _, m := range configMatches {
		configEnvSet[m[1]] = true
	}

	// 2. Extract environment variable keys from .env.example
	examplePath := filepath.Join(rootDir, ".env.example")
	exampleBytes, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("gagal membaca %s: %v", examplePath, err)
	}

	exampleRegex := regexp.MustCompile(`(?m)^([A-Z0-9_]+)=`)
	exampleMatches := exampleRegex.FindAllStringSubmatch(string(exampleBytes), -1)
	exampleEnvSet := make(map[string]bool)
	for _, m := range exampleMatches {
		exampleEnvSet[m[1]] = true
	}

	// 3. Extract environment variable keys from Table B4 of docs/DOC_SECURITY_DEPLOY.md
	deployPath := filepath.Join(rootDir, "docs", "DOC_SECURITY_DEPLOY.md")
	deployBytes, err := os.ReadFile(deployPath)
	if err != nil {
		t.Fatalf("gagal membaca %s: %v", deployPath, err)
	}
	deployStr := string(deployBytes)

	tableB4Start := strings.Index(deployStr, "### B4. Variabel lingkungan")
	if tableB4Start == -1 {
		t.Fatalf("Subbab '### B4. Variabel lingkungan' tidak ditemukan di DOC_SECURITY_DEPLOY.md")
	}
	tableB4Text := deployStr[tableB4Start:]
	// Ambil teks tabel sampai akhir atau subbab berikutnya
	if nextSub := strings.Index(tableB4Text[25:], "\n### "); nextSub != -1 {
		tableB4Text = tableB4Text[:nextSub+25]
	}

	b4Regex := regexp.MustCompile(`(?m)^\|\s*` + "`" + `([A-Z0-9_]+)` + "`" + `\s*\|`)
	b4Matches := b4Regex.FindAllStringSubmatch(tableB4Text, -1)
	b4EnvSet := make(map[string]bool)
	for _, m := range b4Matches {
		b4EnvSet[m[1]] = true
	}

	// Periksa 1: config.go -> .env.example & DOC_SECURITY_DEPLOY Table B4
	for key := range configEnvSet {
		if !exampleEnvSet[key] {
			t.Errorf("Variabel %s dibaca di config.go tetapi tidak ada di .env.example", key)
		}
		if !b4EnvSet[key] {
			t.Errorf("Variabel %s dibaca di config.go tetapi tidak ada di Tabel B4 DOC_SECURITY_DEPLOY.md", key)
		}
	}

	// Periksa 2: .env.example -> config.go & DOC_SECURITY_DEPLOY Table B4
	for key := range exampleEnvSet {
		if !configEnvSet[key] {
			t.Errorf("Variabel %s ada di .env.example tetapi tidak dibaca di config.go", key)
		}
		if !b4EnvSet[key] {
			t.Errorf("Variabel %s ada di .env.example tetapi tidak ada di Tabel B4 DOC_SECURITY_DEPLOY.md", key)
		}
	}

	// Periksa 3: DOC_SECURITY_DEPLOY Table B4 -> config.go & .env.example
	for key := range b4EnvSet {
		if !configEnvSet[key] {
			t.Errorf("Variabel %s ada di Tabel B4 DOC_SECURITY_DEPLOY.md tetapi tidak dibaca di config.go", key)
		}
		if !exampleEnvSet[key] {
			t.Errorf("Variabel %s ada di Tabel B4 DOC_SECURITY_DEPLOY.md tetapi tidak ada di .env.example", key)
		}
	}

	if t.Failed() {
		toSortedList := func(m map[string]bool) []string {
			var list []string
			for k := range m {
				list = append(list, k)
			}
			sort.Strings(list)
			return list
		}
		t.Logf("config.go (%d vars): %v", len(configEnvSet), toSortedList(configEnvSet))
		t.Logf(".env.example (%d vars): %v", len(exampleEnvSet), toSortedList(exampleEnvSet))
		t.Logf("DOC_SECURITY_DEPLOY Table B4 (%d vars): %v", len(b4EnvSet), toSortedList(b4EnvSet))
	}
}
