package turnstile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTurnstile_VerificationScenarios(t *testing.T) {
	var mockStatus int
	var mockResp VerifyResponse

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form err: %v", err)
		}

		if r.FormValue("secret") != "test-secret" {
			t.Errorf("wrong secret: %s", r.FormValue("secret"))
		}

		if mockStatus != 0 {
			w.WriteHeader(mockStatus)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResp)
	}))
	defer server.Close()

	client := NewClient("test-sitekey", "test-secret", "example.com")
	client.VerifyURL = server.URL

	// 1. Sukses valid
	mockStatus = 0
	mockResp = VerifyResponse{
		Success:  true,
		Action:   "login",
		Hostname: "example.com",
	}

	ok, err := client.Verify(context.Background(), "valid-token", "login", "127.0.0.1")
	if !ok || err != nil {
		t.Fatalf("expected success, got ok=%v, err=%v", ok, err)
	}

	// 2. Gagal (token tidak valid / bot)
	mockResp = VerifyResponse{
		Success:    false,
		ErrorCodes: []string{"invalid-input-response"},
	}
	ok, err = client.Verify(context.Background(), "invalid-token", "login", "127.0.0.1")
	if ok || err != ErrVerificationFail {
		t.Errorf("expected ErrVerificationFail, got ok=%v, err=%v", ok, err)
	}

	// 3. Action mismatch
	mockResp = VerifyResponse{
		Success:  true,
		Action:   "register", // seharusnya login
		Hostname: "example.com",
	}
	ok, err = client.Verify(context.Background(), "token", "login", "127.0.0.1")
	if ok || err != ErrActionMismatch {
		t.Errorf("expected ErrActionMismatch, got ok=%v, err=%v", ok, err)
	}

	// 4. Hostname mismatch
	mockResp = VerifyResponse{
		Success:  true,
		Action:   "login",
		Hostname: "attacker.com", // seharusnya example.com
	}
	ok, err = client.Verify(context.Background(), "token", "login", "127.0.0.1")
	if ok || err != ErrHostnameMismatch {
		t.Errorf("expected ErrHostnameMismatch, got ok=%v, err=%v", ok, err)
	}

	// 5. Cloudflare unreachable (500 atau timeout)
	mockStatus = http.StatusInternalServerError
	ok, err = client.Verify(context.Background(), "token", "login", "127.0.0.1")
	if ok || err != ErrServiceOutage {
		t.Errorf("expected ErrServiceOutage on HTTP 500, got ok=%v, err=%v", ok, err)
	}

	// 6. Token kosong
	ok, err = client.Verify(context.Background(), "   ", "login", "127.0.0.1")
	if ok || err != ErrTokenMissing {
		t.Errorf("expected ErrTokenMissing, got ok=%v, err=%v", ok, err)
	}

	// 7. Client disabled (kunci kosong) -> lolos otomatis
	disabledClient := NewClient("", "", "")
	ok, err = disabledClient.Verify(context.Background(), "", "login", "127.0.0.1")
	if !ok || err != nil {
		t.Errorf("expected true when disabled, got ok=%v, err=%v", ok, err)
	}
}

func TestTurnstile_TimeoutHandling(t *testing.T) {
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()

	client := NewClient("site", "sec", "example.com")
	client.VerifyURL = slowServer.URL
	client.HTTPClient.Timeout = 10 * time.Millisecond // paksa timeout

	ok, err := client.Verify(context.Background(), "token", "login", "127.0.0.1")
	if ok || err != ErrServiceOutage {
		t.Errorf("expected ErrServiceOutage on timeout, got ok=%v, err=%v", ok, err)
	}
}
