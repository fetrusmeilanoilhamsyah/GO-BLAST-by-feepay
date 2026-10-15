package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHashAndVerifyPassword(t *testing.T) {
	password := "StrongP@ssw0rd!"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("Expected hash to start with $argon2id$, got: %s", hash)
	}

	match, err := VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("VerifyPassword error: %v", err)
	}
	if !match {
		t.Error("VerifyPassword returned false for correct password")
	}

	match, err = VerifyPassword("WrongPassword!", hash)
	if err != nil {
		t.Fatalf("VerifyPassword error on wrong password: %v", err)
	}
	if match {
		t.Error("VerifyPassword returned true for wrong password")
	}
}

func TestHashPassword_Short(t *testing.T) {
	_, err := HashPassword("short")
	if err == nil {
		t.Error("Expected error for short password, got nil")
	}
}

func TestGenerateSessionToken(t *testing.T) {
	raw, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken error: %v", err)
	}
	if raw == "" || hash == "" {
		t.Error("Generated token or hash is empty")
	}

	expectedHash := HashSessionToken(raw)
	if hash != expectedHash {
		t.Errorf("Hash doesn't match: expected %s, got %s", expectedHash, hash)
	}
}

func TestArgonGate_ConcurrencyLimitAndQueue(t *testing.T) {
	// Gate dengan batas 2 bersamaan, antrean 2, maxWait 50ms
	gate := NewGate(2, 2, 50*time.Millisecond)

	hash, err := gate.HashPassword(context.Background(), "Password123!")
	if err != nil {
		t.Fatalf("gate.HashPassword error: %v", err)
	}

	match, err := gate.VerifyPassword(context.Background(), "Password123!", hash)
	if err != nil || !match {
		t.Fatalf("gate.VerifyPassword error: %v, match: %v", err, match)
	}

	// Tes antrean penuh: isi sem (2) dan antrean (2)
	blockCh := make(chan struct{})
	gate.sem <- struct{}{}
	gate.sem <- struct{}{}
	gate.queue <- struct{}{}
	gate.queue <- struct{}{}

	// Permintaan ke-5 harus langsung ditolak dengan ErrServerBusy karena antrean penuh
	_, err = gate.VerifyPassword(context.Background(), "Password123!", hash)
	if err != ErrServerBusy {
		t.Errorf("saat antrean penuh harus mengembalikan ErrServerBusy, got: %v", err)
	}

	// Lepaskan slot
	<-gate.queue
	<-gate.queue
	<-gate.sem
	<-gate.sem
	_ = blockCh
}

func TestCSRFToken_HMACSigning(t *testing.T) {
	secret := "secret-32-bytes-test-key-random!"
	rawToken := "raw-csrf-random-token-12345"

	signed := SignCSRFToken(rawToken, secret)
	if !strings.HasPrefix(signed, rawToken+".") {
		t.Fatalf("format signed token tidak valid: %s", signed)
	}

	// Verifikasi sukses
	gotRaw, ok := VerifySignedCSRFToken(signed, secret)
	if !ok || gotRaw != rawToken {
		t.Fatalf("verifikasi signed CSRF gagal: gotRaw=%s, ok=%v", gotRaw, ok)
	}

	// Verifikasi gagal jika token diubah
	_, ok = VerifySignedCSRFToken(signed+"tampered", secret)
	if ok {
		t.Errorf("token yang di-tamper seharusnya gagal verifikasi")
	}

	// Verifikasi gagal jika secret salah
	_, ok = VerifySignedCSRFToken(signed, "wrong-secret-123")
	if ok {
		t.Errorf("token dengan secret salah seharusnya gagal")
	}
}

func TestFormTimingToken_Verification(t *testing.T) {
	secret := "session-secret-for-form-timing-test"
	baseTime := time.Date(2026, 10, 15, 6, 0, 0, 0, time.UTC)

	token := SignFormTimingToken(baseTime, secret)

	minAge := 2 * time.Second
	maxAge := 2 * time.Hour

	// 1. Submit terlalu cepat (1 detik setelah render) -> harus DITOLAK
	tooFast := baseTime.Add(1 * time.Second)
	if VerifyFormTimingToken(token, secret, tooFast, minAge, maxAge) {
		t.Errorf("submit dalam 1 detik (< 2s) seharusnya ditolak")
	}

	// 2. Submit normal (5 detik setelah render) -> harus DITERIMA
	normalTime := baseTime.Add(5 * time.Second)
	if !VerifyFormTimingToken(token, secret, normalTime, minAge, maxAge) {
		t.Errorf("submit dalam 5 detik seharusnya diterima")
	}

	// 3. Submit basi (> 2 jam setelah render) -> harus DITOLAK
	tooLate := baseTime.Add(2*time.Hour + 1*time.Minute)
	if VerifyFormTimingToken(token, secret, tooLate, minAge, maxAge) {
		t.Errorf("submit setelah 2 jam seharusnya ditolak")
	}

	// 4. Token palsu / salah secret -> harus DITOLAK
	if VerifyFormTimingToken(token, "wrong-secret", normalTime, minAge, maxAge) {
		t.Errorf("token dengan secret salah seharusnya ditolak")
	}
}

