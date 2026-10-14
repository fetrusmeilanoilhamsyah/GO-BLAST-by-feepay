package campaign

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"goblast/internal/testutil"
)

// TestUpload100kNumbers membuktikan bahwa transaksi unggah batch besar (100.000 nomor)
// berhasil dieksekusi dengan aman menggunakan SET LOCAL statement_timeout = 0 tanpa terkena
// pemutusan statement_timeout koneksi umum (30 detik).
func TestUpload100kNumbers(t *testing.T) {
	if testing.Short() {
		t.Skip("melewati tes unggah 100k pada mode -short")
	}

	root := testutil.RootDir(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	ctx := context.Background()

	tmpDir := filepath.Join("data", "tmp")
	_ = os.MkdirAll(tmpDir, 0755)

	// 1. Buat user admin
	var adminID int64
	nonce := time.Now().UnixNano()
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, referral_code, display_name)
		 VALUES ($1, 'hash', 'admin', $2, 'Admin 100k Test') RETURNING id`,
		fmt.Sprintf("admin_100k_%d@test.com", nonce),
		fmt.Sprintf("REF100K%d", nonce),
	).Scan(&adminID)
	if err != nil {
		t.Fatalf("Gagal membuat user admin: %v", err)
	}

	// 2. Buat campaign draf
	var campaignID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ('Campaign 100k Bulk', 'draf', 'kartu_link', 'Halo {name}', '{}', $1)
		 RETURNING id`,
		adminID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("Gagal membuat campaign: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM campaign_numbers WHERE campaign_id = $1", campaignID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM number_batches WHERE campaign_id = $1", campaignID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM campaigns WHERE id = $1", campaignID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", adminID)
	})

	// 3. Siapkan 100.000 data nomor fiktif valid
	const totalNumbers = 100000
	previewID := fmt.Sprintf("%032x", nonce)

	type ValidRow struct {
		Phone      string            `json:"phone"`
		CC         string            `json:"cc"`
		Name       string            `json:"name,omitempty"`
		CustomData map[string]string `json:"custom_data,omitempty"`
	}

	validRows := make([]ValidRow, totalNumbers)
	for i := 0; i < totalNumbers; i++ {
		// Menggunakan prefix fiktif 628100... sesuai aturan privasi nomor telepon fiktif
		validRows[i] = ValidRow{
			Phone: fmt.Sprintf("628100%07d", i+1),
			CC:    "62",
			Name:  fmt.Sprintf("Penerima %d", i+1),
		}
	}

	validJSON, err := json.Marshal(validRows)
	if err != nil {
		t.Fatalf("Gagal marshal data valid: %v", err)
	}

	previewMeta := PreviewData{
		ID:         previewID,
		CampaignID: campaignID,
		Filename:   "100k_fictitious_numbers.csv",
		CreatedAt:  time.Now(),
		Stats: UploadStats{
			Total:         totalNumbers,
			Accepted:      totalNumbers,
			DupInFile:     0,
			DupInCampaign: 0,
			Invalid:       0,
			Ambiguous:     0,
			CountryCodes:  map[string]int{"62": totalNumbers},
		},
	}
	metaJSON, err := json.Marshal(previewMeta)
	if err != nil {
		t.Fatalf("Gagal marshal metadata preview: %v", err)
	}

	metaPath := filepath.Join(tmpDir, previewID+"_meta.json")
	validPath := filepath.Join(tmpDir, previewID+"_valid.json")

	if err := os.WriteFile(metaPath, metaJSON, 0600); err != nil {
		t.Fatalf("Gagal menulis file meta: %v", err)
	}
	defer os.Remove(metaPath)

	if err := os.WriteFile(validPath, validJSON, 0600); err != nil {
		t.Fatalf("Gagal menulis file valid: %v", err)
	}
	defer os.Remove(validPath)

	// 4. Eksekusi ConfirmUpload
	start := time.Now()
	err = ConfirmUpload(ctx, pool, campaignID, adminID, previewID)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("ConfirmUpload 100k gagal: %v", err)
	}

	t.Logf("ConfirmUpload 100.000 nomor berhasil dalam %v", duration)

	// 5. Verifikasi jumlah nomor tersimpan di database
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1", campaignID).Scan(&count)
	if err != nil {
		t.Fatalf("Gagal verifikasi count campaign_numbers: %v", err)
	}

	if count != totalNumbers {
		t.Fatalf("Jumlah nomor tersimpan tidak sesuai: diharapkan %d, didapat %d", totalNumbers, count)
	}
}
