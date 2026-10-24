package web

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nyaruka/phonenumbers"
	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/i18n"
	"goblast/internal/numbers"
)

func (s *Server) HandleAdminCampaigns(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/campaigns" {
		http.NotFound(w, r)
		return
	}

	showArchived := r.URL.Query().Get("show_archived") == "1" || r.URL.Query().Get("archived") == "1"

	query := "SELECT id, name, status, button_mode, created_at, archived_at, started_at, purged_at FROM campaigns"
	if !showArchived {
		query += " WHERE archived_at IS NULL"
	}
	query += " ORDER BY id DESC"

	rows, err := s.DB.Pool.Query(r.Context(), query)
	if err != nil {
		slog.Error("Gagal memuat daftar kampanye", "err", err)
		http.Error(w, "Gagal memuat kampanye", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var campaigns []campaign.Campaign
	for rows.Next() {
		var c campaign.Campaign
		if err := rows.Scan(&c.ID, &c.Name, &c.Status, &c.ButtonMode, &c.CreatedAt, &c.ArchivedAt, &c.StartedAt, &c.PurgedAt); err != nil {
			continue
		}
		campaigns = append(campaigns, c)
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	data := struct {
		Lang         string
		Title        string
		Theme        string
		CSRFToken    string
		ActiveNav    string
		Campaigns    []campaign.Campaign
		ShowArchived bool
		Error        string
		Message      string
		Nonce        string
		T            func(string) string
	}{
		Lang:         lang,
		Title:        "Campaigns - GO BLAST",
		Theme:        getTheme(r),
		CSRFToken:    getCSRF(r),
		ActiveNav:    "campaigns",
		Campaigns:    campaigns,
		ShowArchived: showArchived,
		Error:        r.URL.Query().Get("err"),
		Message:      r.URL.Query().Get("msg"),
		Nonce:        getNonce(r),
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

	defaultCountry := "ID"
	var countryVal string
	if err := s.DB.Pool.QueryRow(r.Context(), "SELECT value FROM settings WHERE key = 'default_country'").Scan(&countryVal); err == nil {
		var val string
		if json.Unmarshal([]byte(countryVal), &val) == nil && val != "" {
			defaultCountry = strings.ToUpper(strings.TrimSpace(val))
		}
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	csrfToken := getCSRF(r)

	data := struct {
		Lang           string
		Title          string
		Theme          string
		CSRFToken      string
		ActiveNav      string
		Campaign       campaign.Campaign
		DefaultMode    string
		CountryOptions []numbers.CountryOption
		Nonce          string
		T              func(string) string
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
			ButtonMode:     defaultMode,
			DefaultCountry: defaultCountry,
		},
		DefaultMode:    defaultMode,
		CountryOptions: numbers.SupportedCountryOptions(),
		Nonce:          getNonce(r),
		T: func(key string) string {
			return i18n.T(lang, key)
		},
	}

	s.Tmpl["admin_campaign_form.html"].ExecuteTemplate(w, "base.html", data)
}

func (s *Server) HandleAdminCampaignNewPOST(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(6 << 20)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "Ukuran file melebihi batas (maksimal 6 MB)", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Nama campaign wajib diisi", http.StatusBadRequest)
		return
	}

	mode := r.FormValue("button_mode")
	if mode == campaign.ModeTombolAsli {
		if r.FormValue("agree_experimental_buttons") != "1" {
			http.Error(w, "Persetujuan opsi eksperimental tombol asli wajib dicentang", http.StatusBadRequest)
			return
		}
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

	defaultCountry := strings.ToUpper(strings.TrimSpace(r.FormValue("default_country")))
	if defaultCountry == "" {
		defaultCountry = "ID"
	}
	if phonenumbers.GetCountryCodeForRegion(defaultCountry) == 0 {
		http.Error(w, "Kode negara default tidak valid", http.StatusBadRequest)
		return
	}

	if err := campaign.ValidateTemplate(body, mode, elements, defaultCountry); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var imagePath *string
	file, header, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		lr := io.LimitReader(file, campaign.MaxImageUploadBytes+1)
		imgData, readErr := io.ReadAll(lr)
		if readErr != nil {
			http.Error(w, "Gagal membaca berkas gambar", http.StatusInternalServerError)
			return
		}
		if len(imgData) > campaign.MaxImageUploadBytes {
			http.Error(w, "Ukuran file melebihi batas (maksimal 5 MB)", http.StatusBadRequest)
			return
		}
		savedPath, saveErr := campaign.ProcessAndSaveCampaignImage(imgData, header.Filename)
		if saveErr != nil {
			http.Error(w, saveErr.Error(), http.StatusBadRequest)
			return
		}
		imagePath = &savedPath
	}

	elementsJSON, _ := json.Marshal(elements)
	user, _ := currentUser(r)
	userID := user.ID

	_, err = s.DB.Pool.Exec(r.Context(), `
		INSERT INTO campaigns (name, status, button_mode, body, image_path, elements, created_by, default_country)
		VALUES ($1, 'draf', $2, $3, $4, $5, $6, $7)
	`, name, mode, body, imagePath, string(elementsJSON), userID, defaultCountry)

	if err != nil {
		slog.Error("Gagal menyimpan kampanye baru", "err", err)
		http.Error(w, "Gagal menyimpan kampanye", http.StatusInternalServerError)
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
	var archivedAt *time.Time
	err := s.DB.Pool.QueryRow(r.Context(), "SELECT id, name, status, button_mode, body, image_path, elements, archived_at, default_country FROM campaigns WHERE id = $1", id).Scan(
		&c.ID, &c.Name, &c.Status, &c.ButtonMode, &c.Body, &imgPath, &elJSON, &archivedAt, &c.DefaultCountry)

	if err != nil {
		http.NotFound(w, r)
		return
	}

	if archivedAt != nil {
		http.Error(w, "Campaign terarsip hanya-baca", http.StatusForbidden)
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
		Lang           string
		Title          string
		Theme          string
		CSRFToken      string
		ActiveNav      string
		Campaign       campaign.Campaign
		CountryOptions []numbers.CountryOption
		Nonce          string
		T              func(string) string
	}{
		Lang:           lang,
		Title:          "Edit Campaign - GO BLAST",
		Theme:          getTheme(r),
		CSRFToken:      csrfToken,
		ActiveNav:      "campaigns",
		Campaign:       c,
		CountryOptions: numbers.SupportedCountryOptions(),
		Nonce:          getNonce(r),
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
	var archivedAt *time.Time
	err := s.DB.Pool.QueryRow(r.Context(), "SELECT status, image_path, archived_at FROM campaigns WHERE id = $1 FOR UPDATE", id).Scan(&currentStatus, &currentImagePath, &archivedAt)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if archivedAt != nil {
		http.Error(w, "Campaign terarsip hanya-baca", http.StatusForbidden)
		return
	}

	if currentStatus != campaign.StatusDraf {
		http.Error(w, "Hanya campaign draf yang bisa diubah", http.StatusForbidden)
		return
	}

	err = r.ParseMultipartForm(6 << 20)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "Ukuran file melebihi batas (maksimal 6 MB)", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Nama campaign wajib diisi", http.StatusBadRequest)
		return
	}

	mode := r.FormValue("button_mode")
	if mode == campaign.ModeTombolAsli {
		if r.FormValue("agree_experimental_buttons") != "1" {
			http.Error(w, "Persetujuan opsi eksperimental tombol asli wajib dicentang", http.StatusBadRequest)
			return
		}
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

	defaultCountry := strings.ToUpper(strings.TrimSpace(r.FormValue("default_country")))
	if defaultCountry == "" {
		defaultCountry = "ID"
	}
	if phonenumbers.GetCountryCodeForRegion(defaultCountry) == 0 {
		http.Error(w, "Kode negara default tidak valid", http.StatusBadRequest)
		return
	}

	if err := campaign.ValidateTemplate(body, mode, elements, defaultCountry); err != nil {
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
		lr := io.LimitReader(file, campaign.MaxImageUploadBytes+1)
		imgData, readErr := io.ReadAll(lr)
		if readErr != nil {
			http.Error(w, "Gagal membaca berkas gambar", http.StatusInternalServerError)
			return
		}
		if len(imgData) > campaign.MaxImageUploadBytes {
			http.Error(w, "Ukuran file melebihi batas (maksimal 5 MB)", http.StatusBadRequest)
			return
		}
		savedPath, saveErr := campaign.ProcessAndSaveCampaignImage(imgData, header.Filename)
		if saveErr != nil {
			http.Error(w, saveErr.Error(), http.StatusBadRequest)
			return
		}
		imagePath = &savedPath
	} else if r.FormValue("remove_image") == "on" {
		imagePath = nil
	}

	elementsJSON, _ := json.Marshal(elements)

	_, err = s.DB.Pool.Exec(r.Context(), `
		UPDATE campaigns 
		SET name = $1, button_mode = $2, body = $3, image_path = $4, elements = $5, default_country = $6
		WHERE id = $7
	`, name, mode, body, imagePath, string(elementsJSON), defaultCountry, id)

	if err != nil {
		slog.Error("Gagal memperbarui kampanye", "err", err, "id", id)
		http.Error(w, "Gagal memperbarui kampanye", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/campaigns", http.StatusSeeOther)
}

func (s *Server) HandleAdminCampaignPreview(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(6 << 20)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "Ukuran file melebihi batas (maksimal 6 MB)", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	mode := r.FormValue("button_mode")
	if mode == "" {
		mode = campaign.ModeKartuLink
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

	if r.FormValue("existing_image_path") != "" {
		http.Error(w, "Field existing_image_path tidak diizinkan", http.StatusBadRequest)
		return
	}

	defaultCountry := strings.ToUpper(strings.TrimSpace(r.FormValue("default_country")))
	if defaultCountry == "" {
		defaultCountry = "ID"
	}
	if err := campaign.ValidateTemplate(body, mode, elements, defaultCountry); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var imageURI string
	var thumbURI string

	file, _, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		lr := io.LimitReader(file, campaign.MaxImageUploadBytes+1)
		imgData, readErr := io.ReadAll(lr)
		if readErr != nil {
			http.Error(w, "Gagal membaca berkas gambar", http.StatusInternalServerError)
			return
		}
		if len(imgData) > campaign.MaxImageUploadBytes {
			http.Error(w, "Ukuran file melebihi batas (maksimal 5 MB)", http.StatusBadRequest)
			return
		}
		uri, dataErr := campaign.ImageDataURI(imgData)
		if dataErr != nil {
			http.Error(w, dataErr.Error(), http.StatusBadRequest)
			return
		}
		imageURI = uri

		if mode == campaign.ModeKartuLink {
			thumbBytes, _ := campaign.GenerateThumbnailFromBytes(imgData, campaign.DefaultThumbWidth, campaign.DefaultThumbQuality)
			if len(thumbBytes) > 0 {
				thumbURI, _ = campaign.ImageDataURI(thumbBytes)
			}
		}
	} else if r.FormValue("remove_image") != "on" {
		// Jika tidak ada upload file baru dan tidak dihapus, cek apakah mengedit campaign yang sudah ada
		if idStr := r.FormValue("id"); idStr != "" {
			if campID, parseErr := strconv.ParseInt(idStr, 10, 64); parseErr == nil && campID > 0 {
				var dbImagePath *string
				_ = s.DB.Pool.QueryRow(r.Context(), "SELECT image_path FROM campaigns WHERE id = $1", campID).Scan(&dbImagePath)
				if dbImagePath != nil && *dbImagePath != "" {
					uri, fileErr := campaign.ImageFileToDataURI(*dbImagePath)
					if fileErr == nil {
						imageURI = uri
					}
					if mode == campaign.ModeKartuLink {
						thumbBytes, _ := campaign.GetOrCreateThumbnail(*dbImagePath, campaign.DefaultThumbWidth, campaign.DefaultThumbQuality)
						if len(thumbBytes) > 0 {
							thumbURI, _ = campaign.ImageDataURI(thumbBytes)
						}
					}
				}
			}
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

	tmpl := campaign.Template{
		Body:       body,
		ImagePath:  imageURI,
		ButtonMode: mode,
		Elements:   elements,
	}
	rec := campaign.Recipient{
		Name:       sampleVars["nama"],
		Phone:      "081234567890",
		CustomData: sampleVars,
	}

	msg := campaign.RenderForRecipient(tmpl, rec)

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	data := struct {
		campaign.Message
		ImageURI     template.URL
		ThumbnailURI template.URL
		Lang         string
		T            func(string) string
	}{
		Message:      msg,
		ImageURI:     template.URL(imageURI),
		ThumbnailURI: template.URL(thumbURI),
		Lang:         lang,
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
		SELECT id, name, status, button_mode, body, image_path, elements, created_at, started_at, finished_at, archived_at, purged_at, purge_summary, default_country 
		FROM campaigns WHERE id = $1
	`, id).Scan(&c.ID, &c.Name, &c.Status, &c.ButtonMode, &c.Body, &imgPath, &elJSON, &c.CreatedAt, &c.StartedAt, &c.FinishedAt, &c.ArchivedAt, &c.PurgedAt, &c.PurgeSummary, &c.DefaultCountry)

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

	// Jika campaign sudah dibersihkan, gunakan angka ringkasan historis (nomor tersedia selalu 0 karena dihapus fisik)
	if c.PurgedAt != nil {
		counts["tersedia"] = 0
		if c.PurgeSummary != nil {
			for k, v := range c.PurgeSummary {
				if k != "tersedia" {
					counts[k] = v
				}
			}
		}
	}

	var uncertainCount int
	_ = s.DB.Pool.QueryRow(r.Context(), `
		SELECT COUNT(*) 
		FROM campaign_numbers 
		WHERE campaign_id = $1 AND status = 'gagal' AND last_error = 'hasil_tidak_pasti'
	`, id).Scan(&uncertainCount)

	var variableWarnings []string
	tmplVars := campaign.FindTemplateVariables(c.Body)
	if len(tmplVars) > 0 && counts["tersedia"] > 0 {
		var missingVars []string
		for _, v := range tmplVars {
			normKey := campaign.NormalizeCustomKey(v)
			hasData, err := campaign.HasAvailableVariableData(r.Context(), s.DB.Pool, id, normKey)
			if err == nil && !hasData {
				missingVars = append(missingVars, v)
			}
		}
		if len(missingVars) > 0 {
			variableWarnings = append(variableWarnings, fmt.Sprintf("Variabel {%s} digunakan dalam template pesan tetapi tidak ditemukan pada data nomor yang tersedia.", strings.Join(missingVars, ", ")))
		}
	}

	lang := "id"
	if u, ok := currentUser(r); ok && u.Language != "" {
		lang = u.Language
	}

	data := struct {
		Lang             string
		Title            string
		Theme            string
		CSRFToken        string
		ActiveNav        string
		Campaign         campaign.Campaign
		Counts           map[string]int
		UncertainCount   int
		VariableWarnings []string
		Error            string
		Nonce            string
		T                func(string) string
	}{
		Lang:             lang,
		Title:            c.Name + " - GO BLAST",
		Theme:            getTheme(r),
		CSRFToken:        getCSRF(r),
		ActiveNav:        "campaigns",
		Campaign:         c,
		Counts:           counts,
		UncertainCount:   uncertainCount,
		VariableWarnings: variableWarnings,
		Error:            r.URL.Query().Get("err"),
		Nonce:            getNonce(r),
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

	var status, buttonMode string
	var archivedAt *time.Time
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT status, button_mode, archived_at FROM campaigns WHERE id = $1 FOR UPDATE", id).Scan(&status, &buttonMode, &archivedAt)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if archivedAt != nil {
		http.Error(w, "Campaign terarsip tidak bisa dimulai", http.StatusBadRequest)
		return
	}

	if status != campaign.StatusDraf {
		http.Error(w, "Campaign hanya bisa dimulai dari status draf", http.StatusBadRequest)
		return
	}

	if buttonMode == campaign.ModeTombolAsli && config.MaxRecipientsNativeFlow > 0 {
		var totalNumbers int
		_ = s.DB.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1", id).Scan(&totalNumbers)
		if totalNumbers > config.MaxRecipientsNativeFlow {
			http.Error(w, fmt.Sprintf("Mode tombol asli dibatasi maksimal %d penerima (campaign ini memiliki %d nomor)", config.MaxRecipientsNativeFlow, totalNumbers), http.StatusBadRequest)
			return
		}
	}

	availableCount, err := campaign.CampaignAvailableCount(r.Context(), s.DB.Pool, id)
	if err != nil {
		slog.Error("Gagal menghitung nomor tersedia kampanye", "err", err, "id", id)
		http.Error(w, "Terjadi kesalahan sistem", http.StatusInternalServerError)
		return
	}

	if availableCount < 1 {
		http.Error(w, "Tidak ada nomor tersedia untuk memulai campaign", http.StatusBadRequest)
		return
	}

	now := time.Now()
	_, err = s.DB.Pool.Exec(r.Context(), "UPDATE campaigns SET status = 'berjalan', started_at = COALESCE(started_at, $1) WHERE id = $2", now, id)
	if err != nil {
		slog.Error("Gagal memulai kampanye", "err", err, "id", id)
		http.Error(w, "Terjadi kesalahan sistem", http.StatusInternalServerError)
		return
	}

	user, _ := currentUser(r)
	auditMeta := fmt.Sprintf("mode: %s", buttonMode)
	s.logAudit(&user.ID, "campaign_start", "campaign", strconv.FormatInt(id, 10), auditMeta)

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
		slog.Error("Gagal menjeda kampanye", "err", err, "id", id)
		http.Error(w, "Terjadi kesalahan sistem", http.StatusInternalServerError)
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
		slog.Error("Gagal menyelesaikan kampanye", "err", err, "id", id)
		http.Error(w, "Terjadi kesalahan sistem", http.StatusInternalServerError)
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

	lang := "id"
	if user.Language != "" {
		lang = user.Language
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

	// 1. Hanya boleh menghapus campaign berstatus draf
	if status != campaign.StatusDraf {
		errMsg := i18n.T(lang, "campaign.err_delete_not_draft")
		http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d?err=%s", id, url.QueryEscape(errMsg)), http.StatusSeeOther)
		return
	}

	// 2. Tidak boleh punya nomor berstatus terkirim
	var sentCount int
	err = tx.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'terkirim'", id).Scan(&sentCount)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if sentCount > 0 {
		errMsg := i18n.T(lang, "campaign.err_delete_has_sent")
		http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d?err=%s", id, url.QueryEscape(errMsg)), http.StatusSeeOther)
		return
	}

	// 3. Tidak boleh punya baris ledger_entries yang merujuk nomornya
	var ledgerCount int
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM ledger_entries 
		WHERE campaign_number_id IN (SELECT id FROM campaign_numbers WHERE campaign_id = $1)
	`, id).Scan(&ledgerCount)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if ledgerCount > 0 {
		errMsg := i18n.T(lang, "campaign.err_delete_has_ledger")
		http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d?err=%s", id, url.QueryEscape(errMsg)), http.StatusSeeOther)
		return
	}

	// 4. Hapus campaign_numbers jika ada
	_, err = tx.Exec(ctx, "DELETE FROM campaign_numbers WHERE campaign_id = $1", id)
	if err != nil {
		slog.Error("Gagal menghapus nomor kampanye", "err", err, "id", id)
		http.Error(w, "Gagal menghapus kampanye", http.StatusInternalServerError)
		return
	}

	// 5. Hapus number_batches jika ada
	_, err = tx.Exec(ctx, "DELETE FROM number_batches WHERE campaign_id = $1", id)
	if err != nil {
		slog.Error("Gagal menghapus batch nomor kampanye", "err", err, "id", id)
		http.Error(w, "Gagal menghapus kampanye", http.StatusInternalServerError)
		return
	}

	// 6. Hapus campaign
	_, err = tx.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", id)
	if err != nil {
		slog.Error("Gagal menghapus kampanye", "err", err, "id", id)
		http.Error(w, "Gagal menghapus kampanye", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("Gagal commit transaksi penghapusan kampanye", "err", err, "id", id)
		http.Error(w, "Gagal menyelesaikan penghapusan kampanye", http.StatusInternalServerError)
		return
	}

	// Hapus file gambar jika ada
	if imagePath != nil && *imagePath != "" {
		_ = os.Remove(*imagePath)
	}

	s.logAudit(&user.ID, "campaign_delete", "campaign", strconv.FormatInt(id, 10), "campaign deleted")
	http.Redirect(w, r, "/admin/campaigns", http.StatusSeeOther)
}

func (s *Server) HandleAdminCampaignArchive(w http.ResponseWriter, r *http.Request) {
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

	lang := "id"
	if user.Language != "" {
		lang = user.Language
	}

	ctx := r.Context()
	var status string
	var archivedAt *time.Time
	err = s.DB.Pool.QueryRow(ctx, "SELECT status, archived_at FROM campaigns WHERE id = $1 FOR UPDATE", id).Scan(&status, &archivedAt)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if status == campaign.StatusBerjalan {
		errMsg := i18n.T(lang, "campaign.err_archive_running")
		http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d?err=%s", id, url.QueryEscape(errMsg)), http.StatusSeeOther)
		return
	}

	_, err = s.DB.Pool.Exec(ctx, "UPDATE campaigns SET archived_at = CURRENT_TIMESTAMP WHERE id = $1", id)
	if err != nil {
		slog.Error("Gagal mengarsipkan kampanye", "err", err, "id", id)
		http.Error(w, "Terjadi kesalahan sistem", http.StatusInternalServerError)
		return
	}

	s.logAudit(&user.ID, "campaign_archive", "campaign", strconv.FormatInt(id, 10), "campaign archived")
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

// HandleAdminCampaignRequeueUncertain menangani aksi admin POST /admin/campaigns/{id}/requeue-uncertain (Keputusan H1).
// Mengubah nomor status gagal dengan last_error hasil_tidak_pasti kembali menjadi tersedia dengan attempts = 0.
func (s *Server) HandleAdminCampaignRequeueUncertain(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Error(w, "Akses ditolak", http.StatusForbidden)
		return
	}

	res, err := s.DB.Pool.Exec(r.Context(), `
		UPDATE campaign_numbers
		SET status = 'tersedia',
		    last_error = NULL,
		    attempts = 0,
		    claimed_account_id = NULL,
		    claimed_at = NULL
		WHERE campaign_id = $1
		  AND status = 'gagal'
		  AND last_error = 'hasil_tidak_pasti'
	`, id)
	if err != nil {
		slog.Error("Gagal memperbarui status nomor kampanye (requeue uncertain)", "err", err, "id", id)
		http.Error(w, "Terjadi kesalahan sistem", http.StatusInternalServerError)
		return
	}

	count := res.RowsAffected()

	// Tulis audit log: admin_id, aksi "campaign_requeue_uncertain", campaign_id, jumlah nomor yang terpengaruh
	s.logAudit(&user.ID, "campaign_requeue_uncertain", "campaign", strconv.FormatInt(id, 10), fmt.Sprintf(`{"requeued_count":%d}`, count))

	// Beri sinyal ke engine bahwa nomor tersedia bertambah (NotifyCampaignAvailable)
	if s.Engine != nil {
		s.Engine.NotifyCampaignAvailable()
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d", id), http.StatusSeeOther)
}

// HandleAdminCampaignExportNumbers menangani GET /admin/campaigns/{id}/numbers/export (Keputusan R).
// Mengekspor hanya nomor berstatus 'tersedia' dalam format CSV yang siap diimpor ke campaign lain.
func (s *Server) HandleAdminCampaignExportNumbers(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "ID tidak valid", http.StatusBadRequest)
		return
	}

	user, ok := currentUser(r)
	if !ok || user.Role != "admin" {
		http.Error(w, "Akses ditolak: khusus admin", http.StatusForbidden)
		return
	}

	if s.AdminCampaignExportNumbersLimiter != nil {
		key := strconv.FormatInt(user.ID, 10)
		if allowed, wait := s.AdminCampaignExportNumbersLimiter.Take(key); !allowed {
			waitMins := int(wait.Minutes()) + 1
			http.Error(w, fmt.Sprintf("Batas ekspor tercapai (maksimal 5 kali per jam). Coba lagi dalam %d menit.", waitMins), http.StatusTooManyRequests)
			return
		}
	}

	var name, status string
	err = s.DB.Pool.QueryRow(r.Context(), "SELECT name, status FROM campaigns WHERE id = $1", id).Scan(&name, &status)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if status == campaign.StatusBerjalan {
		http.Error(w, "Nomor tidak bisa diekspor saat campaign sedang berjalan. Jeda campaign terlebih dahulu.", http.StatusBadRequest)
		return
	}

	// Ambil semua kunci unik custom_data di nomor campaign ini
	customKeys, err := campaign.GetAvailableCustomKeys(r.Context(), s.DB.Pool, id)
	if err != nil {
		customKeys = nil
	}

	rows, err := campaign.QueryAvailableExportNumbers(r.Context(), s.DB.Pool, id)
	if err != nil {
		slog.Error("Gagal mengambil sisa nomor campaign", "err", err, "id", id)
		http.Error(w, "Terjadi kesalahan sistem", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	rndBytes := make([]byte, 4)
	rand.Read(rndBytes)
	exportFilename := fmt.Sprintf("sisa_nomor_campaign_%d_%s.csv", id, hex.EncodeToString(rndBytes))

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", exportFilename))
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")

	cw := csv.NewWriter(w)

	// Tulis header
	header := []string{"nomor", "nama"}
	header = append(header, customKeys...)
	_ = cw.Write(header)

	exportedCount := 0
	for rows.Next() {
		var phone, recName string
		var rawCustom []byte
		if err := rows.Scan(&phone, &recName, &rawCustom); err != nil {
			continue
		}

		var cMap map[string]string
		if len(rawCustom) > 0 {
			_ = json.Unmarshal(rawCustom, &cMap)
		}

		record := []string{
			sanitizeCSVCell(phone),
			sanitizeCSVCell(recName),
		}
		for _, k := range customKeys {
			val := ""
			if cMap != nil {
				val = cMap[k]
			}
			record = append(record, sanitizeCSVCell(val))
		}

		_ = cw.Write(record)
		exportedCount++
	}
	cw.Flush()

	s.logAudit(&user.ID, "campaign_export_remaining_numbers", "campaign", strconv.FormatInt(id, 10), fmt.Sprintf("berhasil mengekspor %d nomor tersedia", exportedCount))
}

func sanitizeCSVCell(v string) string {
	trimmed := strings.TrimSpace(v)
	if strings.HasPrefix(trimmed, "=") || strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "@") {
		return "'" + v
	}
	return v
}

// HandleAdminCampaignPurgePOST membersihkan dan menganonimkan data customer pada campaign (Keputusan Pemilik Opsi B).
func (s *Server) HandleAdminCampaignPurgePOST(w http.ResponseWriter, r *http.Request) {
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

	lang := "id"
	if user.Language != "" {
		lang = user.Language
	}

	if s.AdminStoragePurgeLimiter != nil {
		key := fmt.Sprintf("admin_purge_%d", user.ID)
		if allowed, wait := s.AdminStoragePurgeLimiter.Take(key); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			errMsg := i18n.T(lang, "campaign.err_purge_rate_limit")
			http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d?err=%s", id, url.QueryEscape(errMsg)), http.StatusSeeOther)
			return
		}
	}

	ctx := r.Context()

	var name, status string
	err = s.DB.Pool.QueryRow(ctx, "SELECT name, status FROM campaigns WHERE id = $1", id).Scan(&name, &status)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	confirmName := strings.TrimSpace(r.FormValue("confirm_name"))
	if confirmName == "" || confirmName != strings.TrimSpace(name) {
		errMsg := i18n.T(lang, "campaign.err_purge_confirm_mismatch")
		http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d?err=%s", id, url.QueryEscape(errMsg)), http.StatusSeeOther)
		return
	}

	deleteImage := r.FormValue("delete_image") == "1" || r.FormValue("delete_image") == "true"
	result, err := campaign.Purge(ctx, s.DB.Pool, id, deleteImage)
	if err != nil {
		if errors.Is(err, campaign.ErrCampaignRunning) {
			errMsg := i18n.T(lang, "campaign.err_purge_running")
			http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d?err=%s", id, url.QueryEscape(errMsg)), http.StatusSeeOther)
			return
		}
		if errors.Is(err, campaign.ErrCampaignHasInFlight) {
			errMsg := i18n.T(lang, "campaign.err_purge_has_inflight")
			http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d?err=%s", id, url.QueryEscape(errMsg)), http.StatusSeeOther)
			return
		}
		slog.Error("Gagal membersihkan campaign", "err", err, "id", id)
		http.Error(w, "Terjadi kesalahan sistem saat pembersihan data", http.StatusInternalServerError)
		return
	}

	auditMeta := fmt.Sprintf("tersedia_deleted: %d, terkirim_anon: %d, gagal_anon: %d, tidak_terdaftar_anon: %d, image_deleted: %v",
		result.TersediaDeleted, result.TerkirimAnon, result.GagalAnon, result.TidakTerdaftarAnon, result.ImageDeleted)
	s.logAudit(&user.ID, "campaign_purge", "campaign", strconv.FormatInt(id, 10), auditMeta)

	msg := i18n.T(lang, "campaign.purge_success")
	http.Redirect(w, r, fmt.Sprintf("/admin/campaigns/%d?msg=%s", id, url.QueryEscape(msg)), http.StatusSeeOther)
}


