package money

import (
	"errors"
	"fmt"
	"strings"
)

var ErrNonPositiveKurs = errors.New("kurs harus lebih dari 0")

// UsdtMicroFromIDR mengonversi rupiah ke mikro-USDT dengan pembulatan ke bawah.
// Rumus: (idr * 1_000_000) / kurs.
// Semua perhitungan menggunakan bilangan bulat 64-bit murni (tanpa float).
func UsdtMicroFromIDR(idr, kurs int64) (int64, error) {
	if kurs <= 0 {
		return 0, ErrNonPositiveKurs
	}
	if idr <= 0 {
		return 0, nil
	}
	return (idr * 1000000) / kurs, nil
}

// FormatIDR memformat bilangan bulat rupiah dengan pemisah ribuan titik.
// Contoh: 1000000 -> "1.000.000", -5000 -> "-5.000".
func FormatIDR(n int64) string {
	if n < 0 {
		return "-" + formatIDRPositive(-n)
	}
	return formatIDRPositive(n)
}

func formatIDRPositive(n int64) string {
	s := fmt.Sprintf("%d", n)
	var out []byte
	l := len(s)
	for i, c := range s {
		if i > 0 && (l-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

// FormatUSDT memformat mikro-USDT (1.000.000 = 1 USDT) menjadi string desimal
// dengan maksimal 6 angka di belakang koma, serta membuang nol dan titik di ujung.
// Contoh: 1000000 -> "1", 666 -> "0.000666", 1500000 -> "1.5", 0 -> "0".
func FormatUSDT(micro int64) string {
	if micro == 0 {
		return "0"
	}
	neg := micro < 0
	if neg {
		micro = -micro
	}
	whole := micro / 1000000
	frac := micro % 1000000
	s := fmt.Sprintf("%d.%06d", whole, frac)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		s = "0"
	}
	if neg {
		return "-" + s
	}
	return s
}
