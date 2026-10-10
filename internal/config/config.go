package config

import (
	"encoding/base64"
	"errors"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv        string
	AppAddr       string
	DatabaseURL   string
	SessionSecret string
	EncryptionKey []byte
}

func Load() (*Config, error) {
	// Optional: load .env file if it exists. In production, we usually rely on real environment variables.
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:        os.Getenv("APP_ENV"),
		AppAddr:       os.Getenv("APP_ADDR"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		SessionSecret: os.Getenv("SESSION_SECRET"),
	}

	if cfg.AppEnv == "" {
		cfg.AppEnv = "dev"
	}

	if cfg.AppAddr == "" {
		cfg.AppAddr = "127.0.0.1:8080"
	}

	encKeyStr := os.Getenv("ENCRYPTION_KEY")
	if encKeyStr != "" {
		key, err := base64.StdEncoding.DecodeString(encKeyStr)
		if err != nil {
			return nil, errors.New("ENCRYPTION_KEY is not a valid base64 string")
		}
		if len(key) != 32 {
			return nil, errors.New("ENCRYPTION_KEY must be exactly 32 bytes when decoded")
		}
		cfg.EncryptionKey = key
	}

	if cfg.AppEnv == "prod" {
		if cfg.DatabaseURL == "" {
			return nil, errors.New("DATABASE_URL is required in prod")
		}
		if cfg.SessionSecret == "" {
			return nil, errors.New("SESSION_SECRET is required in prod")
		}
		if encKeyStr == "" {
			return nil, errors.New("ENCRYPTION_KEY is required in prod")
		}
	}

	return cfg, nil
}
