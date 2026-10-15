package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// TOTPPeriod durasi tiap langkah waktu TOTP (30 detik sesuai RFC 6238).
	TOTPPeriod = 30

	// TOTPDigits jumlah digit kode TOTP (6 digit).
	TOTPDigits = 6

	// TOTPSecretBytes panjang byte acak rahasia TOTP (20 byte = 160 bit).
	TOTPSecretBytes = 20
)

var (
	// ErrInvalidTOTPSecret format rahasia TOTP tidak valid.
	ErrInvalidTOTPSecret = errors.New("rahasia TOTP tidak valid")

	// ErrInvalidTOTPCode format kode TOTP tidak valid.
	ErrInvalidTOTPCode = errors.New("kode TOTP harus berupa 6 angka")
)

// GenerateTOTPSecret menghasilkan rahasia acak 20-byte yang di-encode base32 tanpa padding.
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, TOTPSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("gagal membuat rahasia TOTP acak: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// TOTPAuthURI menghasilkan URI otpauth standar untuk aplikasi authenticator (Google Authenticator, Aegis, dll).
func TOTPAuthURI(accountName, issuer, secret string) string {
	cleanSecret := strings.ToUpper(strings.TrimSpace(secret))
	label := url.PathEscape(issuer + ":" + accountName)
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		label, cleanSecret, url.QueryEscape(issuer), TOTPDigits, TOTPPeriod)
}

// GenerateTOTPCode menghitung kode TOTP 6-digit untuk rahasia dan waktu yang diberikan.
// Mengembalikan (code string, step int64, err error).
func GenerateTOTPCode(secret string, t time.Time) (string, int64, error) {
	cleanSecret := strings.ToUpper(strings.TrimSpace(secret))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cleanSecret)
	if err != nil {
		// Coba dengan padding jika ada
		key, err = base32.StdEncoding.DecodeString(cleanSecret)
		if err != nil {
			return "", 0, ErrInvalidTOTPSecret
		}
	}

	step := t.Unix() / TOTPPeriod
	code := computeHOTP(key, step)
	return fmt.Sprintf("%06d", code), step, nil
}

// ValidateTOTP memverifikasi kode 6-digit dengan toleransi 1 langkah (+/- 30 detik).
// Mencegah replay attack: jika step yang cocok <= lastStep, verifikasi ditolak.
// Mengembalikan (valid bool, matchedStep int64, err error).
func ValidateTOTP(secret, code string, t time.Time, lastStep int64) (bool, int64, error) {
	code = strings.TrimSpace(code)
	if len(code) != TOTPDigits {
		return false, 0, ErrInvalidTOTPCode
	}

	cleanSecret := strings.ToUpper(strings.TrimSpace(secret))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cleanSecret)
	if err != nil {
		key, err = base32.StdEncoding.DecodeString(cleanSecret)
		if err != nil {
			return false, 0, ErrInvalidTOTPSecret
		}
	}

	currentStep := t.Unix() / TOTPPeriod

	// Urutan toleransi: waktu sekarang (0), satu langkah lalu (-1), satu langkah depan (+1)
	offsets := []int64{0, -1, 1}
	for _, offset := range offsets {
		step := currentStep + offset
		// Cegah penggunaan ulang kode pada step yang sama atau step yang lebih lama (anti-replay)
		if step <= lastStep {
			continue
		}

		expectedInt := computeHOTP(key, step)
		expectedStr := fmt.Sprintf("%06d", expectedInt)

		if subtle.ConstantTimeCompare([]byte(code), []byte(expectedStr)) == 1 {
			return true, step, nil
		}
	}

	return false, 0, nil
}

// computeHOTP menghitung nilai HOTP berbasis RFC 4226 (HMAC-SHA1 truncation).
func computeHOTP(key []byte, step int64) uint32 {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(step))

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	h := mac.Sum(nil)

	// Dynamic truncation
	offset := h[len(h)-1] & 0x0f
	binaryCode := binary.BigEndian.Uint32(h[offset : offset+4])
	binaryCode &= 0x7fffffff // Clear most significant bit

	return binaryCode % 1000000
}

// GenerateRecoveryCodes menghasilkan `count` kode pemulihan acak.
// Mengembalikan (plainCodes, hashedCodes, error).
// Kode teks biasa diformat XXXXX-XXXXX (10 karakter alfabet non-ambigu).
// Kode hash disimpan menggunakan SHA-256 hex.
func GenerateRecoveryCodes(count int) ([]string, []string, error) {
	if count <= 0 {
		count = 10
	}

	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // Tanpa O, 0, 1, I
	plainCodes := make([]string, count)
	hashedCodes := make([]string, count)

	for i := 0; i < count; i++ {
		bytes := make([]byte, 10)
		if _, err := rand.Read(bytes); err != nil {
			return nil, nil, fmt.Errorf("gagal membuat entropy kode pemulihan: %w", err)
		}

		var sb strings.Builder
		for j, b := range bytes {
			if j == 5 {
				sb.WriteByte('-')
			}
			sb.WriteByte(charset[int(b)%len(charset)])
		}

		code := sb.String()
		plainCodes[i] = code

		// Hash SHA-256 dari kode yang dinormalisasi (huruf kapital tanpa strip)
		normalized := strings.ToUpper(strings.ReplaceAll(code, "-", ""))
		h := sha256.Sum256([]byte(normalized))
		hashedCodes[i] = hex.EncodeToString(h[:])
	}

	return plainCodes, hashedCodes, nil
}

// HashRecoveryCode menghitung hash SHA-256 untuk kode pemulihan yang diinput pengguna.
func HashRecoveryCode(code string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])
}
