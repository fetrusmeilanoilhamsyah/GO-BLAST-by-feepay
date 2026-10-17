package backup

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"goblast/internal/withdraw"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RestoreResult mencatat ringkasan eksekusi pemulihan database.
type RestoreResult struct {
	RestoredTables     int
	RestoredRows       int64
	RestoredFiles      int
	EncryptionKeyCheck string
}

// Restore menjalankan pemulihan basis data dari berkas cadangan .gbak secara atomik dalam satu transaksi.
func Restore(ctx context.Context, pool *pgxpool.Pool, filePath string, key []byte, opts RestoreOptions) (*RestoreResult, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("kunci BACKUP_KEY harus tepat 32 byte")
	}

	// 1. Kunci mutex lokal
	opMu.Lock()
	defer opMu.Unlock()

	// 2. Ambil PostgreSQL advisory lock
	var lockAcquired bool
	if err := pool.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", AdvisoryLockID).Scan(&lockAcquired); err != nil {
		return nil, fmt.Errorf("gagal memeriksa advisory lock PostgreSQL: %w", err)
	}
	if !lockAcquired {
		return nil, fmt.Errorf("operasi backup atau restore lain sedang berlangsung di database")
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", AdvisoryLockID)
	}()

	// 3. Verifikasi berkas cadangan secara menyeluruh sebelum menyentuh database
	header, verifyErr := Verify(filePath, [][]byte{key})
	if verifyErr != nil {
		return nil, fmt.Errorf("verifikasi berkas cadangan gagal (berkas rusak atau dimanipulasi): %w", verifyErr)
	}

	// 4. Periksa versi migrasi database saat ini
	var currentSchemaVersion int64
	if err := pool.QueryRow(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version").Scan(&currentSchemaVersion); err != nil {
		return nil, fmt.Errorf("gagal membaca versi migrasi database saat ini: %w", err)
	}
	if currentSchemaVersion != header.SchemaVersion {
		return nil, fmt.Errorf("versi migrasi database tidak cocok: database aktif versi %d, berkas cadangan versi %d. Jalankan migrasi terlebih dahulu.", currentSchemaVersion, header.SchemaVersion)
	}

	// 5. Periksa apakah database kosong
	var userCount int64
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&userCount)
	if userCount > 0 && !opts.ForceNonEmpty {
		return nil, fmt.Errorf("database tidak kosong (terdapat %d pengguna). Gunakan flag --force-nonempty untuk mengizinkan penimpaan.", userCount)
	}

	// 6. Periksa koneksi aplikasi yang aktif (application_name = 'goblast')
	if !opts.SkipAppCheck && !opts.StoppedTheApp {
		var activeApps int64
		err := pool.QueryRow(ctx, `
			SELECT count(*) 
			FROM pg_stat_activity 
			WHERE application_name = 'goblast' 
			  AND pid <> pg_backend_pid()
		`).Scan(&activeApps)
		if err == nil && activeApps > 0 {
			return nil, fmt.Errorf("aplikasi atau mesin kirim masih berjalan (terdeteksi %d koneksi aktif dengan application_name 'goblast'). Hentikan aplikasi sebelum restore, atau gunakan --i-stopped-the-app jika yakin.", activeApps)
		}
	}

	// 7. Mulai transaksi pemulihan tunggal
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("gagal memulai transaksi pemulihan database: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// a. Nonaktifkan trigger pengguna pada ledger_entries untuk eksekusi COPY
	_, err = tx.Exec(ctx, `
		ALTER TABLE ledger_entries DISABLE TRIGGER trg_prevent_ledger_update_delete;
		ALTER TABLE ledger_entries DISABLE TRIGGER trg_check_withdrawal_balance;
	`)
	if err != nil {
		return nil, fmt.Errorf("gagal menonaktifkan trigger buku besar: %w", err)
	}

	// b. Kosongkan seluruh tabel aplikasi yang dipulihkan secara bersih
	truncateSQL := `
		TRUNCATE TABLE 
			backups, notifications, broadcasts, support_tickets, audit_logs, 
			withdrawals, wallet_entries, ledger_entries, currency_conversions, 
			campaign_numbers, wa_accounts, number_batches, campaigns, 
			settings, admin_recovery_codes, users 
		CASCADE;
	`
	if _, err := tx.Exec(ctx, truncateSQL); err != nil {
		return nil, fmt.Errorf("gagal mengosongkan tabel untuk pemulihan: %w", err)
	}

	// c. Buka kembali berkas cadangan dan alirkan COPY FROM per tabel
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka berkas cadangan: %w", err)
	}
	defer file.Close()

	_, headerHash, err := ReadHeader(file)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca header cadangan: %w", err)
	}

	chunkReader, err := NewChunkReader(file, key, headerHash)
	if err != nil {
		return nil, fmt.Errorf("gagal menginisialisasi pembaca chunk: %w", err)
	}

	gzipReader, err := gzip.NewReader(chunkReader)
	if err != nil {
		return nil, fmt.Errorf("gagal menginisialisasi pembaca gzip: %w", err)
	}
	defer gzipReader.Close()

	streamReader := NewStreamReader(gzipReader)
	pgConn := tx.Conn().PgConn()

	restoredTables := 0
	var restoredRows int64
	restoredFiles := 0

	for {
		entryType, err := streamReader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("gagal membaca entri stream pemulihan: %w", err)
		}

		switch entryType {
		case EntryTypeTable:
			tblHeader, tableChunkReader, err := streamReader.ReadTableHeader()
			if err != nil {
				return nil, fmt.Errorf("gagal membaca header tabel: %w", err)
			}

			colList := strings.Join(tblHeader.Columns, ", ")
			copyFromSQL := fmt.Sprintf("COPY %s (%s) FROM STDIN", tblHeader.Name, colList)

			if _, err := pgConn.CopyFrom(ctx, tableChunkReader, copyFromSQL); err != nil {
				return nil, fmt.Errorf("gagal COPY FROM tabel %s: %w", tblHeader.Name, err)
			}
			restoredTables++
			restoredRows += tblHeader.RowCount

		case EntryTypeFile:
			fileHeader, err := streamReader.ReadFileHeader()
			if err != nil {
				return nil, fmt.Errorf("gagal membaca header file: %w", err)
			}

			// Path traversal guard
			cleanRel := filepath.Clean(fileHeader.RelPath)
			if strings.HasPrefix(cleanRel, "..") || filepath.IsAbs(cleanRel) {
				return nil, fmt.Errorf("path file tidak aman di dalam cadangan: %s", fileHeader.RelPath)
			}

			targetPath := filepath.Join("uploads", cleanRel)
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return nil, fmt.Errorf("gagal membuat direktori uploads/%s: %w", filepath.Dir(cleanRel), err)
			}

			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return nil, fmt.Errorf("gagal membuka target berkas %s: %w", targetPath, err)
			}

			written, err := io.Copy(outFile, io.LimitReader(gzipReader, fileHeader.Size))
			_ = outFile.Close()
			if err != nil {
				return nil, fmt.Errorf("gagal menulis berkas %s: %w", targetPath, err)
			}
			if written != fileHeader.Size {
				return nil, fmt.Errorf("panjang data berkas %s tidak sesuai (diharapkan %d, tertulis %d)", targetPath, fileHeader.Size, written)
			}
			restoredFiles++

		case EntryTypeEnd:
			break
		default:
			return nil, fmt.Errorf("tipe entri stream tidak dikenal (0x%02x)", entryType)
		}

		if entryType == EntryTypeEnd {
			break
		}
	}

	// d. Keputusan B1: Seluruh sesi WhatsApp tidak dipulihkan, ubah status wa_accounts menjadi terputus
	_, err = tx.Exec(ctx, `
		UPDATE wa_accounts 
		SET status = 'terputus', 
		    stop_reason = 'restored from backup', 
		    whatsmeow_device_id = NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("gagal memperbarui status akun WhatsApp ke terputus: %w", err)
	}

	// e. Atur ulang seluruh sequence database ke max(id) + 1
	for _, tbl := range ApplicationTables {
		if tbl == "settings" {
			continue
		}
		seqQuery := fmt.Sprintf(`
			SELECT setval(
				pg_get_serial_sequence('%s', 'id'), 
				COALESCE((SELECT MAX(id) FROM %s), 1), 
				(SELECT MAX(id) FROM %s) IS NOT NULL
			)
		`, tbl, tbl, tbl)
		if _, err := tx.Exec(ctx, seqQuery); err != nil {
			return nil, fmt.Errorf("gagal mengatur sequence tabel %s: %w", tbl, err)
		}
	}

	// f. Aktifkan kembali trigger buku besar sebelum validasi invarian
	_, err = tx.Exec(ctx, `
		ALTER TABLE ledger_entries ENABLE TRIGGER trg_prevent_ledger_update_delete;
		ALTER TABLE ledger_entries ENABLE TRIGGER trg_check_withdrawal_balance;
	`)
	if err != nil {
		return nil, fmt.Errorf("gagal mengaktifkan kembali trigger buku besar: %w", err)
	}

	// g. Pemeriksaan Invarian Keuangan dan Integritas Data
	// Invarian 1: Tidak ada saldo negatif (user_balance)
	var negBalanceUsers int
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM users u
		CROSS JOIN (VALUES ('IDR'), ('USDT')) AS c(curr)
		WHERE user_balance(u.id, c.curr) < 0
	`).Scan(&negBalanceUsers)
	if err != nil {
		return nil, fmt.Errorf("gagal menjalankan pemeriksaan saldo negatif: %w", err)
	}
	if negBalanceUsers > 0 {
		return nil, fmt.Errorf("PELANGGARAN INVARIAN: ditemukan %d akun dengan saldo negatif setelah restore!", negBalanceUsers)
	}

	// Invarian 2: Tiap campaign_number terkirim punya paling banyak satu baris upah
	var dupUpahCount int
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM (
			SELECT campaign_number_id
			FROM ledger_entries
			WHERE kind = 'upah' AND campaign_number_id IS NOT NULL
			GROUP BY campaign_number_id
			HAVING COUNT(*) > 1
		) dups
	`).Scan(&dupUpahCount)
	if err != nil {
		return nil, fmt.Errorf("gagal memeriksa keunikan upah: %w", err)
	}
	if dupUpahCount > 0 {
		return nil, fmt.Errorf("PELANGGARAN INVARIAN: ditemukan %d nomor target dengan upah ganda!", dupUpahCount)
	}

	// Invarian 3: Tidak ada baris referral ganda
	var dupRefCount int
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM (
			SELECT campaign_number_id
			FROM ledger_entries
			WHERE kind = 'referral' AND campaign_number_id IS NOT NULL
			GROUP BY campaign_number_id
			HAVING COUNT(*) > 1
		) dups
	`).Scan(&dupRefCount)
	if err != nil {
		return nil, fmt.Errorf("gagal memeriksa keunikan referral: %w", err)
	}
	if dupRefCount > 0 {
		return nil, fmt.Errorf("PELANGGARAN INVARIAN: ditemukan %d nomor target dengan komisi referral ganda!", dupRefCount)
	}

	// Invarian 4: Seluruh Foreign Key terpenuhi (tidak ada orphan)
	var orphanBatches, orphanNumbers, orphanLedger int
	_ = tx.QueryRow(ctx, "SELECT COUNT(*) FROM number_batches WHERE campaign_id NOT IN (SELECT id FROM campaigns)").Scan(&orphanBatches)
	_ = tx.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id NOT IN (SELECT id FROM campaigns)").Scan(&orphanNumbers)
	_ = tx.QueryRow(ctx, "SELECT COUNT(*) FROM ledger_entries WHERE user_id NOT IN (SELECT id FROM users)").Scan(&orphanLedger)
	if orphanBatches > 0 || orphanNumbers > 0 || orphanLedger > 0 {
		return nil, fmt.Errorf("PELANGGARAN INVARIAN: terdapat entri asing (orphan FK): batches=%d, numbers=%d, ledger=%d", orphanBatches, orphanNumbers, orphanLedger)
	}

	// Invarian 5: Jumlah baris sama persis dengan kepala berkas
	for _, tm := range header.Tables {
		var actualCount int64
		countSQL := fmt.Sprintf("SELECT COUNT(*) FROM %s", tm.Name)
		if err := tx.QueryRow(ctx, countSQL).Scan(&actualCount); err != nil {
			return nil, fmt.Errorf("gagal memeriksa baris tabel %s: %w", tm.Name, err)
		}
		if actualCount != tm.RowCount {
			return nil, fmt.Errorf("PELANGGARAN INVARIAN: jumlah baris tabel %s (%d) tidak cocok dengan header cadangan (%d)", tm.Name, actualCount, tm.RowCount)
		}
	}

	// h. Commit transaksi pemulihan
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("gagal commit transaksi pemulihan: %w", err)
	}

	// 8. Uji kompatibilitas ENCRYPTION_KEY terhadap data tujuan penarikan
	encKeyStatus := "tidak ada data tujuan penarikan untuk diuji"
	if len(opts.EncryptionKey) == 32 {
		var destEncrypted string
		rowErr := pool.QueryRow(ctx, "SELECT destination_encrypted FROM withdrawals WHERE destination_encrypted IS NOT NULL LIMIT 1").Scan(&destEncrypted)
		if rowErr == nil && destEncrypted != "" {
			_, decErr := withdraw.DecryptDestination(destEncrypted, opts.EncryptionKey)
			if decErr != nil {
				encKeyStatus = fmt.Sprintf("PERINGATAN: dekripsi rekening gagal (%v). Pastikan ENCRYPTION_KEY sama dengan saat cadangan dibuat!", decErr)
				slog.Warn("Peringatan ketidakcocokan ENCRYPTION_KEY setelah restore", "err", decErr)
			} else {
				encKeyStatus = "sukses: data rekening terenkripsi berhasil didekripsi dengan ENCRYPTION_KEY saat ini"
			}
		}
	}

	slog.Info("Pemulihan basis data selesai dengan sukses",
		"tables", restoredTables,
		"rows", restoredRows,
		"files", restoredFiles,
		"enc_check", encKeyStatus,
	)

	return &RestoreResult{
		RestoredTables:     restoredTables,
		RestoredRows:       restoredRows,
		RestoredFiles:      restoredFiles,
		EncryptionKeyCheck: encKeyStatus,
	}, nil
}
