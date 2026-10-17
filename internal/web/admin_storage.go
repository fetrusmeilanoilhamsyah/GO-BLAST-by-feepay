package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/i18n"
)

type TableSizeInfo struct {
	Name       string
	Bytes      int64
	PrettySize string
}

type FolderSizeInfo struct {
	Path       string
	Bytes      int64
	PrettySize string
	FileCount  int
}

type StorageCampaignItem struct {
	ID             int64
	Name           string
	Status         string
	CreatedAt      time.Time
	ArchivedAt     *time.Time
	PurgedAt       *time.Time
	AgeDays        int
	IsPurged       bool
	Tersedia       int
	Dikirim        int
	Terkirim       int
	Gagal          int
	TidakTerdaftar int
	Total          int
}

type CleanupPreview struct {
	FileCount  int
	TotalBytes int64
	PrettySize string
	FileNames  []string
}

type RetentionPreview struct {
	ExpiredSessions int64
	FailedAudits    int64
	ReadNotifs      int64
	TotalRows       int64
}

type VacuumTableStat struct {
	TableName      string
	LiveTuples     int64
	DeadTuples     int64
	LastVacuum     string
	LastAutoVacuum string
}

type StorageViewData struct {
	Lang              string
	Title             string
	Theme             string
	CSRFToken         string
	Nonce             string
	ActiveNav         string
	Message           string
	Error             string

	TableSizes        []TableSizeInfo
	TotalDBBytes      int64
	TotalDBPretty     string

	FolderSizes       []FolderSizeInfo

	ArchivedCampaigns []StorageCampaignItem
	FinishedCampaigns []StorageCampaignItem
	Campaigns         []StorageCampaignItem

	TmpPreview        CleanupPreview
	OrphansPreview    CleanupPreview
	RetentionPreview  RetentionPreview
	VacuumStats       []VacuumTableStat

	T                 func(string) string
}

func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func getDirStats(dirPath string) (int64, int, error) {
	var totalBytes int64
	var count int
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return 0, 0, nil
	}
	err := filepath.Walk(dirPath, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			totalBytes += info.Size()
			count++
		}
		return nil
	})
	return totalBytes, count, err
}

func getTableSizes(ctx context.Context, pool *pgxpool.Pool) ([]TableSizeInfo, int64, error) {
	tableNames := []string{"campaign_numbers", "ledger_entries", "audit_logs", "web_sessions"}
	rows, err := pool.Query(ctx, `
		SELECT c.relname, pg_total_relation_size(c.oid) AS total_bytes, pg_size_pretty(pg_total_relation_size(c.oid)) AS pretty_size
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname = ANY($1)
		ORDER BY total_bytes DESC
	`, tableNames)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var results []TableSizeInfo
	var totalBytes int64
	for rows.Next() {
		var t TableSizeInfo
		if err := rows.Scan(&t.Name, &t.Bytes, &t.PrettySize); err == nil {
			results = append(results, t)
			totalBytes += t.Bytes
		}
	}
	return results, totalBytes, nil
}

func getCampaignStorageList(ctx context.Context, pool *pgxpool.Pool) ([]StorageCampaignItem, []StorageCampaignItem, error) {
	stats, err := campaign.GetStorageCampaignStats(ctx, pool)
	if err != nil {
		return nil, nil, err
	}

	var archivedList, finishedList []StorageCampaignItem
	for _, s := range stats {
		item := StorageCampaignItem{
			ID:             s.ID,
			Name:           s.Name,
			Status:         s.Status,
			CreatedAt:      s.CreatedAt,
			ArchivedAt:     s.ArchivedAt,
			PurgedAt:       s.PurgedAt,
			IsPurged:       s.PurgedAt != nil,
			Tersedia:       s.Tersedia,
			Dikirim:        s.Dikirim,
			Terkirim:       s.Terkirim,
			Gagal:          s.Gagal,
			TidakTerdaftar: s.TidakTerdaftar,
			Total:          s.Tersedia + s.Dikirim + s.Terkirim + s.Gagal + s.TidakTerdaftar,
		}

		refTime := item.CreatedAt
		if item.ArchivedAt != nil {
			refTime = *item.ArchivedAt
		}
		item.AgeDays = int(time.Since(refTime).Hours() / 24)

		if item.ArchivedAt != nil {
			archivedList = append(archivedList, item)
		} else if item.Status == "selesai" {
			finishedList = append(finishedList, item)
		}
	}

	return archivedList, finishedList, nil
}

func previewCleanTmp(tmpDir string, maxAge time.Duration) (CleanupPreview, error) {
	var preview CleanupPreview
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return preview, nil
		}
		return preview, err
	}
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			preview.FileCount++
			preview.TotalBytes += info.Size()
			preview.FileNames = append(preview.FileNames, entry.Name())
		}
	}
	preview.PrettySize = FormatBytes(preview.TotalBytes)
	return preview, nil
}

func executeCleanTmp(tmpDir string, maxAge time.Duration) (int, int64, error) {
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	now := time.Now()
	var deletedCount int
	var deletedBytes int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			filePath := filepath.Join(tmpDir, entry.Name())
			if err := os.Remove(filePath); err == nil {
				deletedCount++
				deletedBytes += info.Size()
			}
		}
	}
	return deletedCount, deletedBytes, nil
}

func previewCleanOrphans(ctx context.Context, pool *pgxpool.Pool, uploadsDir string) (CleanupPreview, error) {
	var preview CleanupPreview
	rows, err := pool.Query(ctx, "SELECT DISTINCT image_path FROM campaigns WHERE image_path IS NOT NULL AND image_path <> ''")
	if err != nil {
		return preview, err
	}
	defer rows.Close()

	activeImages := make(map[string]bool)
	for rows.Next() {
		var imgPath string
		if err := rows.Scan(&imgPath); err == nil {
			activeImages[filepath.Base(imgPath)] = true
		}
	}

	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return preview, nil
		}
		return preview, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !activeImages[entry.Name()] {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			preview.FileCount++
			preview.TotalBytes += info.Size()
			preview.FileNames = append(preview.FileNames, entry.Name())
		}
	}
	preview.PrettySize = FormatBytes(preview.TotalBytes)
	return preview, nil
}

func executeCleanOrphans(ctx context.Context, pool *pgxpool.Pool, uploadsDir string) (int, int64, error) {
	rows, err := pool.Query(ctx, "SELECT DISTINCT image_path FROM campaigns WHERE image_path IS NOT NULL AND image_path <> ''")
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	activeImages := make(map[string]bool)
	for rows.Next() {
		var imgPath string
		if err := rows.Scan(&imgPath); err == nil {
			activeImages[filepath.Base(imgPath)] = true
		}
	}

	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	var deletedCount int
	var deletedBytes int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !activeImages[entry.Name()] {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			filePath := filepath.Join(uploadsDir, entry.Name())
			if err := os.Remove(filePath); err == nil {
				deletedCount++
				deletedBytes += info.Size()
			}
		}
	}
	return deletedCount, deletedBytes, nil
}

func previewCleanRetention(ctx context.Context, pool *pgxpool.Pool) (RetentionPreview, error) {
	var prev RetentionPreview
	_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM web_sessions WHERE expires_at < NOW()").Scan(&prev.ExpiredSessions)
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_logs
		WHERE created_at < NOW() - INTERVAL '30 days'
		  AND action IN ('login_failed', 'login_rejected', 'totp_login_failed', 'admin_stepup_failed')
	`).Scan(&prev.FailedAudits)
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM notifications
		WHERE read_at IS NOT NULL AND read_at < NOW() - INTERVAL '90 days'
	`).Scan(&prev.ReadNotifs)
	prev.TotalRows = prev.ExpiredSessions + prev.FailedAudits + prev.ReadNotifs
	return prev, nil
}

func executeCleanRetention(ctx context.Context, pool *pgxpool.Pool) (deletedSessions int64, deletedAudits int64, deletedNotifs int64, err error) {
	tag1, err1 := pool.Exec(ctx, "DELETE FROM web_sessions WHERE expires_at < NOW()")
	if err1 == nil {
		deletedSessions = tag1.RowsAffected()
	}
	tag2, err2 := pool.Exec(ctx, `
		DELETE FROM audit_logs
		WHERE created_at < NOW() - INTERVAL '30 days'
		  AND action IN ('login_failed', 'login_rejected', 'totp_login_failed', 'admin_stepup_failed')
	`)
	if err2 == nil {
		deletedAudits = tag2.RowsAffected()
	}
	tag3, err3 := pool.Exec(ctx, `
		DELETE FROM notifications
		WHERE read_at IS NOT NULL AND read_at < NOW() - INTERVAL '90 days'
	`)
	if err3 == nil {
		deletedNotifs = tag3.RowsAffected()
	}
	if err1 != nil {
		return deletedSessions, deletedAudits, deletedNotifs, err1
	}
	if err2 != nil {
		return deletedSessions, deletedAudits, deletedNotifs, err2
	}
	return deletedSessions, deletedAudits, deletedNotifs, err3
}

func previewVacuum(ctx context.Context, pool *pgxpool.Pool) ([]VacuumTableStat, error) {
	tables := []string{"campaign_numbers", "ledger_entries", "audit_logs", "web_sessions"}
	rows, err := pool.Query(ctx, `
		SELECT relname, n_live_tup, n_dead_tup,
		       COALESCE(to_char(last_vacuum, 'YYYY-MM-DD HH24:MI:SS'), '-'),
		       COALESCE(to_char(last_autovacuum, 'YYYY-MM-DD HH24:MI:SS'), '-')
		FROM pg_stat_user_tables
		WHERE relname = ANY($1)
		ORDER BY n_dead_tup DESC
	`, tables)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []VacuumTableStat
	for rows.Next() {
		var s VacuumTableStat
		if err := rows.Scan(&s.TableName, &s.LiveTuples, &s.DeadTuples, &s.LastVacuum, &s.LastAutoVacuum); err == nil {
			stats = append(stats, s)
		}
	}
	return stats, nil
}

func executeVacuum(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	_, _ = conn.Exec(ctx, "SET lock_timeout = '5s'")
	if _, err := conn.Exec(ctx, "VACUUM ANALYZE campaign_numbers"); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "VACUUM ANALYZE ledger_entries"); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "VACUUM ANALYZE audit_logs"); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "VACUUM ANALYZE web_sessions"); err != nil {
		return err
	}
	return nil
}

// HandleAdminStorageGET menampilkan halaman penyimpanan data (/admin/storage).
func (s *Server) HandleAdminStorageGET(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	lang := "id"
	if user.Language != "" {
		lang = user.Language
	}

	ctx := r.Context()

	// 1. Ukuran Tabel Database Utama
	tblSizes, totalDBBytes, _ := getTableSizes(ctx, s.DB.Pool)

	// 2. Ukuran Folder Sistem
	uploadsPath := "uploads"
	tmpPath := "data/tmp"
	backupsPath := "backups"

	upBytes, upCount, _ := getDirStats(uploadsPath)
	tmpBytes, tmpCount, _ := getDirStats(tmpPath)
	bkBytes, bkCount, _ := getDirStats(backupsPath)

	folders := []FolderSizeInfo{
		{Path: "uploads/", Bytes: upBytes, PrettySize: FormatBytes(upBytes), FileCount: upCount},
		{Path: "data/tmp/", Bytes: tmpBytes, PrettySize: FormatBytes(tmpBytes), FileCount: tmpCount},
		{Path: "backups/", Bytes: bkBytes, PrettySize: FormatBytes(bkBytes), FileCount: bkCount},
	}

	// 3. Campaign Terarsip & Selesai
	archivedCamps, finishedCamps, _ := getCampaignStorageList(ctx, s.DB.Pool)

	// 4. Mode Pratinjau Pembersihan
	tmpPrev, _ := previewCleanTmp(tmpPath, config.TmpFilesMaxAge)
	orphansPrev, _ := previewCleanOrphans(ctx, s.DB.Pool, uploadsPath)
	retPrev, _ := previewCleanRetention(ctx, s.DB.Pool)
	vacStats, _ := previewVacuum(ctx, s.DB.Pool)

	var allCamps []StorageCampaignItem
	allCamps = append(allCamps, archivedCamps...)
	allCamps = append(allCamps, finishedCamps...)

	viewData := StorageViewData{
		Lang:              lang,
		Title:             "Penyimpanan Data - GO BLAST",
		Theme:             getTheme(r),
		CSRFToken:         getCSRF(r),
		Nonce:             getNonce(r),
		ActiveNav:         "storage",
		Message:           r.URL.Query().Get("msg"),
		Error:             r.URL.Query().Get("err"),
		TableSizes:        tblSizes,
		TotalDBBytes:      totalDBBytes,
		TotalDBPretty:     FormatBytes(totalDBBytes),
		FolderSizes:       folders,
		ArchivedCampaigns: archivedCamps,
		FinishedCampaigns: finishedCamps,
		Campaigns:         allCamps,
		TmpPreview:        tmpPrev,
		OrphansPreview:    orphansPrev,
		RetentionPreview:  retPrev,
		VacuumStats:       vacStats,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["admin_storage.html"].ExecuteTemplate(w, "base.html", viewData)
}

// HandleAdminStorageCleanTmpPOST membersihkan file sementara di data/tmp > 1 jam.
func (s *Server) HandleAdminStorageCleanTmpPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	lang := "id"
	if user.Language != "" {
		lang = user.Language
	}

	userIDStr := strconv.FormatInt(user.ID, 10)
	if s.AdminStorageCleanLimiter != nil {
		if allowed, wait := s.AdminStorageCleanLimiter.Take(userIDStr); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			errMsg := i18n.T(lang, "storage.err_rate_limit")
			http.Redirect(w, r, fmt.Sprintf("/admin/storage?err=%s", url.QueryEscape(errMsg)), http.StatusSeeOther)
			return
		}
	}

	delCount, delBytes, err := executeCleanTmp("data/tmp", config.TmpFilesMaxAge)
	if err != nil {
		slog.Error("Gagal membersihkan file sementara", "err", err)
		http.Redirect(w, r, "/admin/storage?err=Gagal+membersihkan+file+sementara", http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "storage_clean_tmp", "filesystem", "data/tmp", fmt.Sprintf("deleted %d files, %d bytes", delCount, delBytes))
	msg := fmt.Sprintf("Berhasil membersihkan %d file sementara (%s).", delCount, FormatBytes(delBytes))
	http.Redirect(w, r, fmt.Sprintf("/admin/storage?msg=%s", url.QueryEscape(msg)), http.StatusSeeOther)
}

// HandleAdminStorageCleanOrphansPOST membersihkan file yatim di uploads/.
func (s *Server) HandleAdminStorageCleanOrphansPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	lang := "id"
	if user.Language != "" {
		lang = user.Language
	}

	userIDStr := strconv.FormatInt(user.ID, 10)
	if s.AdminStorageCleanLimiter != nil {
		if allowed, wait := s.AdminStorageCleanLimiter.Take(userIDStr); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			errMsg := i18n.T(lang, "storage.err_rate_limit")
			http.Redirect(w, r, fmt.Sprintf("/admin/storage?err=%s", url.QueryEscape(errMsg)), http.StatusSeeOther)
			return
		}
	}

	delCount, delBytes, err := executeCleanOrphans(r.Context(), s.DB.Pool, "uploads")
	if err != nil {
		slog.Error("Gagal membersihkan file yatim uploads", "err", err)
		http.Redirect(w, r, "/admin/storage?err=Gagal+membersihkan+file+yatim", http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "storage_clean_orphans", "filesystem", "uploads", fmt.Sprintf("deleted %d orphan files, %d bytes", delCount, delBytes))
	msg := fmt.Sprintf("Berhasil membersihkan %d file yatim (%s).", delCount, FormatBytes(delBytes))
	http.Redirect(w, r, fmt.Sprintf("/admin/storage?msg=%s", url.QueryEscape(msg)), http.StatusSeeOther)
}

// HandleAdminStorageCleanRetentionPOST membersihkan data retensi sistem.
func (s *Server) HandleAdminStorageCleanRetentionPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	lang := "id"
	if user.Language != "" {
		lang = user.Language
	}

	userIDStr := strconv.FormatInt(user.ID, 10)
	if s.AdminStorageCleanLimiter != nil {
		if allowed, wait := s.AdminStorageCleanLimiter.Take(userIDStr); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			errMsg := i18n.T(lang, "storage.err_rate_limit")
			http.Redirect(w, r, fmt.Sprintf("/admin/storage?err=%s", url.QueryEscape(errMsg)), http.StatusSeeOther)
			return
		}
	}

	delSessions, delAudits, delNotifs, err := executeCleanRetention(r.Context(), s.DB.Pool)
	if err != nil {
		slog.Error("Gagal membersihkan retensi sistem", "err", err)
		http.Redirect(w, r, "/admin/storage?err=Gagal+membersihkan+data+retensi", http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "storage_clean_retention", "database", "retention", fmt.Sprintf("sessions: %d, audits: %d, notifs: %d", delSessions, delAudits, delNotifs))
	msg := fmt.Sprintf("Berhasil menghapus %d sesi kedaluwarsa, %d riwayat audit gagal lama, dan %d notifikasi lama.", delSessions, delAudits, delNotifs)
	http.Redirect(w, r, fmt.Sprintf("/admin/storage?msg=%s", url.QueryEscape(msg)), http.StatusSeeOther)
}

// HandleAdminStorageVacuumPOST memicu VACUUM ANALYZE pada tabel utama.
func (s *Server) HandleAdminStorageVacuumPOST(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	lang := "id"
	if user.Language != "" {
		lang = user.Language
	}

	userIDStr := strconv.FormatInt(user.ID, 10)
	if s.AdminStorageCleanLimiter != nil {
		if allowed, wait := s.AdminStorageCleanLimiter.Take(userIDStr); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			errMsg := i18n.T(lang, "storage.err_rate_limit")
			http.Redirect(w, r, fmt.Sprintf("/admin/storage?err=%s", url.QueryEscape(errMsg)), http.StatusSeeOther)
			return
		}
	}

	if err := executeVacuum(r.Context(), s.DB.Pool); err != nil {
		slog.Error("Gagal menjalankan VACUUM ANALYZE", "err", err)
		http.Redirect(w, r, "/admin/storage?err=Gagal+menjalankan+optimasi+database", http.StatusSeeOther)
		return
	}

	s.logAudit(&user.ID, "storage_vacuum", "database", "vacuum_analyze", "campaign_numbers, ledger_entries, audit_logs, web_sessions")
	msg := "Optimasi database (VACUUM ANALYZE) berhasil diselesaikan untuk semua tabel utama."
	http.Redirect(w, r, fmt.Sprintf("/admin/storage?msg=%s", url.QueryEscape(msg)), http.StatusSeeOther)
}
