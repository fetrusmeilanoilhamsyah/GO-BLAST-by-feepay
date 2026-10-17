package backup

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// Mutex proses tunggal untuk mencegah konkurensi backup/restore di dalam instance aplikasi
	opMu sync.Mutex

	// ApplicationTables dalam urutan dependensi Foreign Key
	ApplicationTables = []string{
		"users",
		"admin_recovery_codes",
		"settings",
		"campaigns",
		"number_batches",
		"wa_accounts",
		"campaign_numbers",
		"currency_conversions",
		"ledger_entries",
		"wallet_entries",
		"withdrawals",
		"audit_logs",
		"support_tickets",
		"broadcasts",
		"notifications",
		"backups",
	}

	// ExcludedTables yang dilarang keras ikut ke dalam cadangan
	ExcludedTables = map[string]bool{
		"goose_db_version": true,
		"web_sessions":     true,
	}
)

// IsTableExcluded memeriksa apakah suatu tabel dikecualikan dari cadangan (termasuk whatsmeow_*).
func IsTableExcluded(tableName string) bool {
	if ExcludedTables[tableName] {
		return true
	}
	if strings.HasPrefix(tableName, "whatsmeow_") {
		return true
	}
	return false
}

// GetSortColumn mengembalikan nama kolom pengurutan konsisten untuk COPY TO.
func GetSortColumn(tableName string) string {
	if tableName == "settings" {
		return "key"
	}
	return "id"
}

// Create menjalankan pembuatan cadangan terenkripsi (.gbak) secara atomik dan streaming.
func Create(ctx context.Context, pool *pgxpool.Pool, opts BackupOptions) (*BackupRecord, error) {
	if len(opts.Key) != 32 {
		return nil, fmt.Errorf("kunci BACKUP_KEY harus tepat 32 byte")
	}

	// 1. Kunci mutex lokal
	opMu.Lock()
	defer opMu.Unlock()

	dir := opts.Dir
	if dir == "" {
		dir = DefaultBackupDir
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("gagal membuat folder cadangan %s: %w", dir, err)
	}

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

	// 3. Periksa ruang disk bebas (minimal 2x ukuran backup terakhir atau 100MB)
	var lastSize int64
	_ = pool.QueryRow(ctx, "SELECT size_bytes FROM backups ORDER BY created_at DESC LIMIT 1").Scan(&lastSize)
	minSpaceNeeded := uint64(MinFreeDiskSpaceBytes)
	if lastSize > 0 && uint64(lastSize*2) > minSpaceNeeded {
		minSpaceNeeded = uint64(lastSize * 2)
	}

	freeDisk, err := GetFreeDiskSpace(dir)
	if err == nil && freeDisk < minSpaceNeeded {
		errMsg := fmt.Sprintf("ruang disk bebas tidak mencukupi: tersedia %d byte, butuh minimal %d byte", freeDisk, minSpaceNeeded)
		slog.Error("Penolakan backup karena disk penuh", "err", errMsg)
		return nil, fmt.Errorf("%s", errMsg)
	}

	// 4. Buka transaksi REPEATABLE READ READ ONLY untuk konsistensi data absolut
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("gagal memulai transaksi snapshot database: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Baca versi skema goose saat ini
	var schemaVersion int64
	if err := tx.QueryRow(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version").Scan(&schemaVersion); err != nil {
		return nil, fmt.Errorf("gagal membaca versi migrasi database: %w", err)
	}

	// 5. Hitung baris dan struktur tabel
	var tablesMeta []TableMeta
	rowCountsMap := make(map[string]interface{})
	var totalRows int64

	for _, tbl := range ApplicationTables {
		if IsTableExcluded(tbl) {
			continue
		}

		var count int64
		countQuery := fmt.Sprintf("SELECT count(*) FROM %s", tbl)
		if err := tx.QueryRow(ctx, countQuery).Scan(&count); err != nil {
			return nil, fmt.Errorf("gagal menghitung baris tabel %s: %w", tbl, err)
		}

		rows, err := tx.Query(ctx, `
			SELECT column_name 
			FROM information_schema.columns 
			WHERE table_schema = 'public' AND table_name = $1 
			ORDER BY ordinal_position
		`, tbl)
		if err != nil {
			return nil, fmt.Errorf("gagal membaca kolom tabel %s: %w", tbl, err)
		}

		var cols []string
		for rows.Next() {
			var col string
			if err := rows.Scan(&col); err == nil {
				cols = append(cols, col)
			}
		}
		rows.Close()

		sortCol := GetSortColumn(tbl)
		tablesMeta = append(tablesMeta, TableMeta{
			Name:     tbl,
			RowCount: count,
			SortCol:  sortCol,
			Columns:  cols,
		})
		rowCountsMap[tbl] = count
		totalRows += count
	}

	// Hitung berkas di uploads/
	uploadsCount := 0
	_ = filepath.Walk("uploads", func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			uploadsCount++
		}
		return nil
	})

	now := time.Now().UTC()
	keyID := ComputeKeyID(opts.Key)

	header := Header{
		FormatVersion: FormatVersion,
		SchemaVersion: schemaVersion,
		CreatedAt:     now,
		KeyID:         keyID,
		Tables:        tablesMeta,
		UploadsCount:  uploadsCount,
		TotalRows:     totalRows,
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat JSON kepala cadangan: %w", err)
	}
	headerHash := sha256.Sum256(headerJSON)

	// 6. Siapkan berkas sementara
	var randSuffix [4]byte
	_, _ = rand.Read(randSuffix[:])
	tmpFileName := fmt.Sprintf(".tmp_%d_%x.gbak", now.UnixNano(), randSuffix[:])
	tmpFilePath := filepath.Join(dir, tmpFileName)

	tmpFile, err := os.OpenFile(tmpFilePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat berkas sementara cadangan: %w", err)
	}

	var success bool
	defer func() {
		_ = tmpFile.Close()
		if !success {
			_ = os.Remove(tmpFilePath)
		}
	}()

	// 7. Tulis magic + headerLen + headerJSON ke berkas sementara
	if _, err := tmpFile.Write([]byte(Magic)); err != nil {
		return nil, fmt.Errorf("gagal menulis magic header: %w", err)
	}
	var hLenBuf [4]byte
	binary.BigEndian.PutUint32(hLenBuf[:], uint32(len(headerJSON)))
	if _, err := tmpFile.Write(hLenBuf[:]); err != nil {
		return nil, fmt.Errorf("gagal menulis panjang header: %w", err)
	}
	if _, err := tmpFile.Write(headerJSON); err != nil {
		return nil, fmt.Errorf("gagal menulis header JSON: %w", err)
	}

	// 8. Siapkan pipeline streaming: StreamWriter -> Gzip -> ChunkWriter -> File
	chunkWriter, err := NewChunkWriter(tmpFile, opts.Key, headerHash)
	if err != nil {
		return nil, fmt.Errorf("gagal menginisialisasi ChunkWriter: %w", err)
	}

	gzipWriter := gzip.NewWriter(chunkWriter)
	streamWriter := NewStreamWriter(gzipWriter)

	// 9. Stream seluruh tabel lewat COPY TO
	pgConn := tx.Conn().PgConn()
	for _, meta := range tablesMeta {
		colList := strings.Join(meta.Columns, ", ")
		copySQL := fmt.Sprintf("COPY (SELECT %s FROM %s ORDER BY %s) TO STDOUT", colList, meta.Name, meta.SortCol)

		tableChunkWriter, err := streamWriter.BeginTable(meta.Name, meta.RowCount, meta.Columns)
		if err != nil {
			return nil, fmt.Errorf("gagal memulai streaming tabel %s: %w", meta.Name, err)
		}

		if _, err := pgConn.CopyTo(ctx, tableChunkWriter, copySQL); err != nil {
			return nil, fmt.Errorf("gagal COPY TO tabel %s: %w", meta.Name, err)
		}

		if err := tableChunkWriter.Close(); err != nil {
			return nil, fmt.Errorf("gagal menutup streaming tabel %s: %w", meta.Name, err)
		}
	}

	// 10. Stream seluruh berkas uploads/
	_ = filepath.Walk("uploads", func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel("uploads", path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		_ = streamWriter.WriteFile(rel, info.Size(), f)
		return nil
	})

	if err := streamWriter.Finish(); err != nil {
		return nil, fmt.Errorf("gagal menyelesaikan stream cadangan: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("gagal menutup gzip writer: %w", err)
	}
	if err := chunkWriter.Close(); err != nil {
		return nil, fmt.Errorf("gagal menutup chunk writer: %w", err)
	}

	// 11. Hitung total SHA-256 dan ukuran berkas sementara
	if err := tmpFile.Sync(); err != nil {
		return nil, fmt.Errorf("gagal sync berkas cadangan: %w", err)
	}
	fileInfo, err := tmpFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("gagal membaca ukuran berkas cadangan: %w", err)
	}
	sizeBytes := fileInfo.Size()

	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("gagal seek berkas cadangan: %w", err)
	}
	fileHasher := sha256.New()
	if _, err := io.Copy(fileHasher, tmpFile); err != nil {
		return nil, fmt.Errorf("gagal menghitung checksum berkas cadangan: %w", err)
	}
	fileSHA256 := hex.EncodeToString(fileHasher.Sum(nil))
	_ = tmpFile.Close()

	// 12. Ganti nama berkas menjadi nama permanen
	finalFileName := fmt.Sprintf("%s_%s_%s.gbak", now.Format("20060102_150405"), opts.Kind, fileSHA256[:8])
	finalFilePath := filepath.Join(dir, finalFileName)
	if err := os.Rename(tmpFilePath, finalFilePath); err != nil {
		return nil, fmt.Errorf("gagal rename berkas cadangan: %w", err)
	}
	success = true

	// Commit transaksi baca
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("gagal commit transaksi baca snapshot: %w", err)
	}

	// 13. Simpan metadata ke tabel backups
	rowCountsJSON, _ := json.Marshal(rowCountsMap)
	var recID int64
	var recCreated time.Time
	err = pool.QueryRow(ctx, `
		INSERT INTO backups (kind, filename, created_at, created_by, size_bytes, sha256, schema_version, row_counts, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at
	`, opts.Kind, finalFileName, now, opts.CreatedBy, sizeBytes, fileSHA256, schemaVersion, rowCountsJSON, opts.Note).Scan(&recID, &recCreated)
	if err != nil {
		return nil, fmt.Errorf("gagal menyimpan rekaman cadangan ke database: %w", err)
	}

	// 14. Otomatis verifikasi cadangan yang baru saja dibuat
	verifyOK := false
	var verifiedAt *time.Time
	verHeader, verifyErr := Verify(finalFilePath, [][]byte{opts.Key})
	vNow := time.Now().UTC()
	verifiedAt = &vNow
	if verifyErr == nil && verHeader != nil {
		verifyOK = true
	} else {
		slog.Error("Verifikasi otomatis gagal setelah backup dibuat", "err", verifyErr, "file", finalFileName)
	}

	_, _ = pool.Exec(ctx, `
		UPDATE backups SET verified_at = $1, verify_ok = $2 WHERE id = $3
	`, verifiedAt, verifyOK, recID)

	rec := &BackupRecord{
		ID:            recID,
		Kind:          opts.Kind,
		Filename:      finalFileName,
		CreatedAt:     recCreated,
		CreatedBy:     opts.CreatedBy,
		SizeBytes:     sizeBytes,
		SHA256:        fileSHA256,
		SchemaVersion: schemaVersion,
		RowCounts:     rowCountsMap,
		VerifiedAt:    verifiedAt,
		VerifyOK:      &verifyOK,
	}
	if opts.Note != "" {
		rec.Note = &opts.Note
	}

	slog.Info("Cadangan database berhasil dibuat",
		"filename", finalFileName,
		"size_bytes", sizeBytes,
		"total_rows", totalRows,
		"verify_ok", verifyOK,
	)

	return rec, nil
}
