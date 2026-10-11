package web

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"goblast/internal/campaign"
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

	var status string
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT status FROM campaigns WHERE id = $1", campaignID).Scan(&status)
	if err != nil {
		http.Error(w, "Campaign not found", http.StatusNotFound)
		return
	}
	if status != "draf" && status != "berjalan" {
		http.Error(w, "Nomor hanya bisa ditambahkan ke campaign draf atau berjalan", http.StatusBadRequest)
		return
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	// Get CSRF from cookie
	csrfToken := ""
	if c, err := r.Cookie("gb_csrf"); err == nil {
		csrfToken = c.Value
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(fmt.Sprintf(`
		<h1>%s</h1>
		<form method="post" action="/admin/campaigns/%d/numbers/preview" enctype="multipart/form-data">
			<input type="hidden" name="csrf_token" value="%s">
			<label>%s</label>
			<input type="file" name="file" accept=".csv,.txt,.xlsx">
			<br>
			<label>%s</label>
			<textarea name="pasted"></textarea>
			<br>
			<button type="submit">%s</button>
		</form>
	`, i18n.T(lang, "numbers.upload_title"), campaignID, csrfToken, i18n.T(lang, "numbers.file_label"), i18n.T(lang, "numbers.paste_label"), i18n.T(lang, "numbers.preview_btn"))))
}

// HandleAdminCampaignNumbersPreview POST reads the file and shows preview
func (s *Server) HandleAdminCampaignNumbersPreview(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	campaignID, _ := strconv.ParseInt(idStr, 10, 64)

	err := r.ParseMultipartForm(5 << 20) // 5 MB max
	if err != nil {
		http.Error(w, "Gagal memproses form (batas 5MB)", http.StatusBadRequest)
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

	defaultCC := "62"
	_ = s.DB.Pool.QueryRow(r.Context(), "SELECT value->>'default_country_code' FROM settings WHERE key = 'app_settings'").Scan(&defaultCC)
	if defaultCC == "" {
		defaultCC = "62"
	}

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

	var ccTable strings.Builder
	if len(preview.Stats.CountryCodes) > 0 {
		ccTable.WriteString("<div style='margin-top: 15px;'><h3>Rincian Kode Negara</h3><table border='1' cellpadding='5' style='border-collapse: collapse;'><thead><tr><th>Kode Negara</th><th>Jumlah</th></tr></thead><tbody>")
		for cc, count := range preview.Stats.CountryCodes {
			ccTable.WriteString(fmt.Sprintf("<tr><td>+%s</td><td>%d</td></tr>", cc, count))
		}
		ccTable.WriteString("</tbody></table></div>")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(fmt.Sprintf(`
		<h1>%s</h1>
		<p>%s: %d</p>
		<p>%s: %d</p>
		<p>%s: %d</p>
		<p>%s: %d</p>
		<p>%s: %d</p>
		<p>%s: %d</p>
		%s
		<br>
		<a href="/admin/campaigns/%d/numbers/rejected/%s">%s</a>
		
		<form method="post" action="/admin/campaigns/%d/numbers/confirm">
			<input type="hidden" name="csrf_token" value="%s">
			<input type="hidden" name="preview_id" value="%s">
			<button type="submit">%s</button>
		</form>
	`, i18n.T(lang, "numbers.preview_title"),
		i18n.T(lang, "numbers.total"), preview.Stats.Total,
		i18n.T(lang, "numbers.accepted"), preview.Stats.Accepted,
		i18n.T(lang, "numbers.dup_file"), preview.Stats.DupInFile,
		i18n.T(lang, "numbers.dup_campaign"), preview.Stats.DupInCampaign,
		i18n.T(lang, "numbers.invalid"), preview.Stats.Invalid,
		i18n.T(lang, "numbers.ambiguous"), preview.Stats.Ambiguous,
		ccTable.String(),
		campaignID, preview.ID, i18n.T(lang, "numbers.download_rejected"),
		campaignID, csrfToken, preview.ID, i18n.T(lang, "numbers.confirm_btn"))))
}

// HandleAdminCampaignNumbersConfirm POST confirm upload
func (s *Server) HandleAdminCampaignNumbersConfirm(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	campaignID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID campaign tidak valid", http.StatusBadRequest)
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
