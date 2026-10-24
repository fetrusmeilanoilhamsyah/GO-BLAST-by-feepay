package campaign

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"goblast/internal/numbers"
)

const (
	ModeTombolAsli = "tombol_asli"
	ModeKartuLink  = "kartu_link"
	StatusDraf     = "draf"
	StatusBerjalan = "berjalan"
	StatusSelesai  = "selesai"
)

type Campaign struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	ButtonMode string     `json:"button_mode"`
	Body       string     `json:"body"`
	ImagePath  string     `json:"image_path"`
	Elements   Elements   `json:"elements"`
	CreatedBy      int64          `json:"created_by"`
	CreatedAt      time.Time      `json:"created_at"`
	StartedAt      *time.Time     `json:"started_at"`
	FinishedAt     *time.Time     `json:"finished_at"`
	ArchivedAt     *time.Time     `json:"archived_at"`
	PurgedAt       *time.Time     `json:"purged_at"`
	PurgeSummary   map[string]int `json:"purge_summary"`
	DefaultCountry string         `json:"default_country"`
}

func (c Campaign) IsPurged() bool {
	return c.PurgedAt != nil
}


type Elements struct {
	CSActive bool   `json:"cs_active"`
	CSNumber string `json:"cs_number"` // "link wa.me/<nomor>" or just URL

	LinkWebActive bool   `json:"link_web_active"`
	LinkWebURL    string `json:"link_web_url"`

	PhoneActive bool   `json:"phone_active"`
	PhoneNumber string `json:"phone_number"` // E.164

	PromoActive bool   `json:"promo_active"`
	PromoCode   string `json:"promo_code"`
	PromoURL    string `json:"promo_url"`
}

type Button struct {
	Type    string
	Text    string
	Payload string
}

// Preview menyimpan metadata pratinjau tautan untuk kartu_link.
type Preview struct {
	Title       string
	Description string
	URL         string
	Thumbnail   []byte // JPEG bytes, lebar 300px, kualitas 70 (ThumbnailSpec)
}

type Message struct {
	Text      string
	ImagePath string
	Buttons   []Button
	Preview   *Preview // Diisi untuk mode kartu_link jika terdapat tautan
}

func ValidateTemplate(body string, mode string, elements Elements, defaultRegion ...string) error {
	if mode != ModeKartuLink && mode != ModeTombolAsli {
		return errors.New("mode tidak valid")
	}

	runeCount := utf8.RuneCountInString(body)
	if runeCount == 0 {
		return errors.New("body pesan wajib diisi")
	}
	if runeCount > 1000 {
		return errors.New("body pesan tidak boleh lebih dari 1000 karakter")
	}

	if elements.CSActive {
		if elements.CSNumber == "" {
			return errors.New("nomor/link CS wajib diisi")
		}
		if strings.HasPrefix(elements.CSNumber, "http") {
			if err := checkURL(elements.CSNumber); err != nil {
				return fmt.Errorf("link CS tidak valid: %w", err)
			}
		}
	}

	if elements.LinkWebActive {
		if elements.LinkWebURL == "" {
			return errors.New("link web harus diisi jika aktif")
		}
		if err := checkURL(elements.LinkWebURL); err != nil {
			return fmt.Errorf("link web tidak valid: %w", err)
		}
	}

	if elements.PhoneActive {
		if elements.PhoneNumber == "" {
			return errors.New("nomor telepon harus diisi jika aktif")
		}
		region := "ID"
		if len(defaultRegion) > 0 && defaultRegion[0] != "" {
			region = defaultRegion[0]
		}
		_, status, _ := numbers.Normalize(elements.PhoneNumber, region)
		if status != "ok" {
			return errors.New("nomor telepon tidak valid")
		}
	}

	if elements.PromoActive {
		if elements.PromoCode == "" || elements.PromoURL == "" {
			return errors.New("kode dan link promo harus diisi jika aktif")
		}
		if err := checkURL(elements.PromoURL); err != nil {
			return fmt.Errorf("link promo tidak valid: %w", err)
		}
	}

	return nil
}

func checkURL(u string) error {
	parsed, err := url.Parse(u)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" {
		return errors.New("URL harus menggunakan https")
	}
	if parsed.Host == "" {
		return errors.New("URL harus memiliki host yang valid")
	}
	return nil
}

// Recipient memuat data penerima pesan untuk pengisian variabel template.
type Recipient struct {
	Phone      string            `json:"phone"`
	Name       string            `json:"name"`
	CustomData map[string]string `json:"custom_data"`
}

// Template membungkus konfigurasi template kampanye untuk rendering pesan.
type Template struct {
	Body       string   `json:"body"`
	ImagePath  string   `json:"image_path"`
	Elements   Elements `json:"elements"`
	ButtonMode string   `json:"button_mode"`
}

var variableRegex = regexp.MustCompile(`\{\{?([a-zA-Z0-9_]+)(?:\|([^}]+))?\}?\}`)

// SanitizeVariableValue membersihkan nilai variabel:
// 1. Menghapus karakter kontrol (ASCII < 32 atau 127) dan baris baru (\r, \n)
// 2. Memotong panjang maksimal 60 karakter (runes)
// 3. Menetralkan karakter format WhatsApp (*, _, ~, `) agar nama tidak merusak format pesan.
func SanitizeVariableValue(val string) string {
	if val == "" {
		return ""
	}
	var sb strings.Builder
	count := 0
	for _, r := range val {
		// Hapus karakter kontrol dan baris baru
		if r < 32 || r == 127 {
			continue
		}
		// Netralkan karakter format WhatsApp
		if r == '*' || r == '_' || r == '~' || r == '`' {
			continue
		}
		sb.WriteRune(r)
		count++
		if count >= 60 {
			break
		}
	}
	return strings.TrimSpace(sb.String())
}

// NormalizeCustomKey menormalisasi nama variabel/kolom menjadi huruf kecil (a-z, 0-9, _)
// serta memetakan alias umum (usn, username, user -> usn/username; id, userid, user_id -> id; nama, name -> nama/name).
func NormalizeCustomKey(k string) string {
	return numbers.NormalizeCustomKey(k)
}

// FindTemplateVariables mengembalikan daftar nama variabel unik yang dipakai di dalam body template.
func FindTemplateVariables(body string) []string {
	matches := variableRegex.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool)
	var result []string
	for _, m := range matches {
		if len(m) >= 2 {
			k := NormalizeCustomKey(m[1])
			if !seen[k] {
				seen[k] = true
				result = append(result, k)
			}
		}
	}
	return result
}

// ReplaceVariables substitutes template placeholders such as {nama}, {{name}}, {nama|Kak}, {usn}, {id}.
// Selalu dijalankan untuk setiap pesan:
// - Jika variabel ada di vars, diganti dengan nilainya yang sudah disanitasi.
// - Jika tidak ada di vars, gunakan fallback jika tersedia ({nama|Kak} -> "Kak").
// - Jika tidak ada fallback, placeholder dihapus bersih dan spasi sebelum tanda baca dirapikan.
func ReplaceVariables(body string, vars map[string]string) string {
	lookup := make(map[string]string)
	for k, v := range vars {
		sanitizedKey := NormalizeCustomKey(k)
		lookup[sanitizedKey] = SanitizeVariableValue(v)
	}

	// Sinkronisasi alias baku
	if val, ok := lookup["nama"]; ok && val != "" {
		lookup["name"] = val
	} else if val, ok := lookup["name"]; ok && val != "" {
		lookup["nama"] = val
	}

	if val, ok := lookup["usn"]; ok && val != "" {
		lookup["username"] = val
	} else if val, ok := lookup["username"]; ok && val != "" {
		lookup["usn"] = val
	}

	replaced := variableRegex.ReplaceAllStringFunc(body, func(match string) string {
		sub := variableRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		varName := NormalizeCustomKey(sub[1])
		fallback := ""
		if len(sub) >= 3 {
			fallback = SanitizeVariableValue(sub[2])
		}

		if val, ok := lookup[varName]; ok && val != "" {
			return val
		}
		if fallback != "" {
			return fallback
		}
		return ""
	})

	lines := strings.Split(replaced, "\n")
	for i, l := range lines {
		l = strings.ReplaceAll(l, " ,", ",")
		l = strings.ReplaceAll(l, " .", ".")
		l = strings.ReplaceAll(l, " !", "!")
		l = strings.ReplaceAll(l, " ?", "?")
		l = strings.ReplaceAll(l, " :", ":")
		l = strings.ReplaceAll(l, " ;", ";")
		for strings.Contains(l, "  ") {
			l = strings.ReplaceAll(l, "  ", " ")
		}
		lines[i] = strings.TrimRight(l, " ")
	}

	return strings.Join(lines, "\n")
}

// RenderForRecipient menyusun pesan menggunakan satu jalur kode terpadu untuk pratinjau dan kirim.
func RenderForRecipient(tmpl Template, r Recipient) Message {
	vars := make(map[string]string)
	for k, v := range r.CustomData {
		vars[k] = v
	}
	if r.Name != "" {
		vars["nama"] = r.Name
		vars["name"] = r.Name
	}
	if r.Phone != "" {
		vars["phone"] = r.Phone
		vars["nomor"] = r.Phone
	}
	return Render(tmpl.Body, tmpl.ImagePath, tmpl.Elements, tmpl.ButtonMode, vars)
}

func Render(body string, imagePath string, elements Elements, mode string, recipientVars ...map[string]string) Message {
	var vars map[string]string
	if len(recipientVars) > 0 {
		vars = recipientVars[0]
	}
	body = ReplaceVariables(body, vars)

	msg := Message{
		ImagePath: imagePath,
	}

	var csLink string
	if elements.CSActive && elements.CSNumber != "" {
		csLink = elements.CSNumber
		if !strings.HasPrefix(csLink, "http") {
			csLink = "https://wa.me/" + csLink
		}
	}

	if mode == ModeTombolAsli {
		msg.Text = body

		if elements.LinkWebActive && elements.LinkWebURL != "" {
			msg.Buttons = append(msg.Buttons, Button{Type: "url", Text: "Kunjungi Web", Payload: elements.LinkWebURL})
		}
		if elements.PhoneActive && elements.PhoneNumber != "" {
			msg.Buttons = append(msg.Buttons, Button{Type: "phone", Text: "Telepon", Payload: elements.PhoneNumber})
		}
		if elements.PromoActive && elements.PromoCode != "" && elements.PromoURL != "" {
			msg.Buttons = append(msg.Buttons, Button{Type: "copy", Text: "Salin Promo", Payload: elements.PromoCode})
			msg.Buttons = append(msg.Buttons, Button{Type: "url", Text: "Gunakan Promo", Payload: elements.PromoURL})
		}
		if elements.CSActive && csLink != "" {
			msg.Buttons = append(msg.Buttons, Button{Type: "url", Text: "Hubungi CS", Payload: csLink})
		}

	} else if mode == ModeKartuLink {
		lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
		if len(lines) > 0 && lines[0] != "" {
			if !strings.HasPrefix(lines[0], "*") && !strings.HasSuffix(lines[0], "*") {
				lines[0] = "*" + lines[0] + "*"
			}
		}

		var sb strings.Builder
		sb.WriteString(strings.Join(lines, "\n"))

		hasElements := (elements.LinkWebActive && elements.LinkWebURL != "") ||
			(elements.PromoActive && elements.PromoURL != "") ||
			(elements.PhoneActive && elements.PhoneNumber != "") ||
			(elements.CSActive && csLink != "")

		if hasElements {
			sb.WriteString("\n\n---\n")
			if elements.LinkWebActive && elements.LinkWebURL != "" {
				sb.WriteString(fmt.Sprintf("Web: %s\n", elements.LinkWebURL))
			}
			if elements.PromoActive && elements.PromoURL != "" {
				sb.WriteString(fmt.Sprintf("Promo (%s): %s\n", elements.PromoCode, elements.PromoURL))
			}
			if elements.PhoneActive && elements.PhoneNumber != "" {
				sb.WriteString(fmt.Sprintf("Telp: %s\n", elements.PhoneNumber))
			}
			if elements.CSActive && csLink != "" {
				sb.WriteString(fmt.Sprintf("CS: %s\n", csLink))
			}
		}

		msg.Text = strings.TrimRight(sb.String(), "\n")

		var previewURL string
		if elements.LinkWebActive && elements.LinkWebURL != "" {
			previewURL = elements.LinkWebURL
		} else if elements.PromoActive && elements.PromoURL != "" {
			previewURL = elements.PromoURL
		} else if elements.CSActive && csLink != "" {
			previewURL = csLink
		}

		if previewURL != "" {
			title := ""
			if len(lines) > 0 {
				title = strings.Trim(lines[0], "* \t")
			}
			desc := ""
			if len(lines) > 1 {
				var descParts []string
				for _, l := range lines[1:] {
					t := strings.TrimSpace(l)
					if t != "" {
						descParts = append(descParts, t)
					}
				}
				desc = strings.Join(descParts, " ")
				if len(desc) > 120 {
					desc = desc[:120] + "..."
				}
			}
			if desc == "" {
				desc = "Klik tautan untuk informasi selengkapnya."
			}

			var thumb []byte
			if imagePath != "" && !strings.HasPrefix(imagePath, "data:") {
				var thumbErr error
				thumb, thumbErr = GetOrCreateThumbnail(imagePath, DefaultThumbWidth, DefaultThumbQuality)
				if thumbErr != nil {
					slog.Error("campaign: gagal membuat thumbnail kartu_link", "image_path", imagePath, "err", thumbErr)
				}
			}

			msg.Preview = &Preview{
				Title:       title,
				Description: desc,
				URL:         previewURL,
				Thumbnail:   thumb,
			}
		}
	}

	return msg
}

func RenderWithRecipient(body string, imagePath string, elements Elements, mode string, recipientName string) Message {
	return Render(body, imagePath, elements, mode, map[string]string{"nama": recipientName, "name": recipientName})
}
