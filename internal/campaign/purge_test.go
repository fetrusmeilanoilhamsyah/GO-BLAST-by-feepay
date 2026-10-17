package campaign_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"goblast/internal/campaign"
	"goblast/internal/testutil"
)

func createTestUser(ctx context.Context, pool *pgxpool.Pool, prefix, role string) (int64, error) {
	uniqueID := time.Now().UnixNano()
	username := fmt.Sprintf("%s_%d", prefix, uniqueID)
	refCode := fmt.Sprintf("R%d", uniqueID%1000000000)
	email := fmt.Sprintf("%s@example.com", username)
	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO users (username, display_name, email, password_hash, role, status, referral_code)
		VALUES ($1, $1, $2, 'dummyhash', $3, 'active', $4)
		RETURNING id
	`, username, email, role, refCode).Scan(&id)
	return id, err
}

func TestPurge_AnonymizeCustomerData(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	// 1. Buat User Admin & Worker
	adminID, err := createTestUser(ctx, pool, "admin_purge_test", "admin")
	if err != nil {
		t.Fatalf("Failed to create admin: %v", err)
	}

	workerID, err := createTestUser(ctx, pool, "worker_purge_test", "worker")
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}

	// 2. Buat Campaign berstatus selesai
	var campID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, finished_at)
		VALUES ('Campaign Purge Anonymize', 'selesai', 'kartu_link', 'Halo pesan', '{}', $1, NOW())
		RETURNING id
	`, adminID).Scan(&campID)
	if err != nil {
		t.Fatalf("Failed to create campaign: %v", err)
	}

	// Buat Batch
	var batchID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO number_batches (campaign_id, filename, uploaded_by)
		VALUES ($1, 'pelanggan_rahasia.csv', $2)
		RETURNING id
	`, campID, adminID).Scan(&batchID)
	if err != nil {
		t.Fatalf("Failed to create batch: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID, workerID}, []int64{campID})
	})

	// 3. Masukkan 7 Nomor dengan berbagai status
	type numSeed struct {
		phone     string
		status    string
		lastError *string
	}
	errPasti := "hasil_tidak_pasti"
	errInternal := "galat_internal"
	errUnknown := "dial tcp 123.45.67.89: connection reset by peer"

	seeds := []numSeed{
		{"628111111111", "tersedia", nil},
		{"628222222222", "tersedia", nil},
		{"628333333333", "terkirim", nil},
		{"628444444444", "terkirim", nil},
		{"628555555555", "gagal", &errPasti},
		{"628666666666", "gagal", &errInternal},
		{"628777777777", "gagal", &errUnknown},
		{"628888888888", "tidak_terdaftar", nil},
	}

	var sentNumIDs []int64
	for _, s := range seeds {
		var nID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO campaign_numbers (
				campaign_id, batch_id, phone_e164, country_code,
				recipient_name, custom_data, status, last_error
			) VALUES (
				$1, $2, $3, 'ID',
				'Nama Rahasia', '{"vip": true, "saldo": 50000}'::jsonb, $4, $5
			) RETURNING id
		`, campID, batchID, s.phone, s.status, s.lastError).Scan(&nID)
		if err != nil {
			t.Fatalf("Failed to insert campaign number: %v", err)
		}
		if s.status == "terkirim" {
			sentNumIDs = append(sentNumIDs, nID)
		}
	}

	// 4. Masukkan Buku Besar Upah untuk nomor terkirim
	for _, nID := range sentNumIDs {
		_, err = pool.Exec(ctx, `
			INSERT INTO ledger_entries (user_id, campaign_number_id, kind, currency, amount, rate_snapshot)
			VALUES ($1, $2, 'upah', 'IDR', 50, 16000)
		`, workerID, nID)
		if err != nil {
			t.Fatalf("Failed to insert ledger entry: %v", err)
		}
	}

	// 5. Eksekusi Purge
	res, err := campaign.Purge(ctx, pool, campID, false)
	if err != nil {
		t.Fatalf("Purge failed: %v", err)
	}

	if res.TersediaDeleted != 2 {
		t.Errorf("Ekspektasi TersediaDeleted=2, dapat %d", res.TersediaDeleted)
	}
	if res.TerkirimAnon != 2 {
		t.Errorf("Ekspektasi TerkirimAnon=2, dapat %d", res.TerkirimAnon)
	}
	if res.GagalAnon != 3 {
		t.Errorf("Ekspektasi GagalAnon=3, dapat %d", res.GagalAnon)
	}
	if res.TidakTerdaftarAnon != 1 {
		t.Errorf("Ekspektasi TidakTerdaftarAnon=1, dapat %d", res.TidakTerdaftarAnon)
	}

	// 6. Verifikasi Integritas Database Setelah Purge
	// a. Nomor 'tersedia' harus 0
	var countTersedia int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'tersedia'", campID).Scan(&countTersedia)
	if countTersedia != 0 {
		t.Errorf("Ekspektasi 0 nomor tersedia tersisa, dapat %d", countTersedia)
	}

	// b. Total nomor tersisa harus 6 (2 terkirim + 3 gagal + 1 tidak_terdaftar)
	var totalRemaining int
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1", campID).Scan(&totalRemaining)
	if totalRemaining != 6 {
		t.Errorf("Ekspektasi 6 nomor tersisa di database, dapat %d", totalRemaining)
	}

	// c. Verifikasi anonimisasi seluruh nomor tersisa
	rows, err := pool.Query(ctx, `
		SELECT id, phone_e164, country_code, recipient_name, custom_data, status, last_error
		FROM campaign_numbers WHERE campaign_id = $1
	`, campID)
	if err != nil {
		t.Fatalf("Failed to query remaining numbers: %v", err)
	}
	defer rows.Close()

	anonPhoneRegex := regexp.MustCompile(`^x\d+$`)

	for rows.Next() {
		var id int64
		var phone, country, status string
		var name, custom, lastErr *string
		if err := rows.Scan(&id, &phone, &country, &name, &custom, &status, &lastErr); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}

		// phone_e164 harus berupa 'x' + id, tidak ada digit nomor customer asli
		if !anonPhoneRegex.MatchString(phone) {
			t.Errorf("Nomor ID %d tidak dianonimkan dengan benar: phone_e164=%s", id, phone)
		}
		expectedPhone := fmt.Sprintf("x%d", id)
		if phone != expectedPhone {
			t.Errorf("Nomor ID %d: ekspektasi %s, dapat %s", id, expectedPhone, phone)
		}

		if country != "XX" {
			t.Errorf("Nomor ID %d: country_code harus 'XX', dapat %s", id, country)
		}
		if name != nil {
			t.Errorf("Nomor ID %d: recipient_name harus NULL, dapat %v", id, *name)
		}
		if custom != nil {
			t.Errorf("Nomor ID %d: custom_data harus NULL, dapat %v", id, *custom)
		}

		if status == "terkirim" || status == "tidak_terdaftar" {
			if lastErr != nil {
				t.Errorf("Nomor ID %d (status %s): last_error harus NULL, dapat %v", id, status, *lastErr)
			}
		} else if status == "gagal" {
			if lastErr == nil {
				t.Errorf("Nomor ID %d (status gagal): last_error tidak boleh NULL", id)
			} else {
				if *lastErr != "hasil_tidak_pasti" && *lastErr != "galat_internal" && *lastErr != "lainnya" {
					t.Errorf("Nomor ID %d (status gagal): last_error tidak disanitasi: %s", id, *lastErr)
				}
			}
		}
	}

	// d. Batch filename harus dikosongkan
	var bFilename string
	_ = pool.QueryRow(ctx, "SELECT filename FROM number_batches WHERE id = $1", batchID).Scan(&bFilename)
	if bFilename != "" {
		t.Errorf("Filename batch tidak dikosongkan: %s", bFilename)
	}

	// e. Buku besar upah harus tetap 2 baris dan saldo worker tetap utuh (FK Restrict tidak rusak)
	var ledgerCount int
	var totalWage int64
	_ = pool.QueryRow(ctx, "SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM ledger_entries WHERE user_id = $1", workerID).Scan(&ledgerCount, &totalWage)
	if ledgerCount != 2 || totalWage != 100 {
		t.Errorf("Buku besar rusak setelah purge: count=%d, totalWage=%d", ledgerCount, totalWage)
	}

	// f. Campaign purged_at dan purge_summary harus terisi
	var purgedAt *time.Time
	var pSummary map[string]int
	err = pool.QueryRow(ctx, "SELECT purged_at, purge_summary FROM campaigns WHERE id = $1", campID).Scan(&purgedAt, &pSummary)
	if err != nil || purgedAt == nil {
		t.Fatalf("Campaign purged_at belum tercatat: %v", err)
	}
	if pSummary["terkirim"] != 2 || pSummary["gagal"] != 3 || pSummary["tidak_terdaftar"] != 1 || pSummary["tersedia"] != 2 {
		t.Errorf("Ringkasan purge_summary tidak cocok: %+v", pSummary)
	}
}

func TestPurge_RunningAndInFlightRejected(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	adminID, err := createTestUser(ctx, pool, "admin_purge_guards", "admin")
	if err != nil {
		t.Fatalf("Failed to create admin: %v", err)
	}

	// Kasus 1: Campaign sedang berjalan
	var runningCampID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Running Campaign', 'berjalan', 'kartu_link', 'Pesan', '{}', $1)
		RETURNING id
	`, adminID).Scan(&runningCampID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID}, []int64{runningCampID})
	})

	_, err = campaign.Purge(ctx, pool, runningCampID, false)
	if err == nil || err != campaign.ErrCampaignRunning {
		t.Errorf("Ekspektasi ErrCampaignRunning untuk campaign berjalan, dapat: %v", err)
	}

	// Kasus 2: Campaign draf/selesai tetapi memiliki nomor status 'dikirim' (in flight)
	workerID, err := createTestUser(ctx, pool, "worker_inflight_test", "worker")
	if err != nil {
		t.Fatalf("Failed to create worker for inflight test: %v", err)
	}

	var waAccID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO wa_accounts (worker_id, label, status)
		VALUES ($1, 'WA Inflight', 'terhubung')
		RETURNING id
	`, workerID).Scan(&waAccID)
	if err != nil {
		t.Fatalf("Failed to create wa_account: %v", err)
	}

	var inFlightCampID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Inflight Campaign', 'selesai', 'kartu_link', 'Pesan', '{}', $1)
		RETURNING id
	`, adminID).Scan(&inFlightCampID)
	if err != nil {
		t.Fatalf("Failed to create inflight campaign: %v", err)
	}

	defer func() {
		testutil.CleanupTestData(pool, []int64{workerID}, []int64{inFlightCampID})
	}()

	var inflightBatchID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO number_batches (campaign_id, filename, uploaded_by)
		VALUES ($1, 'batch_inflight.csv', $2)
		RETURNING id
	`, inFlightCampID, adminID).Scan(&inflightBatchID)
	if err != nil {
		t.Fatalf("Failed to create batch for inflight: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at)
		VALUES ($1, $2, '628999999999', 'ID', 'dikirim', $3, NOW())
	`, inFlightCampID, inflightBatchID, waAccID)
	if err != nil {
		t.Fatalf("Failed to insert inflight number: %v", err)
	}

	_, err = campaign.Purge(ctx, pool, inFlightCampID, false)
	if err == nil || err != campaign.ErrCampaignHasInFlight {
		t.Errorf("Ekspektasi ErrCampaignHasInFlight jika ada nomor 'dikirim', dapat: %v", err)
	}
}

func TestPurge_Idempotent(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	adminID, err := createTestUser(ctx, pool, "admin_purge_idemp", "admin")
	if err != nil {
		t.Fatalf("Failed to create admin: %v", err)
	}

	var campID int64
	_ = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Idempotent Purge Camp', 'selesai', 'kartu_link', 'Pesan', '{}', $1)
		RETURNING id
	`, adminID).Scan(&campID)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID}, []int64{campID})
	})

	res1, err1 := campaign.Purge(ctx, pool, campID, false)
	if err1 != nil {
		t.Fatalf("Purge pertama gagal: %v", err1)
	}

	res2, err2 := campaign.Purge(ctx, pool, campID, false)
	if err2 != nil {
		t.Fatalf("Purge kedua (idempoten) gagal: %v", err2)
	}

	if !res2.AlreadyPurged {
		t.Errorf("Ekspektasi AlreadyPurged=true pada pemanggilan Purge kedua")
	}
	if res1.CampaignID != res2.CampaignID {
		t.Errorf("CampaignID tidak cocok antara pemanggilan pertama dan kedua")
	}
}

func TestPurge_ImageDeletion(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	adminID, err := createTestUser(ctx, pool, "admin_purge_img", "admin")
	if err != nil {
		t.Fatalf("Failed to create admin: %v", err)
	}

	// Buat file dummy di uploads/
	_ = os.MkdirAll("uploads", 0755)
	dummyFile := filepath.Join("uploads", fmt.Sprintf("test_purge_img_%d.jpg", time.Now().UnixNano()))
	if err := os.WriteFile(dummyFile, []byte("fake image data"), 0644); err != nil {
		t.Fatalf("Failed to create dummy image: %v", err)
	}
	defer func() {
		_ = os.Remove(dummyFile)
	}()

	var campID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, image_path, elements, created_by)
		VALUES ('Image Purge Camp', 'selesai', 'kartu_link', 'Pesan', $1, '{}', $2)
		RETURNING id
	`, filepath.ToSlash(dummyFile), adminID).Scan(&campID)
	if err != nil {
		t.Fatalf("Failed to create campaign with image: %v", err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID}, []int64{campID})
	})

	// Purge dengan deleteImage = true
	res, err := campaign.Purge(ctx, pool, campID, true)
	if err != nil {
		t.Fatalf("Purge with image failed: %v", err)
	}

	if !res.ImageDeleted {
		t.Errorf("Ekspektasi ImageDeleted=true, dapat false")
	}

	// Pastikan file fisik telah dihapus dari disk
	if _, err := os.Stat(dummyFile); !os.IsNotExist(err) {
		t.Errorf("File gambar fisik masih ada di disk setelah purge: %s", dummyFile)
	}

	// Pastikan kolom image_path di database menjadi NULL
	var dbImgPath *string
	_ = pool.QueryRow(ctx, "SELECT image_path FROM campaigns WHERE id = $1", campID).Scan(&dbImgPath)
	if dbImgPath != nil {
		t.Errorf("Kolom campaigns.image_path belum NULL: %v", *dbImgPath)
	}
}
