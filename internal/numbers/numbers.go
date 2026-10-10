package numbers

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nyaruka/phonenumbers"
	"github.com/xuri/excelize/v2"
)

var (
	ErrFileTooLarge = errors.New("file terlalu besar (batas 5 MB)")
	ErrTooManyRows  = errors.New("terlalu banyak baris (batas 100.000)")
	ErrUnsupported  = errors.New("format file tidak didukung")
)

// Normalize parses and normalizes a phone number to E.164 format without the '+' sign.
// Returns normalized string, status ("ok", "tidak_valid", "ambigu"), and country code string.
func Normalize(raw string, defaultCC string) (string, string, string) {
	// a. Buang spasi, tanda hubung, titik, dan kurung
	clean := strings.NewReplacer(" ", "", "-", "", ".", "", "(", "", ")", "").Replace(raw)
	if clean == "" {
		return "", "tidak_valid", ""
	}
	
	// Check if there are invalid characters (only + and digits are allowed)
	for i, r := range clean {
		if i == 0 && r == '+' {
			continue
		}
		if r < '0' || r > '9' {
			return "", "tidak_valid", ""
		}
	}

	var toParse string

	// b. Awalan + atau 00: dianggap internasional
	if strings.HasPrefix(clean, "+") {
		toParse = clean
	} else if strings.HasPrefix(clean, "00") {
		toParse = "+" + clean[2:]
	} else if strings.HasPrefix(clean, "0") {
		// c. Awalan 0 tunggal (bukan 00): buang 0, tambahkan kode negara default
		toParse = "+" + defaultCC + clean[1:]
	} else {
		// d. Hanya angka tanpa awalan
		toParse = "+" + clean
	}

	num, err := phonenumbers.Parse(toParse, "")
	if err != nil {
		return "", "tidak_valid", ""
	}

	// e. Nomor valid hanya jika phonenumbers.IsValidNumber
	if !phonenumbers.IsValidNumber(num) {
		return "", "tidak_valid", ""
	}

	formatted := phonenumbers.Format(num, phonenumbers.E164)
	formatted = strings.TrimPrefix(formatted, "+")
	ccStr := fmt.Sprintf("%d", num.GetCountryCode())

	return formatted, "ok", ccStr
}

const maxBytes = 5 * 1024 * 1024
const maxRows = 100000

func Read(r io.Reader, filename string) ([]string, error) {
	// Limit to 5MB + 1 byte to detect overflow
	lr := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, ErrFileTooLarge
	}

	lowerName := strings.ToLower(filename)
	if strings.HasSuffix(lowerName, ".csv") {
		return readCSV(data)
	} else if strings.HasSuffix(lowerName, ".txt") {
		return readTXT(data)
	} else if strings.HasSuffix(lowerName, ".xlsx") {
		return readXLSX(data)
	}
	return nil, ErrUnsupported
}

func readCSV(data []byte) ([]string, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	// Try to read header
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("gagal membaca CSV: %w", err)
	}

	if len(records) == 0 {
		return []string{}, nil
	}

	if len(records) > maxRows {
		return nil, ErrTooManyRows
	}

	colIdx := 0
	header := records[0]
	hasHeader := false
	for i, h := range header {
		lowerH := strings.ToLower(strings.TrimSpace(h))
		if lowerH == "nomor" || lowerH == "phone" {
			colIdx = i
			hasHeader = true
			break
		}
	}

	startIndex := 0
	if hasHeader {
		startIndex = 1
	}

	var nums []string
	for i := startIndex; i < len(records); i++ {
		if colIdx < len(records[i]) {
			val := strings.TrimSpace(records[i][colIdx])
			if val != "" {
				nums = append(nums, val)
			}
		}
	}

	return nums, nil
}

func readTXT(data []byte) ([]string, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxRows {
		return nil, ErrTooManyRows
	}

	var nums []string
	for _, line := range lines {
		val := strings.TrimSpace(line)
		if val != "" {
			nums = append(nums, val)
		}
	}
	return nums, nil
}

func readXLSX(data []byte) ([]string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gagal membaca XLSX: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return []string{}, nil
	}

	sheetName := sheets[0]
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca baris XLSX: %w", err)
	}

	if len(rows) > maxRows {
		return nil, ErrTooManyRows
	}

	var nums []string
	for _, row := range rows {
		if len(row) > 0 {
			val := strings.TrimSpace(row[0])
			if val != "" {
				nums = append(nums, val)
			}
		}
	}
	return nums, nil
}
