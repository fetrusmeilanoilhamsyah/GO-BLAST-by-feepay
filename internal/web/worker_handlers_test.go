package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"goblast/internal/db"
	"goblast/internal/i18n"
)

func TestWorkerPanelFlows(t *testing.T) {
	root := findProjectRoot(t)
	_ = i18n.Load(root + "/locales")
	godotenv.Load(root + "/.env")

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skip("Database connection failed: ", err)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		t.Skip("Database not reachable: ", err)
	}

	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	// Create test worker user
	now := time.Now().UnixNano()
	email := fmt.Sprintf("worker_test_%d@example.com", now)
	refCode := fmt.Sprintf("RF%d", now)

	var workerID int64
	err = pool.QueryRow(context.Background(), `
		INSERT INTO users (email, password_hash, role, display_name, language, currency, referral_code)
		VALUES ($1, 'hash_pass', 'worker', 'Pekerja Uji', 'id', 'IDR', $2)
		RETURNING id
	`, email, refCode).Scan(&workerID)
	if err != nil {
		t.Fatalf("Failed to insert test worker: %v", err)
	}

	srv, err := NewServer(&db.DB{Pool: pool}, "test")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	userSess := UserSession{
		ID:          workerID,
		Role:        "worker",
		Email:       email,
		DisplayName: "Pekerja Uji",
		Language:    "id",
		Currency:    "IDR",
	}

	withWorker := func(req *http.Request) *http.Request {
		ctx := context.WithValue(req.Context(), userContextKey, userSess)
		return req.WithContext(ctx)
	}

	t.Run("HandleWorkerHome rendering", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/worker", nil)
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleWorkerHome(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "Pekerja Uji") {
			t.Errorf("Expected body to contain worker display name")
		}
		if !strings.Contains(body, "GO BLAST") {
			t.Errorf("Expected body to contain GO BLAST brand")
		}
		if !strings.Contains(body, "by feepay") {
			t.Errorf("Expected body to contain by feepay tagline")
		}
	})

	t.Run("HandleWorkerWithdrawalsGET rendering", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/worker/withdrawals", nil)
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleWorkerWithdrawalsGET(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "Penarikan") {
			t.Errorf("Expected body to contain Penarikan title")
		}
	})

	t.Run("HandleWorkerWithdrawalsPOST validation", func(t *testing.T) {
		// 1. Invalid amount
		{
			form := url.Values{
				"amount":       {"-5000"},
				"method":       {"dana"},
				"destination":  {"081234567890"},
				"account_name": {"Budi"},
			}
			req := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerWithdrawalsPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
				t.Errorf("Expected redirect with error for negative amount, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}
		}

		// 2. Unsupported method
		{
			form := url.Values{
				"amount":       {"10000"},
				"method":       {"paypal"},
				"destination":  {"budi@test.com"},
				"account_name": {"Budi"},
			}
			req := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerWithdrawalsPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
				t.Errorf("Expected redirect with error for unsupported method, got code %d", rr.Code)
			}
		}

		// 3. Insufficient balance (balance is 0)
		{
			form := url.Values{
				"amount":       {"50000"},
				"method":       {"dana"},
				"destination":  {"081234567890"},
				"account_name": {"Budi"},
			}
			req := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerWithdrawalsPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "error=") {
				t.Errorf("Expected redirect with insufficient balance error, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}
		}

		// 4. Successful withdrawal with sufficient balance
		{
			// Give user balance via upah in ledger_entries
			_, err := pool.Exec(context.Background(), `
				INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
				VALUES ($1, 'upah', 'IDR', 100000, 'Upah tes')
			`, workerID)
			if err != nil {
				t.Fatalf("Failed to credit test balance: %v", err)
			}

			form := url.Values{
				"amount":       {"40000"},
				"method":       {"seabank"},
				"destination":  {"9012345678"},
				"account_name": {"Pekerja Uji"},
			}
			req := httptest.NewRequest("POST", "/worker/withdrawals", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withWorker(req)
			rr := httptest.NewRecorder()

			srv.HandleWorkerWithdrawalsPOST(rr, req)
			if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "success=1") {
				t.Errorf("Expected redirect with success=1, got code %d, loc %s", rr.Code, rr.Header().Get("Location"))
			}

			// Verify remaining balance is 60,000
			var remaining int64
			_ = pool.QueryRow(context.Background(), "SELECT user_balance($1, 'IDR')", workerID).Scan(&remaining)
			if remaining != 60000 {
				t.Errorf("Expected remaining balance 60000, got %d", remaining)
			}
		}
	})

	t.Run("HandleWorkerReferralGET rendering", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/worker/referral", nil)
		req = withWorker(req)
		rr := httptest.NewRecorder()

		srv.HandleWorkerReferralGET(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, refCode) {
			t.Errorf("Expected body to contain referral code %s", refCode)
		}
		if !strings.Contains(body, "register?ref="+refCode) {
			t.Errorf("Expected body to contain referral link with refCode")
		}
	})
}
