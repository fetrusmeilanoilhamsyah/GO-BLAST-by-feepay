package watest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"goblast/internal/wa"
)

// SentRecord menyimpan catatan kirim di FakeSender.
type SentRecord struct {
	AccountID int64
	Phone     string
	Msg       wa.OutMessage
	SentAt    time.Time
}

// FakeSender implementasi palsu untuk tes. Thread-safe.
type FakeSender struct {
	mu        sync.Mutex
	Sent      []SentRecord
	ErrFunc   func(accountID int64) error // opsional, untuk simulasi error
	SendDelay time.Duration               // opsional, jeda buatan saat kirim
}

var _ wa.Sender = (*FakeSender)(nil)

// Send mencatat pesan ke slice Sent. Mengembalikan error dari ErrFunc jika ada.
func (f *FakeSender) Send(ctx context.Context, accountID int64, phone string, msg wa.OutMessage) (wa.Result, error) {
	if f.SendDelay > 0 {
		select {
		case <-time.After(f.SendDelay):
		case <-ctx.Done():
			return wa.Result{}, ctx.Err()
		}
	}

	if f.ErrFunc != nil {
		if err := f.ErrFunc(accountID); err != nil {
			return wa.Result{}, err
		}
	}

	now := time.Now()
	f.mu.Lock()
	f.Sent = append(f.Sent, SentRecord{
		AccountID: accountID,
		Phone:     phone,
		Msg:       msg,
		SentAt:    now,
	})
	f.mu.Unlock()

	return wa.Result{
		MessageID: fmt.Sprintf("fake-%d-%d", accountID, now.UnixNano()),
		SentAt:    now,
	}, nil
}

// SentCount mengembalikan jumlah pesan yang berhasil dikirim.
func (f *FakeSender) SentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Sent)
}

// Reset mengosongkan catatan kirim.
func (f *FakeSender) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = nil
}
