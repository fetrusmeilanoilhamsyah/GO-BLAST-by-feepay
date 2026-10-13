package testutil_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"goblast/internal/testutil"
)

func TestTestDBSafetyChecks(t *testing.T) {
	pool := testutil.TestDB(t)
	if pool == nil {
		t.Fatal("Expected non-nil pool from TestDB")
	}

	testDSN := os.Getenv("TEST_DATABASE_URL")
	if !strings.Contains(testDSN, "_test") {
		t.Fatalf("TEST_DATABASE_URL should contain _test")
	}

	// Test cleanup with ledger entry
	ctx := context.Background()
	var uid int64
	err := pool.QueryRow(ctx, "INSERT INTO users (email, password_hash, role, referral_code, display_name) VALUES ($1, 'h', 'worker', $2, 'U') RETURNING id",
		fmt.Sprintf("test_u_%d@test.com", time.Now().UnixNano()), fmt.Sprintf("REF_%d", time.Now().UnixNano())).Scan(&uid)
	if err != nil {
		t.Fatalf("insert user failed: %v", err)
	}

	_, err = pool.Exec(ctx, "INSERT INTO ledger_entries (user_id, kind, currency, amount) VALUES ($1, 'koreksi', 'IDR', 100)", uid)
	if err != nil {
		t.Fatalf("insert ledger failed: %v", err)
	}

	// Test disabling trigger for clean deletion
	_, err = pool.Exec(ctx, "ALTER TABLE ledger_entries DISABLE TRIGGER trg_prevent_ledger_update_delete")
	if err != nil {
		t.Fatalf("disable trigger failed: %v", err)
	}
	_, err = pool.Exec(ctx, "DELETE FROM ledger_entries WHERE user_id = $1", uid)
	if err != nil {
		t.Fatalf("delete ledger with where failed: %v", err)
	}
	_, err = pool.Exec(ctx, "ALTER TABLE ledger_entries ENABLE TRIGGER trg_prevent_ledger_update_delete")
	if err != nil {
		t.Fatalf("enable trigger failed: %v", err)
	}
	_, err = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", uid)
	if err != nil {
		t.Fatalf("delete user failed: %v", err)
	}
}
