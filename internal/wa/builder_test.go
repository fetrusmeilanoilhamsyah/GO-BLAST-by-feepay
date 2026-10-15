package wa

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageBuilder_TeksBiasa(t *testing.T) {
	b := &DefaultMessageBuilder{}
	msg := OutMessage{
		Text: "Halo ini pesan teks biasa",
		Mode: "teks_biasa",
	}

	waMsg, err := b.Build(msg)
	if err != nil {
		t.Fatalf("Build gagal: %v", err)
	}
	if waMsg == nil {
		t.Fatal("waMsg nil")
	}
	if waMsg.Conversation == nil || *waMsg.Conversation != msg.Text {
		t.Fatalf("Conversation tidak cocok: %v", waMsg.Conversation)
	}
	if waMsg.InteractiveMessage != nil {
		t.Error("InteractiveMessage seharusnya nil untuk teks biasa")
	}
	if waMsg.ExtendedTextMessage != nil {
		t.Error("ExtendedTextMessage seharusnya nil untuk teks biasa")
	}
}

func TestMessageBuilder_KartuLink(t *testing.T) {
	b := &DefaultMessageBuilder{}
	msg := OutMessage{
		Text: "Halo ini pesan kartu",
		Mode: "kartu_link",
		Preview: &Preview{
			Title:       "Judul Promo",
			Description: "Deskripsi Promo",
			URL:         "https://feepay.web.id",
			Thumbnail:   []byte("fake-jpeg-thumb"),
		},
	}

	waMsg, err := b.Build(msg)
	if err != nil {
		t.Fatalf("Build gagal: %v", err)
	}
	if waMsg.ExtendedTextMessage == nil {
		t.Fatal("ExtendedTextMessage seharusnya tidak nil untuk kartu_link")
	}
	ep := waMsg.ExtendedTextMessage
	if *ep.Text != msg.Text {
		t.Errorf("Text tidak cocok: got %s, want %s", *ep.Text, msg.Text)
	}
	if *ep.Title != "Judul Promo" || *ep.MatchedText != "https://feepay.web.id" {
		t.Errorf("Metadata kartu salah: title=%v, matched=%v", ep.Title, ep.MatchedText)
	}
	if string(ep.JPEGThumbnail) != "fake-jpeg-thumb" {
		t.Errorf("Thumbnail salah: %s", string(ep.JPEGThumbnail))
	}
}

func TestMessageBuilder_TombolAsli_NativeFlow(t *testing.T) {
	b := &DefaultMessageBuilder{}
	msg := OutMessage{
		Text: "Pilih menu berikut:",
		Mode: "tombol_asli",
		Buttons: []Button{
			{Type: "cta_url", Text: "Buka Web", Payload: "https://feepay.web.id"},
			{Type: "cta_call", Text: "Hubungi Call Center", Payload: "+628123456789"},
			{Type: "cta_copy", Text: "Salin Kode", Payload: "DISKON100"},
			{Type: "quick_reply", Text: "Menu Bantuan", Payload: "help_menu"},
		},
	}

	waMsg, err := b.Build(msg)
	if err != nil {
		t.Fatalf("Build gagal: %v", err)
	}
	if waMsg.InteractiveMessage == nil {
		t.Fatal("InteractiveMessage seharusnya tidak nil untuk tombol_asli")
	}
	im := waMsg.InteractiveMessage
	if im.Body == nil || *im.Body.Text != msg.Text {
		t.Fatalf("Body text tidak cocok: %v", im.Body)
	}
	nf := im.GetNativeFlowMessage()
	if nf == nil {
		t.Fatal("NativeFlowMessage seharusnya tidak nil")
	}
	if len(nf.Buttons) != 4 {
		t.Fatalf("Jumlah tombol salah: got %d, want 4", len(nf.Buttons))
	}

	// Button 1: cta_url
	if *nf.Buttons[0].Name != "cta_url" {
		t.Errorf("Button 0 name bukan cta_url: %s", *nf.Buttons[0].Name)
	}
	var p1 map[string]string
	if err := json.Unmarshal([]byte(*nf.Buttons[0].ButtonParamsJSON), &p1); err != nil {
		t.Fatalf("JSON button 0 invalid: %v", err)
	}
	if p1["display_text"] != "Buka Web" || p1["url"] != "https://feepay.web.id" {
		t.Errorf("Payload button 0 tidak cocok: %v", p1)
	}

	// Button 2: cta_call
	if *nf.Buttons[1].Name != "cta_call" {
		t.Errorf("Button 1 name bukan cta_call: %s", *nf.Buttons[1].Name)
	}
	var p2 map[string]string
	_ = json.Unmarshal([]byte(*nf.Buttons[1].ButtonParamsJSON), &p2)
	if p2["phone_number"] != "+628123456789" {
		t.Errorf("Payload button 1 tidak cocok: %v", p2)
	}

	// Button 3: cta_copy
	if *nf.Buttons[2].Name != "cta_copy" {
		t.Errorf("Button 2 name bukan cta_copy: %s", *nf.Buttons[2].Name)
	}
	var p3 map[string]string
	_ = json.Unmarshal([]byte(*nf.Buttons[2].ButtonParamsJSON), &p3)
	if p3["copy_code"] != "DISKON100" {
		t.Errorf("Payload button 2 tidak cocok: %v", p3)
	}

	// Button 4: quick_reply
	if *nf.Buttons[3].Name != "quick_reply" {
		t.Errorf("Button 3 name bukan quick_reply: %s", *nf.Buttons[3].Name)
	}
	var p4 map[string]string
	_ = json.Unmarshal([]byte(*nf.Buttons[3].ButtonParamsJSON), &p4)
	if p4["id"] != "help_menu" {
		t.Errorf("Payload button 3 tidak cocok: %v", p4)
	}
}

func TestMessageBuilder_Max10Buttons(t *testing.T) {
	b := &DefaultMessageBuilder{}
	var buttons []Button
	for i := 1; i <= 11; i++ {
		buttons = append(buttons, Button{Type: "quick_reply", Text: "Btn", Payload: "p"})
	}

	msg := OutMessage{
		Text:    "Test over buttons",
		Mode:    "tombol_asli",
		Buttons: buttons,
	}

	_, err := b.Build(msg)
	if err == nil {
		t.Fatal("Seharusnya error jika jumlah tombol > 10")
	}
	if !strings.Contains(err.Error(), "maksimal 10") {
		t.Fatalf("Pesan error salah: %v", err)
	}
}
