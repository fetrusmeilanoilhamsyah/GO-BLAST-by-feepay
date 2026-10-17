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
		{
			name: "prod menolak TURNSTILE_SITE_KEY bernilai test key",
			env: map[string]string{
				"APP_ENV": "prod", "APP_ADDR": "127.0.0.1:8080", "ALLOW_PUBLIC_BIND": "",
				"ENCRYPTION_KEY": realKey, "SESSION_SECRET": realSecret, "DATABASE_URL": realDB,
				"MAX_ACTIVE_SESSIONS": "50",
				"TURNSTILE_SITE_KEY":  "1x00000000000000000000AA",
				"TURNSTILE_SECRET_KEY": "1x0000000000000000000000000000000AA",
			},
			wantErr: true, errContains: "kunci uji Turnstile",
		},
		{
			name: "dev menerima TURNSTILE_SITE_KEY bernilai test key",
			env: map[string]string{
				"APP_ENV": "dev", "APP_ADDR": "127.0.0.1:8080",
				"ENCRYPTION_KEY": realKey, "SESSION_SECRET": realSecret,
				"TURNSTILE_SITE_KEY":  "1x00000000000000000000AA",
				"TURNSTILE_SECRET_KEY": "1x0000000000000000000000000000000AA",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear env
			keys := []string{"APP_ENV", "APP_ADDR", "ALLOW_PUBLIC_BIND", "ENCRYPTION_KEY", "ENCRYPTION_KEY_PREV", "SESSION_SECRET", "DATABASE_URL", "MAX_ACTIVE_SESSIONS", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"}
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

func TestCSURLValidation(t *testing.T) {
	validKey := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	origKey := os.Getenv("ENCRYPTION_KEY")
	origCSWA := os.Getenv("CS_WHATSAPP_URL")
	origCSTG := os.Getenv("CS_TELEGRAM_URL")
	origChTG := os.Getenv("CHANNEL_TELEGRAM_URL")

	defer func() {
		os.Setenv("ENCRYPTION_KEY", origKey)
		os.Setenv("CS_WHATSAPP_URL", origCSWA)
		os.Setenv("CS_TELEGRAM_URL", origCSTG)
		os.Setenv("CHANNEL_TELEGRAM_URL", origChTG)
	}()

	os.Setenv("ENCRYPTION_KEY", validKey)

	// Insecure scheme javascript:
	os.Setenv("CS_WHATSAPP_URL", "javascript:alert(1)")
	_, err := config.Load()
	if err == nil {
		t.Errorf("Expected error for javascript: scheme in CS_WHATSAPP_URL, got nil")
	}

	// Insecure scheme http:
	os.Setenv("CS_WHATSAPP_URL", "http://wa.me/6281200000000")
	_, err = config.Load()
	if err == nil {
		t.Errorf("Expected error for http: scheme in CS_WHATSAPP_URL, got nil")
	}

	// Valid https:
	os.Setenv("CS_WHATSAPP_URL", "https://wa.me/6281200000000")
	os.Setenv("CS_TELEGRAM_URL", "https://t.me/feepay_cs")
	os.Setenv("CHANNEL_TELEGRAM_URL", "https://t.me/goblast_channel")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Expected success for valid https URLs, got: %v", err)
	}
	if cfg.CSWhatsAppURL != "https://wa.me/6281200000000" {
		t.Errorf("Unexpected CSWhatsAppURL: %s", cfg.CSWhatsAppURL)
	}
}

func TestConfigBackupKey(t *testing.T) {
	origEnc := os.Getenv("ENCRYPTION_KEY")
	origBkp := os.Getenv("BACKUP_KEY")
	origBkpPrev := os.Getenv("BACKUP_KEY_PREV")
	origRestore := os.Getenv("ALLOW_WEB_RESTORE")
	origEnv := os.Getenv("APP_ENV")
	defer func() {
		os.Setenv("ENCRYPTION_KEY", origEnc)
		os.Setenv("BACKUP_KEY", origBkp)
		os.Setenv("BACKUP_KEY_PREV", origBkpPrev)
		os.Setenv("ALLOW_WEB_RESTORE", origRestore)
		os.Setenv("APP_ENV", origEnv)
	}()

	keyEnc := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	os.Setenv("ENCRYPTION_KEY", keyEnc)
	os.Setenv("APP_ENV", "dev")

	// 1. BACKUP_KEY kosong -> fitur mati dengan peringatan, bukan error
	os.Unsetenv("BACKUP_KEY")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Expected nil error when BACKUP_KEY is empty, got: %v", err)
	}
	if cfg.BackupKey != nil {
		t.Fatalf("Expected nil BackupKey, got: %v", cfg.BackupKey)
	}

	// 2. BACKUP_KEY sama dengan ENCRYPTION_KEY -> error
	os.Setenv("BACKUP_KEY", keyEnc)
	_, err = config.Load()
	if err == nil {
		t.Fatalf("Expected error when BACKUP_KEY equals ENCRYPTION_KEY, got nil")
	}

	// 3. BACKUP_KEY bukan base64 valid -> error
	os.Setenv("BACKUP_KEY", "bukan-base64!!")
	_, err = config.Load()
	if err == nil {
		t.Fatalf("Expected error when BACKUP_KEY is invalid base64, got nil")
	}

	// 4. BACKUP_KEY bukan 32 byte -> error
	os.Setenv("BACKUP_KEY", base64.StdEncoding.EncodeToString([]byte("hanya-16-bytes!!")))
	_, err = config.Load()
	if err == nil {
		t.Fatalf("Expected error when BACKUP_KEY is not 32 bytes, got nil")
	}

	// 5. BACKUP_KEY valid dan berbeda dari ENCRYPTION_KEY -> sukses
	keyBkp := base64.StdEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	os.Setenv("BACKUP_KEY", keyBkp)
	os.Setenv("ALLOW_WEB_RESTORE", "true")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("Expected success for valid BACKUP_KEY, got: %v", err)
	}
	if cfg.BackupKey == nil || string(cfg.BackupKey) != "abcdefghijklmnopqrstuvwxyz012345" {
		t.Fatalf("Unexpected BackupKey value")
	}
	if !cfg.AllowWebRestore {
		t.Fatalf("Expected AllowWebRestore=true")
	}

	// 6. Di produksi: BACKUP_KEY lemah (semua byte sama) -> ditolak
	os.Setenv("APP_ENV", "prod")
	os.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	os.Setenv("SESSION_SECRET", "session_secret_32_characters_long_min")
	weakKey := base64.StdEncoding.EncodeToString([]byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"))
	os.Setenv("BACKUP_KEY", weakKey)
	_, err = config.Load()
	if err == nil {
		t.Fatalf("Expected error in production for weak BACKUP_KEY, got nil")
	}
}


