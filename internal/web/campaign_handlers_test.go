package web

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"goblast/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func TestCampaignHandlers(t *testing.T) {
	os.Chdir("../..")
	godotenv.Load()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skip("Database not configured: ", err)
	}
	defer pool.Close()
	
	var userID int64
	err = pool.QueryRow(context.Background(), "INSERT INTO users (email, password_hash, role, currency, referral_code, display_name) VALUES ($1, 'hash', 'admin', 'IDR', $2, 'Test Admin') RETURNING id", fmt.Sprintf("testcamp%d@test.com", time.Now().UnixNano()), fmt.Sprintf("REFCAMP%d", time.Now().UnixNano())).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}

	s, _ := NewServer(&db.DB{Pool: pool}, "test")
	
	withUser := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), userContextKey, &UserSession{ID: userID, Role: "admin"})
			r = r.WithContext(ctx)
			h(w, r)
		}
	}

	t.Run("Create campaign with fake png", func(t *testing.T) {
		body := new(bytes.Buffer)
		writer := multipart.NewWriter(body)
		
		writer.WriteField("name", "Test C1")
		writer.WriteField("button_mode", "kartu_link")
		writer.WriteField("body", "FEEPAY promo")
		writer.WriteField("cs_number", "62812")
		
		part, _ := writer.CreateFormFile("image", "fake.png")
		part.Write([]byte("this is just text, not a real image"))
		
		writer.Close()

		req := httptest.NewRequest("POST", "/admin/campaigns/new", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rr := httptest.NewRecorder()

		withUser(s.HandleAdminCampaignNewPOST)(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("Expected 400 Bad Request for fake image, got %d", rr.Code)
		}
		if !bytes.Contains(rr.Body.Bytes(), []byte("Hanya menerima gambar JPEG atau PNG")) {
			t.Errorf("Expected error message about image format")
		}
	})

	t.Run("Edit running campaign rejected", func(t *testing.T) {
		var campID int64
		err := pool.QueryRow(context.Background(), "INSERT INTO campaigns (name, status, button_mode, body, elements, created_by) VALUES ('Test Run', 'berjalan', 'kartu_link', 'FEEPAY', '{}', $1) RETURNING id", userID).Scan(&campID)
		if err != nil {
			t.Fatal(err)
		}

		body := new(bytes.Buffer)
		writer := multipart.NewWriter(body)
		writer.WriteField("name", "Test Edit")
		writer.WriteField("button_mode", "kartu_link")
		writer.WriteField("body", "FEEPAY")
		writer.WriteField("cs_number", "62812")
		writer.Close()

		req := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/edit", campID), body)
		req.SetPathValue("id", fmt.Sprintf("%d", campID))
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rr := httptest.NewRecorder()

		withUser(s.HandleAdminCampaignEditPOST)(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden when editing running campaign, got %d", rr.Code)
		}
	})
}
