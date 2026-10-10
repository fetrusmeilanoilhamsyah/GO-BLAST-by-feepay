package numbers

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/nyaruka/phonenumbers"
	"github.com/xuri/excelize/v2"
)

func TestNormalize(t *testing.T) {
	// Let's generate a valid Indonesia number and US number using phonenumbers
	idNum := phonenumbers.GetExampleNumber("ID")
	usNum := phonenumbers.GetExampleNumber("US")

	idE164 := strings.TrimPrefix(phonenumbers.Format(idNum, phonenumbers.E164), "+") // e.g. 62812345678
	idLocal := "0" + fmt.Sprintf("%d", idNum.GetNationalNumber())                    // e.g. 0812345678
	idNoPrefix := fmt.Sprintf("%d", idNum.GetNationalNumber())                       // e.g. 812345678

	usE164 := strings.TrimPrefix(phonenumbers.Format(usNum, phonenumbers.E164), "+") // e.g. 12015550123

	tests := []struct {
		name      string
		raw       string
		defaultCC string
		wantE164  string
		wantStat  string
	}{
		// Awalan 0
		{"ID Awalan 0", idLocal, "62", idE164, "ok"},
		{"ID Awalan 0 dengan spasi", "0 812 345 678", "62", "62812345678", "ok"}, // assume idLocal is 0812345678
		
		// Awalan + dan 00
		{"ID Awalan +", "+" + idE164, "62", idE164, "ok"},
		{"US Awalan +", "+" + usE164, "62", usE164, "ok"},
		{"ID Awalan 00", "00" + idE164, "62", idE164, "ok"},
		
		// Tanpa awalan
		{"ID Tanpa awalan, valid (diawali CC)", idE164, "62", idE164, "ok"},
		{"ID Tanpa awalan, valid (diawali CC), dengan strip", "62-812-345", "62", "62812345", "ok"}, // might not match exact example, let's use exact
		{"ID Tanpa awalan, ambigu", idNoPrefix, "62", "", "ambigu"},
		{"US Tanpa awalan, ambigu di ID", usE164, "62", "", "ambigu"}, // 12015550123 doesn't start with 62
		
		// Karakter dihilangkan
		{"Kurung dan spasi", "+62 (812) 345-678", "62", "62812345678", "ok"},
		
		// Tidak valid
		{"Kosong", "", "62", "", "tidak_valid"},
		{"Huruf saja", "abc", "62", "", "tidak_valid"},
		{"Nomor terlalu pendek", "081", "62", "", "tidak_valid"},
		{"Nomor terlalu panjang", "+62812345678901234567890", "62", "", "tidak_valid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// adjust format for fixed strings if needed, actually let's just use exact strings for testing formatting
			// since phonenumbers.GetExampleNumber changes, let's use fixed known good numbers
			// we will ignore the dynamic part for fixed strings and just use fixed tests
		})
	}
}

func TestNormalizeFixed(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		defaultCC string
		wantE164  string
		wantStat  string
	}{
		// 1. Awalan 0
		{"Awalan 0", "081234567890", "62", "6281234567890", "ok"},
		{"Awalan 0 dengan spasi", "0 812 345 678 90", "62", "6281234567890", "ok"},
		
		// 2. Awalan +
		{"Awalan + ID", "+6281234567890", "62", "6281234567890", "ok"},
		{"Awalan + US", "+14155552671", "62", "14155552671", "ok"},
		
		// 3. Awalan 00
		{"Awalan 00 ID", "006281234567890", "62", "6281234567890", "ok"},
		{"Awalan 00 US", "0014155552671", "62", "14155552671", "ok"},
		
		// 4. Tanpa awalan
		{"Tanpa awalan, CC default valid", "6281234567890", "62", "6281234567890", "ok"},
		{"Tanpa awalan, kehilangan 0 (ambigu)", "81234567890", "62", "", "ambigu"},
		{"Tanpa awalan, CC beda (ambigu)", "14155552671", "62", "", "ambigu"},
		{"Tanpa awalan, CC default valid spasi", "62 812 3456 7890", "62", "6281234567890", "ok"},
		
		// 5. Spasi, tanda hubung, titik, kurung
		{"Format kompleks", "+62 (812) 345-678.90", "62", "6281234567890", "ok"},
		{"Format titik saja", "081.234.567.890", "62", "6281234567890", "ok"},
		{"Format kurung banyak", "(0)812(345)67890", "62", "6281234567890", "ok"},
		{"Format internasional dengan strip", "+1-415-555-2671", "62", "14155552671", "ok"},
		
		// 6. Tidak valid
		{"Kosong", "", "62", "", "tidak_valid"},
		{"Spasi saja", "   ", "62", "", "tidak_valid"},
		{"Huruf saja", "abcdef", "62", "", "tidak_valid"},
		{"Terlalu pendek", "081", "62", "", "tidak_valid"},
		{"Hanya +", "+", "62", "", "tidak_valid"},
		{"Huruf dan angka", "0812abc345", "62", "", "tidak_valid"},
		{"Karakter aneh", "0812*345", "62", "", "tidak_valid"},
		{"Kode negara salah/tidak ada", "+9998123456789", "62", "", "tidak_valid"},
		{"Nomor terlalu panjang", "+62812345678901234567890", "62", "", "tidak_valid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotE164, gotStat, _ := Normalize(tt.raw, tt.defaultCC)
			if gotStat != tt.wantStat {
				t.Errorf("Normalize(%q, %q) status = %q, want %q", tt.raw, tt.defaultCC, gotStat, tt.wantStat)
			}
			if gotStat == "ok" && gotE164 != tt.wantE164 {
				t.Errorf("Normalize(%q, %q) e164 = %q, want %q", tt.raw, tt.defaultCC, gotE164, tt.wantE164)
			}
		})
	}
}

func TestReadCSV(t *testing.T) {
	// Dengan header "nomor"
	csvData1 := `nama,nomor,umur
Budi,081234567890,20
Andi,081234567891,21
`
	nums, err := Read(bytes.NewReader([]byte(csvData1)), "test.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(nums) != 2 || nums[0] != "081234567890" || nums[1] != "081234567891" {
		t.Errorf("Unexpected CSV result: %v", nums)
	}

	// Tanpa header khusus, ambil kolom pertama
	csvData2 := `081234567890,Budi
081234567891,Andi
`
	nums2, err := Read(bytes.NewReader([]byte(csvData2)), "test2.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(nums2) != 2 || nums2[0] != "081234567890" || nums2[1] != "081234567891" {
		t.Errorf("Unexpected CSV result without header: %v", nums2)
	}
}

func TestReadTXT(t *testing.T) {
	txtData := "081234567890\n081234567891\n\n  \n081234567892"
	nums, err := Read(bytes.NewReader([]byte(txtData)), "test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(nums) != 3 || nums[0] != "081234567890" || nums[1] != "081234567891" || nums[2] != "081234567892" {
		t.Errorf("Unexpected TXT result: %v", nums)
	}
}

func TestReadXLSX(t *testing.T) {
	f := excelize.NewFile()
	f.SetCellValue("Sheet1", "A1", "081234567890")
	f.SetCellValue("Sheet1", "A2", 81234567891) // Numeric cell
	var buf bytes.Buffer
	f.Write(&buf)

	nums, err := Read(&buf, "test.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if len(nums) != 2 || nums[0] != "081234567890" || nums[1] != "81234567891" {
		t.Errorf("Unexpected XLSX result: %v", nums)
	}
}
