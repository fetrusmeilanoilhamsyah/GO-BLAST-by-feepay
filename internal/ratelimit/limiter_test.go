package ratelimit

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestLimiter_InjectedClockAndTokenBucket(t *testing.T) {
	fakeNow := time.Date(2026, 10, 15, 6, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		return fakeNow
	}

	// 5 percobaan per 15 menit
	cfg := Config{
		Layer:   "pair",
		Rate:    rate.Every(15 * time.Minute / 5),
		Burst:   5,
		Window:  15 * time.Minute,
		MaxKeys: 100,
		Clock:   clock,
	}
	lim := NewLimiter(cfg)
	defer lim.Stop()

	key := "127.0.0.1:user1"

	// 1. Konsumsi 5 token gagal berturut-turut
	for i := 0; i < 5; i++ {
		allow, _ := lim.Allow(key)
		if !allow {
			t.Fatalf("iterasi %d seharusnya masih diizinkan", i)
		}
		lim.RecordFailure(key)
	}

	// 2. Percobaan ke-6 harus ditolak
	allow, retryAfter := lim.Allow(key)
	if allow {
		t.Fatalf("percobaan ke-6 seharusnya ditolak!")
	}
	if retryAfter <= 0 {
		t.Fatalf("retryAfter harus positif, didapat %v", retryAfter)
	}

	// 3. Majukan waktu 3 menit (1 token terisi kembali)
	fakeNow = fakeNow.Add(3 * time.Minute + 1*time.Second)

	allow, _ = lim.Allow(key)
	if !allow {
		t.Fatalf("setelah 3 menit seharusnya 1 token terisi dan diizinkan")
	}

	// 4. Reset keberhasilan
	lim.Reset(key)
	allow, _ = lim.Allow(key)
	if !allow {
		t.Fatalf("setelah reset seharusnya diizinkan")
	}
}

func TestLimiter_MaxKeysLRUAndFailClosed(t *testing.T) {
	fakeNow := time.Now()
	clock := func() time.Time { return fakeNow }

	// Kapasitas hanya 3 kunci, fail-closed false
	lim := NewLimiter(Config{
		Layer:      "ip",
		Rate:       rate.Every(1 * time.Minute),
		Burst:      5,
		Window:     5 * time.Minute,
		MaxKeys:    3,
		Clock:      clock,
		FailClosed: false,
	})
	defer lim.Stop()

	lim.Allow("k1")
	lim.Allow("k2")
	lim.Allow("k3")

	// Peta memiliki 3 kunci
	lim.mu.Lock()
	count := len(lim.entries)
	lim.mu.Unlock()
	if count != 3 {
		t.Fatalf("ekspektasi 3 kunci, didapat %d", count)
	}

	// Tambah k4 -> k1 tertua harus dibuang
	lim.Allow("k4")

	lim.mu.Lock()
	count = len(lim.entries)
	_, hasK1 := lim.entries["k1"]
	_, hasK4 := lim.entries["k4"]
	lim.mu.Unlock()

	if count > 3 {
		t.Fatalf("jumlah kunci melebihi batas 3: %d", count)
	}
	if hasK1 {
		t.Errorf("k1 tertua seharusnya sudah dibuang!")
	}
	if !hasK4 {
		t.Errorf("k4 baru seharusnya ada!")
	}

	// Uji FailClosed: jika fail-closed true, saat penuh dan tidak bisa buang / tolak
	limFailClosed := NewLimiter(Config{
		Layer:      "login",
		Rate:       rate.Every(1 * time.Minute),
		Burst:      2,
		Window:     5 * time.Minute,
		MaxKeys:    2,
		Clock:      clock,
		FailClosed: true,
	})
	defer limFailClosed.Stop()

	limFailClosed.Allow("a")
	limFailClosed.Allow("b")

	// Kunci 'c' baru pada fail-closed limiter harus ditolak
	allow, retry := limFailClosed.Allow("c")
	if allow {
		t.Errorf("kunci baru saat limiter penuh dengan fail-closed seharusnya ditolak!")
	}
	if retry <= 0 {
		t.Errorf("retryAfter harus positif")
	}
}

func TestLimiter_ActiveRestrictionsAndMasking(t *testing.T) {
	lim := NewLimiter(Config{
		Layer:   "pair",
		Rate:    rate.Every(15 * time.Minute / 5),
		Burst:   1,
		Window:  15 * time.Minute,
		MaxKeys: 10,
	})
	defer lim.Stop()

	key := "203.0.113.195:john_doe@example.com"
	lim.RecordFailure(key) // Habiskan 1 token burst

	restr := lim.ActiveRestrictions()
	if len(restr) != 1 {
		t.Fatalf("ekspektasi 1 restriksi aktif, didapat %d", len(restr))
	}

	entry := restr[0]
	if !strings.Contains(entry.MaskedKey, "***") {
		t.Errorf("MaskedKey harus menyamarkan IP dan identifier: %q", entry.MaskedKey)
	}
	if entry.Remaining <= 0 {
		t.Errorf("Remaining harus > 0: %v", entry.Remaining)
	}

	// Unblock via Reset
	lim.Reset(key)
	restrAfter := lim.ActiveRestrictions()
	if len(restrAfter) != 0 {
		t.Errorf("setelah reset, restriksi harus 0, didapat %d", len(restrAfter))
	}
}

func TestMaskLimiterKey(t *testing.T) {
	cases := []struct {
		input    string
		contains string
	}{
		{"192.168.1.50", "192.168.***.***"},
		{"user@gmail.com", "us***@gmail.com"},
		{"192.168.1.50:user@gmail.com", "192.168.***.***:us***@gmail.com"},
		{"admin", "ad***"},
	}

	for _, tc := range cases {
		out := MaskLimiterKey(tc.input)
		if !strings.Contains(out, "***") {
			t.Errorf("input %s: output %s tidak mengandung ***", tc.input, out)
		}
	}
}
