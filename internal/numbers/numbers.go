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

type Item struct {
	Raw           string
	Name          string
	CustomData    map[string]string
	IsNumericXLSX bool
}

// Normalize parses and normalizes a phone number to E.164 format without the '+' sign.
// Returns normalized string, status ("ok", "tidak_valid", "ambigu"), and country code string.
// If isNumericXLSX is true (from XLSX numeric cell or read from Excel without leading 0),
// and the number does not start with defaultCC, status is reported as "ambigu".
func Normalize(raw string, defaultCC string, isNumericXLSX ...bool) (string, string, string) {
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

	if len(isNumericXLSX) > 0 && isNumericXLSX[0] {
		digitsOnly := strings.TrimPrefix(clean, "+")
		if !strings.HasPrefix(digitsOnly, defaultCC) {
			return "", "ambigu", ""
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

func Read(r io.Reader, filename string) ([]Item, error) {
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
		return ReadCSV(data)
	} else if strings.HasSuffix(lowerName, ".txt") {
		return ReadTXT(data)
	} else if strings.HasSuffix(lowerName, ".xlsx") {
		return ReadXLSX(data)
	}
	return nil, ErrUnsupported
}

func isPhoneLike(s string) bool {
	digits := 0
	letters := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits++
		} else if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			letters++
		}
	}
	return digits >= 5 && letters == 0
}

func splitLineFields(line string) (string, string, map[string]string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", nil
	}

	var seps = []string{"\t", "|", ";", ",", ":"}
	var parts []string
	for _, sep := range seps {
		if strings.Contains(line, sep) {
			split := strings.Split(line, sep)
			var nonEmp []string
			for _, p := range split {
				tp := strings.TrimSpace(p)
				if tp != "" {
					nonEmp = append(nonEmp, tp)
				}
			}
			if len(nonEmp) > 1 {
				parts = nonEmp
				break
			}
		}
	}

	if len(parts) == 0 {
		return line, "", nil
	}

	raw := parts[0]
	name := parts[1]
	if !isPhoneLike(parts[0]) && isPhoneLike(parts[1]) {
		raw = parts[1]
		name = parts[0]
	}

	var custom map[string]string
	if len(parts) > 2 {
		custom = make(map[string]string)
		for i := 2; i < len(parts); i++ {
			custom[fmt.Sprintf("col%d", i)] = parts[i]
		}
		custom["usn"] = parts[2]
		custom["id"] = parts[2]
	}

	return raw, name, custom
}

func ReadCSV(data []byte) ([]Item, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("gagal membaca CSV: %w", err)
	}

	if len(records) == 0 {
		return []Item{}, nil
	}

	if len(records) > maxRows {
		return nil, ErrTooManyRows
	}

	phoneColIdx := -1
	nameColIdx := -1
	usnColIdx := -1
	header := records[0]
	hasHeader := false
	for i, h := range header {
		lowerH := strings.ToLower(strings.TrimSpace(h))
		if lowerH == "nomor" || lowerH == "phone" || lowerH == "telepon" || lowerH == "telp" || lowerH == "no" || lowerH == "hp" || lowerH == "handphone" || lowerH == "wa" || lowerH == "whatsapp" {
			phoneColIdx = i
			hasHeader = true
		} else if lowerH == "nama" || lowerH == "name" || lowerH == "full_name" || lowerH == "fullname" || lowerH == "kontak" || lowerH == "contact" || lowerH == "customer" {
			nameColIdx = i
			hasHeader = true
		} else if lowerH == "username" || lowerH == "usn" || lowerH == "id" || lowerH == "user_id" {
			usnColIdx = i
			hasHeader = true
		}
	}

	if nameColIdx == -1 && usnColIdx != -1 {
		nameColIdx = usnColIdx
	}

	startIndex := 0
	if hasHeader {
		startIndex = 1
		if phoneColIdx == -1 {
			phoneColIdx = 0
		}
	}

	var items []Item
	for i := startIndex; i < len(records); i++ {
		row := records[i]
		if len(row) == 0 {
			continue
		}

		var raw, name string
		var custom map[string]string

		if hasHeader {
			if phoneColIdx >= 0 && phoneColIdx < len(row) {
				raw = strings.TrimSpace(row[phoneColIdx])
			}
			if nameColIdx >= 0 && nameColIdx < len(row) {
				name = strings.TrimSpace(row[nameColIdx])
			}
			for cIdx, cVal := range row {
				if cIdx != phoneColIdx && cIdx != nameColIdx {
					tVal := strings.TrimSpace(cVal)
					if tVal != "" {
						if custom == nil {
							custom = make(map[string]string)
						}
						colName := fmt.Sprintf("col%d", cIdx)
						if cIdx < len(header) {
							colName = strings.ToLower(strings.TrimSpace(header[cIdx]))
						}
						custom[colName] = tVal
					}
				}
			}
			if usnColIdx >= 0 && usnColIdx != nameColIdx && usnColIdx < len(row) {
				if custom == nil {
					custom = make(map[string]string)
				}
				custom["usn"] = strings.TrimSpace(row[usnColIdx])
			}
		} else {
			if len(row) == 1 {
				raw = strings.TrimSpace(row[0])
			} else if len(row) >= 2 {
				p0 := strings.TrimSpace(row[0])
				p1 := strings.TrimSpace(row[1])
				if !isPhoneLike(p0) && isPhoneLike(p1) {
					raw = p1
					name = p0
				} else {
					raw = p0
					name = p1
				}
				if len(row) > 2 {
					custom = make(map[string]string)
					for cIdx := 2; cIdx < len(row); cIdx++ {
						tVal := strings.TrimSpace(row[cIdx])
						if tVal != "" {
							custom[fmt.Sprintf("col%d", cIdx)] = tVal
							if cIdx == 2 {
								custom["usn"] = tVal
								custom["id"] = tVal
							}
						}
					}
				}
			}
		}

		if raw != "" {
			items = append(items, Item{
				Raw:           raw,
				Name:          name,
				CustomData:    custom,
				IsNumericXLSX: false,
			})
		}
	}

	return items, nil
}

func ReadTXT(data []byte) ([]Item, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxRows {
		return nil, ErrTooManyRows
	}

	var items []Item
	for _, line := range lines {
		raw, name, custom := splitLineFields(line)
		if raw != "" {
			items = append(items, Item{
				Raw:           raw,
				Name:          name,
				CustomData:    custom,
				IsNumericXLSX: false,
			})
		}
	}
	return items, nil
}

func ReadXLSX(data []byte) ([]Item, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gagal membaca XLSX: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return []Item{}, nil
	}

	sheetName := sheets[0]
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca baris XLSX: %w", err)
	}

	if len(rows) > maxRows {
		return nil, ErrTooManyRows
	}

	if len(rows) == 0 {
		return []Item{}, nil
	}

	phoneColIdx := -1
	nameColIdx := -1
	usnColIdx := -1
	hasHeader := false
	header := rows[0]
	for i, h := range header {
		lowerH := strings.ToLower(strings.TrimSpace(h))
		if lowerH == "nomor" || lowerH == "phone" || lowerH == "telepon" || lowerH == "telp" || lowerH == "no" || lowerH == "hp" || lowerH == "handphone" || lowerH == "wa" || lowerH == "whatsapp" {
			phoneColIdx = i
			hasHeader = true
		} else if lowerH == "nama" || lowerH == "name" || lowerH == "full_name" || lowerH == "fullname" || lowerH == "kontak" || lowerH == "contact" || lowerH == "customer" {
			nameColIdx = i
			hasHeader = true
		} else if lowerH == "username" || lowerH == "usn" || lowerH == "id" || lowerH == "user_id" {
			usnColIdx = i
			hasHeader = true
		}
	}

	if nameColIdx == -1 && usnColIdx != -1 {
		nameColIdx = usnColIdx
	}

	startIndex := 0
	if hasHeader {
		startIndex = 1
		if phoneColIdx == -1 {
			phoneColIdx = 0
		}
	}

	var items []Item
	for rIdx := startIndex; rIdx < len(rows); rIdx++ {
		row := rows[rIdx]
		if len(row) == 0 {
			continue
		}

		var raw, name string
		var custom map[string]string
		phoneColTarget := 0

		if hasHeader {
			phoneColTarget = phoneColIdx
			if phoneColIdx >= 0 && phoneColIdx < len(row) {
				raw = strings.TrimSpace(row[phoneColIdx])
			}
			if nameColIdx >= 0 && nameColIdx < len(row) {
				name = strings.TrimSpace(row[nameColIdx])
			}
			for cIdx, cVal := range row {
				if cIdx != phoneColIdx && cIdx != nameColIdx {
					tVal := strings.TrimSpace(cVal)
					if tVal != "" {
						if custom == nil {
							custom = make(map[string]string)
						}
						colName := fmt.Sprintf("col%d", cIdx)
						if cIdx < len(header) {
							colName = strings.ToLower(strings.TrimSpace(header[cIdx]))
						}
						custom[colName] = tVal
					}
				}
			}
			if usnColIdx >= 0 && usnColIdx != nameColIdx && usnColIdx < len(row) {
				if custom == nil {
					custom = make(map[string]string)
				}
				custom["usn"] = strings.TrimSpace(row[usnColIdx])
			}
		} else {
			if len(row) == 1 {
				raw = strings.TrimSpace(row[0])
				phoneColTarget = 0
			} else if len(row) >= 2 {
				p0 := strings.TrimSpace(row[0])
				p1 := strings.TrimSpace(row[1])
				if !isPhoneLike(p0) && isPhoneLike(p1) {
					raw = p1
					name = p0
					phoneColTarget = 1
				} else {
					raw = p0
					name = p1
					phoneColTarget = 0
				}
				if len(row) > 2 {
					custom = make(map[string]string)
					for cIdx := 2; cIdx < len(row); cIdx++ {
						tVal := strings.TrimSpace(row[cIdx])
						if tVal != "" {
							custom[fmt.Sprintf("col%d", cIdx)] = tVal
							if cIdx == 2 {
								custom["usn"] = tVal
								custom["id"] = tVal
							}
						}
					}
				}
			}
		}

		if raw != "" {
			cellName, _ := excelize.CoordinatesToCellName(phoneColTarget+1, rIdx+1)
			cType, _ := f.GetCellType(sheetName, cellName)
			isNumeric := (cType == excelize.CellTypeNumber) || (!strings.HasPrefix(raw, "0") && !strings.HasPrefix(raw, "+") && !strings.HasPrefix(raw, "00"))
			items = append(items, Item{
				Raw:           raw,
				Name:          name,
				CustomData:    custom,
				IsNumericXLSX: isNumeric,
			})
		}
	}
	return items, nil
}
