package withdraw_test

import (
	"crypto/rand"
	"errors"
	"io"
	"strings"
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
	if !strings.HasPrefix(ciphertext, "v1:k1:") {
		t.Errorf("Ciphertext baru harus berformat 'v1:k1:<base64>', got: %s", ciphertext)
	}

	decrypted, err := withdraw.DecryptDestination(ciphertext, key1)
	if err != nil {
		t.Fatalf("DecryptDestination gagal: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("Hasil dekripsi tidak cocok: got %q, want %q", decrypted, plaintext)
	}

	// 2. Kunci salah menghasilkan error dan pesan aman
	key2 := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key2); err != nil {
		t.Fatalf("Gagal menghasilkan kunci acak 2: %v", err)
	}

	_, err = withdraw.DecryptDestination(ciphertext, key2)
	if err == nil {
		t.Fatalf("Diharapkan error saat dekripsi dengan kunci berbeda, tetapi berhasil")
	}
	if !errors.Is(err, withdraw.ErrDecryptionFailed) {
		t.Errorf("Diharapkan ErrDecryptionFailed saat dekripsi salah kunci, got: %v", err)
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
		if err == nil {
			t.Errorf("DecryptDestination dengan kunci panjang %d harus mengembalikan error", len(k))
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

func TestKeyRotation_K0andK1(t *testing.T) {
	keyOld := make([]byte, 32)
	keyNew := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, keyOld)
	_, _ = io.ReadFull(rand.Reader, keyNew)

	plaintext := "081200000002"

	// Enkripsi dengan kunci lama (k0)
	cipherK0, err := withdraw.EncryptDestinationWithKeyID(plaintext, withdraw.KeyIDPrev, keyOld)
	if err != nil {
		t.Fatalf("gagal enkripsi k0: %v", err)
	}
	if !strings.HasPrefix(cipherK0, "v1:k0:") {
		t.Fatalf("expected prefix v1:k0:, got %s", cipherK0)
	}

	// Coba dekripsi hanya dengan keyNew (tanpa keyPrev) -> harus gagal
	_, err = withdraw.DecryptDestinationWithPrev(cipherK0, keyNew, nil)
	if err == nil {
		t.Fatalf("seharusnya gagal jika keyOld tidak disediakan di keyPrev")
	}

	// Dekripsi dengan keyNew sebagai aktif dan keyOld sebagai prev -> harus berhasil
	dec, err := withdraw.DecryptDestinationWithPrev(cipherK0, keyNew, keyOld)
	if err != nil {
		t.Fatalf("gagal dekripsi rotasi: %v", err)
	}
	if dec != plaintext {
		t.Fatalf("got %s, want %s", dec, plaintext)
	}

	// Enkripsi dengan kunci baru (k1)
	cipherK1, err := withdraw.EncryptDestination(plaintext, keyNew)
	if err != nil {
		t.Fatalf("gagal enkripsi k1: %v", err)
	}
	decK1, err := withdraw.DecryptDestinationWithPrev(cipherK1, keyNew, keyOld)
	if err != nil {
		t.Fatalf("gagal dekripsi k1: %v", err)
	}
	if decK1 != plaintext {
		t.Fatalf("got %s, want %s", decK1, plaintext)
	}
}

func TestDestinationHashing(t *testing.T) {
	key := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, key)

	d1 := " 0812-3456-7890 "
	d2 := "081234567890 (Budi Santoso)"
	d3 := "081234567890"

	h1 := withdraw.HashDestination(d1, key)
	h2 := withdraw.HashDestination(d2, key)
	h3 := withdraw.HashDestination(d3, key)

	if h1 == "" || h2 == "" || h3 == "" {
		t.Fatalf("hash tidak boleh kosong")
	}
	if h1 != h2 || h2 != h3 {
		t.Fatalf("hash normalisasi harus identik: h1=%s, h2=%s, h3=%s", h1, h2, h3)
	}

	// Tujuan berbeda menghasilkan hash berbeda
	hOther := withdraw.HashDestination("081299999999", key)
	if hOther == h1 {
		t.Fatalf("hash untuk nomor berbeda tidak boleh sama")
	}

	// Kunci berbeda menghasilkan hash berbeda
	keyOther := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, keyOther)
	hDifferentKey := withdraw.HashDestination(d1, keyOther)
	if hDifferentKey == h1 {
		t.Fatalf("hash dengan kunci berbeda tidak boleh sama")
	}
}
