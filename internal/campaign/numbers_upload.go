package campaign

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"goblast/internal/config"
	"goblast/internal/numbers"
	"goblast/internal/safe"
)

var previewIDRegex = regexp.MustCompile(`^[0-9a-f]{32}$`)

func IsValidPreviewID(id string) bool {
	return previewIDRegex.MatchString(id)
}

type VariableWarning struct {
	Variable   string `json:"variable"`
	EmptyCount int    `json:"empty_count"`
	TotalCount int    `json:"total_count"`
	Message    string `json:"message"`
}

type UploadStats struct {
	Total            int               `json:"total"`
	Accepted         int               `json:"accepted"`
	DupInFile        int               `json:"dup_in_file"`
	DupInCampaign    int               `json:"dup_in_campaign"`
	Invalid          int               `json:"invalid"`
	Ambiguous        int               `json:"ambiguous"`
	CountryCodes     map[string]int    `json:"country_codes"`
	VariableWarnings []VariableWarning `json:"variable_warnings,omitempty"`
}

type PreviewData struct {
	ID         string      `json:"id"`
	CampaignID int64       `json:"campaign_id"`
	Filename   string      `json:"filename"`
	Stats      UploadStats `json:"stats"`
	CreatedAt  time.Time   `json:"created_at"`
}

type RejectedRow struct {
	RawNumber string
	Reason    string // "dup_in_file", "dup_in_campaign", "invalid", "ambigu"
}

// ProcessPreview reads the raw uploaded data, normalizes it, checks DB for duplicates,
// generates a preview ID, and saves the rejected list to a CSV file.
func ProcessPreview(ctx context.Context, db *pgxpool.Pool, campaignID int64, rawData []byte, filename string, defaultCC string) (PreviewData, error) {
	var status, buttonMode, body string
	var archivedAt *time.Time
	err := db.QueryRow(ctx, "SELECT status, button_mode, body, archived_at FROM campaigns WHERE id = $1", campaignID).Scan(&status, &buttonMode, &body, &archivedAt)
	if err != nil {
		return PreviewData{}, fmt.Errorf("campaign tidak ditemukan: %w", err)
	}
	if archivedAt != nil {
		return PreviewData{}, errors.New("campaign terarsip tidak menerima unggahan nomor baru")
	}
	if status != "draf" && status != "berjalan" {
		return PreviewData{}, errors.New("nomor hanya bisa ditambahkan ke campaign draf atau berjalan")
	}

	// Read and parse
	rawNums, err := numbers.Read(bytes.NewReader(rawData), filename)
	if err != nil {
		return PreviewData{}, err
	}

	// Fetch existing numbers in this campaign
	existing := make(map[string]bool)
	rows, err := db.Query(ctx, "SELECT phone_e164 FROM campaign_numbers WHERE campaign_id = $1", campaignID)
	if err == nil {
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err == nil {
				existing[p] = true
			}
		}
		rows.Close()
	}

	stats := UploadStats{
		Total:        len(rawNums),
		CountryCodes: make(map[string]int),
	}
	seenInFile := make(map[string]bool)

	type ValidRow struct {
		Phone      string            `json:"phone"`
		CC         string            `json:"cc"`
		Name       string            `json:"name,omitempty"`
		CustomData map[string]string `json:"custom_data,omitempty"`
	}
	var validNumbers []ValidRow
	var rejected []RejectedRow

	for _, item := range rawNums {
		e164, status, ccStr := numbers.Normalize(item.Raw, defaultCC, item.IsNumericXLSX)

		if status == "tidak_valid" {
			stats.Invalid++
			rejected = append(rejected, RejectedRow{item.Raw, "tidak_valid"})
			continue
		}
		if status == "ambigu" {
			stats.Ambiguous++
			rejected = append(rejected, RejectedRow{item.Raw, "ambigu"})
			continue
		}

		// ok
		if seenInFile[e164] {
			stats.DupInFile++
			rejected = append(rejected, RejectedRow{item.Raw, "dup_in_file"})
			continue
		}
		seenInFile[e164] = true

		if existing[e164] {
			stats.DupInCampaign++
			rejected = append(rejected, RejectedRow{item.Raw, "dup_in_campaign"})
			continue
		}

		stats.Accepted++
		stats.CountryCodes[ccStr]++
		validNumbers = append(validNumbers, ValidRow{
			Phone:      e164,
			CC:         ccStr,
			Name:       item.Name,
			CustomData: item.CustomData,
		})
	}

	if buttonMode == ModeTombolAsli && config.MaxRecipientsNativeFlow > 0 {
		totalAfter := len(existing) + stats.Accepted
		if totalAfter > config.MaxRecipientsNativeFlow {
			return PreviewData{}, fmt.Errorf("mode tombol asli dibatasi maksimal %d penerima (saat ini: %d, setelah unggah: %d)", config.MaxRecipientsNativeFlow, len(existing), totalAfter)
		}
	}

	tmplVars := FindTemplateVariables(body)
	for _, v := range tmplVars {
		emptyCount := 0
		for _, row := range validNumbers {
			val := ""
			switch v {
			case "nama", "name":
				if row.Name != "" {
					val = row.Name
				} else if row.CustomData != nil {
					val = row.CustomData["nama"]
				}
			case "phone", "nomor":
				val = row.Phone
			default:
				if row.CustomData != nil {
					val = row.CustomData[v]
				}
			}
			if strings.TrimSpace(val) == "" {
				emptyCount++
			}
		}
		if emptyCount > 0 {
			stats.VariableWarnings = append(stats.VariableWarnings, VariableWarning{
				Variable:   "{" + v + "}",
				EmptyCount: emptyCount,
				TotalCount: len(validNumbers),
				Message:    fmt.Sprintf("Catatan: %d nomor tidak memiliki nilai untuk {%s}, akan memakai nilai default atau dikosongkan.", emptyCount, v),
			})
		}
	}

	// Generate ID
	b := make([]byte, 16)
	rand.Read(b)
	previewID := hex.EncodeToString(b)
	if !IsValidPreviewID(previewID) {
		return PreviewData{}, errors.New("gagal membuat preview ID yang valid")
	}

	preview := PreviewData{
		ID:         previewID,
		CampaignID: campaignID,
		Filename:   filename,
		Stats:      stats,
		CreatedAt:  time.Now(),
	}

	tmpDir := filepath.Join("data", "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return PreviewData{}, fmt.Errorf("gagal membuat direktori data/tmp: %w", err)
	}

	// Save valid numbers to json (0600)
	validBytes, err := json.Marshal(validNumbers)
	if err != nil {
		return PreviewData{}, err
	}
	if err := os.WriteFile(filepath.Join(tmpDir, previewID+"_valid.json"), validBytes, 0600); err != nil {
		return PreviewData{}, fmt.Errorf("gagal menulis file valid.json: %w", err)
	}

	// Save metadata (0600)
	metaBytes, err := json.Marshal(preview)
	if err != nil {
		return PreviewData{}, err
	}
	if err := os.WriteFile(filepath.Join(tmpDir, previewID+"_meta.json"), metaBytes, 0600); err != nil {
		return PreviewData{}, fmt.Errorf("gagal menulis file meta.json: %w", err)
	}

	// Save rejected as CSV (0600)
	if len(rejected) > 0 {
		f, err := os.OpenFile(filepath.Join(tmpDir, previewID+"_rejected.csv"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return PreviewData{}, fmt.Errorf("gagal membuat file rejected.csv: %w", err)
		}
		cw := csv.NewWriter(f)
		cw.Write([]string{"Nomor Asli", "Alasan Ditolak"})
		for _, r := range rejected {
			cw.Write([]string{r.RawNumber, r.Reason})
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			f.Close()
			return PreviewData{}, err
		}
		if err := f.Close(); err != nil {
			return PreviewData{}, err
		}
	}

	return preview, nil
}

// ConfirmUpload inserts the valid numbers into the database.
func ConfirmUpload(ctx context.Context, db *pgxpool.Pool, campaignID int64, userID int64, previewID string) error {
	if !IsValidPreviewID(previewID) {
		return errors.New("preview ID tidak valid")
	}

	metaPath := filepath.Join("data", "tmp", previewID+"_meta.json")
	validPath := filepath.Join("data", "tmp", previewID+"_valid.json")

	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("preview tidak ditemukan atau sudah kedaluwarsa")
	}
	var preview PreviewData
	if err := json.Unmarshal(metaBytes, &preview); err != nil {
		return fmt.Errorf("metadata preview tidak valid: %w", err)
	}
	if preview.CampaignID != campaignID {
		return errors.New("preview tidak cocok dengan campaign ini")
	}

	validBytes, err := os.ReadFile(validPath)
	if err != nil {
		return fmt.Errorf("data valid tidak ditemukan")
	}
	type ValidRow struct {
		Phone      string            `json:"phone"`
		CC         string            `json:"cc"`
		Name       string            `json:"name,omitempty"`
		CustomData map[string]string `json:"custom_data,omitempty"`
	}
	var validNumbers []ValidRow
	if err := json.Unmarshal(validBytes, &validNumbers); err != nil {
		return fmt.Errorf("data valid tidak valid: %w", err)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Operasi sah berukuran besar memakai SET LOCAL di transaksinya sendiri
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = 0"); err != nil {
		return fmt.Errorf("gagal mengatur timeout lokal: %w", err)
	}

	// Check campaign status and button_mode
	var status, buttonMode string
	var archivedAt *time.Time
	err = tx.QueryRow(ctx, "SELECT status, button_mode, archived_at FROM campaigns WHERE id = $1 FOR UPDATE", campaignID).Scan(&status, &buttonMode, &archivedAt)
	if err != nil {
		return fmt.Errorf("campaign tidak ditemukan")
	}
	if archivedAt != nil {
		return fmt.Errorf("campaign terarsip tidak menerima unggahan nomor baru")
	}
	if status != "draf" && status != "berjalan" {
		return fmt.Errorf("nomor hanya bisa ditambahkan ke campaign draf atau berjalan")
	}

	if buttonMode == ModeTombolAsli && config.MaxRecipientsNativeFlow > 0 {
		var currentCount int
		_ = tx.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1", campaignID).Scan(&currentCount)
		if currentCount+len(validNumbers) > config.MaxRecipientsNativeFlow {
			return fmt.Errorf("mode tombol asli dibatasi maksimal %d penerima (saat ini: %d, akan ditambah: %d)", config.MaxRecipientsNativeFlow, currentCount, len(validNumbers))
		}
	}

	// Insert number_batch
	var batchID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted, dup_in_file, dup_in_campaign, invalid, ambiguous)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, campaignID, preview.Filename, userID, preview.Stats.Total, preview.Stats.Accepted, preview.Stats.DupInFile, preview.Stats.DupInCampaign, preview.Stats.Invalid, preview.Stats.Ambiguous).Scan(&batchID)
	if err != nil {
		return fmt.Errorf("gagal mencatat batch: %w", err)
	}

	// Bulk insert campaign_numbers
	insertedCount := 0
	if len(validNumbers) > 0 {
		phones := make([]string, len(validNumbers))
		ccs := make([]string, len(validNumbers))
		names := make([]string, len(validNumbers))
		customs := make([]string, len(validNumbers))
		for i, v := range validNumbers {
			phones[i] = v.Phone
			ccs[i] = v.CC
			names[i] = v.Name
			if len(v.CustomData) > 0 {
				cb, _ := json.Marshal(v.CustomData)
				customs[i] = string(cb)
			} else {
				customs[i] = ""
			}
		}

		q := `
			INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, recipient_name, custom_data, status)
			SELECT $1, $2, t.phone, t.cc, NULLIF(t.name, ''), CASE WHEN t.custom = '' THEN NULL ELSE t.custom::jsonb END, 'tersedia'
			FROM (
				SELECT unnest($3::text[]) AS phone, unnest($4::text[]) AS cc, unnest($5::text[]) AS name, unnest($6::text[]) AS custom
			) t
			ON CONFLICT (campaign_id, phone_e164) DO NOTHING
		`

		res, err := tx.Exec(ctx, q, campaignID, batchID, phones, ccs, names, customs)
		if err != nil {
			return fmt.Errorf("gagal memasukkan nomor: %w", err)
		}
		insertedCount = int(res.RowsAffected())
	}

	// Audit log
	detailsJSON, _ := json.Marshal(map[string]interface{}{
		"filename":    preview.Filename,
		"total":       preview.Stats.Total,
		"inserted":    insertedCount,
		"dup_in_file": preview.Stats.DupInFile,
		"invalid":     preview.Stats.Invalid,
		"ambiguous":   preview.Stats.Ambiguous,
	})

	_, err = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta, ip)
		VALUES ($1, 'upload_numbers', 'campaign', $2, $3, '')
	`, userID, fmt.Sprintf("%d", campaignID), detailsJSON)
	if err != nil {
		return fmt.Errorf("gagal mencatat audit log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// Clean up temp files (meta, valid, and rejected CSV)
	_ = os.Remove(metaPath)
	_ = os.Remove(validPath)
	_ = os.Remove(filepath.Join("data", "tmp", previewID+"_rejected.csv"))

	return nil
}

// CancelUpload removes all temporary files associated with a preview ID (meta, valid, rejected).
func CancelUpload(previewID string) error {
	if !IsValidPreviewID(previewID) {
		return errors.New("preview ID tidak valid")
	}
	tmpDir := filepath.Join("data", "tmp")
	_ = os.Remove(filepath.Join(tmpDir, previewID+"_meta.json"))
	_ = os.Remove(filepath.Join(tmpDir, previewID+"_valid.json"))
	_ = os.Remove(filepath.Join(tmpDir, previewID+"_rejected.csv"))
	return nil
}

// CleanTmpFiles removes files in data/tmp older than maxAge.
func CleanTmpFiles(maxAge time.Duration) {
	tmpDir := filepath.Join("data", "tmp")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(tmpDir, entry.Name()))
		}
	}
}

// StartTmpCleaner starts a background goroutine that cleans data/tmp every hour with panic recovery.
func StartTmpCleaner(ctx context.Context) {
	CleanTmpFiles(config.TmpFilesMaxAge)
	safe.Loop(ctx, "campaign.tmp_cleaner", 1*time.Hour, func(c context.Context) {
		CleanTmpFiles(config.TmpFilesMaxAge)
	})
}
