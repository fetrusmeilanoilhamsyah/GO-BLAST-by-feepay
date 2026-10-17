package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Manager mengelola seluruh siklus hidup cadangan: pembuatan, verifikasi, pemulihan, dan retensi.
type Manager struct {
	pool    *pgxpool.Pool
	dir     string
	key     []byte
	keyPrev []byte
}

func NewManager(pool *pgxpool.Pool, dir string, key, keyPrev []byte) *Manager {
	if dir == "" {
		dir = DefaultBackupDir
	}
	return &Manager{
		pool:    pool,
		dir:     dir,
		key:     key,
		keyPrev: keyPrev,
	}
}

func (m *Manager) Dir() string {
	return m.dir
}

func (m *Manager) Key() []byte {
	return m.key
}

func (m *Manager) CandidateKeys() [][]byte {
	var keys [][]byte
	if len(m.key) == 32 {
		keys = append(keys, m.key)
	}
	if len(m.keyPrev) == 32 {
		keys = append(keys, m.keyPrev)
	}
	return keys
}

// CreateManual membuat cadangan berjenis 'manual'.
func (m *Manager) CreateManual(ctx context.Context, adminID int64, note string) (*BackupRecord, error) {
	return Create(ctx, m.pool, BackupOptions{
		Kind:      "manual",
		CreatedBy: &adminID,
		Note:      note,
		Dir:       m.dir,
		Key:       m.key,
		KeyPrev:   m.keyPrev,
	})
}

// CreateAutomatic membuat cadangan berjenis 'otomatis' dan menjalankan pembersihan retensi.
func (m *Manager) CreateAutomatic(ctx context.Context) (*BackupRecord, error) {
	rec, err := Create(ctx, m.pool, BackupOptions{
		Kind:    "otomatis",
		Dir:     m.dir,
		Key:     m.key,
		KeyPrev: m.keyPrev,
	})
	if err != nil {
		return nil, err
	}

	// Jalankan retensi berkala setelah backup otomatis sukses
	_, _ = PruneRetention(ctx, m.pool, m.dir)
	return rec, nil
}

// CreateBeforeRestore membuat cadangan darurat berjenis 'sebelum_restore' sebelum eksekusi restore.
func (m *Manager) CreateBeforeRestore(ctx context.Context, adminID int64) (*BackupRecord, error) {
	return Create(ctx, m.pool, BackupOptions{
		Kind:      "sebelum_restore",
		CreatedBy: &adminID,
		Note:      "Cadangan otomatis sebelum operasi pemulihan dijalankan",
		Dir:       m.dir,
		Key:       m.key,
		KeyPrev:   m.keyPrev,
	})
}

// Verify memeriksa integritas cadangan berdasarkan ID database dan memperbarui status verifikasinya.
func (m *Manager) Verify(ctx context.Context, backupID int64) (*Header, error) {
	var filename string
	err := m.pool.QueryRow(ctx, "SELECT filename FROM backups WHERE id = $1", backupID).Scan(&filename)
	if err != nil {
		return nil, fmt.Errorf("cadangan tidak ditemukan: %w", err)
	}

	filePath := filepath.Join(m.dir, filename)
	header, vErr := Verify(filePath, m.CandidateKeys())

	now := time.Now().UTC()
	verifyOK := (vErr == nil && header != nil)

	_, _ = m.pool.Exec(ctx, `
		UPDATE backups SET verified_at = $1, verify_ok = $2 WHERE id = $3
	`, now, verifyOK, backupID)

	if vErr != nil {
		return nil, vErr
	}
	return header, nil
}

// Restore memulihkan basis data dari cadangan tertentu berdasarkan ID database.
func (m *Manager) Restore(ctx context.Context, backupID int64, opts RestoreOptions) (*RestoreResult, error) {
	var filename string
	err := m.pool.QueryRow(ctx, "SELECT filename FROM backups WHERE id = $1", backupID).Scan(&filename)
	if err != nil {
		return nil, fmt.Errorf("cadangan tidak ditemukan: %w", err)
	}

	filePath := filepath.Join(m.dir, filename)
	return Restore(ctx, m.pool, filePath, m.key, opts)
}

// Delete menghapus cadangan dari disk dan database jika memenuhi syarat retensi.
func (m *Manager) Delete(ctx context.Context, backupID int64) error {
	canDelete, filename, err := CanDeleteBackup(ctx, m.pool, backupID)
	if err != nil {
		return err
	}
	if !canDelete {
		return fmt.Errorf("cadangan tidak diizinkan dihapus")
	}

	filePath := filepath.Join(m.dir, filename)
	_ = os.Remove(filePath)

	_, delErr := m.pool.Exec(ctx, "DELETE FROM backups WHERE id = $1", backupID)
	return delErr
}

// List mengambil seluruh rekaman cadangan dari database diurutkan dari yang terbaru.
func (m *Manager) List(ctx context.Context) ([]BackupRecord, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT id, kind, filename, created_at, created_by, size_bytes, sha256, schema_version, row_counts, verified_at, verify_ok, note 
		FROM backups 
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca daftar cadangan: %w", err)
	}
	defer rows.Close()

	var records []BackupRecord
	for rows.Next() {
		var r BackupRecord
		var rowCountsBytes []byte
		err := rows.Scan(
			&r.ID, &r.Kind, &r.Filename, &r.CreatedAt, &r.CreatedBy,
			&r.SizeBytes, &r.SHA256, &r.SchemaVersion, &rowCountsBytes,
			&r.VerifiedAt, &r.VerifyOK, &r.Note,
		)
		if err == nil {
			_ = json.Unmarshal(rowCountsBytes, &r.RowCounts)
			records = append(records, r)
		}
	}
	return records, nil
}

// Get mengambil satu rekaman cadangan berdasarkan ID.
func (m *Manager) Get(ctx context.Context, backupID int64) (*BackupRecord, error) {
	var r BackupRecord
	var rowCountsBytes []byte
	err := m.pool.QueryRow(ctx, `
		SELECT id, kind, filename, created_at, created_by, size_bytes, sha256, schema_version, row_counts, verified_at, verify_ok, note 
		FROM backups 
		WHERE id = $1
	`, backupID).Scan(
		&r.ID, &r.Kind, &r.Filename, &r.CreatedAt, &r.CreatedBy,
		&r.SizeBytes, &r.SHA256, &r.SchemaVersion, &rowCountsBytes,
		&r.VerifiedAt, &r.VerifyOK, &r.Note,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(rowCountsBytes, &r.RowCounts)
	return &r, nil
}

// GetStats mengembalikan agregasi statistik cadangan untuk pemantauan dan health check.
func (m *Manager) GetStats(ctx context.Context) (totalCount int, totalBytes int64, lastBackupTime *time.Time, lastVerifiedTime *time.Time, lastVerifyOK bool, err error) {
	err = m.pool.QueryRow(ctx, `
		SELECT 
			COUNT(*),
			COALESCE(SUM(size_bytes), 0),
			MAX(created_at)
		FROM backups
	`).Scan(&totalCount, &totalBytes, &lastBackupTime)
	if err != nil {
		return 0, 0, nil, nil, false, err
	}

	var verifyOK *bool
	_ = m.pool.QueryRow(ctx, `
		SELECT verified_at, verify_ok 
		FROM backups 
		WHERE verified_at IS NOT NULL 
		ORDER BY created_at DESC 
		LIMIT 1
	`).Scan(&lastVerifiedTime, &verifyOK)

	if verifyOK != nil && *verifyOK {
		lastVerifyOK = true
	}
	return
}
