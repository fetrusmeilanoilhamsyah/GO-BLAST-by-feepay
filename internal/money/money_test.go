package money_test

import (
	"testing"

	"goblast/internal/money"
)

func TestUsdtMicroFromIDR(t *testing.T) {
	// 1. Pembulatan ke bawah (floor): 10.000 IDR dengan kurs 15.000 -> 666.666... dibulatkan ke bawah jadi 666.666 mikro
	// (10.000 * 1.000.000) / 15.000 = 666666 mikro
	micro, err := money.UsdtMicroFromIDR(10000, 15000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if micro != 666666 {
		t.Errorf("got %d, want 666666", micro)
	}

	// 10 IDR kurs 15.000: (10 * 1.000.000) / 15.000 = 666 mikro (bukan 667)
	microSmall, err := money.UsdtMicroFromIDR(10, 15000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if microSmall != 666 {
		t.Errorf("got %d, want 666", microSmall)
	}

	// 15.000 IDR kurs 15.000 -> tepat 1.000.000 mikro (1 USDT)
	microExact, err := money.UsdtMicroFromIDR(15000, 15000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if microExact != 1000000 {
		t.Errorf("got %d, want 1000000", microExact)
	}

	// 0 IDR -> 0
	microZero, err := money.UsdtMicroFromIDR(0, 15000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if microZero != 0 {
		t.Errorf("got %d, want 0", microZero)
	}

	// Kurs <= 0 menghasilkan error
	_, errZeroKurs := money.UsdtMicroFromIDR(10000, 0)
	if errZeroKurs == nil {
		t.Errorf("expected error for kurs = 0, got nil")
	}

	_, errNegKurs := money.UsdtMicroFromIDR(10000, -15000)
	if errNegKurs == nil {
		t.Errorf("expected error for kurs < 0, got nil")
	}
}

func TestFormatIDR(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{25, "25"},
		{400, "400"},
		{1000, "1.000"},
		{15000, "15.000"},
		{1000000, "1.000.000"},
		{25000000, "25.000.000"},
		{-5000, "-5.000"},
		{-1000000, "-1.000.000"},
	}

	for _, tt := range tests {
		got := money.FormatIDR(tt.input)
		if got != tt.want {
			t.Errorf("FormatIDR(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatUSDT(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{666, "0.000666"},
		{1000000, "1"},
		{1500000, "1.5"},
		{2000, "0.002"},
		{3000, "0.003"},
		{10000000, "10"},
		{10050000, "10.05"},
		{-1000000, "-1"},
		{-666, "-0.000666"},
	}

	for _, tt := range tests {
		got := money.FormatUSDT(tt.input)
		if got != tt.want {
			t.Errorf("FormatUSDT(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
