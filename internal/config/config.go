package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
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
	TrustedProxyCIDRs  []string
	ParsedTrustedCIDRs []*net.IPNet
	ClientIPHeader     string
	TurnstileSiteKey   string
	TurnstileSecretKey string
	AppPublicHost      string
	BackupKey          []byte
	BackupKeyPrev      []byte
	AllowWebRestore    bool
	DBMaxConns         int
	DBMinConns         int
	TestDatabaseURL    string
}

func Load() (*Config, error) {
	// Optional: load .env file if it exists. In production, we usually rely on real environment variables.
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:             os.Getenv("APP_ENV"),
		AppAddr:            os.Getenv("APP_ADDR"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		TestDatabaseURL:    os.Getenv("TEST_DATABASE_URL"),
		SessionSecret:      os.Getenv("SESSION_SECRET"),
		CSWhatsAppURL:      os.Getenv("CS_WHATSAPP_URL"),
		CSTelegramURL:      os.Getenv("CS_TELEGRAM_URL"),
		ChannelTelegramURL: os.Getenv("CHANNEL_TELEGRAM_URL"),
		ClientIPHeader:     os.Getenv("CLIENT_IP_HEADER"),
		TurnstileSiteKey:   os.Getenv("TURNSTILE_SITE_KEY"),
		TurnstileSecretKey: os.Getenv("TURNSTILE_SECRET_KEY"),
		AppPublicHost:      os.Getenv("APP_PUBLIC_HOST"),
	}

	if valStr := os.Getenv("DB_MAX_CONNS"); valStr != "" {
		if val, err := strconv.Atoi(valStr); err == nil && val > 0 {
			cfg.DBMaxConns = val
		}
	}
	if cfg.DBMaxConns == 0 {
		cfg.DBMaxConns = DefaultDBMaxConns
	}

	if valStr := os.Getenv("DB_MIN_CONNS"); valStr != "" {
		if val, err := strconv.Atoi(valStr); err == nil && val >= 0 {
			cfg.DBMinConns = val
		}
	}
	if cfg.DBMinConns == 0 {
		cfg.DBMinConns = DefaultDBMinConns
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

	if err := validateHTTPSURL(cfg.CSWhatsAppURL, "CS_WHATSAPP_URL"); err != nil {
		return nil, err
	}
	if err := validateHTTPSURL(cfg.CSTelegramURL, "CS_TELEGRAM_URL"); err != nil {
		return nil, err
	}
	if err := validateHTTPSURL(cfg.ChannelTelegramURL, "CHANNEL_TELEGRAM_URL"); err != nil {
		return nil, err
	}

	if cfg.ClientIPHeader == "" {
		cfg.ClientIPHeader = "CF-Connecting-IP"
	}

	// Parsing TRUSTED_PROXY_CIDRS
	trustedCIDRStr := os.Getenv("TRUSTED_PROXY_CIDRS")
	if trustedCIDRStr == "" {
		trustedCIDRStr = "127.0.0.1/32,::1/128"
	}
	rawCIDRs := strings.Split(trustedCIDRStr, ",")
	for _, raw := range rawCIDRs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			if strings.Contains(raw, ":") {
				raw = raw + "/128"
			} else {
				raw = raw + "/32"
			}
		}
		_, ipNet, err := net.ParseCIDR(raw)
		if err == nil && ipNet != nil {
			cfg.TrustedProxyCIDRs = append(cfg.TrustedProxyCIDRs, raw)
			cfg.ParsedTrustedCIDRs = append(cfg.ParsedTrustedCIDRs, ipNet)
		}
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

	// BACKUP_KEY (32 byte base64; jika kosong matikan fitur dengan peringatan)
	backupKeyStr := os.Getenv("BACKUP_KEY")
	if backupKeyStr != "" {
		bkey, err := base64.StdEncoding.DecodeString(backupKeyStr)
		if err != nil {
			return nil, errors.New("BACKUP_KEY is not a valid base64 string")
		}
		if len(bkey) != 32 {
			return nil, errors.New("BACKUP_KEY must be exactly 32 bytes when decoded")
		}
		if bytes.Equal(bkey, cfg.EncryptionKey) {
			return nil, errors.New("BACKUP_KEY cannot be the same as ENCRYPTION_KEY")
		}
		cfg.BackupKey = bkey
	} else {
		slog.Warn("BACKUP_KEY tidak dikonfigurasi; fitur cadangan database terenkripsi dinonaktifkan")
	}

	backupKeyPrevStr := os.Getenv("BACKUP_KEY_PREV")
	if backupKeyPrevStr != "" {
		bkeyPrev, err := base64.StdEncoding.DecodeString(backupKeyPrevStr)
		if err != nil {
			return nil, errors.New("BACKUP_KEY_PREV is not a valid base64 string")
		}
		if len(bkeyPrev) != 32 {
			return nil, errors.New("BACKUP_KEY_PREV must be exactly 32 bytes when decoded")
		}
		if bytes.Equal(bkeyPrev, cfg.EncryptionKey) {
			return nil, errors.New("BACKUP_KEY_PREV cannot be the same as ENCRYPTION_KEY")
		}
		cfg.BackupKeyPrev = bkeyPrev
	}

	if os.Getenv("ALLOW_WEB_RESTORE") == "true" {
		cfg.AllowWebRestore = true
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

		if cfg.BackupKey != nil && isWeakOrSequentialKey(cfg.BackupKey) {
			return nil, errors.New("BACKUP_KEY semua byte-nya sama atau berpola berurutan di produksi")
		}

		if IsTurnstileTestKey(cfg.TurnstileSiteKey) || IsTurnstileTestKey(cfg.TurnstileSecretKey) {
			return nil, errors.New("kunci uji Turnstile tidak boleh digunakan di lingkungan produksi")
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

// IsTurnstileTestKey memeriksa apakah kunci merupakan kunci uji resmi Cloudflare (misal diawali 1x0000, 2x0000, 3x0000).
func IsTurnstileTestKey(k string) bool {
	k = strings.TrimSpace(k)
	if k == "" {
		return false
	}
	return strings.HasPrefix(k, "1x0000") || strings.HasPrefix(k, "2x0000") || strings.HasPrefix(k, "3x0000")
}

func validateHTTPSURL(rawURL, paramName string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s: invalid URL: %w", paramName, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%s: URL must use https scheme with a valid host, got %q", paramName, rawURL)
	}
	return nil
}

