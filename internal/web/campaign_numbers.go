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

// Get CSRF from cookie
	csrfToken := ""
	if c, err := r.Cookie("gb_csrf"); err == nil {
		csrfToken = c.Value
	}
	
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(fmt.Sprintf(`
		<h1>Upload Nomor</h1>
		<form method="post" action="/admin/campaigns/%d/numbers/preview" enctype="multipart/form-data">
			<input type="hidden" name="csrf_token" value="%s">
			<label>Upload File (CSV/TXT/XLSX):</label>
			<input type="file" name="file" accept=".csv,.txt,.xlsx">
			<br>
			<label>Atau Paste Nomor (satu per baris):</label>
			<textarea name="pasted"></textarea>
			<br>
			<button type="submit">Preview</button>
		</form>
	`, campaignID, csrfToken)))
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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(fmt.Sprintf(`
		<h1>Preview Upload</h1>
		<p>Total: %d</p>
		<p>Diterima: %d</p>
		<p>Duplikat (File): %d</p>
		<p>Duplikat (Campaign): %d</p>
		<p>Tidak Valid: %d</p>
		<p>Ambigu: %d</p>
		<a href="/admin/campaigns/%d/numbers/rejected/%s">Download Daftar Ditolak (CSV)</a>
		
		<form method="post" action="/admin/campaigns/%d/numbers/confirm">
			<input type="hidden" name="csrf_token" value="%s">
			<input type="hidden" name="preview_id" value="%s">
			<button type="submit">Konfirmasi & Masukkan Nomor</button>
		</form>
	`, preview.Stats.Total, preview.Stats.Accepted, preview.Stats.DupInFile, preview.Stats.DupInCampaign, preview.Stats.Invalid, preview.Stats.Ambiguous,
	campaignID, preview.ID, campaignID, csrfToken, preview.ID)))
}

// HandleAdminCampaignNumbersConfirm POST confirm upload
func (s *Server) HandleAdminCampaignNumbersConfirm(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	campaignID, _ := strconv.ParseInt(idStr, 10, 64)

	previewID := r.FormValue("preview_id")
	if previewID == "" {
		http.Error(w, "Preview ID missing", http.StatusBadRequest)
		return
	}
	
	userSessionCtx := r.Context().Value(userContextKey)
	if userSessionCtx == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID := userSessionCtx.(UserSession).ID

	err := campaign.ConfirmUpload(r.Context(), s.DB.Pool, campaignID, userID, previewID)
	if err != nil {
		http.Error(w, "Gagal mengonfirmasi upload: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d", campaignID), http.StatusSeeOther)
}

// HandleAdminCampaignNumbersRejected GET rejected list CSV
func (s *Server) HandleAdminCampaignNumbersRejected(w http.ResponseWriter, r *http.Request) {
	previewID := r.PathValue("previewId")
	if previewID == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	filePath := filepath.Join("data", "tmp", previewID+"_rejected.csv")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "File tidak ditemukan atau sudah dihapus", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"rejected_%s.csv\"", previewID))
	http.ServeFile(w, r, filePath)
}
