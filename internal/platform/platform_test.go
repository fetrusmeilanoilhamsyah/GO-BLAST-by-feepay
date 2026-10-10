package platform

import (
	"testing"
)

func TestMaskPhone(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal id",
			input:    "62812345678",
			expected: "6281****678",
		},
		{
			name:     "with plus",
			input:    "+62812345678",
			expected: "6281****678",
		},
		{
			name:     "short",
			input:    "123",
			expected: "123",
		},
		{
			name:     "empty",
			input:    "",
			expected: "",
		},
		{
			name:     "exactly 7",
			input:    "1234567",
			expected: "1234567",
		},
		{
			name:     "eight digits",
			input:    "12345678",
			expected: "1234*678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskPhone(tt.input); got != tt.expected {
				t.Errorf("MaskPhone() = %v, want %v", got, tt.expected)
			}
		})
	}
}
