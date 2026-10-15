package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	ArgonTime    = 1
	ArgonMemory  = 64 * 1024
	ArgonThreads = 4
	ArgonKeyLen  = 32
	ArgonSaltLen = 16

	MaxPasswordBytes = 128
	MinPasswordBytes = 10
)

var (
	// ErrServerBusy dikembalikan saat antrean atau slot hashing argon2 sedang penuh.
	ErrServerBusy = errors.New("server sibuk, coba lagi")

	// ErrPasswordTooLong dikembalikan jika panjang kata sandi melebihi 128 byte.
	ErrPasswordTooLong = errors.New("kata sandi maksimal 128 karakter")

	// ErrPasswordTooShort dikembalikan jika panjang kata sandi kurang dari 10 karakter.
	ErrPasswordTooShort = errors.New("kata sandi minimal 10 karakter")

	// ShadowHash adalah hash tiruan yang dihitung sekali saat inisialisasi paket
	// untuk pencegahan timing attack ketika akun tidak ditemukan.
	ShadowHash string
)

func init() {
	var err error
	ShadowHash, err = HashPassword("constant-shadow-standin-pass")
	if err != nil {
		panic("auth: gagal menginisialisasi ShadowHash: " + err.Error())
	}
}

// ArgonGate membatasi jumlah komputasi argon2 yang berjalan bersamaan
// dan mengantrekan permintaan yang masuk agar RAM tidak terkuras habis.
type ArgonGate struct {
	sem         chan struct{}
	queue       chan struct{}
	maxWait     time.Duration
	activeCount atomic.Int64
	peakActive  atomic.Int64
}

// NewGate membuat instance ArgonGate baru dengan batas konkurensi, batas antrean, dan waktu tunggu maksimal.
func NewGate(concurrency, queue int, maxWait time.Duration) *ArgonGate {
	if concurrency <= 0 {
		concurrency = 2
	}
	if queue <= 0 {
		queue = 8
	}
	if maxWait <= 0 {
		maxWait = 2 * time.Second
	}
	return &ArgonGate{
		sem:     make(chan struct{}, concurrency),
		queue:   make(chan struct{}, queue),
		maxWait: maxWait,
	}
}

// ActiveCount mengembalikan jumlah komputasi hashing yang sedang berjalan bersamaan saat ini.
func (g *ArgonGate) ActiveCount() int64 {
	return g.activeCount.Load()
}

// PeakActive mengembalikan jumlah puncak komputasi hashing yang pernah tercapai bersamaan.
func (g *ArgonGate) PeakActive() int64 {
	return g.peakActive.Load()
}

var defaultGate = NewGate(2, 8, 2*time.Second)

// DefaultGate mengembalikan gate argon2 bawaan paket.
func DefaultGate() *ArgonGate {
	return defaultGate
}

// HashPassword hashes a password using argon2id with predefined parameters.
// Format: $argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordBytes {
		return "", ErrPasswordTooShort
	}
	if len(password) > MaxPasswordBytes {
		return "", ErrPasswordTooLong
	}

	salt := make([]byte, ArgonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	hash := argon2.IDKey([]byte(password), salt, ArgonTime, ArgonMemory, ArgonThreads, ArgonKeyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, ArgonMemory, ArgonTime, ArgonThreads, b64Salt, b64Hash)

	return encoded, nil
}

// VerifyPassword verifies a password against a hash string.
func VerifyPassword(password, encodedHash string) (bool, error) {
	if len(password) > MaxPasswordBytes {
		return false, ErrPasswordTooLong
	}

	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return false, errors.New("invalid hash format")
	}

	if parts[1] != "argon2id" {
		return false, errors.New("unsupported hashing algorithm")
	}

	var version int
	_, err := fmt.Sscanf(parts[2], "v=%d", &version)
	if err != nil {
		return false, err
	}
	if version != argon2.Version {
		return false, errors.New("incompatible version")
	}

	var memory uint32
	var time uint32
	var threads uint8
	_, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads)
	if err != nil {
		return false, err
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}

	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}

	keyLen := uint32(len(hash))

	comparisonHash := argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)

	if subtle.ConstantTimeCompare(hash, comparisonHash) == 1 {
		return true, nil
	}

	return false, nil
}

// HashPasswordGated menjalankan HashPassword dengan pengaman antrean dan konkurensi.
func (g *ArgonGate) HashPassword(ctx context.Context, password string) (string, error) {
	if len(password) < MinPasswordBytes {
		return "", ErrPasswordTooShort
	}
	if len(password) > MaxPasswordBytes {
		return "", ErrPasswordTooLong
	}

	// 1. Coba masuk antrean
	select {
	case g.queue <- struct{}{}:
		defer func() { <-g.queue }()
	default:
		return "", ErrServerBusy
	}

	// 2. Coba peroleh slot eksekusi
	timer := time.NewTimer(g.maxWait)
	defer timer.Stop()

	select {
	case g.sem <- struct{}{}:
		defer func() { <-g.sem }()
	case <-timer.C:
		return "", ErrServerBusy
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// Catat metrik konkurensi aktif
	cur := g.activeCount.Add(1)
	defer g.activeCount.Add(-1)
	for {
		p := g.peakActive.Load()
		if cur <= p || g.peakActive.CompareAndSwap(p, cur) {
			break
		}
	}

	return HashPassword(password)
}

// VerifyPasswordGated menjalankan VerifyPassword dengan pengaman antrean dan konkurensi.
func (g *ArgonGate) VerifyPassword(ctx context.Context, password, encodedHash string) (bool, error) {
	if len(password) > MaxPasswordBytes {
		return false, ErrPasswordTooLong
	}

	// 1. Coba masuk antrean
	select {
	case g.queue <- struct{}{}:
		defer func() { <-g.queue }()
	default:
		return false, ErrServerBusy
	}

	// 2. Coba peroleh slot eksekusi
	timer := time.NewTimer(g.maxWait)
	defer timer.Stop()

	select {
	case g.sem <- struct{}{}:
		defer func() { <-g.sem }()
	case <-timer.C:
		return false, ErrServerBusy
	case <-ctx.Done():
		return false, ctx.Err()
	}

	// Catat metrik konkurensi aktif
	cur := g.activeCount.Add(1)
	defer g.activeCount.Add(-1)
	for {
		p := g.peakActive.Load()
		if cur <= p || g.peakActive.CompareAndSwap(p, cur) {
			break
		}
	}

	return VerifyPassword(password, encodedHash)
}

// HashPasswordGated menggunakan defaultGate untuk membatasi hashing secara global.
func HashPasswordGated(ctx context.Context, password string) (string, error) {
	return defaultGate.HashPassword(ctx, password)
}

// VerifyPasswordGated menggunakan defaultGate untuk membatasi verifikasi kata sandi secara global.
func VerifyPasswordGated(ctx context.Context, password, encodedHash string) (bool, error) {
	return defaultGate.VerifyPassword(ctx, password, encodedHash)
}

// GenerateSessionToken generates a random 32-byte session token and its SHA-256 hash.
// The raw token is sent to the client as a cookie, the hash is saved in the database.
func GenerateSessionToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	rawToken := base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(rawToken))
	hashHex := hex.EncodeToString(hash[:])
	return rawToken, hashHex, nil
}

// HashSessionToken hashes a raw token to look it up in the database.
func HashSessionToken(rawToken string) string {
	hash := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(hash[:])
}

// GenerateCSRFToken generates a random 32-byte CSRF token
func GenerateCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SignCSRFToken menandatangani token CSRF dengan HMAC-SHA256 menggunakan sessionSecret.
// Format: <rawToken>.<hmacSignature>
func SignCSRFToken(token, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(token))
	sig := hex.EncodeToString(mac.Sum(nil))
	return token + "." + sig
}

// VerifySignedCSRFToken memverifikasi keabsahan tanda tangan HMAC pada token CSRF bertanda tangan.
// Mengembalikan token asli dan boolean validitas.
func VerifySignedCSRFToken(signedToken, secret string) (string, bool) {
	parts := strings.Split(signedToken, ".")
	if len(parts) != 2 {
		return "", false
	}
	rawToken := parts[0]
	sentSig := parts[1]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(rawToken))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(sentSig), []byte(expectedSig)) == 1 {
		return rawToken, true
	}
	return "", false
}

// SignFormTimingToken menandatangani token waktu (timestamp) untuk pertahanan bot formulir.
// Format: <timestamp_unix>.<hmacSignature>
func SignFormTimingToken(t time.Time, secret string) string {
	tsStr := fmt.Sprintf("%d", t.Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tsStr))
	sig := hex.EncodeToString(mac.Sum(nil))
	return tsStr + "." + sig
}

// VerifyFormTimingToken memverifikasi token waktu:
// - Tanda tangan HMAC harus valid
// - Form tidak boleh dikirim lebih cepat dari minAge (bawaan 2 detik)
// - Form tidak boleh lebih lama dari maxAge (bawaan 2 jam)
func VerifyFormTimingToken(token, secret string, now time.Time, minAge, maxAge time.Duration) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	tsStr := parts[0]
	sentSig := parts[1]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tsStr))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(sentSig), []byte(expectedSig)) != 1 {
		return false
	}

	var tsUnix int64
	if _, err := fmt.Sscanf(tsStr, "%d", &tsUnix); err != nil {
		return false
	}
	formTime := time.Unix(tsUnix, 0)

	elapsed := now.Sub(formTime)
	if elapsed < minAge || elapsed > maxAge {
		return false
	}
	return true
}
