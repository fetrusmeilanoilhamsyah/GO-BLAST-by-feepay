package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"goblast/internal/campaign"
	"goblast/internal/i18n"
)

func (s *Server) HandleAdminCampaigns(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/campaigns" {
		http.NotFound(w, r)
		return
	}

	rows, err := s.DB.Pool.Query(r.Context(), "SELECT id, name, status, button_mode, created_at FROM campaigns ORDER BY id DESC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var campaigns []campaign.Campaign
	for rows.Next() {
		var c campaign.Campaign
		if err := rows.Scan(&c.ID, &c.Name, &c.Status, &c.ButtonMode, &c.CreatedAt); err != nil {
			continue
		}
		campaigns = append(campaigns, c)
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	data := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		ActiveNav string
		Campaigns []campaign.Campaign
		T         func(string) string
	}{
		Lang:      lang,
		Title:     "Campaigns - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		ActiveNav: "campaigns",
		Campaigns: campaigns,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	s.Tmpl["admin_campaigns.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleAdminCampaignNewGET(w http.ResponseWriter, r *http.Request) {
	// Retrieve default mode from settings
	defaultMode := campaign.ModeKartuLink
	var settingVal string
	err := s.DB.Pool.QueryRow(r.Context(), "SELECT value FROM settings WHERE key = 'default_button_mode'").Scan(&settingVal)
	if err == nil {
		var val string
		json.Unmarshal([]byte(settingVal), &val)
		if val == campaign.ModeKartuLink {
			defaultMode = val
		}
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	csrfToken := getCSRF(r)

	data := struct {
		Lang        string
		Title       string
		Theme       string
		CSRFToken   string
		ActiveNav   string
		Campaign    campaign.Campaign
		DefaultMode string
		T           func(string) string
	}{
		Lang:      lang,
		Title:     "Buat Campaign Baru - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: csrfToken,
		ActiveNav: "campaigns",
		Campaign: campaign.Campaign{
			Elements: campaign.Elements{
				CSActive: false,
			},
			ButtonMode: defaultMode,
		},
		DefaultMode: defaultMode,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	s.Tmpl["admin_campaign_form.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleAdminCampaignNewPOST(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(5 << 20) // 5 MB max memory
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "Nama campaign wajib diisi", http.StatusBadRequest)
		return
	}

	mode := r.FormValue("button_mode")
	if mode == campaign.ModeTombolAsli {
		http.Error(w, "mode tombol_asli dinonaktifkan", http.StatusBadRequest)
		return
	}

	body := r.FormValue("body")

	elements := campaign.Elements{
		CSActive:      r.FormValue("cs_active") == "on",
		CSNumber:      r.FormValue("cs_number"),
		LinkWebActive: r.FormValue("link_web_active") == "on",
		LinkWebURL:    r.FormValue("link_web_url"),
		PhoneActive:   r.FormValue("phone_active") == "on",
		PhoneNumber:   r.FormValue("phone_number"),
		PromoActive:   r.FormValue("promo_active") == "on",
		PromoCode:     r.FormValue("promo_code"),
		PromoURL:      r.FormValue("promo_url"),
	}

	if err := campaign.ValidateTemplate(body, mode, elements); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var imagePath *string
	file, header, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		if header.Size > 1<<20 {
			http.Error(w, "Ukuran file maksimum 1 MB", http.StatusBadRequest)
			return
		}

		buff := make([]byte, 512)
		if _, err = file.Read(buff); err != nil {
			http.Error(w, "Gagal membaca file", http.StatusInternalServerError)
			return
		}
		fileType := http.DetectContentType(buff)
		var ext string
		if fileType == "image/jpeg" {
			ext = ".jpg"
		} else if fileType == "image/png" {
			ext = ".png"
		} else {
			http.Error(w, "Hanya menerima gambar JPEG atau PNG", http.StatusBadRequest)
			return
		}
		file.Seek(0, io.SeekStart)

		b := make([]byte, 16)
		rand.Read(b)
		filename := hex.EncodeToString(b) + ext

		os.MkdirAll("uploads", 0755)
		fullPath := "uploads/" + filename
		out, err := os.Create(fullPath)
		if err != nil {
			http.Error(w, "Gagal menyimpan file", http.StatusInternalServerError)
			return
		}
		defer out.Close()
		io.Copy(out, file)
		imagePath = &fullPath
	}

	elementsJSON, _ := json.Marshal(elements)
	user, _ := currentUser(r)
	userID := user.ID

	_, err = s.DB.Pool.Exec(r.Context(), `
		INSERT INTO campaigns (name, status, button_mode, body, image_path, elements, created_by)
		VALUES ($1, 'draf', $2, $3, $4, $5, $6)
	`, name, mode, body, imagePath, string(elementsJSON), userID)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/campaigns", http.StatusSeeOther)
}

func (s *Server) HandleAdminCampaignEditGET(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	var c campaign.Campaign
	var elJSON string
	var imgPath *string
	err := s.DB.Pool.QueryRow(r.Context(), "SELECT id, name, status, button_mode, body, image_path, elements FROM campaigns WHERE id = $1", id).Scan(
		&c.ID, &c.Name, &c.Status, &c.ButtonMode, &c.Body, &imgPath, &elJSON)

	if err != nil {
		http.NotFound(w, r)
		return
	}

	if imgPath != nil {
		c.ImagePath = *imgPath
	}

	json.Unmarshal([]byte(elJSON), &c.Elements)

	if c.Status != campaign.StatusDraf {
		http.Error(w, "Hanya campaign draf yang bisa diubah", http.StatusForbidden)
		return
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	csrfToken := getCSRF(r)

	data := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		ActiveNav string
		Campaign  campaign.Campaign
		T         func(string) string
	}{
		Lang:      lang,
		Title:     "Edit Campaign - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: csrfToken,
		ActiveNav: "campaigns",
		Campaign:  c,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	s.Tmpl["admin_campaign_form.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleAdminCampaignEditPOST(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	var currentStatus string
	var currentImagePath *string
	err := s.DB.Pool.QueryRow(r.Context(), "SELECT status, image_path FROM campaigns WHERE id = $1 FOR UPDATE", id).Scan(&currentStatus, &currentImagePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if currentStatus != campaign.StatusDraf {
		http.Error(w, "Hanya campaign draf yang bisa diubah", http.StatusForbidden)
		return
	}

	err = r.ParseMultipartForm(5 << 20)
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "Nama campaign wajib diisi", http.StatusBadRequest)
		return
	}

	mode := r.FormValue("button_mode")
	if mode == campaign.ModeTombolAsli {
		http.Error(w, "mode tombol_asli dinonaktifkan", http.StatusBadRequest)
		return
	}

	body := r.FormValue("body")

	elements := campaign.Elements{
		CSActive:      r.FormValue("cs_active") == "on",
		CSNumber:      r.FormValue("cs_number"),
		LinkWebActive: r.FormValue("link_web_active") == "on",
		LinkWebURL:    r.FormValue("link_web_url"),
		PhoneActive:   r.FormValue("phone_active") == "on",
		PhoneNumber:   r.FormValue("phone_number"),
		PromoActive:   r.FormValue("promo_active") == "on",
		PromoCode:     r.FormValue("promo_code"),
		PromoURL:      r.FormValue("promo_url"),
	}

	if err := campaign.ValidateTemplate(body, mode, elements); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var imagePath *string
	if currentImagePath != nil {
		imagePath = currentImagePath
	}

	file, header, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		if header.Size > 1<<20 {
			http.Error(w, "Ukuran file maksimum 1 MB", http.StatusBadRequest)
			return
		}

		buff := make([]byte, 512)
		if _, err = file.Read(buff); err != nil {
			http.Error(w, "Gagal membaca file", http.StatusInternalServerError)
			return
		}
		fileType := http.DetectContentType(buff)
		var ext string
		if fileType == "image/jpeg" {
			ext = ".jpg"
		} else if fileType == "image/png" {
			ext = ".png"
		} else {
			http.Error(w, "Hanya menerima gambar JPEG atau PNG", http.StatusBadRequest)
			return
		}
		file.Seek(0, io.SeekStart)

		b := make([]byte, 16)
		rand.Read(b)
		filename := hex.EncodeToString(b) + ext

		os.MkdirAll("uploads", 0755)
		fullPath := "uploads/" + filename
		out, err := os.Create(fullPath)
		if err != nil {
			http.Error(w, "Gagal menyimpan file", http.StatusInternalServerError)
			return
		}
		defer out.Close()
		io.Copy(out, file)
		imagePath = &fullPath
	} else if r.FormValue("remove_image") == "on" {
		imagePath = nil
	}

	elementsJSON, _ := json.Marshal(elements)

	_, err = s.DB.Pool.Exec(r.Context(), `
		UPDATE campaigns 
		SET name = $1, button_mode = $2, body = $3, image_path = $4, elements = $5
		WHERE id = $6
	`, name, mode, body, imagePath, string(elementsJSON), id)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/campaigns", http.StatusSeeOther)
}

func (s *Server) HandleAdminCampaignPreview(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(5 << 20)
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	mode := r.FormValue("button_mode")
	if mode == campaign.ModeTombolAsli {
		http.Error(w, "mode tombol_asli dinonaktifkan", http.StatusBadRequest)
		return
	}

	body := r.FormValue("body")

	elements := campaign.Elements{
		CSActive:      r.FormValue("cs_active") == "on",
		CSNumber:      r.FormValue("cs_number"),
		LinkWebActive: r.FormValue("link_web_active") == "on",
		LinkWebURL:    r.FormValue("link_web_url"),
		PhoneActive:   r.FormValue("phone_active") == "on",
		PhoneNumber:   r.FormValue("phone_number"),
		PromoActive:   r.FormValue("promo_active") == "on",
		PromoCode:     r.FormValue("promo_code"),
		PromoURL:      r.FormValue("promo_url"),
	}

	if err := campaign.ValidateTemplate(body, mode, elements); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	imagePath := r.FormValue("existing_image_path")
	if imagePath != "" {
		if !strings.HasPrefix(imagePath, "uploads/") || strings.Contains(imagePath, "..") {
			http.Error(w, "Image path tidak valid", http.StatusBadRequest)
			return
		}
	}

	sampleVars := map[string]string{
		"nama": "Budi",
		"name": "Budi",
		"usn":  "budi123",
		"id":   "CUST-001",
	}
	if pName := r.FormValue("preview_name"); pName != "" {
		sampleVars["nama"] = pName
		sampleVars["name"] = pName
	}

	msg := campaign.Render(body, imagePath, elements, mode, sampleVars)

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	data := struct {
		campaign.Message
		Lang string
		T    func(string) string
	}{
		Message: msg,
		Lang:    lang,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	s.Tmpl["admin_campaign_preview.html"].Execute(w, data)
}

func (s *Server) HandleAdminCampaignDetail(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	var c campaign.Campaign
	var elJSON string
	var imgPath *string
	err = s.DB.Pool.QueryRow(r.Context(), `
		SELECT id, name, status, button_mode, body, image_path, elements, created_at, started_at, finished_at 
		FROM campaigns WHERE id = $1
	`, id).Scan(&c.ID, &c.Name, &c.Status, &c.ButtonMode, &c.Body, &imgPath, &elJSON, &c.CreatedAt, &c.StartedAt, &c.FinishedAt)

	if err != nil {
		http.NotFound(w, r)
		return
	}

	if imgPath != nil {
		c.ImagePath = *imgPath
	}
	json.Unmarshal([]byte(elJSON), &c.Elements)

	// Fetch status counts
	counts := map[string]int{
		"tersedia":        0,
		"dikirim":         0,
		"terkirim":        0,
		"tidak_terdaftar": 0,
		"gagal":           0,
	}

	rows, err := s.DB.Pool.Query(r.Context(), `
		SELECT status, COUNT(*) 
		FROM campaign_numbers 
		WHERE campaign_id = $1 
		GROUP BY status
	`, id)
	if err == nil {
		for rows.Next() {
			var st string
			var count int
			if err := rows.Scan(&st, &count); err == nil {
				counts[st] = count
			}
		}
		rows.Close()
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	data := struct {
		Lang      string
		Title     string
		Theme     string
		CSRFToken string
		ActiveNav string
		Campaign  campaign.Campaign
		Counts    map[string]int
		T         func(string) string
	}{
		Lang:      lang,
		Title:     c.Name + " - GO BLAST",
		Theme:     getTheme(r),
		CSRFToken: getCSRF(r),
		ActiveNav: "campaigns",
		Campaign:  c,
		Counts:    counts,
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	s.Tmpl["admin_campaign_detail.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleAdminCampaignStart(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	var status string
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT status FROM campaigns WHERE id = $1 FOR UPDATE", id).Scan(&status)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if status != campaign.StatusDraf {
		http.Error(w, "Campaign hanya bisa dimulai dari status draf", http.StatusBadRequest)
		return
	}

	var availableCount int
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'tersedia'", id).Scan(&availableCount)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if availableCount < 1 {
		http.Error(w, "Tidak ada nomor tersedia untuk memulai campaign", http.StatusBadRequest)
		return
	}

	now := time.Now()
	_, err = s.DB.Pool.Exec(r.Context(), "UPDATE campaigns SET status = 'berjalan', started_at = COALESCE(started_at, $1) WHERE id = $2", now, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	user, _ := currentUser(r)
	s.logAudit(&user.ID, "campaign_start", "campaign", strconv.FormatInt(id, 10), "")

	http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d", id), http.StatusSeeOther)
}

func (s *Server) HandleAdminCampaignPause(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	var status string
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT status FROM campaigns WHERE id = $1 FOR UPDATE", id).Scan(&status)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if status != campaign.StatusBerjalan {
		http.Error(w, "Campaign hanya bisa dijeda dari status berjalan", http.StatusBadRequest)
		return
	}

	_, err = s.DB.Pool.Exec(r.Context(), "UPDATE campaigns SET status = 'draf' WHERE id = $1", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	user, _ := currentUser(r)
	s.logAudit(&user.ID, "campaign_pause", "campaign", strconv.FormatInt(id, 10), "campaign dijeda")

	http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d", id), http.StatusSeeOther)
}

func (s *Server) HandleAdminCampaignFinish(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	var status string
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT status FROM campaigns WHERE id = $1 FOR UPDATE", id).Scan(&status)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if status != campaign.StatusBerjalan {
		http.Error(w, "Campaign hanya bisa diselesaikan dari status berjalan", http.StatusBadRequest)
		return
	}

	now := time.Now()
	_, err = s.DB.Pool.Exec(r.Context(), "UPDATE campaigns SET status = 'selesai', finished_at = $1 WHERE id = $2", now, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	user, _ := currentUser(r)
	s.logAudit(&user.ID, "campaign_finish", "campaign", strconv.FormatInt(id, 10), "")

	http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d", id), http.StatusSeeOther)
}

func (s *Server) HandleAdminCampaignDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	var status string
	var imagePath *string
	err = tx.QueryRow(ctx, "SELECT status, image_path FROM campaigns WHERE id = $1 FOR UPDATE", id).Scan(&status, &imagePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Campaign berjalan tidak boleh dihapus!
	if status == campaign.StatusBerjalan {
		http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d?err=Kampanye+yang+sedang+berjalan+tidak+dapat+dihapus", id), http.StatusSeeOther)
		return
	}

	// 1. Unlink ledger entries foreign key to prevent constraint violation
	_, err = tx.Exec(ctx, `
		UPDATE ledger_entries 
		SET campaign_number_id = NULL 
		WHERE campaign_number_id IN (SELECT id FROM campaign_numbers WHERE campaign_id = $1)
	`, id)
	if err != nil {
		http.Error(w, "Gagal melepaskan relasi buku besar: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 2. Delete campaign numbers
	_, err = tx.Exec(ctx, "DELETE FROM campaign_numbers WHERE campaign_id = $1", id)
	if err != nil {
		http.Error(w, "Gagal menghapus nomor kampanye: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 3. Delete number batches to satisfy number_batches_campaign_id_fkey
	_, err = tx.Exec(ctx, "DELETE FROM number_batches WHERE campaign_id = $1", id)
	if err != nil {
		http.Error(w, "Gagal menghapus batch nomor kampanye: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 4. Delete campaign
	_, err = tx.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", id)
	if err != nil {
		http.Error(w, "Gagal menghapus kampanye: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		http.Error(w, "Gagal menyelesaikan penghapusan kampanye", http.StatusInternalServerError)
		return
	}

	// Remove uploaded image file if present
	if imagePath != nil && *imagePath != "" {
		_ = os.Remove(*imagePath)
	}

	s.logAudit(&user.ID, "campaign_delete", "campaign", strconv.FormatInt(id, 10), "campaign deleted")
	http.Redirect(w, r, "/admin/campaigns", http.StatusSeeOther)
}

func (s *Server) HandleServeUpload(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	// Prevent directory traversal
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join("uploads", filename))
}
