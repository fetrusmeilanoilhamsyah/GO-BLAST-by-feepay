package campaign

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func TestNumbersUpload(t *testing.T) {
	// Change working directory to app root for the test
	os.Chdir("../..")

	godotenv.Load() // Loads .env from root

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skip("Database not configured: ", err)
	}
	defer pool.Close()

	os.MkdirAll(filepath.Join("data", "tmp"), 0755)

	// Create user
	var userID int64
	randEmail := fmt.Sprintf("test%d@test.com", time.Now().UnixNano())
	randRef := fmt.Sprintf("REF%d", time.Now().UnixNano())

	err = pool.QueryRow(context.Background(), "INSERT INTO users (email, password_hash, role, currency, referral_code, display_name) VALUES ($1, 'hash', 'admin', 'IDR', $2, 'Test Admin') RETURNING id", randEmail, randRef).Scan(&userID)
	if err != nil {
		t.Fatal("Failed to create user:", err)
	}

	// Create campaign 1
	var c1 int64
	err = pool.QueryRow(context.Background(), "INSERT INTO campaigns (name, status, button_mode, body, elements, created_by) VALUES ('C1', 'draf', 'kartu_link', 'Halo', '{}', $1) RETURNING id", userID).Scan(&c1)
	if err != nil {
		t.Fatal("Failed to create campaign C1:", err)
	}

	// Create campaign 2
	var c2 int64
	err = pool.QueryRow(context.Background(), "INSERT INTO campaigns (name, status, button_mode, body, elements, created_by) VALUES ('C2', 'draf', 'kartu_link', 'Halo', '{}', $1) RETURNING id", userID).Scan(&c2)
	if err != nil {
		t.Fatal("Failed to create campaign C2:", err)
	}

	csvData := []byte("nomor\n081234567890\n081234567890\n081234567891\nabc\n81234567891\n")

	// Test preview C1
	preview, err := ProcessPreview(context.Background(), pool, c1, csvData, "test.csv", "62")
	if err != nil {
		t.Fatal(err)
	}

	if preview.Stats.Total != 5 {
		t.Errorf("Total = %d, want 5", preview.Stats.Total)
	}
	if preview.Stats.DupInFile != 1 {
		t.Errorf("DupInFile = %d, want 1", preview.Stats.DupInFile)
	}
	if preview.Stats.Invalid != 1 {
		t.Errorf("Invalid = %d, want 1", preview.Stats.Invalid)
	}
	if preview.Stats.Ambiguous != 0 {
		t.Errorf("Ambiguous = %d, want 0", preview.Stats.Ambiguous)
	}
	if preview.Stats.Accepted != 3 {
		t.Errorf("Accepted = %d, want 3", preview.Stats.Accepted)
	}
	if preview.Stats.CountryCodes["62"] != 2 {
		t.Errorf("CountryCodes[62] = %d, want 2", preview.Stats.CountryCodes["62"])
	}
	if preview.Stats.CountryCodes["81"] != 1 {
		t.Errorf("CountryCodes[81] = %d, want 1", preview.Stats.CountryCodes["81"])
	}

	// Test invalid preview ID path traversal
	errInvalidID := ConfirmUpload(context.Background(), pool, c1, userID, "../x")
	if errInvalidID == nil {
		t.Errorf("Expected error for invalid previewID '../x'")
	}

	// Test mismatched campaign ID binding
	errMismatched := ConfirmUpload(context.Background(), pool, c2, userID, preview.ID)
	if errMismatched == nil || !strings.Contains(errMismatched.Error(), "tidak cocok dengan campaign") {
		t.Errorf("Expected error about mismatched campaign ID, got %v", errMismatched)
	}

	// Confirm C1
	err = ConfirmUpload(context.Background(), pool, c1, userID, preview.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Verify numbers inserted
	var count int
	pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1", c1).Scan(&count)
	if count != 3 {
		t.Errorf("DB count = %d, want 3", count)
	}

	// Upload again to C1 -> should be duplicate in campaign
	csvData2 := []byte("081234567890\n081234567892")
	preview2, _ := ProcessPreview(context.Background(), pool, c1, csvData2, "test2.csv", "62")
	if preview2.Stats.DupInCampaign != 1 {
		t.Errorf("DupInCampaign = %d, want 1", preview2.Stats.DupInCampaign)
	}
	if preview2.Stats.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1", preview2.Stats.Accepted)
	}

	err = ConfirmUpload(context.Background(), pool, c1, userID, preview2.ID)
	if err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1", c1).Scan(&count)
	if count != 4 {
		t.Errorf("DB count = %d, want 4", count)
	}

	// Upload the same numbers to C2 -> should NOT be duplicate in campaign (lintas campaign)
	preview3, _ := ProcessPreview(context.Background(), pool, c2, csvData, "test3.csv", "62")
	if preview3.Stats.DupInCampaign != 0 {
		t.Errorf("C2 DupInCampaign = %d, want 0", preview3.Stats.DupInCampaign)
	}
	if preview3.Stats.Accepted != 3 {
		t.Errorf("C2 Accepted = %d, want 3", preview3.Stats.Accepted)
	}
	err = ConfirmUpload(context.Background(), pool, c2, userID, preview3.ID)
	if err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1", c2).Scan(&count)
	if count != 3 {
		t.Errorf("C2 DB count = %d, want 3", count)
	}
}
