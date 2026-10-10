package auth

import (
	"strings"
	"testing"
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
