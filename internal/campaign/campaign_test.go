package campaign

import (
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateTemplate(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		mode     string
		elements Elements
		wantErr  bool
		errMsg   string
	}{
		{
			name: "Valid kartu_link",
			body: "Halo ini dari FEEPAY",
			mode: ModeKartuLink,
			elements: Elements{
				CSActive: true,
				CSNumber: "6281234567890",
			},
			wantErr: false,
		},
		{
			name: "Reject tombol_asli",
			body: "Promo FEEPAY hari ini",
			mode: ModeTombolAsli,
			elements: Elements{
				CSActive: true,
				CSNumber: "6281234567890",
			},
			wantErr: true,
			errMsg:  "mode tombol_asli dinonaktifkan",
		},
		{
			name: "Missing FEEPAY (valid)",
			body: "Halo ini pesan biasa",
			mode: ModeKartuLink,
			elements: Elements{
				CSActive: true,
				CSNumber: "62812",
			},
			wantErr: false,
		},
		{
			name: "CS inactive (valid)",
			body: "FEEPAY promo",
			mode: ModeKartuLink,
			elements: Elements{
				CSActive: false,
			},
			wantErr: false,
		},
		{
			name: "Body too long (byte vs rune check)",
			body: "FEEPAY " + strings.Repeat("A", 1001),
			mode: ModeKartuLink,
			elements: Elements{
				CSActive: true,
				CSNumber: "62812",
			},
			wantErr: true,
			errMsg:  "body pesan tidak boleh lebih dari 1000 karakter",
		},
		{
			name: "Body exactly 1000 runes including multi-byte utf8",
			body: strings.Repeat("日", 1000),
			mode: ModeKartuLink,
			elements: Elements{
				CSActive: false,
			},
			wantErr: false,
		},
		{
			name: "Body 1001 runes multi-byte utf8",
			body: strings.Repeat("日", 1001),
			mode: ModeKartuLink,
			elements: Elements{
				CSActive: false,
			},
			wantErr: true,
			errMsg:  "body pesan tidak boleh lebih dari 1000 karakter",
		},
		{
			name: "Invalid web link scheme",
			body: "FEEPAY",
			mode: ModeKartuLink,
			elements: Elements{
				CSActive:      true,
				CSNumber:      "62812",
				LinkWebActive: true,
				LinkWebURL:    "http://example.com",
			},
			wantErr: true,
		},
		{
			name: "URL empty host",
			body: "FEEPAY",
			mode: ModeKartuLink,
			elements: Elements{
				CSActive:      false,
				LinkWebActive: true,
				LinkWebURL:    "https://",
			},
			wantErr: true,
			errMsg:  "URL harus memiliki host yang valid",
		},
		{
			name: "Phone active valid E.164",
			body: "FEEPAY",
			mode: ModeKartuLink,
			elements: Elements{
				PhoneActive: true,
				PhoneNumber: "081234567890",
			},
			wantErr: false,
		},
		{
			name: "Phone active invalid number",
			body: "FEEPAY",
			mode: ModeKartuLink,
			elements: Elements{
				PhoneActive: true,
				PhoneNumber: "123",
			},
			wantErr: true,
			errMsg:  "nomor telepon tidak valid",
		},
		{
			name: "Javascript URL",
			body: "FEEPAY",
			mode: ModeKartuLink,
			elements: Elements{
				CSActive:      true,
				CSNumber:      "62812",
				LinkWebActive: true,
				LinkWebURL:    "javascript:alert(1)",
			},
			wantErr: true,
		},
		{
			name: "Data URL",
			body: "FEEPAY",
			mode: ModeKartuLink,
			elements: Elements{
				CSActive:      true,
				CSNumber:      "62812",
				LinkWebActive: true,
				LinkWebURL:    "data:text/html,<html>",
			},
			wantErr: true,
		},
		{
			name: "Valid all elements for kartu_link",
			body: "FEEPAY",
			mode: ModeKartuLink,
			elements: Elements{
				CSActive:      true,
				CSNumber:      "https://wa.me/62812",
				LinkWebActive: true,
				LinkWebURL:    "https://feepay.com",
				PhoneActive:   true,
				PhoneNumber:   "081234567890",
				PromoActive:   true,
				PromoCode:     "PROMO123",
				PromoURL:      "https://feepay.com/promo",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTemplate(tt.body, tt.mode, tt.elements)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTemplate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errMsg != "" && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("ValidateTemplate() error msg = %v, wantErrMsg contains %v", err, tt.errMsg)
			}
		})
	}
}

func TestRender(t *testing.T) {
	elements := Elements{
		CSActive:      true,
		CSNumber:      "62812",
		LinkWebActive: true,
		LinkWebURL:    "https://feepay.com",
		PhoneActive:   true,
		PhoneNumber:   "628111",
		PromoActive:   true,
		PromoCode:     "PROMO123",
		PromoURL:      "https://feepay.com/promo",
	}

	t.Run("Tombol Asli with CS", func(t *testing.T) {
		msg := Render("Halo FEEPAY", "img.jpg", elements, ModeTombolAsli)
		if msg.Text != "Halo FEEPAY" {
			t.Errorf("Expected text 'Halo FEEPAY', got '%s'", msg.Text)
		}
		if msg.ImagePath != "img.jpg" {
			t.Errorf("Expected imagePath 'img.jpg', got '%s'", msg.ImagePath)
		}
		if len(msg.Buttons) != 5 {
			t.Errorf("Expected 5 buttons, got %d", len(msg.Buttons))
		}
	})

	t.Run("Kartu Link with CS", func(t *testing.T) {
		msg := Render("Halo FEEPAY\nBaris kedua", "", elements, ModeKartuLink)
		expected := "*Halo FEEPAY*\nBaris kedua\n\n---\nWeb: https://feepay.com\nPromo (PROMO123): https://feepay.com/promo\nTelp: 628111\nCS: https://wa.me/62812"
		if msg.Text != expected {
			t.Errorf("Expected text:\n%s\n\nGot:\n%s", expected, msg.Text)
		}
		if len(msg.Buttons) != 0 {
			t.Errorf("Expected 0 buttons for kartu_link, got %d", len(msg.Buttons))
		}
		if msg.Preview == nil {
			t.Fatal("Expected msg.Preview to be non-nil for kartu_link with web link")
		}
		if msg.Preview.URL != "https://feepay.com" {
			t.Errorf("Expected Preview URL 'https://feepay.com', got '%s'", msg.Preview.URL)
		}
		if msg.Preview.Title != "Halo FEEPAY" {
			t.Errorf("Expected Preview Title 'Halo FEEPAY', got '%s'", msg.Preview.Title)
		}
	})

	t.Run("Render with CSActive false does not produce CS or wa.me", func(t *testing.T) {
		noCSElements := elements
		noCSElements.CSActive = false

		// Kartu link test
		msgKartu := Render("Halo FEEPAY\nBaris kedua", "", noCSElements, ModeKartuLink)
		if strings.Contains(msgKartu.Text, "CS") {
			t.Errorf("Kartu link should not contain 'CS' when CSActive is false. Got: %s", msgKartu.Text)
		}
		if strings.Contains(msgKartu.Text, "wa.me") {
			t.Errorf("Kartu link should not contain 'wa.me' when CSActive is false. Got: %s", msgKartu.Text)
		}

		// Tombol asli test
		msgTombol := Render("Halo FEEPAY", "", noCSElements, ModeTombolAsli)
		for _, btn := range msgTombol.Buttons {
			if strings.Contains(btn.Text, "CS") || strings.Contains(btn.Payload, "wa.me") {
				t.Errorf("Tombol asli should not contain CS button when CSActive is false. Got: %+v", btn)
			}
		}
	})
}

func TestReplaceVariables(t *testing.T) {
	t.Run("Substitusi dengan nama tersedia", func(t *testing.T) {
		body := "Halo {nama}, promo FEEPAY untuk akun {usn}!"
		vars := map[string]string{
			"nama": "Budi Santoso",
			"usn":  "budi99",
		}
		res := ReplaceVariables(body, vars)
		expected := "Halo Budi Santoso, promo FEEPAY untuk akun budi99!"
		if res != expected {
			t.Errorf("Expected %q, got %q", expected, res)
		}
	})

	t.Run("Substitusi dengan alias {name} dan {{nama}}", func(t *testing.T) {
		body := "Halo {{name}}, id Anda: {id}."
		vars := map[string]string{
			"nama": "Agus",
			"id":   "CUST-01",
		}
		res := ReplaceVariables(body, vars)
		expected := "Halo Agus, id Anda: CUST-01."
		if res != expected {
			t.Errorf("Expected %q, got %q", expected, res)
		}
	})

	t.Run("Fallback ketika variabel kosong", func(t *testing.T) {
		body := "Halo {nama|Kak}, selamat datang!"
		res := ReplaceVariables(body, nil)
		expected := "Halo Kak, selamat datang!"
		if res != expected {
			t.Errorf("Expected %q, got %q", expected, res)
		}

		// Jika nama ada, fallback tidak dipakai
		resWithVal := ReplaceVariables(body, map[string]string{"nama": "Siti"})
		expectedWithVal := "Halo Siti, selamat datang!"
		if resWithVal != expectedWithVal {
			t.Errorf("Expected %q, got %q", expectedWithVal, resWithVal)
		}
	})

	t.Run("Tanpa fallback dan variabel kosong (rapi tanpa spasi ganda)", func(t *testing.T) {
		body := "Halo {nama}, promo hari ini siap!"
		res := ReplaceVariables(body, map[string]string{})
		expected := "Halo, promo hari ini siap!"
		if res != expected {
			t.Errorf("Expected %q, got %q", expected, res)
		}
	})

	t.Run("RenderWithRecipient helper", func(t *testing.T) {
		elements := Elements{}
		msg := RenderWithRecipient("Halo {nama}\nBaris kedua", "", elements, ModeKartuLink, "Dewi")
		if !strings.HasPrefix(msg.Text, "*Halo Dewi*") {
			t.Errorf("RenderWithRecipient failed to format title with name: %s", msg.Text)
		}
	})
}

func TestThumbnailCachingAndLogging(t *testing.T) {
	ResetThumbnailCache()

	// 1. Gambar tidak ada: mengembalikan error dan mencatat di log
	_, err := GetOrCreateThumbnail("non_existent_image.jpg", DefaultThumbWidth, DefaultThumbQuality)
	if err == nil {
		t.Fatal("expected error for non-existent image, got nil")
	}

	// 2. Buat gambar temporary valid
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.jpg")
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatalf("failed to create temp image: %v", err)
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
		f.Close()
		t.Fatalf("failed to encode jpeg: %v", err)
	}
	f.Close()

	// Panggilan pertama: menghasilkan thumbnail dan menyimpannya di cache
	thumb1, err := GetOrCreateThumbnail(imgPath, DefaultThumbWidth, DefaultThumbQuality)
	if err != nil {
		t.Fatalf("failed to generate thumbnail: %v", err)
	}
	if len(thumb1) == 0 {
		t.Fatal("expected non-empty thumbnail")
	}

	// Panggilan kedua: harus mengambil dari cache
	thumb2, err := GetOrCreateThumbnail(imgPath, DefaultThumbWidth, DefaultThumbQuality)
	if err != nil {
		t.Fatalf("failed to get cached thumbnail: %v", err)
	}
	if len(thumb2) != len(thumb1) {
		t.Fatalf("cached thumbnail length mismatch: %d vs %d", len(thumb2), len(thumb1))
	}
}


