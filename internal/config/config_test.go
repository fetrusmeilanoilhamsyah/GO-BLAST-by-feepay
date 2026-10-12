package config_test

import (
	"encoding/base64"
	"os"
	"testing"

	"goblast/internal/config"
)

func TestConfigEncryptionKeyRequired(t *testing.T) {
	origKey := os.Getenv("ENCRYPTION_KEY")
	origEnv := os.Getenv("APP_ENV")
	defer func() {
		os.Setenv("ENCRYPTION_KEY", origKey)
		os.Setenv("APP_ENV", origEnv)
	}()

	// 1. Missing ENCRYPTION_KEY
	os.Unsetenv("ENCRYPTION_KEY")
	os.Setenv("APP_ENV", "dev")
	_, err := config.Load()
	if err == nil {
		t.Fatalf("Expected error when ENCRYPTION_KEY is missing in dev, got nil")
	}

	os.Setenv("APP_ENV", "test")
	_, err = config.Load()
	if err == nil {
		t.Fatalf("Expected error when ENCRYPTION_KEY is missing in test, got nil")
	}

	// 2. Invalid base64
	os.Setenv("ENCRYPTION_KEY", "bukan-base64!!")
	_, err = config.Load()
	if err == nil {
		t.Fatalf("Expected error for invalid base64 ENCRYPTION_KEY, got nil")
	}

	// 3. Not 32 bytes
	shortKey := base64.StdEncoding.EncodeToString([]byte("only-16-bytes!!!"))
	os.Setenv("ENCRYPTION_KEY", shortKey)
	_, err = config.Load()
	if err == nil {
		t.Fatalf("Expected error for non-32-byte ENCRYPTION_KEY, got nil")
	}

	// 4. Valid 32-byte key
	validKey := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	os.Setenv("ENCRYPTION_KEY", validKey)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Expected success for valid 32-byte ENCRYPTION_KEY, got: %v", err)
	}
	if len(cfg.EncryptionKey) != 32 {
		t.Fatalf("Expected EncryptionKey to have 32 bytes, got %d", len(cfg.EncryptionKey))
	}
}
