package watest

import (
	"context"
	"errors"
	"strings"
	"sync"

	"goblast/internal/wa"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
)

// FakeClient adalah implementasi tiruan WAClient untuk pengujian unit dan event simulasi.
type FakeClient struct {
	mu            sync.Mutex
	connected     bool
	loggedIn      bool
	eventHandlers []whatsmeow.EventHandler
	qrChan        chan whatsmeow.QRChannelItem
	PairPhoneFunc func(phone string) (string, error)
	SendFunc      func(to types.JID, msg *waProto.Message) (whatsmeow.SendResponse, error)

	LogoutCalled     bool
	DisconnectCalled bool
	ConnectCalled    bool

	// Field simulasi pemeriksaan status nomor WhatsApp (Tahap H13)
	IsOnWhatsAppFunc   func(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error)
	IsOnWhatsAppEmpty  bool
	IsOnWhatsAppError  error
	UnregisteredPhones map[string]bool
}

var _ wa.WAClient = (*FakeClient)(nil)

func NewFakeClient() *FakeClient {
	return &FakeClient{
		connected:          true,
		loggedIn:           true,
		qrChan:             make(chan whatsmeow.QRChannelItem, 10),
		UnregisteredPhones: make(map[string]bool),
	}
}

func (f *FakeClient) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *FakeClient) SetConnected(c bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = c
}

func (f *FakeClient) IsLoggedIn() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loggedIn
}

func (f *FakeClient) SetLoggedIn(l bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loggedIn = l
}

func (f *FakeClient) Connect() error {
	f.mu.Lock()
	f.ConnectCalled = true
	f.connected = true
	f.mu.Unlock()
	return nil
}

func (f *FakeClient) Disconnect() {
	f.mu.Lock()
	f.DisconnectCalled = true
	f.connected = false
	f.mu.Unlock()
}

func (f *FakeClient) Logout(_ context.Context) error {
	f.mu.Lock()
	f.LogoutCalled = true
	f.loggedIn = false
	f.connected = false
	f.mu.Unlock()
	return nil
}

func (f *FakeClient) IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	f.mu.Lock()
	fn := f.IsOnWhatsAppFunc
	empty := f.IsOnWhatsAppEmpty
	err := f.IsOnWhatsAppError
	unreg := f.UnregisteredPhones
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if empty {
		return []types.IsOnWhatsAppResponse{}, nil
	}
	if fn != nil {
		return fn(ctx, phones)
	}

	results := make([]types.IsOnWhatsAppResponse, 0, len(phones))
	for _, p := range phones {
		// Pustaka whatsmeow mensyaratkan format internasional diawali tanda '+'
		// (go.mau.fi/whatsmeow/user.go baris 174-175: "The phone numbers should be in international format, including the '+' prefix.")
		if !strings.HasPrefix(p, "+") || len(p) <= 1 {
			// Nomor tanpa format yang benar tidak dikenali oleh WhatsApp server (hasil kosong / dilewati)
			continue
		}

		clean := strings.TrimPrefix(p, "+")
		isIn := true
		if unreg != nil && (unreg[p] || unreg[clean]) {
			isIn = false
		}

		// Balasan asli whatsmeow (user.go:217-219):
		// info.Query: input query tanpa suffix @c.us (misal "+628123456789")
		// info.JID: normalisasi JID user (nomor tanpa tanda '+', server s.whatsapp.net)
		results = append(results, types.IsOnWhatsAppResponse{
			Query: p,
			JID:   types.NewJID(clean, types.DefaultUserServer),
			IsIn:  isIn,
		})
	}
	return results, nil
}

func (f *FakeClient) PairPhone(_ context.Context, phone string, _ bool, _ whatsmeow.PairClientType, _ string) (string, error) {
	f.mu.Lock()
	fn := f.PairPhoneFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(phone)
	}
	return "FAKE-PAIR", nil
}

func (f *FakeClient) AddEventHandler(handler whatsmeow.EventHandler) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.eventHandlers = append(f.eventHandlers, handler)
	return uint32(len(f.eventHandlers))
}

func (f *FakeClient) DispatchEvent(evt interface{}) {
	f.mu.Lock()
	handlers := make([]whatsmeow.EventHandler, len(f.eventHandlers))
	copy(handlers, f.eventHandlers)
	f.mu.Unlock()

	for _, h := range handlers {
		h(evt)
	}
}

func (f *FakeClient) GetQRChannel(_ context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return f.qrChan, nil
}

func (f *FakeClient) PushQR(code string) {
	select {
	case f.qrChan <- whatsmeow.QRChannelItem{Event: "code", Code: code}:
	default:
	}
}

func (f *FakeClient) SendMessage(_ context.Context, to types.JID, message *waProto.Message, _ ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	f.mu.Lock()
	fn := f.SendFunc
	connected := f.connected
	f.mu.Unlock()

	if !connected {
		return whatsmeow.SendResponse{}, errors.New("disconnected")
	}

	if fn != nil {
		return fn(to, message)
	}

	return whatsmeow.SendResponse{
		ID: "FAKE-MSG-ID",
	}, nil
}
