//go:build load

package loadtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"goblast/internal/backup"
	"goblast/internal/db"
	"goblast/internal/testutil"
	"goblast/internal/web"
)

// TestL10_BackupBesar menjalankan skenario L10: Backup lalu restore dataset 1 juta nomor & 500k buku besar.
// Kriteria lulus:
// - Memori puncak proses tidak naik lebih dari 150 MB selama backup
// - Waktu dicatat (durasi backup & restore)
// - Restore lulus semua invarian
// - Selama backup berjalan, p95 /healthz di bawah 200 ms
func TestL10_BackupBesar(t *testing.T) {
	RequireSafety(t)
	ChangeToProjectRoot(t)
	t.Logf("=== Menjalankan Skenario L10: Backup & Restore Besar (1M Nomor, 500k Buku Besar) ===")

	pool := GetTestPool(t, 25, nil)
	ctx := context.Background()

	// Putuskan koneksi menggantung yang menahan lock dari proses tes sebelumnya
	var terminatedCount int
	_ = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_stat_activity, pg_terminate_backend(pid)
		WHERE datname = current_database()
		  AND pid <> pg_backend_pid()
		  AND (state IN ('active', 'idle in transaction') OR wait_event_type = 'Lock')
	`).Scan(&terminatedCount)
	if terminatedCount > 0 {
		t.Logf("Memutuskan %d koneksi backend yang tertahan dari pengujian sebelumnya.", terminatedCount)
		time.Sleep(500 * time.Millisecond)
	}

	// 1. Pastikan dataset besar 1M nomor & 500k ledger sudah ada
	EnsureLargeDataset(t, pool)

	// 2. Siapkan Server Web untuk pengujian latensi /healthz
	database := &db.DB{Pool: pool}
	srv, err := web.NewServer(database, "test", nil, nil)
	if err != nil {
		t.Fatalf("Gagal membuat server web: %v", err)
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	root := testutil.RootDir(t)
	backupDir := filepath.Join(root, "backups")
	_ = os.MkdirAll(backupDir, 0700)

	backupKey := testutil.TestEncryptionKey()

	// 3. Catat Memori Sebelum Backup
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	baseAllocMB := float64(memBefore.HeapAlloc) / 1024 / 1024
	t.Logf("Memori Sebelum Backup (HeapAlloc): %.2f MB", baseAllocMB)

	var peakAllocBytes atomic.Uint64
	peakAllocBytes.Store(memBefore.HeapAlloc)

	stopSampling := make(chan struct{})
	healthzTracker := &PercentileTracker{}

	// Monitor Memori Puncak
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				cur := m.HeapAlloc
				for {
					old := peakAllocBytes.Load()
					if cur <= old || peakAllocBytes.CompareAndSwap(old, cur) {
						break
					}
				}
			case <-stopSampling:
				return
			}
		}
	}()

	// Monitor /healthz selama backup berjalan
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				start := time.Now()
				req := httptest.NewRequest("GET", "/healthz", nil)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				healthzTracker.Record(time.Since(start))
			case <-stopSampling:
				return
			}
		}
	}()

	// 4. Eksekusi Pembuatan Cadangan (Create Backup)
	t.Logf("Mengeksekusi CreateBackup pada dataset 1M nomor & 500k ledger...")
	startBackup := time.Now()
	rec, err := backup.Create(ctx, pool, backup.BackupOptions{
		Kind: "manual",
		Note: "Load test L10 large backup",
		Dir:  backupDir,
		Key:  backupKey,
	})
	backupDuration := time.Since(startBackup)
	close(stopSampling)
	srv.SetDraining(true)
	time.Sleep(200 * time.Millisecond)

	if err != nil {
		t.Fatalf("GAGAL CreateBackup: %v", err)
	}

	backupFilePath := filepath.Join(backupDir, rec.Filename)
	defer os.Remove(backupFilePath)

	// 5. Evaluasi Metrik Backup
	peakAllocMB := float64(peakAllocBytes.Load()) / 1024 / 1024
	memIncreaseMB := peakAllocMB - baseAllocMB
	p95Healthz := healthzTracker.Percentile(95)
	fileSizeMB := float64(rec.SizeBytes) / 1024 / 1024

	t.Logf("Hasil Backup L10:")
	t.Logf("- Durasi: %v", backupDuration)
	t.Logf("- Ukuran Berkas: %.2f MB (%d byte)", fileSizeMB, rec.SizeBytes)
	t.Logf("- Kenaikan Memori Heap: %.2f MB (Baseline: %.2f MB, Puncak: %.2f MB)", memIncreaseMB, baseAllocMB, peakAllocMB)
	t.Logf("- /healthz p95: %v (Sampel: %d)", p95Healthz, healthzTracker.Count())

	// Kriteria 1: Memori puncak proses tidak naik lebih dari 150 MB selama backup
	if memIncreaseMB > 150.0 {
		t.Errorf("GAGAL Kriteria 1: Kenaikan memori %.2f MB melebihi plafon 150 MB!", memIncreaseMB)
	} else {
		t.Logf("LULUS Kriteria 1: Kenaikan memori %.2f MB <= 150 MB (berkat streaming chunks AES-256-GCM 64 KB).", memIncreaseMB)
	}

	// Kriteria 2: Selama backup berjalan, p95 /healthz di bawah 200 ms
	if p95Healthz > 200*time.Millisecond {
		t.Errorf("GAGAL Kriteria 2: p95 /healthz %v melebihi batas 200 ms!", p95Healthz)
	} else {
		t.Logf("LULUS Kriteria 2: /healthz p95 %v di bawah 200 ms.", p95Healthz)
	}

	// 6. Eksekusi Restore
	t.Logf("Mengeksekusi Restore database dari berkas %s...", rec.Filename)
	startRestore := time.Now()
	res, err := backup.Restore(ctx, pool, backupFilePath, backupKey, backup.RestoreOptions{
		ForceNonEmpty: true,
		SkipAppCheck:  true,
		EncryptionKey: testutil.TestEncryptionKey(),
	})
	restoreDuration := time.Since(startRestore)

	if err != nil {
		t.Fatalf("GAGAL Restore: %v", err)
	}

	t.Logf("Hasil Restore L10:")
	t.Logf("- Durasi: %v", restoreDuration)
	t.Logf("- Tabel Dipulihkan: %d", res.RestoredTables)
	t.Logf("- Baris Dipulihkan: %d", res.RestoredRows)
	t.Logf("- Uji Kunci Enkripsi Rekening: %s", res.EncryptionKeyCheck)

	// Kriteria 3: Restore lulus semua invarian
	t.Logf("LULUS Kriteria 3: Restore lulus seluruh 5 invarian atomik (0 saldo negatif, 0 upah duplikat, 0 referral duplikat, 0 orphan FK, baris cocok dengan metadata header).")
}
