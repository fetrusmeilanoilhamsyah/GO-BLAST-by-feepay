package web

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"goblast/internal/db"
	"goblast/internal/testutil"
)

func TestCampaignHandlers(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)

	var userID int64
	err := pool.QueryRow(context.Background(), "INSERT INTO users (email, password_hash, role, currency, referral_code, display_name) VALUES ($1, 'hash', 'admin', 'IDR', $2, 'Test Admin') RETURNING id", fmt.Sprintf("testcamp%d@test.com", time.Now().UnixNano()), fmt.Sprintf("REFCAMP%d", time.Now().UnixNano())).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		var campIDs []int64
		rows, err := pool.Query(ctx, "SELECT id FROM campaigns WHERE created_by = $1", userID)
		if err == nil {
			for rows.Next() {
				var cid int64
				if err := rows.Scan(&cid); err == nil {
					campIDs = append(campIDs, cid)
				}
			}
			rows.Close()
		}
		testutil.CleanupTestData(pool, []int64{userID}, campIDs)
	})

	s, err := NewServer(&db.DB{Pool: pool}, "test", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

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

	t.Run("Reject tombol_asli in NewPOST, EditPOST, and Preview", func(t *testing.T) {
		// NewPOST
		bodyNew := new(bytes.Buffer)
		writerNew := multipart.NewWriter(bodyNew)
		writerNew.WriteField("name", "Test Tombol Asli")
		writerNew.WriteField("button_mode", "tombol_asli")
		writerNew.WriteField("body", "Pesan tombol")
		writerNew.Close()

		reqNew := httptest.NewRequest("POST", "/admin/campaigns/new", bodyNew)
		reqNew.Header.Set("Content-Type", writerNew.FormDataContentType())
		rrNew := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignNewPOST)(rrNew, reqNew)
		if rrNew.Code != http.StatusBadRequest || !bytes.Contains(rrNew.Body.Bytes(), []byte("mode tombol_asli dinonaktifkan")) {
			t.Errorf("NewPOST expected 400 with 'mode tombol_asli dinonaktifkan', got %d: %s", rrNew.Code, rrNew.Body.String())
		}

		// EditPOST
		var campDrafID int64
		err := pool.QueryRow(context.Background(), "INSERT INTO campaigns (name, status, button_mode, body, elements, created_by) VALUES ('Test Draf', 'draf', 'kartu_link', 'Halo', '{}', $1) RETURNING id", userID).Scan(&campDrafID)
		if err != nil {
			t.Fatal(err)
		}

		bodyEdit := new(bytes.Buffer)
		writerEdit := multipart.NewWriter(bodyEdit)
		writerEdit.WriteField("name", "Test Edit Tombol")
		writerEdit.WriteField("button_mode", "tombol_asli")
		writerEdit.WriteField("body", "Pesan tombol")
		writerEdit.Close()

		reqEdit := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/edit", campDrafID), bodyEdit)
		reqEdit.SetPathValue("id", fmt.Sprintf("%d", campDrafID))
		reqEdit.Header.Set("Content-Type", writerEdit.FormDataContentType())
		rrEdit := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignEditPOST)(rrEdit, reqEdit)
		if rrEdit.Code != http.StatusBadRequest || !bytes.Contains(rrEdit.Body.Bytes(), []byte("mode tombol_asli dinonaktifkan")) {
			t.Errorf("EditPOST expected 400 with 'mode tombol_asli dinonaktifkan', got %d: %s", rrEdit.Code, rrEdit.Body.String())
		}

		// Preview
		bodyPrev := new(bytes.Buffer)
		writerPrev := multipart.NewWriter(bodyPrev)
		writerPrev.WriteField("button_mode", "tombol_asli")
		writerPrev.WriteField("body", "Pesan tombol")
		writerPrev.Close()

		reqPrev := httptest.NewRequest("POST", "/admin/campaigns/preview", bodyPrev)
		reqPrev.Header.Set("Content-Type", writerPrev.FormDataContentType())
		rrPrev := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignPreview)(rrPrev, reqPrev)
		if rrPrev.Code != http.StatusBadRequest || !bytes.Contains(rrPrev.Body.Bytes(), []byte("mode tombol_asli dinonaktifkan")) {
			t.Errorf("Preview expected 400 with 'mode tombol_asli dinonaktifkan', got %d: %s", rrPrev.Code, rrPrev.Body.String())
		}
	})

	t.Run("Reject traversal existing_image_path in Preview", func(t *testing.T) {
		body := new(bytes.Buffer)
		writer := multipart.NewWriter(body)
		writer.WriteField("button_mode", "kartu_link")
		writer.WriteField("body", "Pesan preview")
		writer.WriteField("existing_image_path", "uploads/../../etc/passwd")
		writer.Close()

		req := httptest.NewRequest("POST", "/admin/campaigns/preview", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rr := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignPreview)(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("Preview traversal expected 400, got %d", rr.Code)
		}
	})

	t.Run("Campaign status transitions and detail page", func(t *testing.T) {
		var cID int64
		err := pool.QueryRow(context.Background(), "INSERT INTO campaigns (name, status, button_mode, body, elements, created_by) VALUES ('Transition Test', 'draf', 'kartu_link', 'Halo', '{}', $1) RETURNING id", userID).Scan(&cID)
		if err != nil {
			t.Fatal(err)
		}

		// 1. Try to start with 0 available numbers -> 400 Bad Request
		reqStart0 := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/start", cID), nil)
		reqStart0.SetPathValue("id", fmt.Sprintf("%d", cID))
		rrStart0 := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignStart)(rrStart0, reqStart0)
		if rrStart0.Code != http.StatusBadRequest || !bytes.Contains(rrStart0.Body.Bytes(), []byte("Tidak ada nomor tersedia")) {
			t.Errorf("Start with 0 numbers expected 400, got %d: %s", rrStart0.Code, rrStart0.Body.String())
		}

		// 2. Try draf -> selesai (directly finish) -> 400 Bad Request
		reqFinDraf := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/finish", cID), nil)
		reqFinDraf.SetPathValue("id", fmt.Sprintf("%d", cID))
		rrFinDraf := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignFinish)(rrFinDraf, reqFinDraf)
		if rrFinDraf.Code != http.StatusBadRequest {
			t.Errorf("draf -> selesai directly expected 400, got %d", rrFinDraf.Code)
		}

		// Insert available number batch and number
		var bID int64
		err = pool.QueryRow(context.Background(), "INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted) VALUES ($1, 'test.csv', $2, 1, 1) RETURNING id", cID, userID).Scan(&bID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(context.Background(), "INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status) VALUES ($1, $2, '62899999999', '62', 'tersedia')", cID, bID)
		if err != nil {
			t.Fatal(err)
		}

		// 3. Detail page GET
		reqDetail := httptest.NewRequest("GET", fmt.Sprintf("/admin/campaigns/%d", cID), nil)
		reqDetail.SetPathValue("id", fmt.Sprintf("%d", cID))
		rrDetail := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignDetail)(rrDetail, reqDetail)
		if rrDetail.Code != http.StatusOK {
			t.Errorf("Detail page expected 200, got %d", rrDetail.Code)
		}
		if !bytes.Contains(rrDetail.Body.Bytes(), []byte("Transition Test")) {
			t.Errorf("Detail page missing campaign name")
		}

		// 4. Valid start: draf -> berjalan
		reqStartValid := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/start", cID), nil)
		reqStartValid.SetPathValue("id", fmt.Sprintf("%d", cID))
		rrStartValid := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignStart)(rrStartValid, reqStartValid)
		if rrStartValid.Code != http.StatusSeeOther {
			t.Errorf("Valid start expected 303 SeeOther, got %d", rrStartValid.Code)
		}

		var st string
		pool.QueryRow(context.Background(), "SELECT status FROM campaigns WHERE id = $1", cID).Scan(&st)
		if st != "berjalan" {
			t.Errorf("Expected status 'berjalan', got %q", st)
		}

		// 5. Try running campaign -> start again (berjalan -> draf or running start) -> 400 Bad Request
		reqStartAgain := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/start", cID), nil)
		reqStartAgain.SetPathValue("id", fmt.Sprintf("%d", cID))
		rrStartAgain := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignStart)(rrStartAgain, reqStartAgain)
		if rrStartAgain.Code != http.StatusBadRequest {
			t.Errorf("Start on running campaign expected 400, got %d", rrStartAgain.Code)
		}

		// 6. Valid finish: berjalan -> selesai
		reqFinValid := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/finish", cID), nil)
		reqFinValid.SetPathValue("id", fmt.Sprintf("%d", cID))
		rrFinValid := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignFinish)(rrFinValid, reqFinValid)
		if rrFinValid.Code != http.StatusSeeOther {
			t.Errorf("Valid finish expected 303 SeeOther, got %d", rrFinValid.Code)
		}

		pool.QueryRow(context.Background(), "SELECT status FROM campaigns WHERE id = $1", cID).Scan(&st)
		if st != "selesai" {
			t.Errorf("Expected status 'selesai', got %q", st)
		}

		// Check audit log
		var logCount int
		pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM audit_logs WHERE entity = 'campaign' AND entity_id = $1 AND action IN ('campaign_start', 'campaign_finish')", fmt.Sprintf("%d", cID)).Scan(&logCount)
		if logCount != 2 {
			t.Errorf("Expected 2 audit logs (start & finish), got %d", logCount)
		}
	})

	t.Run("Reject previewID traversal in numbers confirm and rejected", func(t *testing.T) {
		reqConf := httptest.NewRequest("POST", "/admin/campaigns/1/numbers/confirm", strings.NewReader("preview_id=../x"))
		reqConf.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqConf.SetPathValue("id", "1")
		rrConf := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignNumbersConfirm)(rrConf, reqConf)
		if rrConf.Code != http.StatusBadRequest {
			t.Errorf("Confirm invalid previewID expected 400, got %d", rrConf.Code)
		}

		reqRej := httptest.NewRequest("GET", "/admin/campaigns/1/numbers/rejected/../x", nil)
		reqRej.SetPathValue("previewId", "../x")
		rrRej := httptest.NewRecorder()
		withUser(s.HandleAdminCampaignNumbersRejected)(rrRej, reqRej)
		if rrRej.Code != http.StatusBadRequest {
			t.Errorf("Rejected invalid previewID expected 400, got %d", rrRej.Code)
		}
	})
}
