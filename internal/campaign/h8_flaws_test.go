package campaign

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"goblast/internal/testutil"
)

// TestH8_Flaw_ConfirmUploadLeavesRejectedCSV membuktikan bahwa sebelum perbaikan H8,
// ConfirmUpload hanya menghapus _meta.json dan _valid.json, dan meninggalkan _rejected.csv
// di data/tmp yang memuat nomor telepon customer tak terlindungi.
func TestH8_Flaw_ConfirmUploadLeavesRejectedCSV(t *testing.T) {
	root := testutil.RootDir(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	tmpDir := filepath.Join("data", "tmp")
	_ = os.MkdirAll(tmpDir, 0755)

	ctx := context.Background()
	var userID int64
	randEmail := fmt.Sprintf("h8_admin_%d@test.com", time.Now().UnixNano())
	randRef := fmt.Sprintf("REF_H8_%d", time.Now().UnixNano())
	err := pool.QueryRow(ctx, "INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'hash', 'admin', $2, 'H8 Admin') RETURNING id", randEmail, randRef).Scan(&userID)
	if err != nil {
		t.Fatal("Failed to create admin:", err)
	}

	var cID int64
	err = pool.QueryRow(ctx, "INSERT INTO campaigns (name, status, button_mode, body, elements, created_by) VALUES ('H8 Flaw Campaign', 'draf', 'kartu_link', 'Halo', '{}', $1) RETURNING id", userID).Scan(&cID)
	if err != nil {
		t.Fatal("Failed to create campaign:", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE campaign_id = $1", cID)
		_, _ = pool.Exec(ctx, "DELETE FROM number_batches WHERE campaign_id = $1", cID)
		_, _ = pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
	})

	// CSV dengan 1 nomor valid dan 1 nomor ditolak (invalid)
	csvData := []byte("nomor\n081299990001\ninvalid_number\n")
	preview, err := ProcessPreview(ctx, pool, cID, csvData, "test_flaw.csv", "62")
	if err != nil {
		t.Fatalf("ProcessPreview failed: %v", err)
	}

	rejectedPath := filepath.Join(tmpDir, preview.ID+"_rejected.csv")
	if _, err := os.Stat(rejectedPath); os.IsNotExist(err) {
		t.Fatalf("Ekspektasi file rejected.csv dibuat, tetapi tidak ditemukan: %s", rejectedPath)
	}

	// Jalankan ConfirmUpload
	err = ConfirmUpload(ctx, pool, cID, userID, preview.ID)
	if err != nil {
		t.Fatalf("ConfirmUpload gagal: %v", err)
	}

	// PEMBUKTIAN MASALAH (ATURAN 1):
	// Sebelum H8 diperbaiki, ConfirmUpload TIDAK menghapus _rejected.csv.
	// Kita periksa bahwa file _rejected.csv harus sudah dihapus setelah konfirmasi!
	if _, err := os.Stat(rejectedPath); err == nil {
		// File masih ada! Hapus manual di akhir agar tidak mencemari disk
		_ = os.Remove(rejectedPath)
		t.Fatalf("BUKTI BUG P5/P7: ConfirmUpload meninggalkan file rejected.csv di %s tanpa dihapus!", rejectedPath)
	}
}

// TestH8_CancelUploadRemovesAllFiles memverifikasi bahwa CancelUpload menghapus semua berkas preview (meta, valid, rejected).
func TestH8_CancelUploadRemovesAllFiles(t *testing.T) {
	root := testutil.RootDir(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	tmpDir := filepath.Join("data", "tmp")
	_ = os.MkdirAll(tmpDir, 0755)

	ctx := context.Background()
	var userID int64
	randEmail := fmt.Sprintf("h8_cancel_%d@test.com", time.Now().UnixNano())
	randRef := fmt.Sprintf("REF_H8_C_%d", time.Now().UnixNano())
	err := pool.QueryRow(ctx, "INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'hash', 'admin', $2, 'H8 Admin') RETURNING id", randEmail, randRef).Scan(&userID)
	if err != nil {
		t.Fatal("Failed to create admin:", err)
	}

	var cID int64
	err = pool.QueryRow(ctx, "INSERT INTO campaigns (name, status, button_mode, body, elements, created_by) VALUES ('H8 Cancel Campaign', 'draf', 'kartu_link', 'Halo', '{}', $1) RETURNING id", userID).Scan(&cID)
	if err != nil {
		t.Fatal("Failed to create campaign:", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM campaign_numbers WHERE campaign_id = $1", cID)
		_, _ = pool.Exec(ctx, "DELETE FROM number_batches WHERE campaign_id = $1", cID)
		_, _ = pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
	})

	csvData := []byte("nomor\n081299990002\ninvalid_num\n")
	preview, err := ProcessPreview(ctx, pool, cID, csvData, "test_cancel.csv", "62")
	if err != nil {
		t.Fatalf("ProcessPreview failed: %v", err)
	}

	metaFile := filepath.Join(tmpDir, preview.ID+"_meta.json")
	validFile := filepath.Join(tmpDir, preview.ID+"_valid.json")
	rejFile := filepath.Join(tmpDir, preview.ID+"_rejected.csv")

	if _, err := os.Stat(metaFile); os.IsNotExist(err) {
		t.Fatalf("metaFile tidak ditemukan sebelum cancel")
	}

	// Panggil CancelUpload
	err = CancelUpload(preview.ID)
	if err != nil {
		t.Fatalf("CancelUpload error: %v", err)
	}

	// Pastikan ketiganya terhapus
	if _, err := os.Stat(metaFile); err == nil {
		t.Errorf("metaFile masih ada setelah CancelUpload: %s", metaFile)
	}
	if _, err := os.Stat(validFile); err == nil {
		t.Errorf("validFile masih ada setelah CancelUpload: %s", validFile)
	}
	if _, err := os.Stat(rejFile); err == nil {
		t.Errorf("rejFile masih ada setelah CancelUpload: %s", rejFile)
	}
}

// TestH8_CleanTmpFiles1Hour memverifikasi bahwa CleanTmpFiles dengan batas 1 jam
// membersihkan berkas yang lebih tua dari 1 jam dan mempertahankan berkas baru.
func TestH8_CleanTmpFiles1Hour(t *testing.T) {
	root := testutil.RootDir(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	tmpDir := filepath.Join("data", "tmp")
	_ = os.MkdirAll(tmpDir, 0755)

	oldFile := filepath.Join(tmpDir, "test_old_h8.json")
	newFile := filepath.Join(tmpDir, "test_new_h8.json")

	_ = os.WriteFile(oldFile, []byte("old data"), 0600)
	_ = os.WriteFile(newFile, []byte("new data"), 0600)

	t.Cleanup(func() {
		_ = os.Remove(oldFile)
		_ = os.Remove(newFile)
	})

	// Ubah waktu modifikasi oldFile menjadi 2 jam yang lalu
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(oldFile, twoHoursAgo, twoHoursAgo)

	// Jalankan CleanTmpFiles dengan batas 1 jam
	CleanTmpFiles(1 * time.Hour)

	// oldFile harus sudah terhapus
	if _, err := os.Stat(oldFile); err == nil {
		t.Errorf("oldFile masih ada setelah CleanTmpFiles(1h): %s", oldFile)
	}

	// newFile harus tetap ada
	if _, err := os.Stat(newFile); os.IsNotExist(err) {
		t.Errorf("newFile ikut terhapus padahal belum 1 jam: %s", newFile)
	}
}

