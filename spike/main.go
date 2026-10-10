package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"go.mau.fi/whatsmeow"
	waLog "go.mau.fi/whatsmeow/util/log"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

func main() {
	godotenv.Load()
	
	if len(os.Args) < 2 {
		fmt.Println("Gunakan sub-perintah: pair-code, send-text, send-image, send-card, send-buttons, status, logout")
		return
	}

	cmd := os.Args[1]

	dbLog := waLog.Stdout("Database", "WARN", true)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL tidak ditemukan di .env. Format: postgres://<user>:<pass>@localhost:5432/goblast_spike")
	}

	container, err := sqlstore.New(context.Background(), "pgx", dbURL, dbLog)
	if err != nil {
		log.Fatalf("Gagal menyambung database: %v", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		log.Fatalf("Gagal mengambil perangkat: %v", err)
	}

	clientLog := waLog.Stdout("Client", "INFO", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)

	eventHandlerID := client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			fmt.Printf("Menerima pesan dari %v: %v\n", v.Info.Sender, v.Message.GetConversation())
		case *events.Receipt:
			fmt.Printf("Status receipt untuk %v ke %v: %v\n", v.MessageIDs, v.Chat, v.Type)
		case *events.LoggedOut:
			fmt.Println("Sesi dikeluarkan dari WhatsApp.")
			os.Exit(1)
		case *events.Disconnected:
			fmt.Println("Koneksi terputus.")
		case *events.Connected:
			fmt.Println("Berhasil terhubung ke WhatsApp.")
		}
	})

	defer client.RemoveEventHandler(eventHandlerID)

	switch cmd {
	case "pair-qr":
		if client.Store.ID != nil {
			log.Println("Sudah login. Gunakan perintah status atau logout.")
			return
		}
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			log.Fatalf("Gagal connect: %v", err)
		}
		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("Scan QR code ini (atau gunakan API eksternal untuk konversi string ini ke QR):")
				fmt.Println(evt.Code) 
			} else {
				fmt.Println("Event QR:", evt.Event)
			}
		}
		waitForInterrupt()

	case "pair-code":
		if client.Store.ID != nil {
			log.Println("Sudah login.")
			return
		}
		if len(os.Args) < 3 {
			log.Fatal("Format: pair-code <nomor_telepon>")
		}
		number := os.Args[2]
		err = client.Connect()
		if err != nil {
			log.Fatalf("Gagal connect: %v", err)
		}
		// Tunggu sebentar agar websocket benar-benar siap (syarat dari whatsmeow)
		time.Sleep(2 * time.Second)
		code, err := client.PairPhone(context.Background(), number, true, whatsmeow.PairClientChrome, "Chrome (Windows)")
		if err != nil {
			log.Fatalf("Gagal pair: %v", err)
		}
		fmt.Println("Kode Pairing Anda:", code)
		waitForInterrupt()

	case "send-text":
		requireLogin(client)
		if len(os.Args) < 4 {
			log.Fatal("Format: send-text <nomor_tujuan> <pesan>")
		}
		target := parseJID(os.Args[2])
		msg := strings.Join(os.Args[3:], " ")
		resp, err := client.SendMessage(context.Background(), target, &waProto.Message{
			Conversation: proto.String(msg),
		})
		if err != nil {
			log.Fatalf("Gagal kirim: %v", err)
		}
		fmt.Printf("Pesan terkirim. ID: %s, Waktu: %v\n", resp.ID, resp.Timestamp)
		waitForInterrupt()

	case "send-image":
		requireLogin(client)
		if len(os.Args) < 3 {
			log.Fatal("Format: send-image <nomor_tujuan>")
		}
		target := parseJID(os.Args[2])
		
		imgBytes, err := os.ReadFile("full.jpg")
		if err != nil {
			log.Fatalf("Gagal membaca full.jpg: pastikan file gambar ada di folder spike. %v", err)
		}

		fmt.Println("Mengunggah gambar ke server WhatsApp...")
		respUp, err := client.Upload(context.Background(), imgBytes, whatsmeow.MediaImage)
		if err != nil {
			log.Fatalf("Gagal upload gambar: %v", err)
		}

		msg := &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				Caption:       proto.String("Ini adalah gambar penuh (full image). Cocok untuk poster promo vertikal dengan link di teks: https://feepay.web.id"),
				Mimetype:      proto.String("image/jpeg"),
				URL:           &respUp.URL,
				DirectPath:    &respUp.DirectPath,
				MediaKey:      respUp.MediaKey,
				FileEncSHA256: respUp.FileEncSHA256,
				FileSHA256:    respUp.FileSHA256,
				FileLength:    &respUp.FileLength,
			},
		}

		resp, err := client.SendMessage(context.Background(), target, msg)
		if err != nil {
			log.Fatalf("Gagal kirim gambar: %v", err)
		}
		fmt.Printf("Gambar terkirim. ID: %s\n", resp.ID)
		waitForInterrupt()

	case "send-buttons":
		requireLogin(client)
		if len(os.Args) < 3 {
			log.Fatal("Format: send-buttons <nomor_tujuan>")
		}
		target := parseJID(os.Args[2])
		
		msg := &waProto.Message{
			InteractiveMessage: &waProto.InteractiveMessage{
				Header: &waProto.InteractiveMessage_Header{
					Title: proto.String("GO BLAST by FEEPAY"),
					HasMediaAttachment: proto.Bool(false),
				},
				Body: &waProto.InteractiveMessage_Body{
					Text: proto.String("Ini adalah tes pesan tombol langsung tanpa wrapper ViewOnce. Apakah tombolnya muncul?"),
				},
				Footer: &waProto.InteractiveMessage_Footer{
					Text: proto.String("Hubungi CS untuk bantuan"),
				},
				InteractiveMessage: &waProto.InteractiveMessage_NativeFlowMessage_{
					NativeFlowMessage: &waProto.InteractiveMessage_NativeFlowMessage{
						Buttons: []*waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
							{
								Name: proto.String("cta_url"),
								ButtonParamsJSON: proto.String(`{"display_text":"Buka feepay.web.id","url":"https://feepay.web.id","merchant_url":"https://feepay.web.id"}`),
							},
							{
								Name: proto.String("quick_reply"),
								ButtonParamsJSON: proto.String(`{"display_text":"Hubungi CS","id":"cs_btn"}`),
							},
						},
					},
				},
			},
		}
		
		resp, err := client.SendMessage(context.Background(), target, msg)
		if err != nil {
			log.Fatalf("Gagal kirim tombol: %v", err)
		}
		fmt.Printf("Tombol terkirim. ID: %s\n", resp.ID)
		waitForInterrupt()

	case "check":
		requireLogin(client)
		if len(os.Args) < 3 {
			log.Fatal("Format: check <nomor_tujuan>")
		}
		target := "+" + os.Args[2]
		res, err := client.IsOnWhatsApp(context.Background(), []string{target})
		if err != nil {
			log.Fatalf("Gagal cek: %v", err)
		}
		for _, info := range res {
			fmt.Printf("Nomor %v -> Terdaftar: %v, JID: %v\n", info.Query, info.IsIn, info.JID)
		}


	case "send-card":
		requireLogin(client)
		if len(os.Args) < 3 {
			log.Fatal("Format: send-card <nomor_tujuan>")
		}
		target := parseJID(os.Args[2])
		
		var thumbBytes []byte
		if b, err := os.ReadFile("thumb.jpg"); err == nil {
			thumbBytes = b
		} else {
			fmt.Println("Peringatan: thumb.jpg tidak ditemukan, gambar preview mungkin tidak tampil maksimal.")
		}

		linkURL := "https://feepay.web.id"
		textMsg := fmt.Sprintf(`Halo Kak! Promo spesial akhir tahun sedang berlangsung. Dapatkan diskon menarik hingga 50%% untuk setiap transaksi yang Anda lakukan minggu ini. Jangan sampai kehabisan kuota promonya ya!

Untuk melihat katalog lengkap dan melakukan pemesanan, silakan klik tautan di bawah ini:
%s

Jika butuh bantuan lebih lanjut, segera hubungi CS kami dengan klik link WA di bawah ini:
wa.me/6281234567890`, linkURL)

		msg := &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(textMsg),
				Title: proto.String("GO BLAST by FEEPAY"),
				Description: proto.String("Klik link untuk mengunjungi website kami."),
				MatchedText: proto.String(linkURL),
				PreviewType: waProto.ExtendedTextMessage_NONE.Enum(),
				JPEGThumbnail: thumbBytes,
			},
		}
		resp, err := client.SendMessage(context.Background(), target, msg)
		if err != nil {
			log.Fatalf("Gagal kirim kartu: %v", err)
		}
		fmt.Printf("Kartu terkirim. ID: %s\n", resp.ID)
		waitForInterrupt()

	case "status":
		requireLogin(client)
		fmt.Printf("Status: Terhubung sebagai %v\n", client.Store.ID)
		waitForInterrupt()

	case "logout":
		requireLogin(client)
		err := client.Logout(context.Background())
		if err != nil {
			log.Fatalf("Gagal logout: %v", err)
		}
		fmt.Println("Berhasil logout.")
		
	default:
		fmt.Println("Perintah tidak dikenal.")
	}
}

func requireLogin(c *whatsmeow.Client) {
	if c.Store.ID == nil {
		log.Fatal("Belum login. Gunakan pair-code atau pair-qr.")
	}
	err := c.Connect()
	if err != nil {
		log.Fatalf("Gagal connect: %v", err)
	}
}

func parseJID(arg string) types.JID {
	if !strings.Contains(arg, "@") {
		arg += "@s.whatsapp.net"
	}
	jid, err := types.ParseJID(arg)
	if err != nil {
		log.Fatalf("Gagal parse JID: %v", err)
	}
	return jid
}

func waitForInterrupt() {
	fmt.Println("Tekan Ctrl+C untuk keluar...")
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
}
