package wa

import (
	"context"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
)

// WAClient adalah antarmuka kecil metode whatsmeow.Client konkret yang digunakan
// oleh Manager dan RealSender. *whatsmeow.Client memenuhi antarmuka ini secara langsung.
type WAClient interface {
	IsConnected() bool
	IsLoggedIn() bool
	Connect() error
	Disconnect()
	Logout(ctx context.Context) error
	IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error)
	PairPhone(ctx context.Context, phone string, showPushNotification bool, clientType whatsmeow.PairClientType, clientDisplayName string) (string, error)
	AddEventHandler(handler whatsmeow.EventHandler) uint32
	GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error)
	SendMessage(ctx context.Context, to types.JID, message *waProto.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
}

// Memastikan *whatsmeow.Client memenuhi antarmuka WAClient saat kompilasi.
var _ WAClient = (*whatsmeow.Client)(nil)
