package web

import (
	"context"
	"net/http"
	"testing"
)

func TestValidateLang(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"id", "id"},
		{"en", "en"},
		{"zh", "zh"},
		{"", "id"},
		{"fr", "id"},
		{"ID", "id"}, // case sensitive based on current implementation
	}

	for _, tc := range tests {
		actual := validateLang(tc.input)
		if actual != tc.expected {
			t.Errorf("validateLang(%q) = %q; expected %q", tc.input, actual, tc.expected)
		}
	}
}

func TestGetIPSpoofing(t *testing.T) {
	// 1. Loopback with X-Real-IP -> trusted
	req1, _ := http.NewRequest("GET", "/", nil)
	req1.RemoteAddr = "127.0.0.1:1234"
	req1.Header.Set("X-Real-IP", "203.0.113.1")
	if ip := getIP(req1); ip != "203.0.113.1" {
		t.Errorf("Expected 203.0.113.1 from loopback with X-Real-IP, got %q", ip)
	}

	// 2. Loopback IPv6 with X-Real-IP -> trusted
	req2, _ := http.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "[::1]:5678"
	req2.Header.Set("X-Real-IP", "203.0.113.2")
	if ip := getIP(req2); ip != "203.0.113.2" {
		t.Errorf("Expected 203.0.113.2 from loopback IPv6 with X-Real-IP, got %q", ip)
	}

	// 3. Non-loopback remote with spoofed X-Real-IP -> ignored!
	req3, _ := http.NewRequest("GET", "/", nil)
	req3.RemoteAddr = "198.51.100.25:44321"
	req3.Header.Set("X-Real-IP", "1.1.1.1")
	if ip := getIP(req3); ip != "198.51.100.25" {
		t.Errorf("Expected 198.51.100.25 (spoofed X-Real-IP must be ignored), got %q", ip)
	}

	// 4. Non-loopback remote with spoofed X-Forwarded-For -> ignored!
	req4, _ := http.NewRequest("GET", "/", nil)
	req4.RemoteAddr = "198.51.100.25:44321"
	req4.Header.Set("X-Forwarded-For", "8.8.8.8")
	if ip := getIP(req4); ip != "198.51.100.25" {
		t.Errorf("Expected 198.51.100.25 (spoofed X-Forwarded-For must be ignored), got %q", ip)
	}

	// 5. Loopback with X-Forwarded-For (no X-Real-IP) -> X-Forwarded-For is NOT trusted!
	req5, _ := http.NewRequest("GET", "/", nil)
	req5.RemoteAddr = "127.0.0.1:1234"
	req5.Header.Set("X-Forwarded-For", "8.8.8.8")
	if ip := getIP(req5); ip != "127.0.0.1" {
		t.Errorf("Expected 127.0.0.1 (X-Forwarded-For must not be trusted), got %q", ip)
	}
}

func TestCurrentUser(t *testing.T) {
	// Missing context
	req1, _ := http.NewRequest("GET", "/", nil)
	if u, ok := currentUser(req1); ok || u.ID != 0 {
		t.Errorf("Expected empty session and false for missing userContextKey")
	}

	// UserSession value
	req2, _ := http.NewRequest("GET", "/", nil)
	ctx2 := context.WithValue(req2.Context(), userContextKey, UserSession{ID: 42, Email: "test@test.com"})
	req2 = req2.WithContext(ctx2)
	if u, ok := currentUser(req2); !ok || u.ID != 42 || u.Email != "test@test.com" {
		t.Errorf("Expected session with ID 42, got %+v (ok=%v)", u, ok)
	}

	// *UserSession pointer
	req3, _ := http.NewRequest("GET", "/", nil)
	ctx3 := context.WithValue(req3.Context(), userContextKey, &UserSession{ID: 99, Email: "ptr@test.com"})
	req3 = req3.WithContext(ctx3)
	if u, ok := currentUser(req3); !ok || u.ID != 99 || u.Email != "ptr@test.com" {
		t.Errorf("Expected session with ID 99 from pointer, got %+v (ok=%v)", u, ok)
	}
}
