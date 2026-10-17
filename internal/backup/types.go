package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

const (
	// Magic 8-byte file identifier di awal berkas .gbak
	Magic = "GOBLAST1"

	// Trailer 8-byte penanda akhir berkas sebelum SHA-256 total
	MagicTrailer = "GBAKEND1"

	// Versi format berkas cadangan
	FormatVersion = 1

	// Ukuran potongan plaintext (64 KB) sebelum dienkripsi
	ChunkSize = 64 * 1024

	// Batas ruang disk bebas minimal bila belum ada riwayat cadangan (100 MB)
	MinFreeDiskSpaceBytes = 100 * 1024 * 1024

	// Folder bawaan penyimpanan cadangan
	DefaultBackupDir = "backups"

	// Advisory Lock ID di PostgreSQL untuk sinkronisasi operasi backup/restore (ASCII 'GBAK')
	AdvisoryLockID = 0x4742414b
)

// TableMeta menyimpan nama dan jumlah baris tabel yang dicadangkan.
type TableMeta struct {
	Name     string   `json:"name"`
	RowCount int64    `json:"row_count"`
	SortCol  string   `json:"sort_col"`
	Columns  []string `json:"columns"`
}

// Header adalah kepala berkas cadangan tidak terenkripsi dalam format JSON.
type Header struct {
	FormatVersion int         `json:"format_version"`
	SchemaVersion int64       `json:"schema_version"`
	CreatedAt     time.Time   `json:"created_at"`
	KeyID         string      `json:"key_id"`
	Tables        []TableMeta `json:"tables"`
	UploadsCount  int         `json:"uploads_count"`
	TotalRows     int64       `json:"total_rows"`
}

// BackupRecord merepresentasikan catatan cadangan di tabel database backups.
type BackupRecord struct {
	ID            int64                  `json:"id"`
	Kind          string                 `json:"kind"`
	Filename      string                 `json:"filename"`
	CreatedAt     time.Time              `json:"created_at"`
	CreatedBy     *int64                 `json:"created_by"`
	SizeBytes     int64                  `json:"size_bytes"`
	SHA256        string                 `json:"sha256"`
	SchemaVersion int64                  `json:"schema_version"`
	RowCounts     map[string]interface{} `json:"row_counts"`
	VerifiedAt    *time.Time             `json:"verified_at"`
	VerifyOK      *bool                  `json:"verify_ok"`
	Note          *string                `json:"note"`
}

// BackupOptions adalah opsi saat memicu pembuatan cadangan.
type BackupOptions struct {
	Kind      string // 'otomatis', 'manual', 'sebelum_restore'
	CreatedBy *int64
	Note      string
	Dir       string
	Key       []byte
	KeyPrev   []byte
}

// RestoreOptions adalah opsi saat memicu pemulihan database.
type RestoreOptions struct {
	ForceNonEmpty   bool
	StoppedTheApp   bool
	IsWebRestore    bool
	ActorID         *int64
	EncryptionKey   []byte // Untuk uji dekripsi data tujuan penarikan
	SkipAppCheck    bool   // Untuk unit testing
}

// ComputeKeyID menghasilkan 8-karakter hex identitas kunci dari hash SHA-256.
func ComputeKeyID(key []byte) string {
	if len(key) == 0 {
		return ""
	}
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:4])
}
