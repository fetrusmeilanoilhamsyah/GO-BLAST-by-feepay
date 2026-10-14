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

func TestConfigProductionValidation(t *testing.T) {
	realKey := base64.StdEncoding.EncodeToString([]byte("!aB3$eF7*iJ1#mN5&qR9(uV3^yZ7@cE1"))
	realSecret := "super_secret_session_key_with_at_least_32_characters!!"
	realDB := "postgres://user:strong_password@localhost:5432/goblast_prod"

	tests := []struct {
		name        string
		env         map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name: "prod menolak 0.0.0.0 tanpa ALLOW_PUBLIC_BIND",
			env: map[string]string{
				"APP_ENV": "prod", "APP_ADDR": "0.0.0.0:8080", "ALLOW_PUBLIC_BIND": "",
				"ENCRYPTION_KEY": realKey, "SESSION_SECRET": realSecret, "DATABASE_URL": realDB,
				"MAX_ACTIVE_SESSIONS": "50",
			},
			wantErr: true, errContains: "ALLOW_PUBLIC_BIND",
		},
		{
			name: "prod menolak SESSION_SECRET bernilai contoh dummy",
			env: map[string]string{
				"APP_ENV": "prod", "APP_ADDR": "127.0.0.1:8080", "ALLOW_PUBLIC_BIND": "",
				"ENCRYPTION_KEY": realKey, "SESSION_SECRET": "dummy_session_secret_change_me", "DATABASE_URL": realDB,
				"MAX_ACTIVE_SESSIONS": "50",
			},
			wantErr: true, errContains: "SESSION_SECRET",
		},
		{
			name: "prod menolak ENCRYPTION_KEY berpola berurutan",
			env: map[string]string{
				"APP_ENV": "prod", "APP_ADDR": "127.0.0.1:8080", "ALLOW_PUBLIC_BIND": "",
				"ENCRYPTION_KEY": base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")),
				"SESSION_SECRET": realSecret, "DATABASE_URL": realDB,
				"MAX_ACTIVE_SESSIONS": "50",
			},
			wantErr: true, errContains: "ENCRYPTION_KEY",
		},
		{
			name: "prod menolak DATABASE_URL berisi dummy_password",
			env: map[string]string{
				"APP_ENV": "prod", "APP_ADDR": "127.0.0.1:8080", "ALLOW_PUBLIC_BIND": "",
				"ENCRYPTION_KEY": realKey, "SESSION_SECRET": realSecret,
				"DATABASE_URL": "postgres://goblast:dummy_password@localhost:5432/goblast",
				"MAX_ACTIVE_SESSIONS": "50",
			},
			wantErr: true, errContains: "DATABASE_URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear env
			keys := []string{"APP_ENV", "APP_ADDR", "ALLOW_PUBLIC_BIND", "ENCRYPTION_KEY", "ENCRYPTION_KEY_PREV", "SESSION_SECRET", "DATABASE_URL", "MAX_ACTIVE_SESSIONS"}
			saved := make(map[string]string)
			for _, k := range keys {
				saved[k] = os.Getenv(k)
				os.Unsetenv(k)
			}
			defer func() {
				for _, k := range keys {
					if v, ok := saved[k]; ok && v != "" {
						os.Setenv(k, v)
					} else {
						os.Unsetenv(k)
					}
				}
			}()

			for k, v := range tt.env {
				if v != "" {
					os.Setenv(k, v)
				}
			}

			_, err := config.Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Diharapkan error mengandung %q tetapi err nil", tt.errContains)
				}
			} else {
				if err != nil {
					t.Fatalf("Tidak diharapkan error tetapi got: %v", err)
				}
			}
		})
	}
}

