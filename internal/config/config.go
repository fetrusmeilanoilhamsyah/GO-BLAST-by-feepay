package config

import (
	"encoding/base64"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv             string
	AppAddr            string
	DatabaseURL        string
	SessionSecret      string
	EncryptionKey      []byte
	EncryptionKeyPrev  []byte
	CSWhatsAppURL      string
	CSTelegramURL      string
	ChannelTelegramURL string
	MaxActiveSessions  int
}

func Load() (*Config, error) {
	// Optional: load .env file if it exists. In production, we usually rely on real environment variables.
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:             os.Getenv("APP_ENV"),
		AppAddr:            os.Getenv("APP_ADDR"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		SessionSecret:      os.Getenv("SESSION_SECRET"),
		CSWhatsAppURL:      os.Getenv("CS_WHATSAPP_URL"),
		CSTelegramURL:      os.Getenv("CS_TELEGRAM_URL"),
		ChannelTelegramURL: os.Getenv("CHANNEL_TELEGRAM_URL"),
	}

	if cfg.AppEnv == "" {
		cfg.AppEnv = "dev"
	}

	if cfg.AppAddr == "" {
		cfg.AppAddr = "127.0.0.1:8080"
	}

	if cfg.CSWhatsAppURL == "" || cfg.CSWhatsAppURL == "https://wa.me/" {
		cfg.CSWhatsAppURL = "https://wa.me/6281200000000"
	}

	if cfg.CSTelegramURL == "" {
		cfg.CSTelegramURL = "https://t.me/feepay_cs"
	}

	if cfg.ChannelTelegramURL == "" {
		cfg.ChannelTelegramURL = "https://t.me/goblast_channel"
	}

	encKeyStr := os.Getenv("ENCRYPTION_KEY")
	if encKeyStr == "" {
		return nil, errors.New("ENCRYPTION_KEY is required in all environments")
	}
	key, err := base64.StdEncoding.DecodeString(encKeyStr)
	if err != nil {
		return nil, errors.New("ENCRYPTION_KEY is not a valid base64 string")
	}
	if len(key) != 32 {
		return nil, errors.New("ENCRYPTION_KEY must be exactly 32 bytes when decoded")
	}
	cfg.EncryptionKey = key

	encKeyPrevStr := os.Getenv("ENCRYPTION_KEY_PREV")
	if encKeyPrevStr != "" {
		keyPrev, err := base64.StdEncoding.DecodeString(encKeyPrevStr)
		if err != nil {
			return nil, errors.New("ENCRYPTION_KEY_PREV is not a valid base64 string")
		}
		if len(keyPrev) != 32 {
			return nil, errors.New("ENCRYPTION_KEY_PREV must be exactly 32 bytes when decoded")
		}
		cfg.EncryptionKeyPrev = keyPrev
	}

	if cfg.AppEnv == "prod" || cfg.AppEnv == "production" {
		// Validasi APP_ADDR tidak boleh bind public tanpa izin eksplisit
		host, _, err := net.SplitHostPort(cfg.AppAddr)
		if err != nil {
			host = cfg.AppAddr
		}
		if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
			if os.Getenv("ALLOW_PUBLIC_BIND") != "true" {
				return nil, errors.New("APP_ADDR mengikat antarmuka publik (0.0.0.0, ::, atau host kosong) tanpa ALLOW_PUBLIC_BIND=true di produksi")
			}
		}

		if cfg.DatabaseURL == "" {
			return nil, errors.New("DATABASE_URL is required in prod")
		}
		if strings.Contains(cfg.DatabaseURL, "dummy_password") {
			return nil, errors.New("DATABASE_URL berisi nilai contoh dummy_password di produksi")
		}

		if cfg.SessionSecret == "" {
			return nil, errors.New("SESSION_SECRET is required in prod")
		}
		if len(cfg.SessionSecret) < 32 {
			return nil, errors.New("SESSION_SECRET minimal 32 karakter di produksi")
		}
		lowerSec := strings.ToLower(cfg.SessionSecret)
		if strings.Contains(lowerSec, "dummy") || strings.Contains(lowerSec, "change_me") {
			return nil, errors.New("SESSION_SECRET berisi nilai contoh ('dummy' atau 'change_me') di produksi")
		}

		if isWeakOrSequentialKey(cfg.EncryptionKey) {
			return nil, errors.New("ENCRYPTION_KEY semua byte-nya sama atau berpola berurutan di produksi")
		}
	}

	maxActiveStr := os.Getenv("MAX_ACTIVE_SESSIONS")
	if maxActiveStr != "" {
		val, err := strconv.Atoi(maxActiveStr)
		if err != nil || val <= 0 {
			return nil, errors.New("MAX_ACTIVE_SESSIONS must be a positive integer")
		}
		cfg.MaxActiveSessions = val
	} else {
		if cfg.AppEnv == "prod" || cfg.AppEnv == "production" {
			return nil, errors.New("MAX_ACTIVE_SESSIONS is required in production")
		}
		cfg.MaxActiveSessions = 50
	}

	return cfg, nil
}

func isWeakOrSequentialKey(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	// 1. Semua byte sama
	allSame := true
	for i := 1; i < len(b); i++ {
		if b[i] != b[0] {
			allSame = false
			break
		}
	}
	if allSame {
		return true
	}

	// 2. Monotonik naik atau turun berurutan
	inc, dec := true, true
	for i := 1; i < len(b); i++ {
		if b[i] != b[i-1]+1 {
			inc = false
		}
		if b[i] != b[i-1]-1 {
			dec = false
		}
	}
	if inc || dec {
		return true
	}

	// 3. Pola digit berurutan berulang (misal 12345678901234567890...)
	digitSeq := true
	for i := 1; i < len(b); i++ {
		prev := b[i-1]
		curr := b[i]
		expected := prev + 1
		if prev == '9' {
			expected = '0'
		}
		if curr != expected {
			digitSeq = false
			break
		}
	}
	if digitSeq {
		return true
	}

	return false
}
