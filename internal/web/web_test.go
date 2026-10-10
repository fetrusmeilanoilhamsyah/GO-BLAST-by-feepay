package web

import (
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
