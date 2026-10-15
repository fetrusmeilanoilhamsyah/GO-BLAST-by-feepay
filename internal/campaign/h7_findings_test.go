package campaign

import (
	"strings"
	"testing"
)

// TestFinding_P2c_RenderFallbackBug membuktikan bahwa Render saat ini melewati
// penggantian variabel jika kumpulan variabel kosong, sehingga {nama|Kak} terkirim mentah.
func TestFinding_P2c_RenderFallbackBug(t *testing.T) {
	template := "Halo {nama|Kak}, selamat datang! ID Anda: {id}. Kode: {kode}"
	msg := Render(template, "", Elements{}, ModeKartuLink)

	// Jika bug masih ada, {nama|Kak} masih tersisa di msg.Text
	if strings.Contains(msg.Text, "{nama|Kak}") || strings.Contains(msg.Text, "{") {
		t.Errorf("BUG P2c TERBUKTI: Render tidak mengeksekusi fallback saat variabel kosong! Output: %q", msg.Text)
	}
}

// TestFinding_P2d_VariableSanitization membuktikan bahwa karakter kontrol,
// format WhatsApp (*, _, ~, `), baris baru, dan panjang > 60 karakter belum dibersihkan.
func TestFinding_P2d_VariableSanitization(t *testing.T) {
	dirtyVal := "Budi\r\n\x00*Tebal*_Miring_~Coret~`Kode`" + strings.Repeat("X", 100)
	vars := map[string]string{
		"nama": dirtyVal,
	}
	res := ReplaceVariables("Pelanggan: {nama}", vars)

	if strings.Contains(res, "*") || strings.Contains(res, "\n") || strings.Contains(res, "\r") || strings.Contains(res, "~") || strings.Contains(res, "`") || strings.Contains(res, "_") {
		t.Errorf("BUG P2d TERBUKTI: Nilai variabel belum dibersihkan dari format WA atau baris baru! Hasil: %q", res)
	}
	// Periksa pemotongan maksimal 60 karakter
	cleanedName := strings.TrimPrefix(res, "Pelanggan: ")
	if len([]rune(cleanedName)) > 60 {
		t.Errorf("BUG P2d TERBUKTI: Nilai variabel tidak dipotong 60 karakter! Panjang: %d", len([]rune(cleanedName)))
	}
}

// TestFinding_P3a_TombolAsliValidation membuktikan bahwa saat ini ModeTombolAsli ditolak oleh ValidateTemplate.
func TestFinding_P3a_TombolAsliValidation(t *testing.T) {
	err := ValidateTemplate("Halo pesan", ModeTombolAsli, Elements{})
	if err != nil && err.Error() == "mode tombol_asli dinonaktifkan" {
		t.Errorf("TEMUAN P3a TERBUKTI: ValidateTemplate menolak mode tombol_asli: %v", err)
	}
}
