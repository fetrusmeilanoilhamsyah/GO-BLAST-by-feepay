package withdraw_test

import (
	"crypto/rand"
	"errors"
	"io"
	"testing"

	"goblast/internal/withdraw"
)

func TestWithdrawEncryption(t *testing.T) {
	// 1. Enkripsi dan dekripsi dengan kunci acak berhasil
	key1 := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key1); err != nil {
		t.Fatalf("Gagal menghasilkan kunci acak 1: %v", err)
	}

	plaintext := "DANA 081200000001 a.n. Fiktif Worker"

	ciphertext, err := withdraw.EncryptDestination(plaintext, key1)
	if err != nil {
		t.Fatalf("EncryptDestination gagal: %v", err)
	}
	if ciphertext == "" || ciphertext == plaintext {
		t.Fatalf("Ciphertext tidak boleh kosong atau berupa plaintext")
	}

	decrypted, err := withdraw.DecryptDestination(ciphertext, key1)
	if err != nil {
		t.Fatalf("DecryptDestination gagal: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("Hasil dekripsi tidak cocok: got %q, want %q", decrypted, plaintext)
	}

	// 2. Kunci salah menghasilkan error dan ciphertext dari kunci lain tidak terbaca
	key2 := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key2); err != nil {
		t.Fatalf("Gagal menghasilkan kunci acak 2: %v", err)
	}

	_, err = withdraw.DecryptDestination(ciphertext, key2)
	if err == nil {
		t.Fatalf("Diharapkan error saat dekripsi dengan kunci berbeda, tetapi berhasil")
	}

	// 3. Kunci bukan 32 byte menghasilkan error pada enkripsi dan dekripsi
	invalidKeys := [][]byte{
		nil,
		{},
		[]byte("short-key"),
		make([]byte, 31),
		make([]byte, 33),
	}

	for _, k := range invalidKeys {
		_, err := withdraw.EncryptDestination(plaintext, k)
		if !errors.Is(err, withdraw.ErrInvalidKey) {
			t.Errorf("EncryptDestination dengan kunci panjang %d harus mengembalikan ErrInvalidKey, got: %v", len(k), err)
		}

		_, err = withdraw.DecryptDestination(ciphertext, k)
		if !errors.Is(err, withdraw.ErrInvalidKey) {
			t.Errorf("DecryptDestination dengan kunci panjang %d harus mengembalikan ErrInvalidKey, got: %v", len(k), err)
		}
	}

	// 4. Ciphertext tidak valid / rusak
	_, err = withdraw.DecryptDestination("bukan-base64-valid!@#", key1)
	if err == nil {
		t.Errorf("Diharapkan error untuk base64 tidak valid")
	}

	_, err = withdraw.DecryptDestination("AAAA", key1)
	if err == nil {
		t.Errorf("Diharapkan error untuk ciphertext terlalu pendek")
	}
}
