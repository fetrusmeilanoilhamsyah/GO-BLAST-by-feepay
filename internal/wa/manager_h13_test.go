package wa_test

import (
	"context"
	"errors"
	"testing"

	"goblast/internal/config"
	"goblast/internal/testutil"
	"goblast/internal/wa"
	"goblast/internal/wa/watest"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

// TestH13_ManagerIsOnWhatsApp memverifikasi perbaikan pemeriksaan status nomor di WhatsApp:
// 1. Nomor format database (tanpa '+', misal "628100000001") otomatis ditambahkan prefix '+' saat query ke pustaka,
//    dan menghasilkan status CheckResultTerdaftar.
// 2. Balasan server whatsmeow mode LID (JID server 'lid') dicocokkan via r.Query sehingga tetap
//    menghasilkan CheckResultTerdaftar meskipun r.JID.User bukan nomor telepon.
// 3. Nomor yang server nyatakan IsIn: false dengan entri yang cocok menghasilkan CheckResultTidakTerdaftar.
// 4. Balasan kosong atau tidak ada entri yang cocok menghasilkan CheckResultTidakPasti.
// 5. Galat jaringan atau timeout whatsmeow menghasilkan CheckResultTidakPasti.
// 6. Akun/sesi tidak aktif atau tidak terhubung menghasilkan CheckResultTidakPasti.
func TestH13_ManagerIsOnWhatsApp(t *testing.T) {
	pool, database, encKey := setupH4SuiteDB(t)
	ctx := context.Background()

	cfg := &config.Config{
		DatabaseURL:       testutil.TestDBURL(t),
		MaxActiveSessions: 50,
		EncryptionKey:     encKey,
	}
	settingsReader := wa.NewDBSettingsReader(database)
	mgr := wa.NewManager(database, settingsReader, cfg)

	var fakeCli *watest.FakeClient
	mgr.SetClientFactory(func(_ *store.Device) wa.WAClient {
		fakeCli = watest.NewFakeClient()
		return fakeCli
	})

	var accID int64
	err := pool.QueryRow(ctx, `INSERT INTO wa_accounts (worker_id, label, status) VALUES (8801, 'WA Checker Test', 'menunggu_scan') RETURNING id`).Scan(&accID)
	if err != nil {
		t.Fatalf("failed to insert wa_accounts: %v", err)
	}

	_, _, err = mgr.Connect(ctx, accID, 8801)
	if err != nil {
		t.Fatalf("failed to connect session: %v", err)
	}

	t.Run("Nomor terdaftar format database (tanpa '+') berhasil dideteksi Terdaftar", func(t *testing.T) {
		phoneFromDB := "628100000001"
		res := mgr.IsOnWhatsApp(ctx, accID, phoneFromDB)
		if res != wa.CheckResultTerdaftar {
			t.Fatalf("expected CheckResultTerdaftar, got %v", res)
		}
	})

	t.Run("Nomor terdaftar dengan balasan LID JID berhasil dicocokkan lewat r.Query", func(t *testing.T) {
		phoneFromDB := "628100000002"
		fakeCli.IsOnWhatsAppFunc = func(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
			return []types.IsOnWhatsAppResponse{
				{
					Query: "+628100000002",
					JID:   types.NewJID("9988776655", types.HiddenUserServer),
					IsIn:  true,
				},
			}, nil
		}
		defer func() { fakeCli.IsOnWhatsAppFunc = nil }()

		res := mgr.IsOnWhatsApp(ctx, accID, phoneFromDB)
		if res != wa.CheckResultTerdaftar {
			t.Fatalf("expected CheckResultTerdaftar for LID response, got %v", res)
		}
	})

	t.Run("Nomor yang server nyatakan IsIn false menghasilkan CheckResultTidakTerdaftar", func(t *testing.T) {
		phoneFromDB := "628100000003"
		fakeCli.UnregisteredPhones["628100000003"] = true
		defer delete(fakeCli.UnregisteredPhones, "628100000003")

		res := mgr.IsOnWhatsApp(ctx, accID, phoneFromDB)
		if res != wa.CheckResultTidakTerdaftar {
			t.Fatalf("expected CheckResultTidakTerdaftar, got %v", res)
		}
	})

	t.Run("Balasan kosong dari server menghasilkan CheckResultTidakPasti", func(t *testing.T) {
		phoneFromDB := "628100000004"
		fakeCli.IsOnWhatsAppEmpty = true
		defer func() { fakeCli.IsOnWhatsAppEmpty = false }()

		res := mgr.IsOnWhatsApp(ctx, accID, phoneFromDB)
		if res != wa.CheckResultTidakPasti {
			t.Fatalf("expected CheckResultTidakPasti for empty result, got %v", res)
		}
	})

	t.Run("Galat jaringan menghasilkan CheckResultTidakPasti", func(t *testing.T) {
		phoneFromDB := "628100000005"
		fakeCli.IsOnWhatsAppError = errors.New("connection reset by peer")
		defer func() { fakeCli.IsOnWhatsAppError = nil }()

		res := mgr.IsOnWhatsApp(ctx, accID, phoneFromDB)
		if res != wa.CheckResultTidakPasti {
			t.Fatalf("expected CheckResultTidakPasti for network error, got %v", res)
		}
	})

	t.Run("Sesi tidak aktif menghasilkan CheckResultTidakPasti", func(t *testing.T) {
		res := mgr.IsOnWhatsApp(ctx, 999999, "628100000006")
		if res != wa.CheckResultTidakPasti {
			t.Fatalf("expected CheckResultTidakPasti for nonexistent account, got %v", res)
		}
	})
}
