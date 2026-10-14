package web

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoTestDoublesInProdPackages memastikan tidak ada kode/simbol uji (Fake, Mock, Dummy)
// yang didefinisikan atau diekspor di paket produksi non-tes.
// Pengecualian tertulis:
// - folder spike/ (keputusan pemilik, spike di luar produksi)
// - dummyHash di internal/web/wa_handlers.go (nilai pengisi sementara, dijadwalkan diganti di H2 dan H4)
func TestNoTestDoublesInProdPackages(t *testing.T) {
	root := findProjectRoot(t)

	var violations []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "spike" || name == "watest" || name == "data" || name == "uploads" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		// Relatif terhadap root
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("Gagal mem-parse %s: %v", rel, err)
		}

		for _, decl := range node.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						name := s.Name.Name
						if containsTestDoubleWord(name) {
							violations = append(violations, rel+": type "+name)
						}
					case *ast.ValueSpec:
						for _, name := range s.Names {
							if containsTestDoubleWord(name.Name) {
								violations = append(violations, rel+": var/const "+name.Name)
							}
						}
					}
				}
			case *ast.FuncDecl:
				name := d.Name.Name
				if containsTestDoubleWord(name) {
					violations = append(violations, rel+": func "+name)
				}
			}
		}

		return nil
	})

	if err != nil {
		t.Fatalf("Walk error: %v", err)
	}

	if len(violations) > 0 {
		t.Fatalf("Ditemukan simbol uji (Fake/Mock/Dummy) di paket produksi non-tes:\n%s\nKode uji harus dipindahkan ke paket internal/wa/watest atau file _test.go", strings.Join(violations, "\n"))
	}
}

func containsTestDoubleWord(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "fake") || strings.Contains(lower, "mock") || strings.Contains(lower, "dummy")
}
