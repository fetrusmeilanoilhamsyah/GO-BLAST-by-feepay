package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/i18n"
)

// HandleAdminCampaignNumbersGET shows the upload form
func (s *Server) HandleAdminCampaignNumbersGET(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		http.Error(w, "ID required", http.StatusBadRequest)
		return
	}
	campaignID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID format", http.StatusBadRequest)
		return
	}

	var c campaign.Campaign
	var archivedAt *time.Time
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT id, name, status, archived_at FROM campaigns WHERE id = $1", campaignID).Scan(&c.ID, &c.Name, &c.Status, &archivedAt)
	if err != nil {
		http.Error(w, "Campaign not found", http.StatusNotFound)
		return
	}
	if archivedAt != nil {
		http.Error(w, "Campaign terarsip tidak menerima unggahan nomor baru", http.StatusBadRequest)
		return
	}
	if c.Status != "draf" && c.Status != "berjalan" {
		http.Error(w, "Nomor hanya bisa ditambahkan ke campaign draf atau berjalan", http.StatusBadRequest)
		return
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	data := struct {
		ActiveNav string
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		Campaign  campaign.Campaign
		T         func(string) string
	}{
		ActiveNav: "campaigns",
		Lang:      lang,
		Title:     i18n.T(lang, "numbers.upload_title"),
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		Campaign:  c,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if tmpl, ok := s.Tmpl["admin_campaign_numbers.html"]; ok {
		tmpl.ExecuteTemplate(w, "base.html", data)
	} else {
		http.Error(w, "Template not found", http.StatusInternalServerError)
	}
}

// HandleAdminCampaignNumbersPreview POST reads the file and shows preview
func (s *Server) HandleAdminCampaignNumbersPreview(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	campaignID, _ := strconv.ParseInt(idStr, 10, 64)

	var archivedAt *time.Time
	err := s.DB.Pool.QueryRow(r.Context(), "SELECT archived_at FROM campaigns WHERE id = $1", campaignID).Scan(&archivedAt)
	if err != nil {
		http.Error(w, "Campaign not found", http.StatusNotFound)
		return
	}
	if archivedAt != nil {
		http.Error(w, "Campaign terarsip tidak menerima unggahan nomor baru", http.StatusBadRequest)
		return
	}

	err = r.ParseMultipartForm(6 << 20) // 6 MB max
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "Ukuran file melebihi batas (maksimal 6 MB)", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Gagal memproses form", http.StatusBadRequest)
		return
	}

	var rawData []byte
	var filename string

	file, header, err := r.FormFile("file")
	if err == nil {
		defer file.Close()
		rawData, err = io.ReadAll(file)
		if err != nil {
			http.Error(w, "Gagal membaca file", http.StatusInternalServerError)
			return
		}
		filename = header.Filename
	} else {
		pasted := r.FormValue("pasted")
		if strings.TrimSpace(pasted) == "" {
			http.Error(w, "Silakan upload file atau paste nomor", http.StatusBadRequest)
			return
		}
		rawData = []byte(pasted)
		filename = "pasted.txt"
	}

	defaultCC := config.DefaultCountryCode

	preview, err := campaign.ProcessPreview(r.Context(), s.DB.Pool, campaignID, rawData, filename, defaultCC)
	if err != nil {
		http.Error(w, "Error memproses file: "+err.Error(), http.StatusBadRequest)
		return
	}

	csrfToken := r.FormValue("csrf_token")

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	hasRejected := (preview.Stats.DupInFile + preview.Stats.DupInCampaign + preview.Stats.Invalid + preview.Stats.Ambiguous) > 0

	previewData := struct {
		ActiveNav   string
		Lang        string
		Title       string
		Theme       string
		CSRFToken   string
		CampaignID  int64
		PreviewID   string
		Stats       campaign.UploadStats
		HasRejected bool
		T           func(string) string
	}{
		ActiveNav:   "campaigns",
		Lang:        lang,
		Title:       i18n.T(lang, "numbers.preview_title"),
		Theme:       getTheme(r),
		CSRFToken:   csrfToken,
		CampaignID:  campaignID,
		PreviewID:   preview.ID,
		Stats:       preview.Stats,
		HasRejected: hasRejected,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if tmpl, ok := s.Tmpl["admin_campaign_numbers_preview.html"]; ok {
		tmpl.ExecuteTemplate(w, "base.html", previewData)
	} else {
		http.Error(w, "Template not found", http.StatusInternalServerError)
	}
}

// HandleAdminCampaignNumbersConfirm POST confirm upload
func (s *Server) HandleAdminCampaignNumbersConfirm(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	campaignID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID campaign tidak valid", http.StatusBadRequest)
		return
	}

	var archivedAt *time.Time
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT archived_at FROM campaigns WHERE id = $1", campaignID).Scan(&archivedAt)
	if err != nil {
		http.Error(w, "Campaign not found", http.StatusNotFound)
		return
	}
	if archivedAt != nil {
		http.Error(w, "Campaign terarsip tidak menerima unggahan nomor baru", http.StatusBadRequest)
		return
	}

	previewID := r.FormValue("preview_id")
	if previewID == "" || !campaign.IsValidPreviewID(previewID) {
		http.Error(w, "Preview ID tidak valid", http.StatusBadRequest)
		return
	}

	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	err = campaign.ConfirmUpload(r.Context(), s.DB.Pool, campaignID, user.ID, previewID)
	if err != nil {
		http.Error(w, "Gagal mengonfirmasi upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d", campaignID), http.StatusSeeOther)
}

// HandleAdminCampaignNumbersRejected GET rejected list CSV
func (s *Server) HandleAdminCampaignNumbersRejected(w http.ResponseWriter, r *http.Request) {
	previewID := r.PathValue("previewId")
	if previewID == "" || !campaign.IsValidPreviewID(previewID) {
		http.Error(w, "Preview ID tidak valid", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join("data", "tmp", previewID+"_rejected.csv")
	data, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "File tidak ditemukan atau sudah dihapus", http.StatusNotFound)
		return
	}
	_ = os.Remove(filePath)

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"rejected_%s.csv\"", previewID))
	w.Write(data)
}
