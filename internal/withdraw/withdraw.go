package withdraw

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// devFallbackKey dipakai saat ENCRYPTION_KEY kosong pada mode dev/test (tepat 32 byte).
var devFallbackKey = []byte("goblast-feepay-32byte-aes-key!!!")

// ResolveKey memastikan kunci berukuran tepat 32 byte.
func ResolveKey(key []byte) []byte {
	if len(key) == 32 {
		return key
	}
	return devFallbackKey
}

// EncryptDestination mengenkripsi teks tujuan transfer dengan AES-256-GCM.
func EncryptDestination(plaintext string, key []byte) (string, error) {
	k := ResolveKey(key)
	block, err := aes.NewCipher(k)
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
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptDestination mendekripsi teks tujuan transfer dengan AES-256-GCM.
func DecryptDestination(ciphertextB64 string, key []byte) (string, error) {
	k := ResolveKey(key)
	data, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext terlalu pendek")
	}

	// 1. Coba dekripsi dengan kunci utama
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err == nil {
		return string(plaintext), nil
	}

	// 2. Jika kunci utama gagal dan berbeda dengan devFallbackKey, coba devFallbackKey
	if string(k) != string(devFallbackKey) {
		fallbackBlock, fbErr := aes.NewCipher(devFallbackKey)
		if fbErr == nil {
			fallbackGCM, fbGcmErr := cipher.NewGCM(fallbackBlock)
			if fbGcmErr == nil && len(data) >= fallbackGCM.NonceSize() {
				fbSize := fallbackGCM.NonceSize()
				fbNonce, fbCiphertext := data[:fbSize], data[fbSize:]
				if fbPlaintext, fbOpenErr := fallbackGCM.Open(nil, fbNonce, fbCiphertext, nil); fbOpenErr == nil {
					return string(fbPlaintext), nil
				}
			}
		}
	}

	return "", err
}
