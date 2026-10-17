package db_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"goblast/internal/testutil"
)

// TestGuard_DocSkemaMatchesDatabaseCatalog memastikan bahwa semua nama tabel, indeks,
// constraint, dan trigger yang tertulis dalam DOC_SKEMA.md benar-benar ada di database katalog PostgreSQL,
// serta memastikan semua indeks aplikasi hasil migrasi tercatat di DOC_SKEMA.md.
func TestGuard_DocSkemaMatchesDatabaseCatalog(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	rootDir := testutil.RootDir(t)
	docPath := filepath.Join(rootDir, "docs", "DOC_SKEMA.md")
	contentBytes, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("gagal membaca %s: %v", docPath, err)
	}
	doc := string(contentBytes)

	// 1. Ekstrak nama tabel dari Bagian 1 atau Bagian 2
	// Pola: ### 2.x `table_name` atau 1. `table_name`:
	tableRegex := regexp.MustCompile("(?m)^### 2\\.\\d+\\s+`([a-z_]+)`")
	tableMatches := tableRegex.FindAllStringSubmatch(doc, -1)
	if len(tableMatches) == 0 {
		t.Fatalf("tidak ditemukan nama tabel dengan format `### 2.x `table_name`` di DOC_SKEMA")
	}

	docTables := make(map[string]bool)
	for _, m := range tableMatches {
		docTables[m[1]] = true
	}

	// Verifikasi setiap tabel ada di database
	for tbl := range docTables {
		var exists bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables 
				WHERE table_schema = 'public' AND table_name = $1
			)
		`, tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("query cek tabel %s gagal: %v", tbl, err)
		}
		if !exists {
			t.Errorf("Tabel %q yang tercantum di DOC_SKEMA tidak ditemukan di katalog PostgreSQL", tbl)
		}
	}

	// 2. Ekstrak Constraints & Triggers dari Bagian 4
	// Pola baris bernomor: `1. `con_name`: ...`
	sec4Start := strings.Index(doc, "## 4. Constraint Integritas dan Bisnis")
	if sec4Start == -1 {
		t.Fatalf("Bagian 4 '## 4. Constraint Integritas dan Bisnis' tidak ditemukan di DOC_SKEMA")
	}
	sec5Start := strings.Index(doc, "## 5. Indeks Database dan Query yang Dilayani")
	if sec5Start == -1 {
		t.Fatalf("Bagian 5 '## 5. Indeks Database dan Query yang Dilayani' tidak ditemukan di DOC_SKEMA")
	}
	sec4Text := doc[sec4Start:sec5Start]

	conRegex := regexp.MustCompile("(?m)^\\d+\\.\\s+`([a-z_]+)`")
	conMatches := conRegex.FindAllStringSubmatch(sec4Text, -1)
	if len(conMatches) == 0 {
		t.Fatalf("tidak ada constraint yang ditemukan di Bagian 4 DOC_SKEMA")
	}

	for _, m := range conMatches {
		name := m[1]
		if strings.HasPrefix(name, "trg_") {
			// Trigger
			var exists bool
			err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM pg_trigger t
					JOIN pg_class cl ON cl.oid = t.tgrelid
					JOIN pg_namespace n ON n.oid = cl.relnamespace
					WHERE n.nspname = 'public' AND t.tgname = $1 AND NOT t.tgisinternal
				)
			`, name).Scan(&exists)
			if err != nil {
				t.Fatalf("query trigger %s gagal: %v", name, err)
			}
			if !exists {
				t.Errorf("Trigger %q di DOC_SKEMA Bagian 4 tidak ditemukan di pg_trigger", name)
			}
		} else {
			// Constraint
			var exists bool
			err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM pg_constraint c
					JOIN pg_namespace n ON n.oid = c.connamespace
					WHERE n.nspname = 'public' AND c.conname = $1
				)
			`, name).Scan(&exists)
			if err != nil {
				t.Fatalf("query constraint %s gagal: %v", name, err)
			}
			if !exists {
				t.Errorf("Constraint %q di DOC_SKEMA Bagian 4 tidak ditemukan di pg_constraint", name)
			}
		}
	}

	// 3. Ekstrak Indeks dari Bagian 5
	// Pola baris tabel: `| `idx_name` | ...`
	sec6Start := strings.Index(doc, "## 6. Audit Kolom Tak Terpakai")
	if sec6Start == -1 {
		t.Fatalf("Bagian 6 tidak ditemukan di DOC_SKEMA")
	}
	sec5Text := doc[sec5Start:sec6Start]

	idxRegex := regexp.MustCompile("(?m)^\\|\\s+`([a-z_]+)`")
	idxMatches := idxRegex.FindAllStringSubmatch(sec5Text, -1)
	if len(idxMatches) == 0 {
		t.Fatalf("tidak ada indeks yang ditemukan di Bagian 5 DOC_SKEMA")
	}

	docIndexes := make(map[string]bool)
	for _, m := range idxMatches {
		name := m[1]
		docIndexes[name] = true

		var exists bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_indexes
				WHERE schemaname = 'public' AND indexname = $1
			)
		`, name).Scan(&exists)
		if err != nil {
			t.Fatalf("query index %s gagal: %v", name, err)
		}
		if !exists {
			t.Errorf("Indeks %q di DOC_SKEMA Bagian 5 tidak ditemukan di pg_indexes", name)
		}
	}

	// 4. Verifikasi kebalikannya (semua custom index di tabel aplikasi harus terdokumentasi di DOC_SKEMA Bagian 5)
	// Kecualikan indeks bawaan primary key (*_pkey) dan whatsmeow_*
	rows, err := pool.Query(ctx, `
		SELECT indexname, tablename
		FROM pg_indexes
		WHERE schemaname = 'public'
		  AND NOT tablename LIKE 'whatsmeow_%'
		  AND NOT tablename = 'goose_db_version'
		  AND NOT indexname LIKE '%_pkey'
		ORDER BY indexname
	`)
	if err != nil {
		t.Fatalf("query semua pg_indexes gagal: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var iname, tname string
		if err := rows.Scan(&iname, &tname); err != nil {
			t.Fatalf("scan index gagal: %v", err)
		}
		if !docIndexes[iname] {
			t.Errorf("Indeks database %q (pada tabel %s) belum terdokumentasi di DOC_SKEMA Bagian 5", iname, tname)
		}
	}
}
