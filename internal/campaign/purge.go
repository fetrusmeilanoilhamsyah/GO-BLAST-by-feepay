package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrCampaignRunning = errors.New("campaign sedang berjalan tidak dapat dibersihkan")
	ErrCampaignHasInFlight = errors.New("campaign masih memiliki nomor berstatus dikirim")
)

type PurgeResult struct {
	CampaignID          int64          `json:"campaign_id"`
	PurgedAt            time.Time      `json:"purged_at"`
	AlreadyPurged       bool           `json:"already_purged"`
	TersediaDeleted     int            `json:"deleted_tersedia"`
	TerkirimAnon        int            `json:"anonymized_terkirim"`
	GagalAnon           int            `json:"anonymized_gagal"`
	TidakTerdaftarAnon  int            `json:"anonymized_tidak_terdaftar"`
	ImageDeleted        bool           `json:"image_deleted"`
	Summary             map[string]int `json:"summary"`
}

// Purge executes the purge and anonymization process for a campaign in a single atomic transaction:
// 1. Enforces that campaign is not running and has no 'dikirim' numbers.
// 2. Records counts per status in campaigns.purge_summary and fills purged_at.
// 3. Deletes rows with status = 'tersedia'.
// 4. Anonymizes rows with status = 'terkirim' and 'tidak_terdaftar' (phone_e164 = 'x' || id, country_code = 'XX', recipient_name = NULL, custom_data = NULL, last_error = NULL).
// 5. Anonymizes rows with status = 'gagal' (phone_e164 = 'x' || id, country_code = 'XX', recipient_name = NULL, custom_data = NULL, last_error sanitized to known category or 'lainnya').
// 6. Clears number_batches.filename = '' while preserving counts.
// 7. If deleteImage is true, deletes image file from uploads/ and sets campaigns.image_path = NULL.
func Purge(ctx context.Context, pool *pgxpool.Pool, campaignID int64, deleteImage bool) (*PurgeResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("gagal memulai transaksi purge: %w", err)
	}
	defer tx.Rollback(ctx)

	var status string
	var imagePath *string
	var existingPurgedAt *time.Time
	err = tx.QueryRow(ctx, "SELECT status, image_path, purged_at FROM campaigns WHERE id = $1 FOR UPDATE", campaignID).Scan(&status, &imagePath, &existingPurgedAt)
	if err != nil {
		return nil, fmt.Errorf("campaign tidak ditemukan: %w", err)
	}

	if existingPurgedAt != nil {
		return &PurgeResult{
			CampaignID:    campaignID,
			PurgedAt:      *existingPurgedAt,
			AlreadyPurged: true,
		}, nil
	}

	if status == StatusBerjalan {
		return nil, ErrCampaignRunning
	}

	var inFlightCount int
	err = tx.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'dikirim'", campaignID).Scan(&inFlightCount)
	if err != nil {
		return nil, fmt.Errorf("gagal memeriksa nomor dikirim: %w", err)
	}
	if inFlightCount > 0 {
		return nil, ErrCampaignHasInFlight
	}

	// 1. Hitung jumlah nomor per status sebelum dibersihkan
	counts := map[string]int{
		"tersedia":        0,
		"terkirim":        0,
		"gagal":           0,
		"tidak_terdaftar": 0,
	}

	rows, err := tx.Query(ctx, "SELECT status, COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 GROUP BY status", campaignID)
	if err != nil {
		return nil, fmt.Errorf("gagal menghitung status nomor: %w", err)
	}
	for rows.Next() {
		var st string
		var cnt int
		if err := rows.Scan(&st, &cnt); err == nil {
			counts[st] = cnt
		}
	}
	rows.Close()

	// 2. Hapus HANYA baris 'tersedia' lewat claimable.go
	resDelCount, err := DeleteAvailableCampaignNumbers(ctx, tx, campaignID)
	if err != nil {
		return nil, fmt.Errorf("gagal menghapus nomor tersedia: %w", err)
	}
	deletedTersedia := int(resDelCount)

	// 3. Anonimkan baris 'terkirim'
	resAnonSent, err := tx.Exec(ctx, `
		UPDATE campaign_numbers
		SET phone_e164 = 'x' || id::text,
		    country_code = 'XX',
		    recipient_name = NULL,
		    custom_data = NULL,
		    last_error = NULL
		WHERE campaign_id = $1 AND status = 'terkirim'
	`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("gagal menganonimkan nomor terkirim: %w", err)
	}

	// 4. Anonimkan baris 'tidak_terdaftar'
	resAnonUnregistered, err := tx.Exec(ctx, `
		UPDATE campaign_numbers
		SET phone_e164 = 'x' || id::text,
		    country_code = 'XX',
		    recipient_name = NULL,
		    custom_data = NULL,
		    last_error = NULL
		WHERE campaign_id = $1 AND status = 'tidak_terdaftar'
	`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("gagal menganonimkan nomor tidak_terdaftar: %w", err)
	}

	// 5. Anonimkan baris 'gagal' dengan sanitasi last_error
	// Hanya pertahankan kategori yang dikenal, selain itu ganti 'lainnya' agar bebas digit telepon
	resAnonGagal, err := tx.Exec(ctx, `
		UPDATE campaign_numbers
		SET phone_e164 = 'x' || id::text,
		    country_code = 'XX',
		    recipient_name = NULL,
		    custom_data = NULL,
		    last_error = CASE 
		        WHEN last_error IN ('hasil_tidak_pasti', 'terkirim_tidak_tercatat', 'galat_internal') THEN last_error
		        ELSE 'lainnya'
		    END
		WHERE campaign_id = $1 AND status = 'gagal'
	`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("gagal menganonimkan nomor gagal: %w", err)
	}

	// 6. Kosongkan number_batches.filename
	_, err = tx.Exec(ctx, "UPDATE number_batches SET filename = '' WHERE campaign_id = $1", campaignID)
	if err != nil {
		return nil, fmt.Errorf("gagal mengosongkan filename batch: %w", err)
	}

	// 7. Simpan summary JSONB dan purged_at
	now := time.Now()
	summaryData := map[string]int{
		"tersedia":        counts["tersedia"],
		"terkirim":        counts["terkirim"],
		"gagal":           counts["gagal"],
		"tidak_terdaftar": counts["tidak_terdaftar"],
	}
	summaryJSON, err := json.Marshal(summaryData)
	if err != nil {
		return nil, fmt.Errorf("gagal encode purge summary: %w", err)
	}

	imageDeleted := false
	if deleteImage && imagePath != nil && *imagePath != "" {
		_ = os.Remove(*imagePath)
		imageDeleted = true
		_, err = tx.Exec(ctx, "UPDATE campaigns SET purged_at = $1, purge_summary = $2, image_path = NULL WHERE id = $3", now, summaryJSON, campaignID)
	} else {
		_, err = tx.Exec(ctx, "UPDATE campaigns SET purged_at = $1, purge_summary = $2 WHERE id = $3", now, summaryJSON, campaignID)
	}
	if err != nil {
		return nil, fmt.Errorf("gagal memperbarui campaigns purged_at: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("gagal commit transaksi purge: %w", err)
	}

	return &PurgeResult{
		CampaignID:         campaignID,
		PurgedAt:           now,
		TersediaDeleted:    deletedTersedia,
		TerkirimAnon:       int(resAnonSent.RowsAffected()),
		GagalAnon:          int(resAnonGagal.RowsAffected()),
		TidakTerdaftarAnon: int(resAnonUnregistered.RowsAffected()),
		ImageDeleted:       imageDeleted,
		Summary:            summaryData,
	}, nil
}
