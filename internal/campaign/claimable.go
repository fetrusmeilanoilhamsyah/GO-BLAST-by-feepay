package campaign

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ClaimableWhere adalah kondisi WHERE SQL untuk nomor kampanye yang siap diklaim dan dikirim.
const ClaimableWhere = "cn.status = 'tersedia' AND c.status = 'berjalan' AND c.archived_at IS NULL"

// TertahanWhere adalah kondisi WHERE SQL untuk nomor berstatus tersedia milik kampanye yang tidak berjalan atau diarsipkan.
const TertahanWhere = "cn.status = 'tersedia' AND (c.status != 'berjalan' OR c.archived_at IS NOT NULL)"

// Queryer adalah antarmuka minimal untuk menjalankan query baris tunggal (pgxpool.Pool, pgx.Tx, *pgx.Conn).
type Queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ClaimableCount menghitung jumlah nomor kampanye yang berstatus tersedia pada kampanye yang sedang berjalan dan belum diarsipkan.
func ClaimableCount(ctx context.Context, q Queryer) (int, error) {
	query := "SELECT COUNT(*) FROM campaign_numbers cn JOIN campaigns c ON c.id = cn.campaign_id WHERE " + ClaimableWhere
	var count int
	err := q.QueryRow(ctx, query).Scan(&count)
	return count, err
}

// TertahanCount menghitung jumlah nomor berstatus tersedia milik kampanye yang tidak berjalan atau diarsipkan.
func TertahanCount(ctx context.Context, q Queryer) (int, error) {
	query := "SELECT COUNT(*) FROM campaign_numbers cn JOIN campaigns c ON c.id = cn.campaign_id WHERE " + TertahanWhere
	var count int
	err := q.QueryRow(ctx, query).Scan(&count)
	return count, err
}

// CampaignAvailableCount menghitung jumlah nomor berstatus tersedia untuk satu kampanye tertentu.
func CampaignAvailableCount(ctx context.Context, q Queryer, campaignID int64) (int, error) {
	query := "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'tersedia'"
	var count int
	err := q.QueryRow(ctx, query, campaignID).Scan(&count)
	return count, err
}

// CampaignStatRow merepresentasikan ringkasan jumlah nomor per status untuk sebuah kampanye.
type CampaignStatRow struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	Tersedia       int    `json:"tersedia"`
	Dikirim        int    `json:"dikirim"`
	Terkirim       int    `json:"terkirim"`
	Gagal          int    `json:"gagal"`
	TidakTerdaftar int    `json:"tidak_terdaftar"`
}

type RowsQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// GetCampaignStats mengambil ringkasan statistik per kampanye untuk laporan dashboard.
func GetCampaignStats(ctx context.Context, q RowsQueryer) ([]CampaignStatRow, error) {
	cRows, err := q.Query(ctx, `
		SELECT c.id, c.name, c.status,
		       COUNT(CASE WHEN cn.status = 'tersedia' THEN 1 END) AS count_tersedia,
		       COUNT(CASE WHEN cn.status = 'dikirim' THEN 1 END) AS count_dikirim,
		       COUNT(CASE WHEN cn.status = 'terkirim' THEN 1 END) AS count_terkirim,
		       COUNT(CASE WHEN cn.status = 'gagal' THEN 1 END) AS count_gagal,
		       COUNT(CASE WHEN cn.status = 'tidak_terdaftar' THEN 1 END) AS count_tidak_terdaftar
		FROM campaigns c
		LEFT JOIN campaign_numbers cn ON cn.campaign_id = c.id
		GROUP BY c.id, c.name, c.status
		ORDER BY c.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer cRows.Close()

	var campaigns []CampaignStatRow
	for cRows.Next() {
		var c CampaignStatRow
		if err := cRows.Scan(&c.ID, &c.Name, &c.Status, &c.Tersedia, &c.Dikirim, &c.Terkirim, &c.Gagal, &c.TidakTerdaftar); err == nil {
			campaigns = append(campaigns, c)
		}
	}
	return campaigns, cRows.Err()
}

// HasAvailableVariableData memeriksa apakah ada nomor berstatus tersedia yang memiliki nilai untuk variabel tertentu.
func HasAvailableVariableData(ctx context.Context, q Queryer, campaignID int64, normKey string) (bool, error) {
	var count int
	var query string
	if normKey == "nama" || normKey == "name" {
		query = "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'tersedia' AND (recipient_name IS NOT NULL AND recipient_name != '')"
		err := q.QueryRow(ctx, query, campaignID).Scan(&count)
		return count > 0, err
	}
	query = "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'tersedia' AND (custom_data ? $2)"
	err := q.QueryRow(ctx, query, campaignID, normKey).Scan(&count)
	return count > 0, err
}

// GetAvailableCustomKeys mengambil seluruh kunci custom_data yang ada pada nomor tersedia sebuah kampanye.
func GetAvailableCustomKeys(ctx context.Context, q RowsQueryer, campaignID int64) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT jsonb_object_keys(custom_data) 
		FROM campaign_numbers 
		WHERE campaign_id = $1 AND status = 'tersedia' AND custom_data IS NOT NULL
	`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var customKeys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil && k != "" {
			customKeys = append(customKeys, k)
		}
	}
	return customKeys, rows.Err()
}

// QueryAvailableExportNumbers mengambil kursor nomor berstatus tersedia untuk ekspor.
func QueryAvailableExportNumbers(ctx context.Context, q RowsQueryer, campaignID int64) (pgx.Rows, error) {
	return q.Query(ctx, `
		SELECT phone_e164, COALESCE(recipient_name, ''), COALESCE(custom_data, '{}'::jsonb) 
		FROM campaign_numbers 
		WHERE campaign_id = $1 AND status = 'tersedia' 
		ORDER BY id ASC
	`, campaignID)
}

