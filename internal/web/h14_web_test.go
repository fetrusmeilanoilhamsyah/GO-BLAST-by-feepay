package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/store"
	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/testutil"
	"goblast/internal/wa"
	"goblast/internal/wa/watest"
)

func TestH14_WorkerPairing_PhoneValidation(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	ctx := context.Background()

	database := &db.DB{Pool: pool}
	_, _ = pool.Exec(ctx, "INSERT INTO settings (key, value) VALUES ('max_accounts_per_worker', '10'::jsonb) ON CONFLICT (key) DO UPDATE SET value = '10'::jsonb")
	cfg := &config.Config{
		DatabaseURL:       testutil.TestDBURL(t),
		MaxActiveSessions: 50,
		EncryptionKey:     []byte("12345678901234567890123456789012"),
	}
	settingsReader := wa.NewDBSettingsReader(database)
	mgr := wa.NewManager(database, settingsReader, cfg)

	var pairedPhone string
	mgr.SetClientFactory(func(_ *store.Device) wa.WAClient {
		fakeCli := watest.NewFakeClient()
		fakeCli.SetConnected(false)
		fakeCli.SetLoggedIn(false)
		fakeCli.PairPhoneFunc = func(phone string) (string, error) {
			pairedPhone = phone
			return "1234-5678", nil
		}
		return fakeCli
	})

	s, err := NewServer(database, "test", mgr, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	tests := []struct {
		name        string
		phoneInput  string
		expectedErr string
		expectPair  string
	}{
		{
			name:        "Empty phone",
			phoneInput:  "",
			expectedErr: "Nomor HP wajib diisi",
		},
		{
			name:        "Leading 0 Indonesia rejected",
			phoneInput:  "081234567890",
			expectedErr: "tulis dengan kode negara, misalnya +62...",
		},
		{
			name:        "Leading 0 UK rejected",
			phoneInput:  "07911123456",
			expectedErr: "tulis dengan kode negara, misalnya +62...",
		},
		{
			name:        "Fixed-line phone rejected",
			phoneInput:  "+62211234567",
			expectedErr: "Nomor harus berupa nomor seluler WhatsApp",
		},
		{
			name:        "Non-number string rejected",
			phoneInput:  "invalidphone",
			expectedErr: "Nomor telepon tidak valid",
		},
		{
			name:       "Valid Indonesia mobile with +",
			phoneInput: "+6281234567890",
			expectPair: "6281234567890",
		},
		{
			name:       "Valid UK mobile with +",
			phoneInput: "+447911123456",
			expectPair: "447911123456",
		},
		{
			name:       "Valid Malaysia mobile with +",
			phoneInput: "+60123456789",
			expectPair: "60123456789",
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pairedPhone = ""

			var workerID, accountID int64
			err := pool.QueryRow(ctx,
				"INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'hash', 'worker', $2, 'Worker H14') RETURNING id",
				fmt.Sprintf("worker_h14_%d_%d@test.com", time.Now().UnixNano(), i),
				fmt.Sprintf("REF_H14_%d_%d", time.Now().UnixNano(), i),
			).Scan(&workerID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				testutil.CleanupTestData(pool, []int64{workerID}, nil)
			})

			err = pool.QueryRow(ctx,
				"INSERT INTO wa_accounts (worker_id, label, speed, status) VALUES ($1, 'WA Test', 'medium', 'menunggu_scan') RETURNING id",
				workerID,
			).Scan(&accountID)
			if err != nil {
				t.Fatal(err)
			}

			formData := url.Values{}
			formData.Set("phone", tc.phoneInput)

			req := httptest.NewRequest("POST", fmt.Sprintf("/worker/accounts/%d/pair", accountID), strings.NewReader(formData.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", "application/json") // triggers JSON response
			req.SetPathValue("id", fmt.Sprintf("%d", accountID))

			ctxWithUser := context.WithValue(req.Context(), userContextKey, &UserSession{ID: workerID, Role: "worker"})
			req = req.WithContext(ctxWithUser)

			rr := httptest.NewRecorder()
			s.HandleWorkerAccountPairPOST(rr, req)

			var res map[string]interface{}
			_ = json.Unmarshal(rr.Body.Bytes(), &res)

			if tc.expectedErr != "" {
				errMsg, _ := res["error"].(string)
				if !strings.Contains(errMsg, tc.expectedErr) {
					t.Fatalf("expected error containing %q, got %q (body: %s)", tc.expectedErr, errMsg, rr.Body.String())
				}
			} else {
				okVal, _ := res["ok"].(bool)
				if !okVal {
					t.Fatalf("expected ok=true, got %s", rr.Body.String())
				}
				if pairedPhone != tc.expectPair {
					t.Fatalf("expected PairPhone called with %q, got %q", tc.expectPair, pairedPhone)
				}
			}
		})
	}
}

func TestH14_Campaign_DefaultCountry_Flow(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	ctx := context.Background()

	var adminID int64
	err := pool.QueryRow(ctx,
		"INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'hash', 'admin', $2, 'Admin H14 Flow') RETURNING id",
		fmt.Sprintf("admin_h14_%d@test.com", time.Now().UnixNano()),
		fmt.Sprintf("REF_H14_A_%d", time.Now().UnixNano()),
	).Scan(&adminID)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID}, nil)
	})

	database := &db.DB{Pool: pool}
	s, err := NewServer(database, "test", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// 1. Admin Settings: Update default_country to 'MY'
	formSettings := url.Values{}
	formSettings.Set("rate_wage_idr", "30")
	formSettings.Set("rate_idr_to_usdt", "15000")
	formSettings.Set("rate_referral_idr", "5")
	formSettings.Set("max_accounts_per_worker", "5")
	formSettings.Set("default_country", "MY")

	reqSettings := httptest.NewRequest("POST", "/admin/settings", strings.NewReader(formSettings.Encode()))
	reqSettings.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSettings = reqSettings.WithContext(context.WithValue(reqSettings.Context(), userContextKey, &UserSession{ID: adminID, Role: "admin"}))

	rrSettings := httptest.NewRecorder()
	s.HandleAdminSettingsPOST(rrSettings, reqSettings)
	if rrSettings.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect from settings POST, got %d, body: %s", rrSettings.Code, rrSettings.Body.String())
	}

	// Verify setting saved in DB
	var savedSetting string
	err = pool.QueryRow(ctx, "SELECT value::text FROM settings WHERE key = 'default_country'").Scan(&savedSetting)
	if err != nil || savedSetting != `"MY"` {
		t.Fatalf("expected default_country setting to be '\"MY\"', got %q, err: %v", savedSetting, err)
	}

	// Verify audit log
	var auditAction string
	err = pool.QueryRow(ctx, "SELECT action FROM audit_logs WHERE entity = 'settings' AND entity_id = 'default_country' ORDER BY id DESC LIMIT 1").Scan(&auditAction)
	if err != nil || auditAction != "setting_update" {
		t.Fatalf("expected audit log setting_update, got %q, err: %v", auditAction, err)
	}

	// 2. Create new campaign with default_country = 'GB'
	bodyBuf := &bytes.Buffer{}
	mp := multipart.NewWriter(bodyBuf)
	_ = mp.WriteField("name", "Campaign H14 UK")
	_ = mp.WriteField("button_mode", "kartu_link")
	_ = mp.WriteField("body", "Hello UK")
	_ = mp.WriteField("default_country", "GB")
	_ = mp.Close()

	reqCamp := httptest.NewRequest("POST", "/admin/campaigns/new", bodyBuf)
	reqCamp.Header.Set("Content-Type", mp.FormDataContentType())
	reqCamp = reqCamp.WithContext(context.WithValue(reqCamp.Context(), userContextKey, &UserSession{ID: adminID, Role: "admin"}))

	rrCamp := httptest.NewRecorder()
	s.HandleAdminCampaignNewPOST(rrCamp, reqCamp)
	if rrCamp.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect from campaign new POST, got %d, body: %s", rrCamp.Code, rrCamp.Body.String())
	}

	var campID int64
	var campDefCountry string
	err = pool.QueryRow(ctx, "SELECT id, default_country FROM campaigns WHERE name = 'Campaign H14 UK'").Scan(&campID, &campDefCountry)
	if err != nil {
		t.Fatalf("failed to query new campaign: %v", err)
	}
	if campDefCountry != "GB" {
		t.Fatalf("expected campaign default_country to be 'GB', got %q", campDefCountry)
	}

	// 3. Edit campaign: change default_country to 'SG'
	bodyEdit := &bytes.Buffer{}
	mpEdit := multipart.NewWriter(bodyEdit)
	_ = mpEdit.WriteField("name", "Campaign H14 SG")
	_ = mpEdit.WriteField("button_mode", "kartu_link")
	_ = mpEdit.WriteField("body", "Hello SG")
	_ = mpEdit.WriteField("default_country", "SG")
	_ = mpEdit.Close()

	reqEdit := httptest.NewRequest("POST", fmt.Sprintf("/admin/campaigns/%d/edit", campID), bodyEdit)
	reqEdit.Header.Set("Content-Type", mpEdit.FormDataContentType())
	reqEdit.SetPathValue("id", fmt.Sprintf("%d", campID))
	reqEdit = reqEdit.WithContext(context.WithValue(reqEdit.Context(), userContextKey, &UserSession{ID: adminID, Role: "admin"}))

	rrEdit := httptest.NewRecorder()
	s.HandleAdminCampaignEditPOST(rrEdit, reqEdit)
	if rrEdit.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect from campaign edit POST, got %d, body: %s", rrEdit.Code, rrEdit.Body.String())
	}

	err = pool.QueryRow(ctx, "SELECT default_country FROM campaigns WHERE id = $1", campID).Scan(&campDefCountry)
	if err != nil || campDefCountry != "SG" {
		t.Fatalf("expected updated campaign default_country 'SG', got %q, err: %v", campDefCountry, err)
	}
}

func TestH14_Campaign_NumberUpload_International(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	ctx := context.Background()

	var adminID int64
	err := pool.QueryRow(ctx,
		"INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'hash', 'admin', $2, 'Admin H14 Upload') RETURNING id",
		fmt.Sprintf("admin_h14_up_%d@test.com", time.Now().UnixNano()),
		fmt.Sprintf("REF_H14_U_%d", time.Now().UnixNano()),
	).Scan(&adminID)
	if err != nil {
		t.Fatal(err)
	}

	var campID int64
	err = pool.QueryRow(ctx,
		"INSERT INTO campaigns (name, status, button_mode, body, elements, created_by, default_country) VALUES ('UK Campaign', 'draf', 'kartu_link', 'UK Test', '{}', $1, 'GB') RETURNING id",
		adminID,
	).Scan(&campID)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{adminID}, []int64{campID})
	})

	// Upload content:
	// 07911123456 -> UK national mobile format -> 447911123456 (accepted)
	// 02079460000 -> UK fixed-line -> bukan_seluler (rejected)
	// +6281234567890 -> ID international mobile -> 6281234567890 (accepted, foreign to GB)
	uploadContent := "07911123456\n02079460000\n+6281234567890\n"

	preview, err := campaign.ProcessPreview(ctx, pool, campID, []byte(uploadContent), "uk_test.txt")
	if err != nil {
		t.Fatalf("ProcessPreview failed: %v", err)
	}

	if preview.Stats.Total != 3 {
		t.Fatalf("expected total 3, got %d", preview.Stats.Total)
	}
	if preview.Stats.Accepted != 2 {
		t.Fatalf("expected accepted 2, got %d", preview.Stats.Accepted)
	}
	if preview.Stats.NotMobile != 1 {
		t.Fatalf("expected NotMobile 1, got %d", preview.Stats.NotMobile)
	}
	if preview.Stats.CountryCodes["44"] != 1 {
		t.Fatalf("expected CountryCodes['44'] == 1, got %d", preview.Stats.CountryCodes["44"])
	}
	if preview.Stats.CountryCodes["62"] != 1 {
		t.Fatalf("expected CountryCodes['62'] == 1, got %d", preview.Stats.CountryCodes["62"])
	}

	// Confirm upload
	err = campaign.ConfirmUpload(ctx, pool, campID, adminID, preview.ID)
	if err != nil {
		t.Fatalf("ConfirmUpload failed: %v", err)
	}

	// Check DB rows in campaign_numbers
	rows, err := pool.Query(ctx, "SELECT phone_e164, country_code FROM campaign_numbers WHERE campaign_id = $1 ORDER BY phone_e164", campID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var results []struct {
		Phone string
		CC    string
	}
	for rows.Next() {
		var p, cc string
		if err := rows.Scan(&p, &cc); err != nil {
			t.Fatal(err)
		}
		results = append(results, struct {
			Phone string
			CC    string
		}{p, cc})
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 numbers in DB, got %d", len(results))
	}
	if results[0].Phone != "447911123456" || results[0].CC != "44" {
		t.Errorf("expected 447911123456 / 44, got %s / %s", results[0].Phone, results[0].CC)
	}
	if results[1].Phone != "6281234567890" || results[1].CC != "62" {
		t.Errorf("expected 6281234567890 / 62, got %s / %s", results[1].Phone, results[1].CC)
	}
}
