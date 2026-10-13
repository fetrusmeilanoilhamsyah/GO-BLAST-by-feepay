package withdraw

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"strings"
)

var (
	ErrInvalidKey        = errors.New("kunci enkripsi harus tepat 32 byte")
	ErrDecryptionFailed  = errors.New("tujuan tidak terbaca, minta pekerja mengisi ulang")
	ErrCiphertextTooShort = errors.New("ciphertext terlalu pendek")
)

const (
	KeyIDActive = "k1"
	KeyIDPrev   = "k0"
	PrefixV1    = "v1:"
)

// EncryptDestination mengenkripsi teks tujuan transfer dengan AES-256-GCM berformat "v1:k1:<base64>".
func EncryptDestination(plaintext string, key []byte) (string, error) {
	return EncryptDestinationWithKeyID(plaintext, KeyIDActive, key)
}

// EncryptDestinationWithKeyID mengenkripsi teks dengan key ID tertentu (misal k1 atau k0).
func EncryptDestinationWithKeyID(plaintext string, keyID string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return PrefixV1 + keyID + ":" + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptDestination mendekripsi teks tujuan transfer dengan kunci aktif.
func DecryptDestination(ciphertext string, key []byte) (string, error) {
	return DecryptDestinationWithPrev(ciphertext, key, nil)
}

// DecryptDestinationWithPrev mendekripsi teks tujuan transfer dengan kunci aktif (k1) dan kunci lama (k0).
func DecryptDestinationWithPrev(ciphertext string, keyActive []byte, keyPrev []byte) (string, error) {
	ciphertext = strings.TrimSpace(ciphertext)
	if ciphertext == "" {
		return "", ErrDecryptionFailed
	}

	// 1. Format v1:<key_id>:<base64>
	if strings.HasPrefix(ciphertext, PrefixV1) {
		parts := strings.SplitN(ciphertext, ":", 3)
		if len(parts) == 3 {
			keyID := parts[1]
			b64Payload := parts[2]

			switch keyID {
			case KeyIDActive:
				if len(keyActive) == 32 {
					if plain, err := decryptGCM(b64Payload, keyActive); err == nil {
						return plain, nil
					}
				}
				// Fallback ke keyPrev jika ada
				if len(keyPrev) == 32 {
					if plain, err := decryptGCM(b64Payload, keyPrev); err == nil {
						return plain, nil
					}
				}
			case KeyIDPrev:
				if len(keyPrev) == 32 {
					if plain, err := decryptGCM(b64Payload, keyPrev); err == nil {
						return plain, nil
					}
				}
				// Fallback ke keyActive
				if len(keyActive) == 32 {
					if plain, err := decryptGCM(b64Payload, keyActive); err == nil {
						return plain, nil
					}
				}
			default:
				// Key ID tidak dikenal, coba aktif lalu prev
				if len(keyActive) == 32 {
					if plain, err := decryptGCM(b64Payload, keyActive); err == nil {
						return plain, nil
					}
				}
				if len(keyPrev) == 32 {
					if plain, err := decryptGCM(b64Payload, keyPrev); err == nil {
						return plain, nil
					}
				}
			}
		}
	} else {
		// 2. Format legacy (base64 langsung tanpa prefix)
		if len(keyActive) == 32 {
			if plain, err := decryptGCM(ciphertext, keyActive); err == nil {
				return plain, nil
			}
		}
		if len(keyPrev) == 32 {
			if plain, err := decryptGCM(ciphertext, keyPrev); err == nil {
				return plain, nil
			}
		}
	}

	// Jangan pernah log isi ciphertext atau tujuan! Hanya kode kesalahan.
	slog.Error("dekripsi tujuan gagal", "err_code", "ERR_DECRYPT_FAIL")
	return "", ErrDecryptionFailed
}

func decryptGCM(b64Data string, key []byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", ErrCiphertextTooShort
	}
	nonce, cipherBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, cipherBytes, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// NormalizeDestination membersihkan string tujuan: ambil bagian nomor/alamat (sebelum tanda nama kurung), huruf kecil, tanpa spasi.
func NormalizeDestination(dest string) string {
	dest = strings.TrimSpace(dest)
	if idx := strings.Index(dest, " ("); idx != -1 {
		dest = dest[:idx]
	}
	dest = strings.ToLower(dest)
	dest = strings.ReplaceAll(dest, " ", "")
	dest = strings.ReplaceAll(dest, "-", "")
	return dest
}

func deriveHashKey(key []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("goblast_payout_dest_hash_salt"))
	return h.Sum(nil)
}

// HashDestination menghitung HMAC-SHA256 dari tujuan yang dinormalisasi menggunakan kunci turunan dari ENCRYPTION_KEY.
func HashDestination(dest string, key []byte) string {
	if len(key) != 32 {
		return ""
	}
	clean := NormalizeDestination(dest)
	if clean == "" {
		return ""
	}
	hKey := deriveHashKey(key)
	mac := hmac.New(sha256.New, hKey)
	mac.Write([]byte(clean))
	return hex.EncodeToString(mac.Sum(nil))
}
