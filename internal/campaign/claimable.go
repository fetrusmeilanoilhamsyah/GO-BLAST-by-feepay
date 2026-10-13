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

