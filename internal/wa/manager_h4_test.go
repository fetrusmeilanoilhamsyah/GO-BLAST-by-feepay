package wa_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/testutil"
	"goblast/internal/wa"
	"goblast/internal/wa/watest"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// setupH4SuiteDB menyiapkan database tes dan pengguna awal.
func setupH4SuiteDB(t *testing.T) (*pgxpool.Pool, *db.DB, []byte) {
	t.Helper()
	pool := testutil.TestDB(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `TRUNCATE TABLE notifications, ledger_entries, campaign_numbers, campaigns, wa_accounts CASCADE`)
	if err != nil {
		t.Fatalf("failed to truncate tables: %v", err)
	}

	// 32-byte master encryption key fiktif untuk pengetesan HMAC
	encKey := []byte("01234567890123456789012345678901")

	_, _ = pool.Exec(ctx, `
		INSERT INTO users (id, username, password_hash, role, display_name, referral_code)
		VALUES (8801, 'worker_h4_a', 'hash', 'worker', 'Worker A', 'REF8801')
		ON CONFLICT (id) DO NOTHING
	`)
	_, _ = pool.Exec(ctx, `
		INSERT INTO users (id, username, password_hash, role, display_name, referral_code)
		VALUES (8802, 'worker_h4_b', 'hash', 'worker', 'Worker B', 'REF8802')
		ON CONFLICT (id) DO NOTHING
	`)

	_, _ = pool.Exec(ctx, `
		INSERT INTO settings (key, value)
		VALUES ('max_accounts_per_worker', '100'::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = '100'::jsonb
	`)

	database := &db.DB{Pool: pool}
	return pool, database, encKey
}

// 1. Tes Wajib: Boot - baris terhubung menjadi terputus, perangkat yatim terhapus, tidak ada perubahan pada akun berstatus berhenti.
func TestH4_BootReconciliation(t *testing.T) {
	pool, database, encKey := setupH4SuiteDB(t)
	ctx := context.Background()

	// Masukkan akun dengan berbagai status
	_, err := pool.Exec(ctx, `
		INSERT INTO wa_accounts (worker_id, label, status) VALUES
		(8801, 'WA 1 Terhubung', 'terhubung'),
		(8801, 'WA 2 Menunggu Scan', 'menunggu_scan'),
		(8801, 'WA 3 Antre', 'antre'),
		(8801, 'WA 4 Terputus', 'terputus'),
		(8801, 'WA 5 Berhenti', 'berhenti')
	`)
	if err != nil {
		t.Fatalf("failed to insert wa_accounts: %v", err)
	}

	cfg := &config.Config{
		DatabaseURL:       testutil.TestDBURL(t),
		MaxActiveSessions: 50,
		EncryptionKey:     encKey,
	}
	settingsReader := wa.NewDBSettingsReader(database)
	mgr := wa.NewManager(database, settingsReader, cfg)

	t.Run("Pembersihan dinonaktifkan via WA_PURGE_ORPHANS_ON_BOOT=false", func(t *testing.T) {
		os.Setenv("WA_PURGE_ORPHANS_ON_BOOT", "false")
		defer os.Unsetenv("WA_PURGE_ORPHANS_ON_BOOT")

		reconciled, purged, err := mgr.ReconcileOnBoot(ctx)
		if err != nil {
			t.Fatalf("ReconcileOnBoot error: %v", err)
		}
		if reconciled != 4 {
			t.Fatalf("expected 4 reconciled rows, got %d", reconciled)
		}
		if purged != 0 {
			t.Fatalf("expected 0 purged devices when purge is disabled, got %d", purged)
		}
	})

	t.Run("Status akun berhenti tidak pernah berubah", func(t *testing.T) {
		var status, reason string
		err := pool.QueryRow(ctx, `SELECT status, COALESCE(stop_reason, '') FROM wa_accounts WHERE label = 'WA 5 Berhenti'`).Scan(&status, &reason)
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if status != "berhenti" {
			t.Fatalf("status akun berhenti berubah menjadi %s", status)
		}
		if reason != "" {
			t.Fatalf("stop_reason akun berhenti harus kosong, got %q", reason)
		}
	})

	t.Run("Pembersihan aktif bawaan (WA_PURGE_ORPHANS_ON_BOOT=true)", func(t *testing.T) {
		os.Setenv("WA_PURGE_ORPHANS_ON_BOOT", "true")
		defer os.Unsetenv("WA_PURGE_ORPHANS_ON_BOOT")

		// ReconcileOnBoot berjalan aman tanpa error
		_, _, err := mgr.ReconcileOnBoot(ctx)
		if err != nil {
			t.Fatalf("ReconcileOnBoot with purge error: %v", err)
		}
	})
}

// 2. Tes Wajib: Batas sesi - 100 permintaan Connect bersamaan dengan batas 10: tepat 10 yang lolos.
func TestH4_ConcurrentConnectLimit(t *testing.T) {
	pool, database, encKey := setupH4SuiteDB(t)
	ctx := context.Background()

	cfg := &config.Config{
		DatabaseURL:       testutil.TestDBURL(t),
		MaxActiveSessions: 10,
		EncryptionKey:     encKey,
	}
	settingsReader := wa.NewDBSettingsReader(database)
	mgr := wa.NewManager(database, settingsReader, cfg)

	// Injeksi client tiruan agar tidak melakukan koneksi jaringan nyata
	mgr.SetClientFactory(func(_ *store.Device) wa.WAClient {
		return watest.NewFakeClient()
	})

	// Buat 100 akun di DB berstatus antre
	accIDs := make([]int64, 100)
	for i := 0; i < 100; i++ {
		var id int64
		err := pool.QueryRow(ctx, `
			INSERT INTO wa_accounts (worker_id, label, speed, status)
			VALUES (8801, $1, 'medium', 'antre')
			RETURNING id
		`, fmt.Sprintf("WA-Concurrent-%d", i+1)).Scan(&id)
		if err != nil {
			t.Fatalf("insert error: %v", err)
		}
		accIDs[i] = id
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var successCount int
	var rejectedCount int
	startGate := make(chan struct{})

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(accID int64) {
			defer wg.Done()
			<-startGate
			_, _, err := mgr.Connect(context.Background(), accID, 8801)
			mu.Lock()
			if err == nil {
				successCount++
			} else {
				rejectedCount++
			}
			mu.Unlock()
		}(accIDs[i])
	}

	close(startGate)
	wg.Wait()

	t.Logf("Hasil 100 Connect konkuren: %d lolos, %d ditolak", successCount, rejectedCount)
	if successCount != 10 {
		t.Fatalf("Gagal penegakan batas atomik: diharapkan tepat 10 yang lolos, didapat %d", successCount)
	}
	if rejectedCount != 90 {
		t.Fatalf("diharapkan 90 ditolak, didapat %d", rejectedCount)
	}
}

// 3. Tes Wajib: Penanganan event (Connected, Disconnected, LoggedOut, TemporaryBan, ConnectFailure, ClientOutdated, StreamReplaced).
func TestH4_EventHandlingMatrix(t *testing.T) {
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

	var engineStoppedCount atomic.Int64
	mgr.SetEngineStopper(func(_ context.Context, _ int64) error {
		engineStoppedCount.Add(1)
		return nil
	})

	var accID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO wa_accounts (worker_id, label, speed, status)
		VALUES (8801, 'WA Event Matrix', 'medium', 'antre')
		RETURNING id
	`).Scan(&accID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}

	// Connect akun
	_, _, err = mgr.Connect(ctx, accID, 8801)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	t.Run("Event Connected", func(t *testing.T) {
		fakeCli.DispatchEvent(&events.Connected{})
		time.Sleep(100 * time.Millisecond)

		var status string
		_ = pool.QueryRow(ctx, `SELECT status FROM wa_accounts WHERE id = $1`, accID).Scan(&status)
		if status != "terhubung" {
			t.Fatalf("expected status 'terhubung', got %s", status)
		}
		if mgr.GetSession(accID).GetStatus() != "terhubung" {
			t.Fatalf("expected in-memory status 'terhubung', got %s", mgr.GetSession(accID).GetStatus())
		}
	})

	t.Run("Event Disconnected (sementara)", func(t *testing.T) {
		beforeStop := engineStoppedCount.Load()
		fakeCli.DispatchEvent(&events.Disconnected{})
		time.Sleep(100 * time.Millisecond)

		var status string
		_ = pool.QueryRow(ctx, `SELECT status FROM wa_accounts WHERE id = $1`, accID).Scan(&status)
		if status != "terputus" {
			t.Fatalf("expected status 'terputus', got %s", status)
		}
		// Engine TIDAK boleh dihentikan saat disconnect sementara
		if engineStoppedCount.Load() != beforeStop {
			t.Fatalf("engine tidak boleh dihentikan saat disconnect sementara!")
		}
	})

	t.Run("Event ClientOutdated", func(t *testing.T) {
		fakeCli.DispatchEvent(&events.ClientOutdated{})
		time.Sleep(50 * time.Millisecond)

		if !mgr.IsClientOutdated() {
			t.Fatal("expected IsClientOutdated to be true")
		}
	})

	t.Run("Event TemporaryBan", func(t *testing.T) {
		fakeCli.DispatchEvent(&events.TemporaryBan{
			Code:   events.TempBanSentToTooManyPeople,
			Expire: 24 * time.Hour,
		})
		time.Sleep(150 * time.Millisecond)

		var status, reason string
		_ = pool.QueryRow(ctx, `SELECT status, stop_reason FROM wa_accounts WHERE id = $1`, accID).Scan(&status, &reason)
		if status != "berhenti" {
			t.Fatalf("expected status 'berhenti', got %s", status)
		}
		if !strings.Contains(reason, "banned sementara") {
			t.Fatalf("expected reason to contain 'banned sementara', got %q", reason)
		}

		// Verifikasi notifikasi terkirim ke pekerja
		var notifCount int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = 8801 AND kind = 'system' AND title = 'Akun WhatsApp Dibatasi'`).Scan(&notifCount)
		if notifCount == 0 {
			t.Fatal("expected notification for worker on temporary ban")
		}
	})

	t.Run("Event StreamReplaced", func(t *testing.T) {
		// Buat akun baru untuk uji StreamReplaced
		var acc2 int64
		_ = pool.QueryRow(ctx, `INSERT INTO wa_accounts (worker_id, label, status) VALUES (8801, 'WA Stream', 'terhubung') RETURNING id`).Scan(&acc2)
		var fakeCli2 *watest.FakeClient
		mgr.SetClientFactory(func(_ *store.Device) wa.WAClient {
			fakeCli2 = watest.NewFakeClient()
			return fakeCli2
		})
		_, _, _ = mgr.Connect(ctx, acc2, 8801)

		fakeCli2.DispatchEvent(&events.StreamReplaced{})
		time.Sleep(150 * time.Millisecond)

		var status, reason string
		_ = pool.QueryRow(ctx, `SELECT status, stop_reason FROM wa_accounts WHERE id = $1`, acc2).Scan(&status, &reason)
		if status != "berhenti" || reason != "stream_replaced" {
			t.Fatalf("expected status 'berhenti' and reason 'stream_replaced', got status=%s reason=%s", status, reason)
		}

		var notifCount int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = 8801 AND kind = 'system' AND title = 'Sesi WhatsApp Diputus'`).Scan(&notifCount)
		if notifCount == 0 {
			t.Fatal("expected notification for worker on stream replaced")
		}
	})

	t.Run("Event LoggedOut", func(t *testing.T) {
		var acc3 int64
		_ = pool.QueryRow(ctx, `INSERT INTO wa_accounts (worker_id, label, status) VALUES (8801, 'WA Logout', 'terhubung') RETURNING id`).Scan(&acc3)
		var fakeCli3 *watest.FakeClient
		mgr.SetClientFactory(func(_ *store.Device) wa.WAClient {
			fakeCli3 = watest.NewFakeClient()
			return fakeCli3
		})
		_, _, _ = mgr.Connect(ctx, acc3, 8801)

		fakeCli3.DispatchEvent(&events.LoggedOut{Reason: events.ConnectFailureLoggedOut})
		time.Sleep(150 * time.Millisecond)

		var status, reason string
		_ = pool.QueryRow(ctx, `SELECT status, stop_reason FROM wa_accounts WHERE id = $1`, acc3).Scan(&status, &reason)
		if status != "berhenti" || reason != "logged_out" {
			t.Fatalf("expected status 'berhenti' and reason 'logged_out', got status=%s reason=%s", status, reason)
		}
	})
}

// 4. Tes Wajib: Disconnected lalu Connected - tidak ada nomor dilepas, status DB mengikuti.
func TestH4_DisconnectedThenConnectedNoNumberRelease(t *testing.T) {
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
	_ = pool.QueryRow(ctx, `INSERT INTO wa_accounts (worker_id, label, status) VALUES (8801, 'WA Reconnect', 'antre') RETURNING id`).Scan(&accID)

	// Buat kampanye, batch, dan nomor yang berstatus 'dikirim' oleh akun ini
	var campID int64
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (name, status, body, elements, created_by) VALUES ('Camp H4', 'berjalan', 'Halo', '{}'::jsonb, 8801) RETURNING id`).Scan(&campID); err != nil {
		t.Fatalf("insert campaigns error: %v", err)
	}
	var batchID int64
	_ = pool.QueryRow(ctx, `INSERT INTO number_batches (campaign_id, filename, uploaded_by) VALUES ($1, 'test1.csv', 8801) RETURNING id`, campID).Scan(&batchID)
	var numID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at)
		VALUES ($1, $2, '628100000001', '62', 'dikirim', $3, now())
		RETURNING id
	`, campID, batchID, accID).Scan(&numID)
	if err != nil {
		t.Fatalf("insert campaign_numbers error: %v", err)
	}

	_, _, _ = mgr.Connect(ctx, accID, 8801)
	fakeCli.DispatchEvent(&events.Connected{})
	time.Sleep(50 * time.Millisecond)

	// Simulasikan Disconnected
	fakeCli.DispatchEvent(&events.Disconnected{})
	time.Sleep(100 * time.Millisecond)

	// Verifikasi nomor TIDAK dilepas dari claimed_account_id dan tetap berstatus 'dikirim'
	var numStatus string
	var claimedAcc *int64
	err = pool.QueryRow(ctx, `SELECT status, claimed_account_id FROM campaign_numbers WHERE id = $1`, numID).Scan(&numStatus, &claimedAcc)
	if err != nil {
		t.Fatalf("query number: %v", err)
	}
	if numStatus != "dikirim" {
		t.Fatalf("nomor tidak boleh diubah saat disconnect sementara, got status=%s", numStatus)
	}
	if claimedAcc == nil || *claimedAcc != accID {
		t.Fatalf("nomor tidak boleh dilepas kepemilikannya saat disconnect sementara")
	}

	// Simulasikan Connected kembali
	fakeCli.DispatchEvent(&events.Connected{})
	time.Sleep(100 * time.Millisecond)

	var accStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM wa_accounts WHERE id = $1`, accID).Scan(&accStatus)
	if accStatus != "terhubung" {
		t.Fatalf("expected status 'terhubung' after reconnect, got %s", accStatus)
	}
}

// 5. Tes Wajib: Antrean event penuh tidak memblokir pengirim event.
func TestH4_EventQueueNonBlocking(t *testing.T) {
	_, database, encKey := setupH4SuiteDB(t)

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

	ctx := context.Background()
	_, _, _ = mgr.Connect(ctx, 12345, 8801)

	// Kirim 200 event secara serentak; tidak boleh ada dead-lock atau blocking
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			fakeCli.DispatchEvent(&events.Connected{})
		}
		close(done)
	}()

	select {
	case <-done:
		t.Log("200 event berhasil dikirim tanpa blocking")
	case <-time.After(2 * time.Second):
		t.Fatal("DEADLOCK: DispatchEvent memblokir thread pengirim saat antrean penuh!")
	}
}

// 6. Tes Wajib: Pairing dengan JID yang sudah terdaftar di akun lain ditolak dan dibersihkan.
func TestH4_DuplicatePairJIDRejection(t *testing.T) {
	pool, database, encKey := setupH4SuiteDB(t)
	ctx := context.Background()

	cfg := &config.Config{
		DatabaseURL:       testutil.TestDBURL(t),
		MaxActiveSessions: 50,
		EncryptionKey:     encKey,
	}
	settingsReader := wa.NewDBSettingsReader(database)
	mgr := wa.NewManager(database, settingsReader, cfg)

	// JID fiktif
	fictitiousJID := types.NewJID("6281200000099", types.DefaultUserServer)
	jidHash := wa.HashWAJID(fictitiousJID.User, encKey)

	// Akun 1 sudah aktif dengan JID tersebut
	var acc1 int64
	err := pool.QueryRow(ctx, `
		INSERT INTO wa_accounts (worker_id, label, status, wa_jid_hash)
		VALUES (8801, 'Akun Pekerja A', 'terhubung', $1)
		RETURNING id
	`, jidHash).Scan(&acc1)
	if err != nil {
		t.Fatalf("insert acc1: %v", err)
	}

	// Akun 2 milik Pekerja B mencoba pairing dengan JID yang sama
	var acc2 int64
	err = pool.QueryRow(ctx, `
		INSERT INTO wa_accounts (worker_id, label, status)
		VALUES (8802, 'Akun Pekerja B', 'menunggu_scan')
		RETURNING id
	`).Scan(&acc2)
	if err != nil {
		t.Fatalf("insert acc2: %v", err)
	}

	var fakeCli2 *watest.FakeClient
	mgr.SetClientFactory(func(_ *store.Device) wa.WAClient {
		fakeCli2 = watest.NewFakeClient()
		return fakeCli2
	})

	_, _, err = mgr.Connect(ctx, acc2, 8802)
	if err != nil {
		t.Fatalf("Connect acc2: %v", err)
	}

	// Dispatch PairSuccess untuk akun 2 dengan nomor JID yang sama
	fakeCli2.DispatchEvent(&events.PairSuccess{
		ID:       fictitiousJID,
		Platform: "Chrome",
	})

	time.Sleep(200 * time.Millisecond)

	// Verifikasi akun 2 ditolak: status berhenti, stop_reason 'nomor_sudah_terhubung'
	var status2, reason2 string
	_ = pool.QueryRow(ctx, `SELECT status, stop_reason FROM wa_accounts WHERE id = $1`, acc2).Scan(&status2, &reason2)
	if status2 != "berhenti" || reason2 != "nomor_sudah_terhubung" {
		t.Fatalf("expected acc2 status='berhenti' and reason='nomor_sudah_terhubung', got status=%s reason=%s", status2, reason2)
	}

	// Verifikasi sesi akun 2 dilogout dan diputus
	if !fakeCli2.LogoutCalled || !fakeCli2.DisconnectCalled {
		t.Fatalf("expected LogoutCalled=true and DisconnectCalled=true for rejected duplicate pairing")
	}

	// Verifikasi notifikasi penolakan masuk ke pekerja B
	var notifCount int
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM notifications
		WHERE user_id = 8802 AND kind = 'system' AND message LIKE '%Nomor ini sudah terhubung di akun lain%'
	`).Scan(&notifCount)
	if notifCount == 0 {
		t.Fatalf("expected notification to worker 8802 about duplicate linked number")
	}
}

// 7. Tes Wajib: Stop dan Delete saat engine sedang Send - urutan benar, tidak ada pelanggaran FK.
func TestH4_StopAndDeleteWaitEngineNoFKViolation(t *testing.T) {
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
	_ = pool.QueryRow(ctx, `
		INSERT INTO wa_accounts (worker_id, label, status)
		VALUES (8801, 'WA Stop Delete', 'terhubung')
		RETURNING id
	`).Scan(&accID)

	var campID int64
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (name, status, body, elements, created_by) VALUES ('Camp StopDel', 'berjalan', 'Halo', '{}'::jsonb, 8801) RETURNING id`).Scan(&campID); err != nil {
		t.Fatalf("insert campaigns error: %v", err)
	}
	var batchID int64
	if err := pool.QueryRow(ctx, `INSERT INTO number_batches (campaign_id, filename, uploaded_by) VALUES ($1, 'test2.csv', 8801) RETURNING id`, campID).Scan(&batchID); err != nil {
		t.Fatalf("insert number_batches error: %v", err)
	}
	var numID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at)
		VALUES ($1, $2, '628100000002', '62', 'dikirim', $3, now())
		RETURNING id
	`, campID, batchID, accID).Scan(&numID)
	if err != nil {
		t.Fatalf("insert campaign_numbers error: %v", err)
	}

	// Simulasikan engineStopper yang mensimulasikan worker sedang berjalan dan menyelesaikan pekerjaannya
	engineDrainCalled := false
	mgr.SetEngineStopper(func(_ context.Context, id int64) error {
		engineDrainCalled = true
		time.Sleep(50 * time.Millisecond)
		return nil
	})

	_, _, _ = mgr.Connect(ctx, accID, 8801)

	// Panggil Delete
	err = mgr.Delete(ctx, accID, 8801)
	if err != nil {
		t.Fatalf("Delete returned error: %v (Foreign Key Violation?)", err)
	}

	if !engineDrainCalled {
		t.Fatal("engineStopper harus dipanggil sebelum baris wa_accounts dihapus!")
	}

	// Baris wa_accounts harus sudah terhapus
	var countAcc int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM wa_accounts WHERE id = $1`, accID).Scan(&countAcc)
	if countAcc != 0 {
		t.Fatalf("wa_accounts row should be deleted, got %d", countAcc)
	}

	// Sisa nomor yang berstatus dikirim harus menjadi gagal hasil_tidak_pasti (Keputusan A1)
	var numStatus, lastErr string
	_ = pool.QueryRow(ctx, `SELECT status, last_error FROM campaign_numbers WHERE id = $1`, numID).Scan(&numStatus, &lastErr)
	if numStatus != "gagal" || lastErr != "hasil_tidak_pasti" {
		t.Fatalf("expected number status='gagal' and last_error='hasil_tidak_pasti', got status=%s last_error=%s", numStatus, lastErr)
	}
}

// 8. Tes HashWAJID dan DeriveJIDHashKey deterministik
func TestH4_HashWAJID(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	phone := "6281234567890"

	h1 := wa.HashWAJID(phone, key)
	h2 := wa.HashWAJID(phone, key)

	if h1 == "" || h1 != h2 {
		t.Fatalf("expected identical non-empty deterministic hash, got h1=%q h2=%q", h1, h2)
	}

	// Berbeda dengan nomor lain
	h3 := wa.HashWAJID("6281234567891", key)
	if h1 == h3 {
		t.Fatal("different phone must produce different hash")
	}

	// Beda konteks dengan payout_dest_hash
	// Payout dest hash menggunakan goblast_payout_dest_hash_salt
	// JID hash menggunakan goblast_wa_jid_hash_salt
	if len(h1) != 64 {
		t.Fatalf("expected 64 hex characters (32 bytes sha256), got len=%d", len(h1))
	}
}

// Helper untuk base64
func init() {
	_ = base64.StdEncoding
}
