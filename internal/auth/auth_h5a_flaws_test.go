package auth_test

import (
	"strings"
	"testing"

	"goblast/internal/auth"
)

// TestFlaw_K2_PasswordLengthLimit membuktikan bahwa sistem saat ini belum membatasi
// panjang maksimal kata sandi 128 byte sebelum di-hash (misal 5 MB kata sandi).
func TestFlaw_K2_PasswordLengthLimit(t *testing.T) {
	// Kata sandi 150 byte (di atas batas 128 byte)
	longPass := strings.Repeat("A", 150)

	_, err := auth.HashPassword(longPass)
	if err == nil {
		t.Errorf("BUG TERBUKTI: kata sandi > 128 byte (%d byte) diterima tanpa validasi sebelum di-hash!", len(longPass))
	} else {
		t.Logf("Sukses: kata sandi panjang ditolak: %v", err)
	}
}
