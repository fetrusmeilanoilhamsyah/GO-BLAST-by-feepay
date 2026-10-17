package backup

import (
	"context"
	"log/slog"
	"time"

	"goblast/internal/safe"
)

// StartScheduler menjalankan penjadwal pencadangan otomatis harian pada pukul 03:00 WIB (Asia/Jakarta)
// dan memicu pembersihan retensi berkala menggunakan pemulih panic safe.Go.
func StartScheduler(ctx context.Context, m *Manager) {
	if m == nil || len(m.Key()) != 32 {
		slog.Warn("BACKUP_KEY tidak dikonfigurasi atau tidak valid; penjadwal cadangan otomatis harian dinonaktifkan")
		return
	}

	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("WIB", 7*3600)
	}

	safe.Go("backup_scheduler", func() {
		slog.Info("Penjadwal pencadangan database otomatis harian aktif (target: 03:00 WIB)")
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		var lastRunDate string

		for {
			select {
			case <-ctx.Done():
				slog.Info("Penjadwal pencadangan dihentikan")
				return
			case now := <-ticker.C:
				wibNow := now.In(loc)
				todayStr := wibNow.Format("2006-01-02")

				if wibNow.Hour() == 3 && wibNow.Minute() == 0 && lastRunDate != todayStr {
					lastRunDate = todayStr
					slog.Info("Memulai pencadangan otomatis harian pukul 03:00 WIB...", "date", todayStr)

					rec, err := m.CreateAutomatic(ctx)
					if err != nil {
						slog.Error("Pencadangan otomatis harian 03:00 WIB gagal", "err", err)
					} else {
						slog.Info("Pencadangan otomatis harian 03:00 WIB berhasil diselesaikan",
							"filename", rec.Filename,
							"size_bytes", rec.SizeBytes,
						)
					}
				}
			}
		}
	})
}
