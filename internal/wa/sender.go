package wa

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Button mendefinisikan tombol interaktif pesan WhatsApp.
type Button struct {
	Type    string // "cta_url", "cta_call", "cta_copy", "quick_reply"
	Text    string // label tombol
	Payload string // URL, nomor telepon, kode promo, atau ID balasan
}

// OutMessage adalah pesan yang dikirim ke WhatsApp.
type OutMessage struct {
	Text    string
	Preview *Preview // nil jika tidak ada preview
	Buttons []Button // daftar tombol jika mode tombol_asli
	Mode    string   // "kartu_link", "tombol_asli", "teks_biasa"
}

// Preview untuk mode kartu_link.
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

// MessageBuilder menyusun waProto.Message dari wa.OutMessage.
type MessageBuilder interface {
	Build(msg OutMessage) (*waProto.Message, error)
}

// DefaultMessageBuilder implementasi baku MessageBuilder yang mendukung
// tombol_asli (NativeFlowMessage), kartu_link (ExtendedTextMessage), dan teks_biasa (Conversation).
// TemplateMessage usang DILARANG KERAS di sini sesuai instruksi anti-blokir WhatsApp.
type DefaultMessageBuilder struct{}

// Build membangun waProto.Message berdasarkan OutMessage.
func (b *DefaultMessageBuilder) Build(msg OutMessage) (*waProto.Message, error) {
	if msg.Mode == "tombol_asli" || len(msg.Buttons) > 0 {
		if len(msg.Buttons) > 10 {
			return nil, fmt.Errorf("jumlah tombol melebihi batas maksimal 10 (diberikan: %d)", len(msg.Buttons))
		}

		protoButtons := make([]*waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton, 0, len(msg.Buttons))
		for _, btn := range msg.Buttons {
			var name string
			var paramsMap map[string]string

			switch btn.Type {
			case "cta_url", "url":
				name = "cta_url"
				paramsMap = map[string]string{
					"display_text": btn.Text,
					"url":          btn.Payload,
					"merchant_url": btn.Payload,
				}
			case "cta_call", "phone":
				name = "cta_call"
				paramsMap = map[string]string{
					"display_text": btn.Text,
					"phone_number": btn.Payload,
				}
			case "cta_copy", "copy":
				name = "cta_copy"
				paramsMap = map[string]string{
					"display_text": btn.Text,
					"copy_code":    btn.Payload,
				}
			case "quick_reply":
				name = "quick_reply"
				paramsMap = map[string]string{
					"display_text": btn.Text,
					"id":           btn.Payload,
				}
			default:
				return nil, fmt.Errorf("tipe tombol tidak didukung: %s", btn.Type)
			}

			paramsJSON, err := json.Marshal(paramsMap)
			if err != nil {
				return nil, fmt.Errorf("gagal serialize params tombol: %w", err)
			}

			protoButtons = append(protoButtons, &waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
				Name:             proto.String(name),
				ButtonParamsJSON: proto.String(string(paramsJSON)),
			})
		}

		interactive := &waProto.InteractiveMessage{
			Body: &waProto.InteractiveMessage_Body{
				Text: proto.String(msg.Text),
			},
			InteractiveMessage: &waProto.InteractiveMessage_NativeFlowMessage_{
				NativeFlowMessage: &waProto.InteractiveMessage_NativeFlowMessage{
					Buttons:        protoButtons,
					MessageVersion: proto.Int32(1),
				},
			},
		}

		return &waProto.Message{
			InteractiveMessage: interactive,
		}, nil
	}

	if msg.Preview != nil {
		// Mode kartu_link: kirim dengan extended text message (link preview)
		ep := &waProto.ExtendedTextMessage{
			Text:          proto.String(msg.Text),
			Title:         proto.String(msg.Preview.Title),
			Description:   proto.String(msg.Preview.Description),
			MatchedText:   proto.String(msg.Preview.URL),
			PreviewType:   waProto.ExtendedTextMessage_NONE.Enum(),
			JPEGThumbnail: msg.Preview.Thumbnail,
		}
		return &waProto.Message{ExtendedTextMessage: ep}, nil
	}

	// Pesan teks biasa
	return &waProto.Message{
		Conversation: proto.String(msg.Text),
	}, nil
}

// RealSender implementasi nyata menggunakan Manager.
type RealSender struct {
	mgr     *Manager
	builder MessageBuilder
}

func NewRealSender(mgr *Manager) *RealSender {
	return &RealSender{
		mgr:     mgr,
		builder: &DefaultMessageBuilder{},
	}
}

// SetBuilder memungkinkan penggantian implementasi MessageBuilder jika diperlukan.
func (s *RealSender) SetBuilder(b MessageBuilder) {
	s.builder = b
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

	builder := s.builder
	if builder == nil {
		builder = &DefaultMessageBuilder{}
	}
	waMsg, err := builder.Build(msg)
	if err != nil {
		return Result{}, fmt.Errorf("gagal membangun pesan: %w", err)
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
