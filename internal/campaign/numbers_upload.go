package campaign

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"goblast/internal/numbers"
)

type UploadStats struct {
	Total         int
	Accepted      int
	DupInFile     int
	DupInCampaign int
	Invalid       int
	Ambiguous     int
}

type PreviewData struct {
	ID        string      `json:"id"`
	Filename  string      `json:"filename"`
	Stats     UploadStats `json:"stats"`
	CreatedAt time.Time   `json:"created_at"`
}

type RejectedRow struct {
	RawNumber string
	Reason    string // "dup_in_file", "dup_in_campaign", "invalid", "ambigu"
}

// ProcessPreview reads the raw uploaded data, normalizes it, checks DB for duplicates,
// generates a preview ID, and saves the rejected list to a CSV file.
func ProcessPreview(ctx context.Context, db *pgxpool.Pool, campaignID int64, rawData []byte, filename string, defaultCC string) (PreviewData, error) {
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

	stats := UploadStats{Total: len(rawNums)}
	seenInFile := make(map[string]bool)

	type ValidRow struct {
		Phone string `json:"phone"`
		CC    string `json:"cc"`
	}
	var validNumbers []ValidRow
	var rejected []RejectedRow

	for _, raw := range rawNums {
		e164, status, ccStr := numbers.Normalize(raw, defaultCC)
		
		if status == "tidak_valid" {
			stats.Invalid++
			rejected = append(rejected, RejectedRow{raw, "tidak_valid"})
			continue
		}
		if status == "ambigu" {
			stats.Ambiguous++
			rejected = append(rejected, RejectedRow{raw, "ambigu"})
			continue
		}
		
		// ok
		if seenInFile[e164] {
			stats.DupInFile++
			rejected = append(rejected, RejectedRow{raw, "dup_in_file"})
			continue
		}
		seenInFile[e164] = true

		if existing[e164] {
			stats.DupInCampaign++
			rejected = append(rejected, RejectedRow{raw, "dup_in_campaign"})
			continue
		}

		stats.Accepted++
		validNumbers = append(validNumbers, ValidRow{Phone: e164, CC: ccStr})
	}

	// Generate ID
	b := make([]byte, 16)
	rand.Read(b)
	previewID := hex.EncodeToString(b)

	preview := PreviewData{
		ID:        previewID,
		Filename:  filename,
		Stats:     stats,
		CreatedAt: time.Now(),
	}

	// Save valid numbers to json
	validBytes, _ := json.Marshal(validNumbers)
	os.WriteFile(filepath.Join("data", "tmp", previewID+"_valid.json"), validBytes, 0644)

	// Save metadata
	metaBytes, _ := json.Marshal(preview)
	os.WriteFile(filepath.Join("data", "tmp", previewID+"_meta.json"), metaBytes, 0644)

	// Save rejected as CSV
	if len(rejected) > 0 {
		f, err := os.Create(filepath.Join("data", "tmp", previewID+"_rejected.csv"))
		if err == nil {
			cw := csv.NewWriter(f)
			cw.Write([]string{"Nomor Asli", "Alasan Ditolak"})
			for _, r := range rejected {
				cw.Write([]string{r.RawNumber, r.Reason})
			}
			cw.Flush()
			f.Close()
		}
	}

	return preview, nil
}

// ConfirmUpload inserts the valid numbers into the database.
func ConfirmUpload(ctx context.Context, db *pgxpool.Pool, campaignID int64, userID int64, previewID string) error {
	metaPath := filepath.Join("data", "tmp", previewID+"_meta.json")
	validPath := filepath.Join("data", "tmp", previewID+"_valid.json")

	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("preview tidak ditemukan atau sudah kedaluwarsa")
	}
	var preview PreviewData
	json.Unmarshal(metaBytes, &preview)

	validBytes, err := os.ReadFile(validPath)
	if err != nil {
		return fmt.Errorf("data valid tidak ditemukan")
	}
	type ValidRow struct {
		Phone string `json:"phone"`
		CC    string `json:"cc"`
	}
	var validNumbers []ValidRow
	json.Unmarshal(validBytes, &validNumbers)

	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Check campaign status
	var status string
	err = tx.QueryRow(ctx, "SELECT status FROM campaigns WHERE id = $1 FOR UPDATE", campaignID).Scan(&status)
	if err != nil {
		return fmt.Errorf("campaign tidak ditemukan")
	}
	if status != "draf" && status != "berjalan" {
		return fmt.Errorf("nomor hanya bisa ditambahkan ke campaign draf atau berjalan")
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
		// Use copy from for faster insert, or unnest
		// "ON CONFLICT DO NOTHING" requires INSERT ... ON CONFLICT
		
		phones := make([]string, len(validNumbers))
		ccs := make([]string, len(validNumbers))
		for i, v := range validNumbers {
			phones[i] = v.Phone
			ccs[i] = v.CC
		}
		
		q := `
			INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status)
			SELECT $1, $2, t.phone, t.cc, 'tersedia'
			FROM (
				SELECT unnest($3::text[]) AS phone, unnest($4::text[]) AS cc
			) t
			ON CONFLICT (campaign_id, phone_e164) DO NOTHING
		`
		
		res, err := tx.Exec(ctx, q, campaignID, batchID, phones, ccs)
		if err != nil {
			return fmt.Errorf("gagal memasukkan nomor: %w", err)
		}
		insertedCount = int(res.RowsAffected())
	}
	
	// Audit log
	// "siapa mengunggah, kapan, nama file, ringkasan hasil."
	detailsJSON, _ := json.Marshal(map[string]interface{}{
		"filename": preview.Filename,
		"total": preview.Stats.Total,
		"inserted": insertedCount,
		"dup_in_file": preview.Stats.DupInFile,
		"invalid": preview.Stats.Invalid,
		"ambiguous": preview.Stats.Ambiguous,
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
	
	// Clean up temp files
	os.Remove(metaPath)
	os.Remove(validPath)
	// We leave rejected.csv there for downloading, maybe cleaned up by a cron later

	return nil
}
