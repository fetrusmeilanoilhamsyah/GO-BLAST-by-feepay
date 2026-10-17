package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"goblast/internal/backup"
	"goblast/internal/i18n"
)

type AdminBackupsData struct {
	Lang            string
	Title           string
	Theme           string
	CSRFToken       string
	Nonce           string
	ActiveNav       string
	Message         string
	Error           string
	Backups         []BackupItemView
	TotalCount      int
	TotalSizePretty string
	FreeDiskPretty  string
	AllowWebRestore bool
	T               func(string) string
}

type BackupItemView struct {
	ID               int64
	Kind             string
	Filename         string
	CreatedAt        time.Time
	CreatedBy        *int64
	SizeBytes        int64
	PrettySize       string
	SHA256           string
	SchemaVersion    int64
	RowCountsSummary string
	VerifiedAt       *time.Time
	VerifyOK         *bool
	IsVerified       bool
	IsFailed         bool
	SHAShort         string
	AgePretty        string
	Note             string
}

func (s *Server) logBackupAudit(actorID *int64, action, entityID string, meta any) {
	var metaStr string
	if meta != nil {
		if str, ok := meta.(string); ok {
			metaStr = str
		} else {
			if b, err := json.Marshal(meta); err == nil {
				metaStr = string(b)
			}
		}
	}
	s.logAudit(actorID, action, "backups", entityID, metaStr)
}

// HandleAdminBackupsGET menyajikan halaman daftar dan manajemen cadangan database.
func (s *Server) HandleAdminBackupsGET(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	lang := "id"
	if admin.Language != "" {
		lang = admin.Language
	}

	if s.BackupMgr == nil {
		data := &AdminBackupsData{
			Lang:            lang,
			Title:           "Cadangan Database - GO BLAST",
			Theme:           getTheme(r),
			CSRFToken:       getCSRF(r),
			Nonce:           getNonce(r),
			ActiveNav:       "backups",
			Error:           "Fitur cadangan database dinonaktifkan karena BACKUP_KEY belum dikonfigurasi di environment.",
			AllowWebRestore: s.AllowWebRestore,
			T: func(key string) string {
				return i18n.T(lang, key)
			},
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = s.Tmpl["admin_backups.html"].ExecuteTemplate(w, "base.html", data)
		return
	}

	records, err := s.BackupMgr.List(ctx)
	if err != nil {
		slog.Error("Gagal mengambil daftar cadangan", "err", err)
	}

	var totalBytes int64
	var views []BackupItemView
	now := time.Now().UTC()

	for _, rec := range records {
		totalBytes += rec.SizeBytes

		// Hitung ringkasan baris
		var summaryParts []string
		for k, v := range rec.RowCounts {
			if count, ok := v.(float64); ok && count > 0 {
				summaryParts = append(summaryParts, fmt.Sprintf("%s: %d", k, int64(count)))
			}
		}
		summaryStr := strings.Join(summaryParts, ", ")
		if len(summaryStr) > 60 {
			summaryStr = summaryStr[:57] + "..."
		}
		if summaryStr == "" {
			summaryStr = "-"
		}

		// Hitung umur
		age := now.Sub(rec.CreatedAt)
		ageStr := formatDurationShort(age)

		note := ""
		if rec.Note != nil {
			note = *rec.Note
		}

		isVerified := (rec.VerifyOK != nil && *rec.VerifyOK)
		isFailed := (rec.VerifyOK != nil && !*rec.VerifyOK)
		shaShort := rec.SHA256
		if len(shaShort) > 8 {
			shaShort = shaShort[:8]
		}

		views = append(views, BackupItemView{
			ID:               rec.ID,
			Kind:             rec.Kind,
			Filename:         rec.Filename,
			CreatedAt:        rec.CreatedAt,
			CreatedBy:        rec.CreatedBy,
			SizeBytes:        rec.SizeBytes,
			PrettySize:       FormatBytes(rec.SizeBytes),
			SHA256:           rec.SHA256,
			SchemaVersion:    rec.SchemaVersion,
			RowCountsSummary: summaryStr,
			VerifiedAt:       rec.VerifiedAt,
			VerifyOK:         rec.VerifyOK,
			IsVerified:       isVerified,
			IsFailed:         isFailed,
			SHAShort:         shaShort,
			AgePretty:        ageStr,
			Note:             note,
		})
	}

	freeDisk, _ := backup.GetFreeDiskSpace(s.BackupMgr.Dir())

	msg := r.URL.Query().Get("msg")
	errMsg := r.URL.Query().Get("err")

	data := &AdminBackupsData{
		Lang:            lang,
		Title:           "Cadangan Database - GO BLAST",
		Theme:           getTheme(r),
		CSRFToken:       getCSRF(r),
		Nonce:           getNonce(r),
		Backups:         views,
		TotalCount:      len(views),
		TotalSizePretty: FormatBytes(totalBytes),
		FreeDiskPretty:  FormatBytes(int64(freeDisk)),
		AllowWebRestore: s.AllowWebRestore,
		ActiveNav:       "backups",
		Message:         msg,
		Error:           errMsg,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.Tmpl["admin_backups.html"].ExecuteTemplate(w, "base.html", data)
}

// HandleAdminBackupCreatePOST memicu pembuatan cadangan database manual.
func (s *Server) HandleAdminBackupCreatePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.BackupMgr == nil {
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("BACKUP_KEY belum dikonfigurasi"), http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	note := strings.TrimSpace(r.FormValue("note"))
	if len(note) > 255 {
		note = note[:255]
	}

	rec, err := s.BackupMgr.CreateManual(ctx, admin.ID, note)
	if err != nil {
		slog.Error("Pembuatan cadangan manual gagal", "err", err)
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("Pembuatan cadangan gagal: "+err.Error()), http.StatusSeeOther)
		return
	}

	s.logBackupAudit(&admin.ID, "backup_create", rec.Filename, map[string]any{
		"size_bytes": rec.SizeBytes,
		"sha256":     rec.SHA256,
		"kind":       rec.Kind,
	})

	http.Redirect(w, r, "/admin/backups?msg="+url.QueryEscape("Cadangan database berhasil dibuat dan diverifikasi."), http.StatusSeeOther)
}

// HandleAdminBackupVerifyPOST memverifikasi keutuhan berkas cadangan secara manual.
func (s *Server) HandleAdminBackupVerifyPOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("ID cadangan tidak valid"), http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	header, vErr := s.BackupMgr.Verify(ctx, id)
	if vErr != nil {
		slog.Error("Verifikasi cadangan manual gagal", "id", id, "err", vErr)
		s.logBackupAudit(&admin.ID, "backup_verify", idStr, map[string]any{
			"status": "failed",
			"error":  vErr.Error(),
		})
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("Verifikasi cadangan gagal: "+vErr.Error()), http.StatusSeeOther)
		return
	}

	s.logBackupAudit(&admin.ID, "backup_verify", idStr, map[string]any{
		"status":     "ok",
		"total_rows": header.TotalRows,
	})

	http.Redirect(w, r, "/admin/backups?msg="+url.QueryEscape("Verifikasi berhasil: integritas potongan, checksum SHA-256, dan struktur data cocok."), http.StatusSeeOther)
}

// HandleAdminBackupDownloadGET mengalirkan unduhan berkas cadangan dengan perlindungan Step-Up TOTP.
func (s *Server) HandleAdminBackupDownloadGET(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "ID cadangan tidak valid", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	rec, err := s.BackupMgr.Get(ctx, id)
	if err != nil {
		http.Error(w, "Cadangan tidak ditemukan", http.StatusNotFound)
		return
	}

	// Cegah path traversal
	cleanFileName := filepath.Clean(rec.Filename)
	if strings.Contains(cleanFileName, "..") || strings.Contains(cleanFileName, "/") || strings.Contains(cleanFileName, "\\") {
		http.Error(w, "Nama berkas tidak aman", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(s.BackupMgr.Dir(), cleanFileName)
	file, err := os.Open(filePath)
	if err != nil {
		slog.Error("Gagal membuka berkas cadangan untuk unduhan", "file", filePath, "err", err)
		http.Error(w, "Berkas cadangan tidak ditemukan di disk server", http.StatusNotFound)
		return
	}
	defer file.Close()

	// Nama file acak saat diunduh
	var randBytes [4]byte
	_, _ = rand.Read(randBytes[:])
	randomName := fmt.Sprintf("goblast_backup_%s_%s.gbak", rec.CreatedAt.Format("20060102_150405"), hex.EncodeToString(randBytes[:]))

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, randomName))
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Content-Length", strconv.FormatInt(rec.SizeBytes, 10))

	s.logBackupAudit(&admin.ID, "backup_download", rec.Filename, map[string]any{
		"size_bytes": rec.SizeBytes,
	})

	_, _ = io.Copy(w, file)
}

// HandleAdminBackupDeletePOST menghapus cadangan sesuai aturan retensi dengan perlindungan Step-Up TOTP.
func (s *Server) HandleAdminBackupDeletePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("ID cadangan tidak valid"), http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	err = s.BackupMgr.Delete(ctx, id)
	if err != nil {
		slog.Error("Penghapusan cadangan gagal", "id", id, "err", err)
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}

	s.logBackupAudit(&admin.ID, "backup_delete", idStr, nil)
	http.Redirect(w, r, "/admin/backups?msg="+url.QueryEscape("Cadangan database berhasil dihapus."), http.StatusSeeOther)
}

// HandleAdminBackupRestorePOST memulihkan database dari antarmuka web jika diizinkan (ALLOW_WEB_RESTORE=true).
func (s *Server) HandleAdminBackupRestorePOST(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(r)
	if !ok || admin.Role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if !s.AllowWebRestore {
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("Pemulihan melalui antarmuka web dinonaktifkan demi keselamatan. Harap gunakan perintah CLI 'goblast restore'."), http.StatusSeeOther)
		return
	}

	ctx := r.Context()

	// 1. Wajib dalam mode pemeliharaan
	if !s.IsMaintenanceMode(ctx) {
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("Sistem harus berada dalam mode pemeliharaan sebelum memulihkan database!"), http.StatusSeeOther)
		return
	}

	// 2. Periksa kata konfirmasi
	confirmWord := strings.TrimSpace(r.FormValue("confirm_word"))
	if confirmWord != "RESTORE" && confirmWord != "PULIHKAN" {
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("Kata konfirmasi tidak cocok. Ketik 'RESTORE' atau 'PULIHKAN' untuk melanjutkan."), http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("ID cadangan tidak valid"), http.StatusSeeOther)
		return
	}

	// 3. Lakukan pengurasan (Drain) engine terlebih dahulu
	if s.Engine != nil {
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = s.Engine.Drain(drainCtx)
		drainCancel()
	}

	// 4. Buat cadangan otomatis berjenis 'sebelum_restore'
	preBackup, err := s.BackupMgr.CreateBeforeRestore(ctx, admin.ID)
	if err != nil {
		slog.Error("Pembuatan cadangan sebelum pemulihan gagal", "err", err)
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("Gagal membuat cadangan sebelum pemulihan: "+err.Error()), http.StatusSeeOther)
		return
	}

	// 5. Jalankan restore
	res, err := s.BackupMgr.Restore(ctx, id, backup.RestoreOptions{
		ForceNonEmpty: true,
		IsWebRestore:  true,
		ActorID:       &admin.ID,
		EncryptionKey: s.EncryptionKey,
		SkipAppCheck:  true,
	})
	if err != nil {
		slog.Error("Pemulihan database dari web gagal", "err", err)
		s.logBackupAudit(&admin.ID, "backup_restore", idStr, map[string]any{
			"status": "failed",
			"error":  err.Error(),
		})
		http.Redirect(w, r, "/admin/backups?err="+url.QueryEscape("Pemulihan database gagal: "+err.Error()), http.StatusSeeOther)
		return
	}

	s.logBackupAudit(&admin.ID, "backup_restore", idStr, map[string]any{
		"status":          "ok",
		"pre_backup_file": preBackup.Filename,
		"restored_tables": res.RestoredTables,
		"restored_rows":   res.RestoredRows,
	})

	http.Redirect(w, r, "/admin/backups?msg="+url.QueryEscape(fmt.Sprintf("Pemulihan basis data selesai dengan sukses (%d tabel, %d baris). Seluruh sesi WhatsApp telah diatur ke status 'terputus'.", res.RestoredTables, res.RestoredRows)), http.StatusSeeOther)
}

func formatDurationShort(d time.Duration) string {
	if d < time.Minute {
		return "baru saja"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d menit lalu", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d jam lalu", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%d hari lalu", days)
}
