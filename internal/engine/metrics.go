package engine

import (
	"sync"
	"sync/atomic"
	"time"
)

// LatencyBucket merepresentasikan pembagian latensi sederhana.
type LatencyBucket struct {
	Lt10ms  atomic.Uint64
	Lt50ms  atomic.Uint64
	Lt100ms atomic.Uint64
	Lt500ms atomic.Uint64
	Lt1s    atomic.Uint64
	Gte1s   atomic.Uint64
}

func (b *LatencyBucket) Record(d time.Duration) {
	switch {
	case d < 10*time.Millisecond:
		b.Lt10ms.Add(1)
	case d < 50*time.Millisecond:
		b.Lt50ms.Add(1)
	case d < 100*time.Millisecond:
		b.Lt100ms.Add(1)
	case d < 500*time.Millisecond:
		b.Lt500ms.Add(1)
	case d < 1*time.Second:
		b.Lt1s.Add(1)
	default:
		b.Gte1s.Add(1)
	}
}

func (b *LatencyBucket) Snapshot() map[string]uint64 {
	return map[string]uint64{
		"<10ms":  b.Lt10ms.Load(),
		"<50ms":  b.Lt50ms.Load(),
		"<100ms": b.Lt100ms.Load(),
		"<500ms": b.Lt500ms.Load(),
		"<1s":    b.Lt1s.Load(),
		">=1s":   b.Gte1s.Load(),
	}
}

// SendLatencyBucket merepresentasikan histogram latensi pengiriman jaringan (<1s, 1-3s, 3-5s, >5s).
type SendLatencyBucket struct {
	Lt1s  atomic.Uint64
	Lt3s  atomic.Uint64
	Lt5s  atomic.Uint64
	Gte5s atomic.Uint64
}

func (b *SendLatencyBucket) Record(d time.Duration) {
	switch {
	case d < 1*time.Second:
		b.Lt1s.Add(1)
	case d < 3*time.Second:
		b.Lt3s.Add(1)
	case d < 5*time.Second:
		b.Lt5s.Add(1)
	default:
		b.Gte5s.Add(1)
	}
}

func (b *SendLatencyBucket) Snapshot() map[string]uint64 {
	return map[string]uint64{
		"<1s":  b.Lt1s.Load(),
		"1-3s": b.Lt3s.Load(),
		"3-5s": b.Lt5s.Load(),
		">5s":  b.Gte5s.Load(),
	}
}

// Metrics mengumpulkan seluruh metrik pengiriman atomik tanpa dependensi luar.
type Metrics struct {
	ClaimedCount    atomic.Uint64
	SentCount       atomic.Uint64
	GagalCount      atomic.Uint64
	PanicCount      atomic.Uint64
	LostClaimsCount atomic.Uint64
	BackoffCount    atomic.Uint64

	// Penghitung metrik referral (H6)
	ReferralPaidCount            atomic.Uint64
	ReferralSkippedSameDestCount atomic.Uint64
	ReferralSkippedInactiveCount atomic.Uint64
	ReferralSkippedFraudCount    atomic.Uint64

	// Penghitung per kelas hasil
	ClassTerkirim           atomic.Uint64
	ClassPastiBelumTerkirim atomic.Uint64
	ClassTidakPasti         atomic.Uint64
	ClassNomorTidakValid    atomic.Uint64
	ClassMasalahAkun        atomic.Uint64

	ClaimLatency  LatencyBucket
	RecordLatency LatencyBucket
	SendLatency   SendLatencyBucket
}

func (m *Metrics) IncClass(c ResultClass) {
	switch c {
	case ClassTerkirim:
		m.ClassTerkirim.Add(1)
	case ClassPastiBelumTerkirim:
		m.ClassPastiBelumTerkirim.Add(1)
	case ClassTidakPasti:
		m.ClassTidakPasti.Add(1)
	case ClassNomorTidakValid:
		m.ClassNomorTidakValid.Add(1)
	case ClassMasalahAkun:
		m.ClassMasalahAkun.Add(1)
	}
}

// EngineStats adalah representasi snapshot data metrik JSON untuk admin.
type EngineStats struct {
	ActiveWorkers             int               `json:"active_workers"`
	ClaimedCount              uint64            `json:"claimed_count"`
	SentCount                 uint64            `json:"sent_count"`
	GagalCount                uint64            `json:"gagal_count"`
	PanicCount                uint64            `json:"panic_count"`
	LostClaimsCount           uint64            `json:"lost_claims_count"`
	BackoffCount              uint64            `json:"backoff_count"`
	ReferralPaidCount         uint64            `json:"referral_paid_count"`
	ReferralSkippedFraudCount uint64            `json:"referral_skipped_fraud_count"`
	ClassCounts               map[string]uint64 `json:"class_counts"`
	ClaimLatency              map[string]uint64 `json:"claim_latency"`
	RecordLatency             map[string]uint64 `json:"record_latency"`
	SendLatency               map[string]uint64 `json:"send_latency"`
}

// Stats mengembalikan salinan data metrik engine saat ini.
func (e *Engine) Stats() EngineStats {
	e.mu.Lock()
	active := len(e.workers)
	e.mu.Unlock()

	return EngineStats{
		ActiveWorkers:             active,
		ClaimedCount:              e.metrics.ClaimedCount.Load(),
		SentCount:                 e.metrics.SentCount.Load(),
		GagalCount:                e.metrics.GagalCount.Load(),
		PanicCount:                e.metrics.PanicCount.Load(),
		LostClaimsCount:           e.metrics.LostClaimsCount.Load(),
		BackoffCount:              e.metrics.BackoffCount.Load(),
		ReferralPaidCount:         e.metrics.ReferralPaidCount.Load(),
		ReferralSkippedFraudCount: e.metrics.ReferralSkippedFraudCount.Load(),
		ClassCounts: map[string]uint64{
			"terkirim":             e.metrics.ClassTerkirim.Load(),
			"pasti_belum_terkirim": e.metrics.ClassPastiBelumTerkirim.Load(),
			"tidak_pasti":          e.metrics.ClassTidakPasti.Load(),
			"nomor_tidak_valid":    e.metrics.ClassNomorTidakValid.Load(),
			"masalah_akun":         e.metrics.ClassMasalahAkun.Load(),
		},
		ClaimLatency:  e.metrics.ClaimLatency.Snapshot(),
		RecordLatency: e.metrics.RecordLatency.Snapshot(),
		SendLatency:   e.metrics.SendLatency.Snapshot(),
	}
}

// Ensure thread safety for metrics map initialization
var _ sync.Locker = (*sync.Mutex)(nil)
