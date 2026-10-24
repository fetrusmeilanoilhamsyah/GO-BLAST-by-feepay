package numbers

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
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

// CountryOption merepresentasikan pilihan kode wilayah ISO 3166 dan kode telepon untuk dropdown.
type CountryOption struct {
	RegionCode  string `json:"region_code"`
	CountryCode int    `json:"country_code"`
	Label       string `json:"label"`
}

func (c CountryOption) Region() string   { return c.RegionCode }
func (c CountryOption) CallingCode() int { return c.CountryCode }
func (c CountryOption) Name() string     { return c.RegionCode }

// SupportedCountryOptions mengembalikan daftar wilayah yang didukung dari phonenumbers.GetSupportedRegions
// terurut secara alfabetis berdasarkan kode wilayah (contoh "ID (+62)").
func SupportedCountryOptions() []CountryOption {
	regions := phonenumbers.GetSupportedRegions()
	opts := make([]CountryOption, 0, len(regions))
	for reg := range regions {
		if reg == "" || reg == "001" {
			continue
		}
		cc := phonenumbers.GetCountryCodeForRegion(reg)
		if cc <= 0 {
			continue
		}
		opts = append(opts, CountryOption{
			RegionCode:  reg,
			CountryCode: cc,
			Label:       fmt.Sprintf("%s (+%d)", reg, cc),
		})
	}
	sort.Slice(opts, func(i, j int) bool {
		return opts[i].RegionCode < opts[j].RegionCode
	})
	return opts
}

// Normalize menormalisasi nomor telepon ke format E.164 (tanpa tanda '+').
// Menerima parameter defaultRegion (kode wilayah ISO 3166-1 alpha-2, misal 'ID', 'GB', 'US').
// Mengembalikan (formattedE164, status, countryCodeStr).
// Status yang mungkin:
// - "ok": nomor seluler valid
// - "bukan_seluler": nomor telepon rumah (fixed line) atau tipe non-seluler lainnya
// - "ambigu": nomor numerik XLSX tanpa awalan 0 dan tanpa kode negara bawaan
// - "tidak_valid": format tidak dapat dipahami atau tidak valid secara aturan ITU
func stripPhoneFormatting(raw string) string {
	var sb strings.Builder
	sb.Grow(len(raw))
	for _, r := range raw {
		switch r {
		case ' ', '\t', '\n', '\r', '-', '.', '(', ')',
			'\u00a0', '\u00ad', '\u200b', '\u200c', '\u200d', '\u200e', '\u200f',
			'\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u202f',
			'\u2060', '\u2066', '\u2067', '\u2068', '\u2069', '\ufeff':
			continue
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func Normalize(raw string, defaultRegion string, isNumericXLSX ...bool) (string, string, string) {
	// a. Buang spasi, tanda hubung, titik, kurung, dan karakter mark tak terlihat (mis. hasil salin dari WhatsApp)
	clean := stripPhoneFormatting(raw)
	if clean == "" {
		return "", "tidak_valid", ""
	}

	// Hanya karakter '+' di awal dan angka '0'-'9' yang diizinkan
	for i, r := range clean {
		if i == 0 && r == '+' {
			continue
		}
		if r < '0' || r > '9' {
			return "", "tidak_valid", ""
		}
	}

	// Penentuan region ISO 3166 default (default "ID")
	region := strings.ToUpper(strings.TrimSpace(defaultRegion))
	if region == "" {
		region = "ID"
	}
	// Toleransi jika pemanggil lama mengirimkan kode telepon angka (misal "62"):
	if ccInt, err := strconv.Atoi(region); err == nil && ccInt > 0 {
		if reg := phonenumbers.GetRegionCodeForCountryCode(ccInt); reg != "" && reg != "001" {
			region = reg
		}
	}

	defaultCCInt := phonenumbers.GetCountryCodeForRegion(region)
	defaultCC := fmt.Sprintf("%d", defaultCCInt)

	// Pemeriksaan sel numerik XLSX tanpa 0 awal (Tahap H14 Butir 2):
	// Tetap "ambigu" kecuali jika diawali kode telepon negara bawaan
	if len(isNumericXLSX) > 0 && isNumericXLSX[0] {
		digitsOnly := strings.TrimPrefix(clean, "+")
		if !strings.HasPrefix(digitsOnly, defaultCC) {
			return "", "ambigu", ""
		}
	}

	var num *phonenumbers.PhoneNumber
	var parseErr error

	if strings.HasPrefix(clean, "+") {
		// Format internasional dengan '+'
		num, parseErr = phonenumbers.Parse(clean, "")
	} else if strings.HasPrefix(clean, "00") {
		// Format internasional dengan awalan '00'
		num, parseErr = phonenumbers.Parse("+"+clean[2:], "")
	} else {
		// Coba parse sebagai nomor nasional untuk region yang dipilih
		numNat, errNat := phonenumbers.Parse(clean, region)
		if errNat == nil && phonenumbers.IsValidNumber(numNat) {
			num = numNat
		} else {
			// Jika tidak valid secara nasional, coba sebagai nomor internasional tanpa tanda '+'
			numIntl, errIntl := phonenumbers.Parse("+"+clean, "")
			if errIntl == nil && phonenumbers.IsValidNumber(numIntl) {
				num = numIntl
			} else {
				return "", "tidak_valid", ""
			}
		}
	}

	if parseErr != nil || num == nil || !phonenumbers.IsValidNumber(num) {
		return "", "tidak_valid", ""
	}

	// Pemeriksaan jenis nomor: hanya nomor seluler (MOBILE) atau FIXED_LINE_OR_MOBILE (Tahap H14 Butir 3)
	numType := phonenumbers.GetNumberType(num)
	if numType != phonenumbers.MOBILE && numType != phonenumbers.FIXED_LINE_OR_MOBILE {
		return "", "bukan_seluler", fmt.Sprintf("%d", num.GetCountryCode())
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
		// Validasi magic bytes format XLSX / ZIP (PK\x03\x04)
		if len(data) < 4 || data[0] != 0x50 || data[1] != 0x4b || data[2] != 0x03 || data[3] != 0x04 {
			return nil, errors.New("file XLSX tidak valid (format arsip rusak)")
		}
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
		custom["username"] = parts[2]
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
	idColIdx := -1
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
		} else if lowerH == "username" || lowerH == "usn" {
			usnColIdx = i
			hasHeader = true
		} else if lowerH == "id" || lowerH == "user_id" || lowerH == "userid" {
			idColIdx = i
			hasHeader = true
		}
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
							colName = NormalizeCustomKey(header[cIdx])
						}
						custom[colName] = tVal
					}
				}
			}
			if usnColIdx >= 0 && usnColIdx < len(row) {
				val := strings.TrimSpace(row[usnColIdx])
				if val != "" {
					if custom == nil {
						custom = make(map[string]string)
					}
					custom["usn"] = val
					custom["username"] = val
				}
			}
			if idColIdx >= 0 && idColIdx < len(row) {
				val := strings.TrimSpace(row[idColIdx])
				if val != "" {
					if custom == nil {
						custom = make(map[string]string)
					}
					custom["id"] = val
				}
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
	idColIdx := -1
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
		} else if lowerH == "username" || lowerH == "usn" {
			usnColIdx = i
			hasHeader = true
		} else if lowerH == "id" || lowerH == "user_id" || lowerH == "userid" {
			idColIdx = i
			hasHeader = true
		}
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
							colName = NormalizeCustomKey(header[cIdx])
						}
						custom[colName] = tVal
					}
				}
			}
			if usnColIdx >= 0 && usnColIdx < len(row) {
				val := strings.TrimSpace(row[usnColIdx])
				if val != "" {
					if custom == nil {
						custom = make(map[string]string)
					}
					custom["usn"] = val
					custom["username"] = val
				}
			}
			if idColIdx >= 0 && idColIdx < len(row) {
				val := strings.TrimSpace(row[idColIdx])
				if val != "" {
					if custom == nil {
						custom = make(map[string]string)
					}
					custom["id"] = val
				}
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

// NormalizeCustomKey menormalisasi nama variabel/kolom menjadi huruf kecil (a-z, 0-9, _)
// serta memetakan alias umum (usn, username, user -> usn; id, userid, user_id -> id; nama, name -> nama).
func NormalizeCustomKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	switch k {
	case "username", "user", "usn":
		return "usn"
	case "id", "userid", "user_id":
		return "id"
	case "nama", "name":
		return "nama"
	}
	var sb strings.Builder
	for _, r := range k {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return strings.Trim(sb.String(), "_")
}
