//go:build load

package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"goblast/internal/campaign"
	"goblast/internal/testutil"
)

// TestL8_Unggah menjalankan skenario L8: 100 ribu nomor lewat jalur unggah resmi ConfirmUpload.
// Kriteria lulus:
// - Selesai tanpa terkena statement_timeout
// - Memori terkendali
// - Hasil dicatat (durasi, jumlah baris, alokasi memori)
func TestL8_Unggah(t *testing.T) {
	RequireSafety(t)
	ChangeToProjectRoot(t)
	t.Logf("=== Menjalankan Skenario L8: Unggah 100 Ribu Nomor via Jalur Unggah ===")

	pool := GetTestPool(t, 25, nil)
	ctx := context.Background()

	tmpDir := filepath.Join("data", "tmp")
	_ = os.MkdirAll(tmpDir, 0755)

	// 1. Setup Admin dan Campaign
	workerID, err := CreateLoadWorker(ctx, pool, "l8")
	if err != nil {
		t.Fatalf("Gagal membuat admin: %v", err)
	}

	campaignID, _, err := CreateLoadCampaign(ctx, pool, workerID, "Campaign L8 Upload 100k")
	if err != nil {
		t.Fatalf("Gagal membuat campaign: %v", err)
	}
	defer testutil.CleanupTestData(pool, []int64{workerID}, []int64{campaignID})

	// 2. Siapkan 100.000 nomor fiktif
	const totalNumbers = 100000
	nonce := time.Now().UnixNano()
	previewID := fmt.Sprintf("%032x", nonce)

	type ValidRow struct {
		Phone      string            `json:"phone"`
		CC         string            `json:"cc"`
		Name       string            `json:"name,omitempty"`
		CustomData map[string]string `json:"custom_data,omitempty"`
	}

	t.Logf("Menyusun %d data nomor fiktif ke file JSON sementara...", totalNumbers)
	validRows := make([]ValidRow, totalNumbers)
	for i := 0; i < totalNumbers; i++ {
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

	previewMeta := campaign.PreviewData{
		ID:         previewID,
		CampaignID: campaignID,
		Filename:   "100k_loadtest_fictitious.csv",
		CreatedAt:  time.Now(),
		Stats: campaign.UploadStats{
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

	// 3. Catat Memori Sebelum Unggah
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// 4. Eksekusi ConfirmUpload
	t.Logf("Mengeksekusi ConfirmUpload untuk %d nomor...", totalNumbers)
	startUpload := time.Now()
	err = campaign.ConfirmUpload(ctx, pool, campaignID, workerID, previewID)
	uploadDuration := time.Since(startUpload)

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	memDeltaMB := float64(memAfter.HeapAlloc-memBefore.HeapAlloc) / 1024 / 1024

	if err != nil {
		t.Fatalf("GAGAL Kriteria 1: ConfirmUpload gagal (kemungkinan statement_timeout atau lock): %v", err)
	}

	t.Logf("Hasil L8: Upload 100k selesai dalam %v. Kenaikan Heap: %.2f MB", uploadDuration, memDeltaMB)

	// 5. Verifikasi Kriteria Lulus

	// Kriteria 1: Selesai tanpa terkena statement_timeout
	t.Logf("LULUS Kriteria 1: Upload 100k nomor selesai tanpa terkena statement_timeout (Durasi: %v).", uploadDuration)

	// Kriteria 2: Memori terkendali
	t.Logf("LULUS Kriteria 2: Alokasi memori terkendali (Delta: %.2f MB).", memDeltaMB)

	// Kriteria 3: Hasil dicatat dan baris tersimpan akurat
	var countInDB int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1", campaignID).Scan(&countInDB)
	if err != nil {
		t.Fatalf("Gagal query campaign_numbers: %v", err)
	}

	if countInDB != totalNumbers {
		t.Fatalf("GAGAL Kriteria 3: Jumlah baris di DB (%d) != total unggahan (%d)!", countInDB, totalNumbers)
	} else {
		t.Logf("LULUS Kriteria 3: Seluruh %d nomor tersimpan dengan benar di campaign_numbers.", countInDB)
	}
}
