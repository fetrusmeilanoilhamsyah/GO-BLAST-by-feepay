package ratelimit

import (
	"container/list"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"goblast/internal/safe"

	"golang.org/x/time/rate"
)

// Clock mendefinisikan penyedia waktu untuk pengetesan deterministik.
type Clock func() time.Time

// RestrictionEntry merepresentasikan satu pembatasan laju yang sedang aktif.
type RestrictionEntry struct {
	Key         string        `json:"key"`
	MaskedKey   string        `json:"masked_key"`
	Layer       string        `json:"layer"`
	Remaining   time.Duration `json:"remaining"`
	ExpiresAt   time.Time     `json:"expires_at"`
}

// entry menyimpan status rate limiter per kunci dalam cache LRU.
type entry struct {
	key       string
	layer     string
	limiter   *rate.Limiter
	lastSeen  time.Time
	element   *list.Element
	failures  int
	window    time.Duration
	maxBurst  int
	rateLimit rate.Limit
}

// Limiter mengelola sekumpulan rate limiter berbasis token bucket
// dengan batas kapasitas kunci (LRU), pembersih berkala, dan jam yang bisa disuntikkan.
type Limiter struct {
	mu         sync.Mutex
	entries    map[string]*entry
	lruList    *list.List
	maxKeys    int
	rateLimit  rate.Limit
	burst      int
	window     time.Duration
	layer      string
	clock      Clock
	stopCh     chan struct{}
	stopped    bool
	failClosed bool
}

// Config opsi konfigurasi untuk pembuatan Limiter.
type Config struct {
	Layer      string
	Rate       rate.Limit
	Burst      int
	Window     time.Duration
	MaxKeys    int
	Clock      Clock
	FailClosed bool
}

// NewLimiter membuat instance Limiter baru dan memulai goroutine pembersih berkala.
func NewLimiter(cfg Config) *Limiter {
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	maxKeys := cfg.MaxKeys
	if maxKeys <= 0 {
		maxKeys = 10000
	}

	l := &Limiter{
		entries:    make(map[string]*entry),
		lruList:    list.New(),
		maxKeys:    maxKeys,
		rateLimit:  cfg.Rate,
		burst:      cfg.Burst,
		window:     cfg.Window,
		layer:      cfg.Layer,
		clock:      clock,
		stopCh:     make(chan struct{}),
		failClosed: cfg.FailClosed,
	}

	// Goroutine pembersih berkala entri kedaluwarsa dengan pemulihan panic
	safe.Go("ratelimit-cleaner", func() {
		l.cleanupLoop()
	})

	return l
}

// Stop menghentikan goroutine pembersih berkala.
func (l *Limiter) Stop() {
	l.mu.Lock()
	if !l.stopped {
		l.stopped = true
		close(l.stopCh)
	}
	l.mu.Unlock()
}

func (l *Limiter) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.CleanExpired()
		}
	}
}

// CleanExpired membersihkan entri yang sudah tidak aktif melewati durasi jendela.
func (l *Limiter) CleanExpired() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	for k, e := range l.entries {
		// Jika token sudah penuh kembali dan tidak ada aktivitas dalam jendela waktu
		if now.Sub(e.lastSeen) > e.window && e.limiter.TokensAt(now) >= float64(e.maxBurst)-0.001 {
			l.lruList.Remove(e.element)
			delete(l.entries, k)
		}
	}
}

// getOrCreate mengambil entri atau membuatnya jika belum ada.
// Jika kapasitas penuh, entri tertua (LRU) dibuang.
func (l *Limiter) getOrCreate(key string) (*entry, bool) {
	now := l.clock()
	if e, exists := l.entries[key]; exists {
		e.lastSeen = now
		l.lruList.MoveToFront(e.element)
		return e, true
	}

	// Cek kapasitas
	if len(l.entries) >= l.maxKeys {
		if l.failClosed {
			// Gagal tertutup: tidak membuat entri baru, tolak
			return nil, false
		}
		// Buang entri tertua
		oldest := l.lruList.Back()
		if oldest != nil {
			oldEntry := oldest.Value.(*entry)
			l.lruList.Remove(oldest)
			delete(l.entries, oldEntry.key)
		}
	}

	lim := rate.NewLimiter(l.rateLimit, l.burst)
	// Set initial limit & burst at 'now' jika clock dimajukan/mundurkan di tes
	lim.SetBurstAt(now, l.burst)
	lim.SetLimitAt(now, l.rateLimit)

	e := &entry{
		key:       key,
		layer:     l.layer,
		limiter:   lim,
		lastSeen:  now,
		window:    l.window,
		maxBurst:  l.burst,
		rateLimit: l.rateLimit,
	}
	e.element = l.lruList.PushFront(e)
	l.entries[key] = e
	return e, true
}

// Allow memeriksa apakah sebuah kunci diizinkan untuk melakukan percobaan.
// Mengembalikan (allowed bool, retryAfter time.Duration).
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	e, ok := l.getOrCreate(key)
	if !ok {
		// Fail-closed
		return false, 15 * time.Minute
	}

	tokens := e.limiter.TokensAt(now)
	if tokens >= 1.0 {
		return true, 0
	}

	// Hitung sisa waktu tunggu untuk mendapatkan 1 token
	needed := 1.0 - tokens
	fillRate := float64(e.rateLimit)
	if fillRate <= 0 {
		fillRate = float64(l.burst) / l.window.Seconds()
	}
	waitSec := needed / fillRate
	if waitSec < 1.0 {
		waitSec = 1.0
	}
	retryAfter := time.Duration(math.Ceil(waitSec)) * time.Second
	return false, retryAfter
}

// RecordFailure mencatat 1 kegagalan (mengonsumsi 1 token dari bucket).
// Mengembalikan sisa token dan status apakah kunci kini terblokir.
func (l *Limiter) RecordFailure(key string) (tokensRemaining float64, blocked bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	e, ok := l.getOrCreate(key)
	if !ok {
		return 0, true, 15 * time.Minute
	}

	e.failures++
	e.lastSeen = now

	// Konsumsi 1 token jika ada pada waktu 'now'
	if !e.limiter.AllowN(now, 1) {
		// Token sudah habis, terblokir
		tokens := e.limiter.TokensAt(now)
		needed := 1.0 - tokens
		fillRate := float64(e.rateLimit)
		if fillRate <= 0 {
			fillRate = float64(l.burst) / l.window.Seconds()
		}
		waitSec := needed / fillRate
		if waitSec < 1.0 {
			waitSec = 1.0
		}
		return tokens, true, time.Duration(math.Ceil(waitSec)) * time.Second
	}

	tokens := e.limiter.TokensAt(now)
	if tokens < 1.0 {
		// Habis setelah konsumsi ini
		needed := 1.0 - tokens
		fillRate := float64(e.rateLimit)
		waitSec := needed / fillRate
		if waitSec < 1.0 {
			waitSec = 1.0
		}
		return tokens, false, time.Duration(math.Ceil(waitSec)) * time.Second
	}

	return tokens, false, 0
}

// Reset mereset token bucket untuk sebuah kunci (misal saat login sukses).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if e, exists := l.entries[key]; exists {
		l.lruList.Remove(e.element)
		delete(l.entries, key)
	}
}

// ResetAll mengosongkan semua entri pada limiter ini.
func (l *Limiter) ResetAll() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = make(map[string]*entry)
	l.lruList.Init()
}

// ActiveRestrictions mengembalikan daftar entri yang sedang kehabisan token (terblokir).
func (l *Limiter) ActiveRestrictions() []RestrictionEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	var result []RestrictionEntry
	now := l.clock()

	for k, e := range l.entries {
		tokens := e.limiter.TokensAt(now)
		if tokens < 1.0 {
			needed := 1.0 - tokens
			fillRate := float64(e.rateLimit)
			waitSec := needed / fillRate
			if waitSec < 1.0 {
				waitSec = 1.0
			}
			rem := time.Duration(math.Ceil(waitSec)) * time.Second
			result = append(result, RestrictionEntry{
				Key:       k,
				MaskedKey: MaskLimiterKey(k),
				Layer:     e.layer,
				Remaining: rem,
				ExpiresAt: now.Add(rem),
			})
		}
	}
	return result
}

// MaskLimiterKey menyamarkan IP atau identifier agar privasi terjaga di panel admin.
func MaskLimiterKey(key string) string {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) == 2 {
		return maskPart(parts[0]) + ":" + maskPart(parts[1])
	}
	return maskPart(key)
}

func maskPart(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return ""
	}
	// Jika tampak seperti IP v4 (misal 192.168.1.10)
	if strings.Count(s, ".") == 3 {
		dotParts := strings.Split(s, ".")
		return fmt.Sprintf("%s.%s.***.***", dotParts[0], dotParts[1])
	}
	// Jika email
	if at := strings.Index(s, "@"); at > 0 {
		user := s[:at]
		domain := s[at:]
		if len(user) <= 2 {
			return user[:1] + "***" + domain
		}
		return user[:2] + "***" + domain
	}
	// Username atau identifier biasa
	if len(s) <= 3 {
		return s[:1] + "***"
	}
	if len(s) <= 6 {
		return s[:2] + "***"
	}
	return s[:3] + "***" + s[len(s)-2:]
}
