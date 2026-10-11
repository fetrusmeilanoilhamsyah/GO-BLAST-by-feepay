package wa

import (
	"context"
	"fmt"
	"sync"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// OutMessage adalah pesan yang dikirim ke WhatsApp.
type OutMessage struct {
	Text    string
	Preview *Preview // nil jika tidak ada preview
}

// Preview untuk mode kartu_link.
// TODO: konfirmasi ThumbnailSpec setelah uji send-card-thumb (lihat spike/HASIL.md bagian 4)
// Saat ini: lebar 300px, kualitas 70.
type Preview struct {
	Title       string
	Description string
	URL         string
	Thumbnail   []byte // JPEG bytes, lebar 300px, kualitas 70
}

// Result hasil kirim.
type Result struct {
	MessageID string
	SentAt    time.Time
}

// Sender antarmuka untuk mengirim pesan WhatsApp.
type Sender interface {
	Send(ctx context.Context, accountID int64, phone string, msg OutMessage) (Result, error)
}

// RealSender implementasi nyata menggunakan Manager.
type RealSender struct {
	mgr *Manager
}

func NewRealSender(mgr *Manager) *RealSender {
	return &RealSender{mgr: mgr}
}

// Send mengirim pesan ke nomor phone (E.164 tanpa +) menggunakan sesi accountID.
func (s *RealSender) Send(ctx context.Context, accountID int64, phone string, msg OutMessage) (Result, error) {
	sess := s.mgr.GetSession(accountID)
	if sess == nil {
		return Result{}, ErrSessionNotActive
	}
	if !sess.Client.IsConnected() {
		return Result{}, ErrSessionNotConnected
	}

	jid := types.NewJID(phone, types.DefaultUserServer)

	var waMsg *waProto.Message
	if msg.Preview != nil {
		// Mode kartu_link: kirim dengan extended text message (link preview)
		// Kirim hanya JPEGThumbnail (diuji di spike), tanpa pemanggilan Client.Upload per pesan
		ep := &waProto.ExtendedTextMessage{
			Text:          proto.String(msg.Text),
			Title:         proto.String(msg.Preview.Title),
			Description:   proto.String(msg.Preview.Description),
			MatchedText:   proto.String(msg.Preview.URL),
			PreviewType:   waProto.ExtendedTextMessage_NONE.Enum(),
			JPEGThumbnail: msg.Preview.Thumbnail,
		}
		waMsg = &waProto.Message{ExtendedTextMessage: ep}
	} else {
		// Pesan teks biasa
		waMsg = &waProto.Message{
			Conversation: proto.String(msg.Text),
		}
	}

	resp, err := sess.Client.SendMessage(ctx, jid, waMsg)
	if err != nil {
		return Result{}, fmt.Errorf("gagal kirim ke %s: %w", maskPhone(phone), err)
	}

	return Result{
		MessageID: resp.ID,
		SentAt:    resp.Timestamp,
	}, nil
}

// maskPhone menyembunyikan sebagian nomor telepon untuk log (privasi customer).
func maskPhone(phone string) string {
	if len(phone) <= 4 {
		return "****"
	}
	return phone[:2] + "****" + phone[len(phone)-2:]
}

// --- FakeSender (untuk tes) ---

// SentRecord menyimpan catatan kirim di FakeSender.
type SentRecord struct {
	AccountID int64
	Phone     string
	Msg       OutMessage
	SentAt    time.Time
}

// FakeSender implementasi palsu untuk tes. Thread-safe.
type FakeSender struct {
	mu      sync.Mutex
	Sent    []SentRecord
	ErrFunc func(accountID int64) error // opsional, untuk simulasi error
}

// Send mencatat pesan ke slice Sent. Mengembalikan error dari ErrFunc jika ada.
func (f *FakeSender) Send(ctx context.Context, accountID int64, phone string, msg OutMessage) (Result, error) {
	if f.ErrFunc != nil {
		if err := f.ErrFunc(accountID); err != nil {
			return Result{}, err
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

	return Result{
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
