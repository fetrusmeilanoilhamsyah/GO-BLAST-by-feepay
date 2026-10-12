package config

import (
	"encoding/base64"
	"errors"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv             string
	AppAddr            string
	DatabaseURL        string
	SessionSecret      string
	EncryptionKey      []byte
	CSWhatsAppURL      string
	CSTelegramURL      string
	ChannelTelegramURL string
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

	if cfg.AppAddr == "" || cfg.AppAddr == "127.0.0.1:8080" {
		cfg.AppAddr = "0.0.0.0:8080"
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

	if cfg.AppEnv == "prod" {
		if cfg.DatabaseURL == "" {
			return nil, errors.New("DATABASE_URL is required in prod")
		}
		if cfg.SessionSecret == "" {
			return nil, errors.New("SESSION_SECRET is required in prod")
		}
	}

	return cfg, nil
}
