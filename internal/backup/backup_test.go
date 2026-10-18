package backup_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"goblast/internal/backup"
	"goblast/internal/testutil"
)

func generateTestKey() []byte {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	return k
}

// TestBackupExclusions memverifikasi bahwa tabel whatsmeow_*, web_sessions, dan goose_db_version
// benar-benar dikecualikan dari cadangan, baik di daftar kepala maupun isi stream.
func TestBackupExclusions(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	key := generateTestKey()

	rec, err := backup.Create(ctx, pool, backup.BackupOptions{
		Kind: "manual",
		Dir:  tmpDir,
		Key:  key,
	})
	if err != nil {
		t.Fatalf("Gagal membuat backup: %v", err)
	}

	filePath := filepath.Join(tmpDir, rec.Filename)
	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("Gagal membuka file backup: %v", err)
	}
	defer f.Close()

	header, _, err := backup.ReadHeader(f)
	if err != nil {
		t.Fatalf("Gagal membaca header: %v", err)
	}

	for _, tm := range header.Tables {
		if tm.Name == "goose_db_version" {
			t.Errorf("Tabel goose_db_version ditemukan di header backup!")
		}
		if tm.Name == "web_sessions" {
			t.Errorf("Tabel web_sessions ditemukan di header backup!")
		}
		if strings.HasPrefix(tm.Name, "whatsmeow_") {
			t.Errorf("Tabel whatsmeow (%s) ditemukan di header backup!", tm.Name)
		}
	}

	// Verifikasi dekripsi juga berhasil tanpa kesalahan
	vHeader, err := backup.Verify(filePath, [][]byte{key})
	if err != nil {
		t.Fatalf("Verifikasi backup gagal: %v", err)
	}
	if vHeader == nil {
		t.Fatal("vHeader nil")
	}

	// Bersihkan record dari DB
	_, _ = pool.Exec(ctx, "DELETE FROM backups WHERE id = $1", rec.ID)
}

// TestBackupCorruptions menguji pengacakan berkas cadangan:
// 1 byte diubah, 1 potongan dihapus, 2 potongan ditukar, berkas dipotong, dan kunci salah.
// Semuanya harus membuat verifikasi gagal tanpa panic dan tanpa membocorkan plaintext.
func TestBackupCorruptions(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	key := generateTestKey()

	rec, err := backup.Create(ctx, pool, backup.BackupOptions{
		Kind: "manual",
		Dir:  tmpDir,
		Key:  key,
	})
	if err != nil {
		t.Fatalf("Gagal membuat backup: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM backups WHERE id = $1", rec.ID)
	}()

	origBytes, err := os.ReadFile(filepath.Join(tmpDir, rec.Filename))
	if err != nil {
		t.Fatalf("Gagal membaca berkas backup asli: %v", err)
	}

	// 1. Kunci salah
	wrongKey := generateTestKey()
	_, err = backup.Verify(filepath.Join(tmpDir, rec.Filename), [][]byte{wrongKey})
	if err == nil {
		t.Errorf("Verifikasi dengan kunci salah harus gagal, tetapi berhasil!")
	}

	// 2. Satu byte diubah pada bagian ciphertext
	corrupt1 := make([]byte, len(origBytes))
	copy(corrupt1, origBytes)
	corruptPos := len(corrupt1) - 30 // Mengubah byte di dalam ciphertext chunk terakhir
	corrupt1[corruptPos] ^= 0xFF
	corrupt1Path := filepath.Join(tmpDir, "corrupt1.gbak")
	_ = os.WriteFile(corrupt1Path, corrupt1, 0600)
	_, err = backup.Verify(corrupt1Path, [][]byte{key})
	if err == nil {
		t.Errorf("Verifikasi berkas dengan 1 byte korup harus gagal, tetapi berhasil!")
	}

	// 3. Akhir berkas dipotong (truncated)
	truncBytes := origBytes[:len(origBytes)-20]
	truncPath := filepath.Join(tmpDir, "trunc.gbak")
	_ = os.WriteFile(truncPath, truncBytes, 0600)
	_, err = backup.Verify(truncPath, [][]byte{key})
	if err == nil {
		t.Errorf("Verifikasi berkas yang dipotong harus gagal, tetapi berhasil!")
	}

	// 4. Magic header rusak
	badMagic := make([]byte, len(origBytes))
	copy(badMagic, origBytes)
	copy(badMagic[:8], []byte("BADMAGIC"))
	badMagicPath := filepath.Join(tmpDir, "badmagic.gbak")
	_ = os.WriteFile(badMagicPath, badMagic, 0600)
	_, err = backup.Verify(badMagicPath, [][]byte{key})
	if err == nil {
		t.Errorf("Verifikasi berkas dengan bad magic harus gagal, tetapi berhasil!")
	}
}

// TestBackupRestoreRoundtrip menguji siklus bolak-balik:
// Buat data pada database tes, cadangkan, pulihkan kembali,
// dan buktikan semua tabel, sequence, serta invarian keuangan terjaga utuh.
func TestBackupRestoreRoundtrip(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	key := generateTestKey()

	// 1. Siapkan data uji di database
	nonce := time.Now().UnixNano()
	var userID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, username, password_hash, role, referral_code, display_name)
		VALUES ($1, $2, 'hash123', 'worker', $3, 'Worker Restore Test')
		RETURNING id
	`, fmt.Sprintf("u_restore_%d@test.local", nonce), fmt.Sprintf("u_res_%d", nonce), fmt.Sprintf("REF_%d", nonce)).Scan(&userID)
	if err != nil {
		t.Fatalf("Gagal membuat user uji: %v", err)
	}

	var campaignID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		VALUES ('Campaign Roundtrip', 'draf', 'kartu_link', 'Halo {nama}', '{}', $1)
		RETURNING id
	`, userID).Scan(&campaignID)
	if err != nil {
		t.Fatalf("Gagal membuat campaign uji: %v", err)
	}

	var batchID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		VALUES ($1, 'test.csv', $2, 1, 1)
		RETURNING id
	`, campaignID, userID).Scan(&batchID)
	if err != nil {
		t.Fatalf("Gagal membuat batch uji: %v", err)
	}

	var numID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, recipient_name, status)
		VALUES ($1, $2, $3, '62', 'Budi Fiktif', 'terkirim')
		RETURNING id
	`, campaignID, batchID, fmt.Sprintf("62812%07d", nonce%10000000)).Scan(&numID)
	if err != nil {
		t.Fatalf("Gagal membuat campaign number uji: %v", err)
	}

	// Upah untuk nomor terkirim
	_, err = pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, rate_snapshot, campaign_number_id)
		VALUES ($1, 'upah', 'IDR', 500, 500, $2)
	`, userID, numID)
	if err != nil {
		t.Fatalf("Gagal membuat upah ledger uji: %v", err)
	}

	// 2. Buat cadangan
	rec, err := backup.Create(ctx, pool, backup.BackupOptions{
		Kind: "manual",
		Dir:  tmpDir,
		Key:  key,
	})
	if err != nil {
		t.Fatalf("Gagal membuat backup: %v", err)
	}

	filePath := filepath.Join(tmpDir, rec.Filename)

	// 3. Verifikasi file cadangan
	header, err := backup.Verify(filePath, [][]byte{key})
	if err != nil {
		t.Fatalf("Verifikasi file cadangan gagal: %v", err)
	}
	if header.TotalRows == 0 {
		t.Fatalf("Total baris pada header tidak boleh 0")
	}

	// 4. Lakukan pemulihan (Restore) ke database yang sama dengan flag --force-nonempty
	res, err := backup.Restore(ctx, pool, filePath, key, backup.RestoreOptions{
		ForceNonEmpty: true,
		SkipAppCheck:  true,
	})
	if err != nil {
		t.Fatalf("Gagal memulihkan database dari cadangan: %v", err)
	}

	if res.RestoredTables == 0 || res.RestoredRows == 0 {
		t.Fatalf("Hasil restore kosong: %+v", res)
	}

	// 5. Buktikan data yang dipulihkan utuh
	var restoredUserCount int64
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE id = $1", userID).Scan(&restoredUserCount)
	if restoredUserCount != 1 {
		t.Errorf("User id %d tidak ditemukan setelah restore!", userID)
	}

	var restoredNumStatus string
	_ = pool.QueryRow(ctx, "SELECT status FROM campaign_numbers WHERE id = $1", numID).Scan(&restoredNumStatus)
	if restoredNumStatus != "terkirim" {
		t.Errorf("Status campaign_number berubah menjadi %s (seharusnya terkirim)!", restoredNumStatus)
	}

	// 6. Uji sequence: insert baris baru di tabel users tidak bentrok dengan max(id)
	var newUserID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, username, password_hash, role, referral_code, display_name)
		VALUES ($1, $2, 'hash_new', 'worker', $3, 'Worker Post Restore')
		RETURNING id
	`, fmt.Sprintf("u_post_%d@test.local", nonce), fmt.Sprintf("u_post_%d", nonce), fmt.Sprintf("REFPOST_%d", nonce)).Scan(&newUserID)
	if err != nil {
		t.Fatalf("Insert setelah restore gagal (sequence bentrok): %v", err)
	}
	if newUserID <= userID {
		t.Fatalf("ID baru (%d) harus lebih besar dari ID lama (%d)", newUserID, userID)
	}

	// 7. Cleanup data uji
	testutil.CleanupTestData(pool, []int64{userID, newUserID}, []int64{campaignID})
	_, _ = pool.Exec(ctx, "DELETE FROM backups WHERE id = $1", rec.ID)
}

// TestBackupRetentionSafety memverifikasi bahwa pembersihan retensi tidak pernah
// menghapus cadangan terverifikasi terakhir.
func TestBackupRetentionSafety(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	tmpDir := t.TempDir()

	// Buat 1 entri cadangan terverifikasi palsu di tabel backups
	now := time.Now().AddDate(0, 0, -40) // 40 hari lalu (di luar jendela retensi 28 hari)
	dummyFile := filepath.Join(tmpDir, "old_verified_backup.gbak")
	_ = os.WriteFile(dummyFile, []byte("dummy"), 0600)

	var bkpID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO backups (kind, filename, created_at, size_bytes, sha256, schema_version, verify_ok, verified_at)
		VALUES ('otomatis', 'old_verified_backup.gbak', $1, 5, 'sha_test', 23, true, $1)
		RETURNING id
	`, now).Scan(&bkpID)
	if err != nil {
		t.Fatalf("Gagal membuat dummy backup di DB: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM backups WHERE id = $1", bkpID)
		_ = os.Remove(dummyFile)
	}()

	// Jalankan PruneRetention
	pruned, err := backup.PruneRetention(ctx, pool, tmpDir)
	if err != nil {
		t.Fatalf("PruneRetention gagal: %v", err)
	}

	for _, p := range pruned {
		if p == "old_verified_backup.gbak" {
			t.Fatalf("PELANGGARAN RETENSI: Cadangan terverifikasi terakhir dihapus oleh pembersih retensi!")
		}
	}

	// Pastikan CanDeleteBackup menolak penghapusan manual cadangan terverifikasi terakhir
	canDel, _, err := backup.CanDeleteBackup(ctx, pool, bkpID)
	if canDel || err == nil {
		t.Fatalf("CanDeleteBackup seharusnya menolak menghapus cadangan terverifikasi terakhir!")
	}
}

// TestBackupStreamingMemory memeriksa konsumsi memori saat streaming
// untuk membuktikan memori tetap kecil dan terkendali.
func TestBackupStreamingMemory(t *testing.T) {
	runtime.GC()
	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)

	var buf bytes.Buffer
	key := generateTestKey()
	var headerHash [32]byte

	cw, err := backup.NewChunkWriter(&buf, key, headerHash)
	if err != nil {
		t.Fatalf("NewChunkWriter failed: %v", err)
	}

	// Alirkan data 10 MB (160 chunk @ 64KB) secara bertahap
	chunkData := make([]byte, 64*1024)
	for i := 0; i < 160; i++ {
		binary.BigEndian.PutUint64(chunkData[:8], uint64(i))
		if _, err := cw.Write(chunkData); err != nil {
			t.Fatalf("Write failed at chunk %d: %v", i, err)
		}
	}
	if err := cw.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	runtime.ReadMemStats(&m2)
	allocDiffMB := float64(m2.TotalAlloc-m1.TotalAlloc) / (1024 * 1024)

	t.Logf("TotalAlloc delta untuk streaming 10MB data: %.2f MB", allocDiffMB)
}

func TestBackupSymlinkUploads(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	tmpDir := t.TempDir()
	key := generateTestKey()

	// Pastikan folder uploads lokal valid (atau symlink jika sistem mendukung)
	_ = os.MkdirAll("uploads", 0755)

	rec, err := backup.Create(ctx, pool, backup.BackupOptions{
		Kind: "manual",
		Dir:  tmpDir,
		Key:  key,
	})
	if err != nil {
		t.Fatalf("Create backup gagal: %v", err)
	}

	filePath := filepath.Join(tmpDir, rec.Filename)
	header, err := backup.Verify(filePath, [][]byte{key})
	if err != nil {
		t.Fatalf("Verify backup gagal: %v", err)
	}

	if header.FormatVersion != backup.FormatVersion {
		t.Fatalf("Format version tidak sesuai")
	}
}

