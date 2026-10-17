package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PruneRetention menerapkan kebijakan retensi 7 harian dan 4 mingguan.
// Sesuai invarian sistem: cadangan terverifikasi yang terakhir tidak pernah dihapus.
func PruneRetention(ctx context.Context, pool *pgxpool.Pool, dir string) ([]string, error) {
	if dir == "" {
		dir = DefaultBackupDir
	}

	// 1. Temukan ID cadangan terverifikasi terakhir
	var lastVerifiedID int64
	err := pool.QueryRow(ctx, `
		SELECT id 
		FROM backups 
		WHERE verify_ok = true 
		ORDER BY created_at DESC 
		LIMIT 1
	`).Scan(&lastVerifiedID)
	if err != nil {
		// Belum ada cadangan terverifikasi
		lastVerifiedID = 0
	}

	// 2. Ambil seluruh daftar cadangan
	rows, err := pool.Query(ctx, `
		SELECT id, filename, created_at, verify_ok 
		FROM backups 
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca daftar cadangan untuk retensi: %w", err)
	}
	defer rows.Close()

	type item struct {
		id       int64
		filename string
		created  time.Time
		verifyOK *bool
	}

	var allBackups []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.filename, &it.created, &it.verifyOK); err == nil {
			allBackups = append(allBackups, it)
		}
	}

	if len(allBackups) == 0 {
		return nil, nil
	}

	// 3. Klasifikasikan slot retensi
	keepIDs := make(map[int64]bool)

	// Pastikan cadangan terverifikasi terakhir selalu dipertahankan
	if lastVerifiedID > 0 {
		keepIDs[lastVerifiedID] = true
	}

	// Slot harian: 1 cadangan per hari untuk 7 hari terakhir
	dailySlots := make(map[string]bool)
	now := time.Now().UTC()
	cutoffDaily := now.AddDate(0, 0, -7)

	for _, it := range allBackups {
		if it.created.After(cutoffDaily) {
			dayKey := it.created.Format("2006-01-02")
			if !dailySlots[dayKey] {
				dailySlots[dayKey] = true
				keepIDs[it.id] = true
			}
		}
	}

	// Slot mingguan: 1 cadangan per minggu untuk 4 minggu sebelumnya
	cutoffWeekly := now.AddDate(0, 0, -28)
	weeklySlots := make(map[string]bool)

	for _, it := range allBackups {
		if it.created.Before(cutoffDaily) && it.created.After(cutoffWeekly) {
			year, week := it.created.ISOWeek()
			weekKey := fmt.Sprintf("%d-W%02d", year, week)
			if !weeklySlots[weekKey] {
				weeklySlots[weekKey] = true
				keepIDs[it.id] = true
			}
		}
	}

	// 4. Hapus cadangan yang tidak masuk slot retensi
	var prunedFiles []string
	for _, it := range allBackups {
		if keepIDs[it.id] {
			continue
		}

		filePath := filepath.Join(dir, it.filename)
		_ = os.Remove(filePath)

		_, delErr := pool.Exec(ctx, "DELETE FROM backups WHERE id = $1", it.id)
		if delErr != nil {
			slog.Error("Gagal menghapus entri cadangan dari database saat pembersihan retensi", "id", it.id, "err", delErr)
		} else {
			prunedFiles = append(prunedFiles, it.filename)
		}
	}

	if len(prunedFiles) > 0 {
		slog.Info("Pembersihan retensi cadangan selesai", "pruned_count", len(prunedFiles), "pruned_files", prunedFiles)
	}

	return prunedFiles, nil
}

// CanDeleteBackup memeriksa apakah cadangan tertentu diizinkan dihapus secara manual.
func CanDeleteBackup(ctx context.Context, pool *pgxpool.Pool, backupID int64) (bool, string, error) {
	var isVerifyOK *bool
	var filename string
	err := pool.QueryRow(ctx, "SELECT filename, verify_ok FROM backups WHERE id = $1", backupID).Scan(&filename, &isVerifyOK)
	if err != nil {
		return false, "", fmt.Errorf("cadangan tidak ditemukan: %w", err)
	}

	// Cek apakah cadangan ini adalah cadangan terverifikasi terakhir
	var lastVerifiedID int64
	_ = pool.QueryRow(ctx, `
		SELECT id FROM backups 
		WHERE verify_ok = true 
		ORDER BY created_at DESC 
		LIMIT 1
	`).Scan(&lastVerifiedID)

	if backupID == lastVerifiedID {
		return false, filename, fmt.Errorf("cadangan terverifikasi terakhir dilarang dihapus demi keselamatan data")
	}

	return true, filename, nil
}
