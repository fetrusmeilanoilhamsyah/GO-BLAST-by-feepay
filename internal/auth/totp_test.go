package auth

import (
	"strings"
	"testing"
	"time"
)

func TestTOTP_SecretAndAuthURI(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret failed: %v", err)
	}

	if len(secret) < 32 {
		t.Errorf("Secret length expected >= 32, got %d (%s)", len(secret), secret)
	}

	uri := TOTPAuthURI("admin@goblast.com", "GO BLAST", secret)
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Errorf("URI format invalid: %s", uri)
	}
	if !strings.Contains(uri, "secret="+secret) {
		t.Errorf("URI does not contain secret: %s", uri)
	}
	if !strings.Contains(uri, "issuer=GO+BLAST") && !strings.Contains(uri, "issuer=GO%20BLAST") {
		t.Errorf("URI does not contain issuer: %s", uri)
	}
}

func TestTOTP_ValidationAndStepTolerance(t *testing.T) {
	// Rahasia contoh base32: 20 bytes "12345678901234567890" = GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	now := time.Date(2026, 10, 15, 7, 0, 0, 0, time.UTC)

	code, step, err := GenerateTOTPCode(secret, now)
	if err != nil {
		t.Fatalf("GenerateTOTPCode failed: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("Code length expected 6, got %s", code)
	}

	// 1. Verifikasi step sekarang: harus valid
	valid, matchedStep, err := ValidateTOTP(secret, code, now, 0)
	if err != nil || !valid {
		t.Fatalf("ValidateTOTP failed at current time: valid=%v, err=%v", valid, err)
	}
	if matchedStep != step {
		t.Errorf("Matched step expected %d, got %d", step, matchedStep)
	}

	// 2. Anti-Replay: jika lastStep sudah sama dengan matchedStep, kode tidak boleh dipakai ulang
	validReplay, _, err := ValidateTOTP(secret, code, now, matchedStep)
	if err != nil {
		t.Fatalf("ValidateTOTP returned error on replay check: %v", err)
	}
	if validReplay {
		t.Errorf("Anti-Replay GAGAL: kode yang sudah pernah dipakai pada step %d berhasil divalidasi lagi!", matchedStep)
	}

	// 3. Toleransi 1 langkah ke belakang (-30 detik)
	timePast := now.Add(-30 * time.Second)
	codePast, _, _ := GenerateTOTPCode(secret, timePast)
	validPast, _, _ := ValidateTOTP(secret, codePast, now, 0)
	if !validPast {
		t.Errorf("Toleransi -1 step gagal diakui")
	}

	// 4. Toleransi 1 langkah ke depan (+30 detik)
	timeFuture := now.Add(30 * time.Second)
	codeFuture, _, _ := GenerateTOTPCode(secret, timeFuture)
	validFuture, _, _ := ValidateTOTP(secret, codeFuture, now, 0)
	if !validFuture {
		t.Errorf("Toleransi +1 step gagal diakui")
	}

	// 5. Di luar toleransi (misal 2 langkah ke belakang = -60 detik) harus DITOLAK
	timeTooFar := now.Add(-60 * time.Second)
	codeTooFar, _, _ := GenerateTOTPCode(secret, timeTooFar)
	validTooFar, _, _ := ValidateTOTP(secret, codeTooFar, now, 0)
	if validTooFar {
		t.Errorf("Kode 2 langkah ke belakang (-60s) seharusnya ditolak")
	}

	// 6. Kode salah harus ditolak
	validWrong, _, _ := ValidateTOTP(secret, "000000", now, 0)
	if validWrong {
		t.Errorf("Kode salah '000000' seharusnya ditolak")
	}
}

func TestTOTP_RecoveryCodes(t *testing.T) {
	plain, hashed, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes failed: %v", err)
	}

	if len(plain) != 10 || len(hashed) != 10 {
		t.Fatalf("Expected 10 codes, got plain=%d, hashed=%d", len(plain), len(hashed))
	}

	for i := 0; i < 10; i++ {
		p := plain[i]
		h := hashed[i]

		// Format XXXXX-XXXXX
		if len(p) != 11 || p[5] != '-' {
			t.Errorf("Recovery code %d format invalid: %s", i, p)
		}

		// Verifikasi kecocokan hash
		computedHash := HashRecoveryCode(p)
		if computedHash != h {
			t.Errorf("Recovery code hash mismatch for %s: %s != %s", p, computedHash, h)
		}

		// Verifikasi dengan spasi atau huruf kecil
		caseInsensitive := HashRecoveryCode(strings.ToLower(p))
		if caseInsensitive != h {
			t.Errorf("Recovery code case-insensitive hash mismatch for %s", p)
		}
	}
}
