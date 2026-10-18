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
		{"ID Tanpa awalan, diparse", idNoPrefix, "62", idNoPrefix, "ok"},
		{"US Tanpa awalan, diparse", usE164, "62", usE164, "ok"},

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
		{"Tanpa awalan, kehilangan 0 (nasional ID)", "81234567890", "ID", "6281234567890", "ok"},
		{"Tanpa awalan, CC beda (jadi US)", "14155552671", "62", "14155552671", "ok"},
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
	// Dengan header "nomor" dan jumlah kolom berbeda-beda (variable fields)
	csvData1 := `nama,nomor,catatan
Budi,081234567890,catatan tambahan
Andi,081234567891
Citra,081234567892,catatan,ekstra,kolom
`
	nums, err := Read(bytes.NewReader([]byte(csvData1)), "test.csv")
	if err != nil {
		t.Fatalf("ReadCSV failed on variable column count: %v", err)
	}
	if len(nums) != 3 || nums[0].Raw != "081234567890" || nums[1].Raw != "081234567891" || nums[2].Raw != "081234567892" {
		t.Errorf("Unexpected CSV result: %+v", nums)
	}

	// Tanpa header khusus, ambil kolom pertama
	csvData2 := `081234567890,Budi
081234567891,Andi,Tambahan
`
	nums2, err := Read(bytes.NewReader([]byte(csvData2)), "test2.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(nums2) != 2 || nums2[0].Raw != "081234567890" || nums2[1].Raw != "081234567891" {
		t.Errorf("Unexpected CSV result without header: %+v", nums2)
	}
}

func TestReadTXT(t *testing.T) {
	txtData := "081234567890\n081234567891\n\n  \n081234567892"
	nums, err := Read(bytes.NewReader([]byte(txtData)), "test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(nums) != 3 || nums[0].Raw != "081234567890" || nums[1].Raw != "081234567891" || nums[2].Raw != "081234567892" {
		t.Errorf("Unexpected TXT result: %+v", nums)
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
	if len(nums) != 2 || nums[0].Raw != "081234567890" || nums[1].Raw != "81234567891" {
		t.Errorf("Unexpected XLSX result: %+v", nums)
	}
	if nums[0].IsNumericXLSX != false {
		t.Errorf("Expected cell A1 IsNumericXLSX = false, got true")
	}
	if nums[1].IsNumericXLSX != true {
		t.Errorf("Expected cell A2 IsNumericXLSX = true, got false")
	}
}

func TestNumericCellAmbiguityXLSX(t *testing.T) {
	f := excelize.NewFile()
	f.SetCellValue("Sheet1", "A1", "081234567890") // Text with leading 0
	f.SetCellValue("Sheet1", "A2", 81234567891)    // Numeric without 0 (should be ambigu)
	f.SetCellValue("Sheet1", "A3", 6281234567892)  // Numeric starting with defaultCC (should be ok)
	var buf bytes.Buffer
	f.Write(&buf)

	items, err := ReadXLSX(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}

	// 1. A1 text cell -> ok
	_, stat1, _ := Normalize(items[0].Raw, "62", items[0].IsNumericXLSX)
	if stat1 != "ok" {
		t.Errorf("Item 0 status = %q, want 'ok'", stat1)
	}

	// 2. A2 numeric without leading 0 -> ambigu
	_, stat2, _ := Normalize(items[1].Raw, "62", items[1].IsNumericXLSX)
	if stat2 != "ambigu" {
		t.Errorf("Item 1 status = %q, want 'ambigu'", stat2)
	}

	// 3. A3 numeric with defaultCC 62 -> ok
	_, stat3, _ := Normalize(items[2].Raw, "62", items[2].IsNumericXLSX)
	if stat3 != "ok" {
		t.Errorf("Item 2 status = %q, want 'ok'", stat3)
	}

	// 4. Same number in CSV (not XLSX) -> ok (not ambigu)
	_, statCSV, _ := Normalize("81234567891", "62", false)
	if statCSV != "ok" {
		t.Errorf("CSV status = %q, want 'ok'", statCSV)
	}
}

func TestRecipientParsing(t *testing.T) {
	t.Run("TXT formats", func(t *testing.T) {
		txtData := `081234567890
081234567891,Budi Santoso
081234567892:ijattjp
081234567893 | ari421
081234567894;Citra
081234567895	Dewi Sartika
081234567896,Eko,eko_usn,ID-123
Siti Aminah,081234567897`

		items, err := ReadTXT([]byte(txtData))
		if err != nil {
			t.Fatalf("ReadTXT failed: %v", err)
		}
		if len(items) != 8 {
			t.Fatalf("Expected 8 items, got %d", len(items))
		}

		// 1. Pure number
		if items[0].Raw != "081234567890" || items[0].Name != "" {
			t.Errorf("Item 0: expected pure number without name, got raw=%q name=%q", items[0].Raw, items[0].Name)
		}
		// 2. Comma
		if items[1].Raw != "081234567891" || items[1].Name != "Budi Santoso" {
			t.Errorf("Item 1: raw=%q name=%q", items[1].Raw, items[1].Name)
		}
		// 3. Colon
		if items[2].Raw != "081234567892" || items[2].Name != "ijattjp" {
			t.Errorf("Item 2: raw=%q name=%q", items[2].Raw, items[2].Name)
		}
		// 4. Pipe
		if items[3].Raw != "081234567893" || items[3].Name != "ari421" {
			t.Errorf("Item 3: raw=%q name=%q", items[3].Raw, items[3].Name)
		}
		// 5. Semicolon
		if items[4].Raw != "081234567894" || items[4].Name != "Citra" {
			t.Errorf("Item 4: raw=%q name=%q", items[4].Raw, items[4].Name)
		}
		// 6. Tab
		if items[5].Raw != "081234567895" || items[5].Name != "Dewi Sartika" {
			t.Errorf("Item 5: raw=%q name=%q", items[5].Raw, items[5].Name)
		}
		// 7. Extra custom attributes
		if items[6].Raw != "081234567896" || items[6].Name != "Eko" || items[6].CustomData["usn"] != "eko_usn" {
			t.Errorf("Item 6: raw=%q name=%q custom=%+v", items[6].Raw, items[6].Name, items[6].CustomData)
		}
		// 8. Name first, number second
		if items[7].Raw != "081234567897" || items[7].Name != "Siti Aminah" {
			t.Errorf("Item 7: raw=%q name=%q", items[7].Raw, items[7].Name)
		}
	})

	t.Run("CSV formats with and without header", func(t *testing.T) {
		csvWithHeader := `nama,nomor,username
Budi Santoso,081234567890,budi99
Andi,081234567891,andi_usn
`
		items1, err := ReadCSV([]byte(csvWithHeader))
		if err != nil {
			t.Fatalf("ReadCSV failed: %v", err)
		}
		if len(items1) != 2 {
			t.Fatalf("Expected 2 items, got %d", len(items1))
		}
		if items1[0].Raw != "081234567890" || items1[0].Name != "Budi Santoso" || items1[0].CustomData["username"] != "budi99" {
			t.Errorf("Item 0 with header mismatch: %+v", items1[0])
		}

		csvWithoutHeader := `081234567890,Budi Santoso
081234567891
081234567892,Citra,citra123
`
		items2, err := ReadCSV([]byte(csvWithoutHeader))
		if err != nil {
			t.Fatalf("ReadCSV failed: %v", err)
		}
		if len(items2) != 3 {
			t.Fatalf("Expected 3 items, got %d", len(items2))
		}
		if items2[0].Raw != "081234567890" || items2[0].Name != "Budi Santoso" {
			t.Errorf("Item 0 mismatch: %+v", items2[0])
		}
		if items2[1].Raw != "081234567891" || items2[1].Name != "" {
			t.Errorf("Item 1 mismatch: %+v", items2[1])
		}
		if items2[2].Raw != "081234567892" || items2[2].Name != "Citra" || items2[2].CustomData["usn"] != "citra123" {
			t.Errorf("Item 2 mismatch: %+v", items2[2])
		}
	})

	t.Run("XLSX with names", func(t *testing.T) {
		f := excelize.NewFile()
		f.SetCellValue("Sheet1", "A1", "nomor")
		f.SetCellValue("Sheet1", "B1", "nama")
		f.SetCellValue("Sheet1", "A2", "081234567890")
		f.SetCellValue("Sheet1", "B2", "Budi Santoso")
		var buf bytes.Buffer
		f.Write(&buf)

		items, err := ReadXLSX(buf.Bytes())
		if err != nil {
			t.Fatalf("ReadXLSX failed: %v", err)
		}
		if len(items) != 1 || items[0].Raw != "081234567890" || items[0].Name != "Budi Santoso" {
			t.Errorf("Unexpected XLSX result: %+v", items)
		}
	})
}

