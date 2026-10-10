package campaign

import (
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
			name: "Valid tombol_asli",
			body: "Promo FEEPAY hari ini",
			mode: ModeTombolAsli,
			elements: Elements{
				CSActive: true,
				CSNumber: "6281234567890",
			},
			wantErr: false,
		},
		{
			name: "Missing FEEPAY (now valid)",
			body: "Halo ini pesan biasa",
			mode: ModeKartuLink,
			elements: Elements{
				CSActive: true,
				CSNumber: "62812",
			},
			wantErr: false,
		},
		{
			name: "CS inactive (now valid)",
			body: "FEEPAY promo",
			mode: ModeKartuLink,
			elements: Elements{
				CSActive: false,
			},
			wantErr: false,
		},
		{
			name: "Body too long",
			body: "FEEPAY " + strings.Repeat("A", 1001),
			mode: ModeKartuLink,
			elements: Elements{
				CSActive: true,
				CSNumber: "62812",
			},
			wantErr: true,
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
			name: "Valid all elements",
			body: "FEEPAY",
			mode: ModeTombolAsli,
			elements: Elements{
				CSActive:      true,
				CSNumber:      "https://wa.me/62812",
				LinkWebActive: true,
				LinkWebURL:    "https://feepay.com",
				PhoneActive:   true,
				PhoneNumber:   "628111",
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

	t.Run("Tombol Asli", func(t *testing.T) {
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
		// web
		if msg.Buttons[0].Type != "url" || msg.Buttons[0].Payload != "https://feepay.com" {
			t.Errorf("Unexpected button 0: %+v", msg.Buttons[0])
		}
		// phone
		if msg.Buttons[1].Type != "phone" || msg.Buttons[1].Payload != "628111" {
			t.Errorf("Unexpected button 1: %+v", msg.Buttons[1])
		}
		// copy
		if msg.Buttons[2].Type != "copy" || msg.Buttons[2].Payload != "PROMO123" {
			t.Errorf("Unexpected button 2: %+v", msg.Buttons[2])
		}
		// promo url
		if msg.Buttons[3].Type != "url" || msg.Buttons[3].Payload != "https://feepay.com/promo" {
			t.Errorf("Unexpected button 3: %+v", msg.Buttons[3])
		}
		// cs
		if msg.Buttons[4].Type != "url" || msg.Buttons[4].Payload != "https://wa.me/62812" {
			t.Errorf("Unexpected button 4: %+v", msg.Buttons[4])
		}
	})

	t.Run("Kartu Link", func(t *testing.T) {
		msg := Render("Halo FEEPAY\nBaris kedua", "", elements, ModeKartuLink)
		expected := "*Halo FEEPAY*\nBaris kedua\n\n---\nWeb: https://feepay.com\nPromo (PROMO123): https://feepay.com/promo\nTelp: 628111\nCS: https://wa.me/62812"
		if msg.Text != expected {
			t.Errorf("Expected text:\n%s\n\nGot:\n%s", expected, msg.Text)
		}
		if len(msg.Buttons) != 0 {
			t.Errorf("Expected 0 buttons for kartu_link, got %d", len(msg.Buttons))
		}
	})
}
