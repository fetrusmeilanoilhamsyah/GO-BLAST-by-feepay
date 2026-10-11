package withdraw

import (
	"testing"
)

func TestEncryptDecryptDestination(t *testing.T) {
	key := []byte("12345678901234567890123456789012") // 32 bytes
	plaintext := "DANA 081234567890 a.n. Budi Santoso"

	encrypted, err := EncryptDestination(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	if encrypted == "" || encrypted == plaintext {
		t.Fatalf("Ciphertext should not be empty or plaintext")
	}

	decrypted, err := DecryptDestination(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("Expected %q, got %q", plaintext, decrypted)
	}

	// Test with dev fallback key
	encDev, err := EncryptDestination(plaintext, nil)
	if err != nil {
		t.Fatalf("Encrypt with fallback error: %v", err)
	}
	decDev, err := DecryptDestination(encDev, nil)
	if err != nil {
		t.Fatalf("Decrypt with fallback error: %v", err)
	}
	if decDev != plaintext {
		t.Fatalf("Expected %q, got %q", plaintext, decDev)
	}

	// Corrupted ciphertext
	_, err = DecryptDestination("invalid-base64!!!", key)
	if err == nil {
		t.Errorf("Expected error for invalid base64")
	}

	_, err = DecryptDestination("AAAA", key)
	if err == nil {
		t.Errorf("Expected error for too short ciphertext")
	}
}
