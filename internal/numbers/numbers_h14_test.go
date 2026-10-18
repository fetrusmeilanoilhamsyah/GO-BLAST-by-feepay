package numbers

import (
	"testing"
)

// TestH14_Normalize_TenCountriesTable menguji normalisasi untuk sedikitnya 10 negara:
// ID, MY, SG, US, GB, IN, BR, NG, AU, DE dengan format:
// - awalan +
// - awalan 00
// - nasional dengan 0
// - nasional tanpa 0
// - dengan spasi dan tanda hubung
func TestH14_Normalize_TenCountriesTable(t *testing.T) {
	tests := []struct {
		country  string
		region   string
		raw      string
		wantE164 string
		wantCC   string
		wantStat string
	}{
		// 1. ID (Indonesia, +62)
		{"ID Nasional dengan 0", "ID", "081234567890", "6281234567890", "62", "ok"},
		{"ID Awalan +", "ID", "+6281234567890", "6281234567890", "62", "ok"},
		{"ID Awalan 00", "ID", "006281234567890", "6281234567890", "62", "ok"},
		{"ID Nasional tanpa 0", "ID", "81234567890", "6281234567890", "62", "ok"},
		{"ID Spasi dan strip", "ID", "+62 812-3456-7890", "6281234567890", "62", "ok"},

		// 2. MY (Malaysia, +60)
		{"MY Nasional dengan 0", "MY", "0123456789", "60123456789", "60", "ok"},
		{"MY Awalan +", "MY", "+60123456789", "60123456789", "60", "ok"},
		{"MY Awalan 00", "MY", "0060123456789", "60123456789", "60", "ok"},
		{"MY Spasi dan strip", "MY", "012-345 6789", "60123456789", "60", "ok"},

		// 3. SG (Singapura, +65 - nasional tidak menggunakan awalan 0)
		{"SG Nasional tanpa 0", "SG", "91234567", "6591234567", "65", "ok"},
		{"SG Awalan +", "SG", "+6591234567", "6591234567", "65", "ok"},
		{"SG Awalan 00", "SG", "006591234567", "6591234567", "65", "ok"},
		{"SG Spasi dan strip", "SG", "+65 9123-4567", "6591234567", "65", "ok"},

		// 4. US (Amerika Serikat, +1 - nanp tipe FIXED_LINE_OR_MOBILE)
		{"US Nasional tanpa 0", "US", "4155552671", "14155552671", "1", "ok"},
		{"US Awalan +", "US", "+14155552671", "14155552671", "1", "ok"},
		{"US Awalan 00", "US", "0014155552671", "14155552671", "1", "ok"},
		{"US Kurung, spasi, strip", "US", "+1 (415) 555-2671", "14155552671", "1", "ok"},

		// 5. GB (Inggris, +44)
		{"GB Nasional dengan 0", "GB", "07911123456", "447911123456", "44", "ok"},
		{"GB Awalan +", "GB", "+447911123456", "447911123456", "44", "ok"},
		{"GB Awalan 00", "GB", "00447911123456", "447911123456", "44", "ok"},
		{"GB Spasi dan strip", "GB", "07911-123 456", "447911123456", "44", "ok"},

		// 6. IN (India, +91)
		{"IN Nasional dengan 0", "IN", "09876543210", "919876543210", "91", "ok"},
		{"IN Nasional tanpa 0", "IN", "9876543210", "919876543210", "91", "ok"},
		{"IN Awalan +", "IN", "+919876543210", "919876543210", "91", "ok"},
		{"IN Awalan 00", "IN", "00919876543210", "919876543210", "91", "ok"},
		{"IN Spasi dan strip", "IN", "+91 98765-43210", "919876543210", "91", "ok"},

		// 7. BR (Brasil, +55)
		{"BR Nasional tanpa 0", "BR", "11987654321", "5511987654321", "55", "ok"},
		{"BR Awalan +", "BR", "+5511987654321", "5511987654321", "55", "ok"},
		{"BR Awalan 00", "BR", "005511987654321", "5511987654321", "55", "ok"},
		{"BR Spasi dan strip", "BR", "+55 (11) 98765-4321", "5511987654321", "55", "ok"},

		// 8. NG (Nigeria, +234)
		{"NG Nasional dengan 0", "NG", "08031234567", "2348031234567", "234", "ok"},
		{"NG Awalan +", "NG", "+2348031234567", "2348031234567", "234", "ok"},
		{"NG Awalan 00", "NG", "002348031234567", "2348031234567", "234", "ok"},
		{"NG Spasi dan strip", "NG", "0803-123-4567", "2348031234567", "234", "ok"},

		// 9. AU (Australia, +61)
		{"AU Nasional dengan 0", "AU", "0412345678", "61412345678", "61", "ok"},
		{"AU Awalan +", "AU", "+61412345678", "61412345678", "61", "ok"},
		{"AU Awalan 00", "AU", "0061412345678", "61412345678", "61", "ok"},
		{"AU Spasi dan strip", "AU", "+61 412 345 678", "61412345678", "61", "ok"},

		// 10. DE (Jerman, +49)
		{"DE Nasional dengan 0", "DE", "015123456789", "4915123456789", "49", "ok"},
		{"DE Awalan +", "DE", "+4915123456789", "4915123456789", "49", "ok"},
		{"DE Awalan 00", "DE", "004915123456789", "4915123456789", "49", "ok"},
		{"DE Spasi dan strip", "DE", "+49 151-23456789", "4915123456789", "49", "ok"},
	}

	for _, tt := range tests {
		t.Run(tt.country, func(t *testing.T) {
			gotE164, gotStat, gotCC := Normalize(tt.raw, tt.region)
			if gotStat != tt.wantStat {
				t.Errorf("Normalize(%q, %q) status = %q, want %q", tt.raw, tt.region, gotStat, tt.wantStat)
			}
			if gotE164 != tt.wantE164 {
				t.Errorf("Normalize(%q, %q) e164 = %q, want %q", tt.raw, tt.region, gotE164, tt.wantE164)
			}
			if gotCC != tt.wantCC {
				t.Errorf("Normalize(%q, %q) cc = %q, want %q", tt.raw, tt.region, gotCC, tt.wantCC)
			}
		})
	}
}

// TestH14_SamePrefix07_GB_vs_ID menguji bahwa input yang sama "07..." menghasilkan nomor berbeda
// untuk wilayah GB (Inggris) dan wilayah ID (Indonesia).
func TestH14_SamePrefix07_GB_vs_ID(t *testing.T) {
	raw := "07911123456"

	// Wilayah GB: nomor seluler Inggris valid
	e164GB, statGB, ccGB := Normalize(raw, "GB")
	if statGB != "ok" || ccGB != "44" || e164GB != "447911123456" {
		t.Fatalf("Normalize(%s, 'GB') gagal: got e164=%q, stat=%q, cc=%q; want 447911123456, 'ok', '44'", raw, e164GB, statGB, ccGB)
	}

	// Wilayah ID: di Indonesia, 07xx adalah kode area darat/Sumatera atau tidak valid sebagai seluler
	e164ID, statID, _ := Normalize(raw, "ID")
	if statID == "ok" {
		t.Fatalf("Normalize(%s, 'ID') tidak boleh menghasilkan status 'ok' untuk nomor seluler UK, got e164=%q stat=%q", raw, e164ID, statID)
	}
}

// TestH14_LandlinesRejectedAsNotMobile menguji bahwa nomor telepon rumah ditolak dengan status "bukan_seluler",
// sedangkan nomor seluler diterima dengan status "ok".
func TestH14_LandlinesRejectedAsNotMobile(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		region   string
		wantStat string
	}{
		// Telepon rumah (fixed line) harus ditolak sebagai bukan_seluler
		{"ID Telkom Jakarta", "0213840000", "ID", "bukan_seluler"},
		{"ID Telkom Jakarta E164", "+62213840000", "ID", "bukan_seluler"},
		{"GB London Fixed Line", "02079460000", "GB", "bukan_seluler"},
		{"MY KL Fixed Line", "0321234567", "MY", "bukan_seluler"},
		{"AU Sydney Fixed Line", "0298765432", "AU", "bukan_seluler"},
		{"DE Berlin Fixed Line", "0301234567", "DE", "bukan_seluler"},

		// Nomor seluler harus berstatus ok
		{"ID Telkomsel Mobile", "081234567890", "ID", "ok"},
		{"GB Vodafone Mobile", "07911123456", "GB", "ok"},
		{"MY Celcom Mobile", "0123456789", "MY", "ok"},
		{"AU Telstra Mobile", "0412345678", "AU", "ok"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stat, _ := Normalize(tt.raw, tt.region)
			if stat != tt.wantStat {
				t.Errorf("Normalize(%q, %q) status = %q, want %q", tt.raw, tt.region, stat, tt.wantStat)
			}
		})
	}
}

// TestH14_XLSXNumericAmbiguity menguji bahwa sel numerik XLSX tanpa 0 awal dan tanpa kode negara bawaan menjadi ambigu.
func TestH14_XLSXNumericAmbiguity(t *testing.T) {
	// Di Indonesia (defaultCC 62):
	// "81234567890" dari sel numerik XLSX kehilangan 0 dan tidak diawali 62 -> ambigu
	_, stat1, _ := Normalize("81234567890", "ID", true)
	if stat1 != "ambigu" {
		t.Errorf("Normalize numerik XLSX tanpa 0 dan tanpa CC 62 status = %q, want 'ambigu'", stat1)
	}

	// "6281234567890" dari sel numerik XLSX diawali 62 -> ok
	e164_2, stat2, _ := Normalize("6281234567890", "ID", true)
	if stat2 != "ok" || e164_2 != "6281234567890" {
		t.Errorf("Normalize numerik XLSX diawali 62 status = %q (e164=%s), want 'ok'", stat2, e164_2)
	}

	// Di UK (defaultCC 44):
	// "7911123456" dari sel numerik XLSX kehilangan 0 dan tidak diawali 44 -> ambigu
	_, statGB1, _ := Normalize("7911123456", "GB", true)
	if statGB1 != "ambigu" {
		t.Errorf("Normalize numerik XLSX tanpa 0 dan tanpa CC 44 status = %q, want 'ambigu'", statGB1)
	}

	// "447911123456" dari sel numerik XLSX diawali 44 -> ok
	e164_GB2, statGB2, _ := Normalize("447911123456", "GB", true)
	if statGB2 != "ok" || e164_GB2 != "447911123456" {
		t.Errorf("Normalize numerik XLSX diawali 44 status = %q (e164=%s), want 'ok'", statGB2, e164_GB2)
	}
}

// TestH14_SupportedCountryOptions menguji bahwa daftar opsi negara terisi lengkap, memiliki label dan kode telepon, serta terurut.
func TestH14_SupportedCountryOptions(t *testing.T) {
	opts := SupportedCountryOptions()
	if len(opts) < 200 {
		t.Fatalf("Diharapkan lebih dari 200 opsi negara, didapat %d", len(opts))
	}

	foundID, foundGB, foundUS := false, false, false
	for i, o := range opts {
		if o.RegionCode == "ID" && o.CountryCode == 62 && o.Label == "ID (+62)" {
			foundID = true
		}
		if o.RegionCode == "GB" && o.CountryCode == 44 && o.Label == "GB (+44)" {
			foundGB = true
		}
		if o.RegionCode == "US" && o.CountryCode == 1 && o.Label == "US (+1)" {
			foundUS = true
		}
		if i > 0 && opts[i-1].RegionCode >= opts[i].RegionCode {
			t.Errorf("Daftar opsi negara tidak terurut alfabetis: %s >= %s", opts[i-1].RegionCode, opts[i].RegionCode)
		}
	}

	if !foundID || !foundGB || !foundUS {
		t.Errorf("ID, GB, atau US tidak ditemukan dalam SupportedCountryOptions (foundID=%v, foundGB=%v, foundUS=%v)", foundID, foundGB, foundUS)
	}
}
