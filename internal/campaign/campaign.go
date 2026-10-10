package campaign

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
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
	CreatedBy  int64      `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

type Elements struct {
	CSActive      bool   `json:"cs_active"`
	CSNumber      string `json:"cs_number"` // "link wa.me/<nomor>" or just URL

	LinkWebActive bool   `json:"link_web_active"`
	LinkWebURL    string `json:"link_web_url"`

	PhoneActive   bool   `json:"phone_active"`
	PhoneNumber   string `json:"phone_number"` // E.164

	PromoActive   bool   `json:"promo_active"`
	PromoCode     string `json:"promo_code"`
	PromoURL      string `json:"promo_url"`
}

type Button struct {
	Type    string
	Text    string
	Payload string
}

type Message struct {
	Text      string
	ImagePath string
	Buttons   []Button
}

func ValidateTemplate(body string, mode string, elements Elements) error {
	if len(body) == 0 {
		return errors.New("body pesan wajib diisi")
	}
	if len(body) > 1000 {
		return errors.New("body pesan tidak boleh lebih dari 1000 karakter")
	}
	if !strings.Contains(body, "FEEPAY") {
		return errors.New("body pesan harus memuat kata FEEPAY")
	}

	if !elements.CSActive {
		return errors.New("elemen CS wajib aktif")
	}
	if elements.CSNumber == "" {
		return errors.New("nomor/link CS wajib diisi")
	}
	if strings.HasPrefix(elements.CSNumber, "http") {
		if err := checkURL(elements.CSNumber); err != nil {
			return fmt.Errorf("link CS tidak valid: %w", err)
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
	}

	if elements.PromoActive {
		if elements.PromoCode == "" || elements.PromoURL == "" {
			return errors.New("kode dan link promo harus diisi jika aktif")
		}
		if err := checkURL(elements.PromoURL); err != nil {
			return fmt.Errorf("link promo tidak valid: %w", err)
		}
	}

	if mode != ModeKartuLink && mode != ModeTombolAsli {
		return errors.New("mode tidak valid")
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
	return nil
}

func Render(body string, imagePath string, elements Elements, mode string) Message {
	msg := Message{
		ImagePath: imagePath,
	}

	csLink := elements.CSNumber
	if !strings.HasPrefix(csLink, "http") {
		csLink = "https://wa.me/" + csLink
	}

	if mode == ModeTombolAsli {
		msg.Text = body
		
		if elements.LinkWebActive {
			msg.Buttons = append(msg.Buttons, Button{Type: "url", Text: "Kunjungi Web", Payload: elements.LinkWebURL})
		}
		if elements.PhoneActive {
			msg.Buttons = append(msg.Buttons, Button{Type: "phone", Text: "Telepon", Payload: elements.PhoneNumber})
		}
		if elements.PromoActive {
			msg.Buttons = append(msg.Buttons, Button{Type: "copy", Text: "Salin Promo", Payload: elements.PromoCode})
			msg.Buttons = append(msg.Buttons, Button{Type: "url", Text: "Gunakan Promo", Payload: elements.PromoURL})
		}
		
		msg.Buttons = append(msg.Buttons, Button{Type: "url", Text: "Hubungi CS", Payload: csLink})

	} else if mode == ModeKartuLink {
		lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
		if len(lines) > 0 && lines[0] != "" {
			if !strings.HasPrefix(lines[0], "*") && !strings.HasSuffix(lines[0], "*") {
				lines[0] = "*" + lines[0] + "*"
			}
		}
		
		var sb strings.Builder
		sb.WriteString(strings.Join(lines, "\n"))
		sb.WriteString("\n\n---\n")
		
		if elements.LinkWebActive {
			sb.WriteString(fmt.Sprintf("Web: %s\n", elements.LinkWebURL))
		}
		if elements.PromoActive {
			sb.WriteString(fmt.Sprintf("Promo (%s): %s\n", elements.PromoCode, elements.PromoURL))
		}
		if elements.PhoneActive {
			sb.WriteString(fmt.Sprintf("Telp: %s\n", elements.PhoneNumber))
		}
		
		sb.WriteString(fmt.Sprintf("CS: %s", csLink))
		
		msg.Text = sb.String()
	}

	return msg
}
