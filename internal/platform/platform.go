package platform

import (
	"log/slog"
	"os"
	"strings"
)

// MaskPhone masks a phone number, keeping the first 4 and last 3 digits visible.
func MaskPhone(phone string) string {
	// Remove leading '+' if any
	phone = strings.TrimPrefix(phone, "+")

	if len(phone) <= 7 {
		return phone
	}

	firstPart := phone[:4]
	lastPart := phone[len(phone)-3:]
	maskedPart := strings.Repeat("*", len(phone)-7)

	return firstPart + maskedPart + lastPart
}

// InitLog configures slog based on the environment.
func InitLog(env string) {
	var handler slog.Handler

	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}

	if env == "prod" {
		opts.Level = slog.LevelInfo
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
}
