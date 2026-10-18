package web

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"goblast/internal/auth"
	"goblast/internal/db"
	"goblast/internal/testutil"
)

// TestGuard_RoutesClassificationAndCSRF memverifikasi secara statis pada RegisterRoutes di web.go
// bahwa:
// 1. Setiap rute POST dilindungi oleh CSRFMiddleware (kecuali webhook luar yang terdokumentasi).
// 2. Setiap rute POST memiliki RateLimit middleware atau handler khusus yang membatasi laju.
// 3. Setiap endpoint GET JSON / API memiliki RateLimit middleware.
func TestGuard_RoutesClassificationAndCSRF(t *testing.T) {
	root := findProjectRoot(t)
	webFile := filepath.Join(root, "internal", "web", "web.go")

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, webFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Gagal mem-parse %s: %v", webFile, err)
	}

	var registerRoutesFunc *ast.FuncDecl
	for _, decl := range node.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "RegisterRoutes" {
			registerRoutesFunc = fn
			break
		}
	}

	if registerRoutesFunc == nil {
		t.Fatalf("Fungsi RegisterRoutes tidak ditemukan di %s", webFile)
	}

	var postRoutesWithoutCSRF []string
	var postRoutesWithoutRateLimit []string
	var apiGetRoutesWithoutRateLimit []string
	var publicRoutesWithoutRateLimit []string

	ast.Inspect(registerRoutesFunc.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Cari pemanggilan handle("METHOD /path", handler)
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "handle" || len(call.Args) < 2 {
			return true
		}

		routeLit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || routeLit.Kind != token.STRING {
			return true
		}
		routeStr := strings.Trim(routeLit.Value, "\"")

		// Handler expression sebagai string representasi
		handlerNode := call.Args[1]
		var handlerCode strings.Builder
		_ = printerPrint(&handlerCode, fset, handlerNode)
		hStr := handlerCode.String()

		// 1. POST route checks
		if strings.HasPrefix(routeStr, "POST ") {
			if !strings.Contains(hStr, "CSRFMiddleware") {
				postRoutesWithoutCSRF = append(postRoutesWithoutCSRF, routeStr)
			}
			// Rate limiting check
			hasRateLimit := strings.Contains(hStr, "RateLimit") ||
				strings.Contains(hStr, "Limiter") ||
				strings.Contains(hStr, "handleRegister") || // memiliki RegisterHourlyLimiter & DailyLimiter di dalam
				strings.Contains(hStr, "handleLogin") || // memiliki LoginPairLimiter di dalam
				strings.Contains(hStr, "HandleLanguagePOST") || // rate limited by PublicRouteLimiter
				strings.Contains(hStr, "HandleThemePOST") || // rate limited by PublicRouteLimiter
				strings.Contains(hStr, "HandleWorkerProfilePOST") || // WorkerSecurityLimiter
				strings.Contains(hStr, "HandleWorkerSecurityPOST") || // WorkerSecurityLimiter
				strings.Contains(hStr, "HandleWorkerWalletPOST") || // WorkerFinancialLimiter
				strings.Contains(hStr, "HandleWorkerWithdrawalsPOST") || // WorkerFinancialLimiter
				strings.Contains(hStr, "HandleWorkerConvertPOST") || // WorkerFinancialLimiter
				strings.Contains(hStr, "HandleWorkerTicketPOST") || // WorkerTicket10MinLimiter & Daily
				strings.Contains(hStr, "HandleWorkerAccountInitPOST") || // WorkerAccountUserLimiter & IPLimiter
				strings.Contains(hStr, "HandleWorkerAccountNewPOST") || // WorkerAccountUserLimiter & IPLimiter
				strings.Contains(hStr, "HandleWorkerAccountPairPOST") || // WorkerAccountUserLimiter & IPLimiter
				strings.Contains(hStr, "HandleWorkerAccountSettingsPOST") || // WorkerAccountUserLimiter & IPLimiter
				strings.Contains(hStr, "HandleWorkerAccountDelete") || // WorkerAccountUserLimiter & IPLimiter
				strings.Contains(hStr, "HandleWorkerAccountDisconnect") || // WorkerAccountUserLimiter & IPLimiter
				strings.Contains(hStr, "HandleWorkerAccountStop") || // WorkerAccountUserLimiter & IPLimiter
				strings.Contains(hStr, "HandleWorkerAccountWorkStart") || // WorkerWorkCtrlLimiter
				strings.Contains(hStr, "HandleWorkerAccountWorkStop") // WorkerWorkCtrlLimiter
			if !hasRateLimit {
				postRoutesWithoutRateLimit = append(postRoutesWithoutRateLimit, routeStr)
			}
		}

		// 2. GET JSON/API route checks
		if strings.HasPrefix(routeStr, "GET ") && strings.Contains(routeStr, "/api/") {
			hasRateLimit := strings.Contains(hStr, "RateLimit") || strings.Contains(hStr, "Limiter")
			if !hasRateLimit {
				apiGetRoutesWithoutRateLimit = append(apiGetRoutesWithoutRateLimit, routeStr)
			}
		}

		// 3. Public routes classification check (wajib kelas RateLimitPublicMiddleware)
		if routeStr == "/" || routeStr == "GET /syarat" || routeStr == "GET /privasi" || routeStr == "GET /teknologi" {
			if !strings.Contains(hStr, "RateLimitPublicMiddleware") {
				publicRoutesWithoutRateLimit = append(publicRoutesWithoutRateLimit, routeStr)
			}
		}

		return true
	})

	if len(postRoutesWithoutCSRF) > 0 {
		t.Errorf("GUARD GAGAL: Ditemukan rute POST tanpa perlindungan CSRF:\n%s", strings.Join(postRoutesWithoutCSRF, "\n"))
	}
	if len(postRoutesWithoutRateLimit) > 0 {
		t.Errorf("GUARD GAGAL: Ditemukan rute POST tanpa rate limiter:\n%s", strings.Join(postRoutesWithoutRateLimit, "\n"))
	}
	if len(apiGetRoutesWithoutRateLimit) > 0 {
		t.Errorf("GUARD GAGAL: Ditemukan rute GET API tanpa rate limiter:\n%s", strings.Join(apiGetRoutesWithoutRateLimit, "\n"))
	}
	if len(publicRoutesWithoutRateLimit) > 0 {
		t.Errorf("GUARD GAGAL: Ditemukan rute publik tanpa RateLimitPublicMiddleware:\n%s", strings.Join(publicRoutesWithoutRateLimit, "\n"))
	}
}

// printerPrint mencetak AST node kembali ke teks representasi Go
func printerPrint(w *strings.Builder, fset *token.FileSet, node ast.Node) error {
	switch n := node.(type) {
	case *ast.CallExpr:
		_ = printerPrint(w, fset, n.Fun)
		w.WriteString("(")
		for i, arg := range n.Args {
			if i > 0 {
				w.WriteString(", ")
			}
			_ = printerPrint(w, fset, arg)
		}
		w.WriteString(")")
	case *ast.SelectorExpr:
		_ = printerPrint(w, fset, n.X)
		w.WriteString("." + n.Sel.Name)
	case *ast.Ident:
		w.WriteString(n.Name)
	case *ast.BasicLit:
		w.WriteString(n.Value)
	}
	return nil
}

// TestGuard_SQL_NoManualConcat memindai seluruh file .go dalam internal/ untuk memastikan
// pemanggilan DB (Query, QueryRow, Exec) tidak menggunakan penggabungan string (string concat / binary '+')
// melainkan selalu parameterized query ($1, $2, ...).
func TestGuard_SQL_NoManualConcat(t *testing.T) {
	root := findProjectRoot(t)

	var violations []string

	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("Gagal parse %s: %v", rel, err)
		}

		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			method := sel.Sel.Name
			if method != "Query" && method != "QueryRow" && method != "Exec" {
				return true
			}

			if len(call.Args) < 2 {
				return true
			}

			// Argumen kueri SQL biasanya argumen ke-2 (setelah ctx) atau argumen ke-1
			var queryArg ast.Expr
			if len(call.Args) >= 2 {
				queryArg = call.Args[1]
			} else {
				queryArg = call.Args[0]
			}

			// Periksa apakah argumen kueri adalah binary expression dengan '+' (string concatenation dinamis)
			if bin, isBin := queryArg.(*ast.BinaryExpr); isBin && bin.Op == token.ADD {
				// Kecualikan jika kedua sisi adalah string literal konstanta murni
				if !isPureConstConcat(bin) {
					pos := fset.Position(call.Pos())
					violations = append(violations, fmt.Sprintf("%s:%d %s call contains string concatenation (+)", rel, pos.Line, method))
				}
			}

			return true
		})

		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	if len(violations) > 0 {
		t.Errorf("GUARD GAGAL: Terdeteksi kueri database dengan string concatenation:\n%s", strings.Join(violations, "\n"))
	}
}

func isPureConstConcat(bin *ast.BinaryExpr) bool {
	leftConst := false
	rightConst := false

	if _, ok := bin.X.(*ast.BasicLit); ok {
		leftConst = true
	} else if b, ok := bin.X.(*ast.BinaryExpr); ok {
		leftConst = isPureConstConcat(b)
	}

	if _, ok := bin.Y.(*ast.BasicLit); ok {
		rightConst = true
	} else if b, ok := bin.Y.(*ast.BinaryExpr); ok {
		rightConst = isPureConstConcat(b)
	}

	return leftConst && rightConst
}

// TestGuard_IDOR_CrossWorkerAccountsAndTickets memverifikasi bahwa akun worker B tidak dapat
// dimanipulasi oleh worker A (IDOR rejection status 404).
func TestGuard_IDOR_CrossWorkerAccountsAndTickets(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. Buat Worker A
	uniqueSuffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var workerAID int64
	err = database.Pool.QueryRow(ctx, `
		INSERT INTO users (username, display_name, password_hash, role, status, referral_code)
		VALUES ($1, $1, 'hash123', 'worker', 'active', $2)
		RETURNING id
	`, "worker_a_"+uniqueSuffix, "REFA_"+uniqueSuffix).Scan(&workerAID)
	if err != nil {
		t.Fatalf("Gagal membuat worker A: %v", err)
	}

	// 2. Buat Worker B
	var workerBID int64
	err = database.Pool.QueryRow(ctx, `
		INSERT INTO users (username, display_name, password_hash, role, status, referral_code)
		VALUES ($1, $1, 'hash123', 'worker', 'active', $2)
		RETURNING id
	`, "worker_b_"+uniqueSuffix, "REFB_"+uniqueSuffix).Scan(&workerBID)
	if err != nil {
		t.Fatalf("Gagal membuat worker B: %v", err)
	}

	// 3. Buat WhatsApp Account milik Worker A
	var waAccAID int64
	err = database.Pool.QueryRow(ctx, `
		INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, speed, status)
		VALUES ($1, 'Akun WA A', $2, 'medium', 'terhubung')
		RETURNING id
	`, workerAID, "hash_jid_"+uniqueSuffix).Scan(&waAccAID)
	if err != nil {
		t.Fatalf("Gagal membuat wa_account A: %v", err)
	}

	// Helper membuat sesi worker B
	withWorkerB := func(req *http.Request) *http.Request {
		rawToken, hashToken, _ := auth.GenerateSessionToken()
		_, _ = database.Pool.Exec(ctx, `
			INSERT INTO web_sessions (user_id, token_hash, expires_at, ip, user_agent)
			VALUES ($1, $2, $3, '127.0.0.1', 'test-agent')
		`, workerBID, hashToken, time.Now().Add(1*time.Hour))

		req.AddCookie(&http.Cookie{
			Name:  srv.sessionCookieName(),
			Value: rawToken,
		})
		cReq := req.WithContext(context.WithValue(req.Context(), userContextKey, &UserSession{
			ID:       workerBID,
			Username: "worker_b_" + uniqueSuffix,
			Role:     "worker",
		}))
		return cReq
	}

	accIDStr := strconv.FormatInt(waAccAID, 10)

	// Uji aksi-aksi yang mencoba mengakses wa_account milik Worker A atas nama Worker B
	actions := []struct {
		name    string
		method  string
		path    string
		handler http.HandlerFunc
	}{
		{"Pair", "POST", "/worker/accounts/" + accIDStr + "/pair", srv.HandleWorkerAccountPairPOST},
		{"Delete", "POST", "/worker/accounts/" + accIDStr + "/delete", srv.HandleWorkerAccountDelete},
		{"Disconnect", "POST", "/worker/accounts/" + accIDStr + "/disconnect", srv.HandleWorkerAccountDisconnect},
		{"Stop", "POST", "/worker/accounts/" + accIDStr + "/stop", srv.HandleWorkerAccountStop},
		{"WorkStart", "POST", "/worker/accounts/" + accIDStr + "/work/start", srv.HandleWorkerAccountWorkStart},
		{"WorkStop", "POST", "/worker/accounts/" + accIDStr + "/work/stop", srv.HandleWorkerAccountWorkStop},
	}

	for _, tc := range actions {
		t.Run("IDOR_WorkerB_Calls_WorkerA_Account_"+tc.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("code", "123456")
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("id", accIDStr)
			req = withWorkerB(req)
			w := httptest.NewRecorder()

			tc.handler(w, req)

			isRejected := (w.Code == http.StatusNotFound || w.Code == http.StatusForbidden || (w.Code == http.StatusSeeOther && strings.Contains(w.Header().Get("Location"), "error=")))
			if !isRejected {
				t.Errorf("IDOR VULNERABILITY DETECTED in %s: Expected 404/403 or redirect with error, got code %d (loc: %s, body: %s)", tc.name, w.Code, w.Header().Get("Location"), w.Body.String())
			}
		})
	}

	// Verifikasi akun Worker A sama sekali tidak terhapus atau berubah
	var countA int
	_ = database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM wa_accounts WHERE id = $1 AND worker_id = $2", waAccAID, workerAID).Scan(&countA)
	if countA != 1 {
		t.Fatalf("SECURITY IDOR: Worker A's account was modified or deleted by Worker B!")
	}
}

// TestGuard_AdminTOTP_StepUpLifecycle memverifikasi bahwa verifikasi step-up admin
// berlaku selama 5 menit, dan kedaluwarsa setelah 5 menit (menolak aksi sensitif).
func TestGuard_AdminTOTP_StepUpLifecycle(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}

	srv, err := NewServer(database, "dev", nil, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()
	uniqueSuffix := strconv.FormatInt(time.Now().UnixNano(), 10)

	var adminID int64
	err = database.Pool.QueryRow(ctx, `
		INSERT INTO users (username, display_name, email, password_hash, role, status, referral_code)
		VALUES ($1, $1, $2, 'hash123', 'admin', 'active', $3)
		RETURNING id
	`, "stepup_admin_"+uniqueSuffix, "admin_"+uniqueSuffix+"@example.com", "REF_STEP_"+uniqueSuffix).Scan(&adminID)
	if err != nil {
		t.Fatalf("Gagal membuat admin: %v", err)
	}

	rawToken, hashToken, _ := auth.GenerateSessionToken()

	// Handler sensitif dummy yang dibungkus RequireStepUp
	sensitiveCalled := false
	sensitiveHandler := srv.RequireStepUp(func(w http.ResponseWriter, r *http.Request) {
		sensitiveCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok sensitive action executed"))
	})

	createReq := func(stepupTime *time.Time) *http.Request {
		_, _ = database.Pool.Exec(ctx, "DELETE FROM web_sessions WHERE token_hash = $1", hashToken)
		_, _ = database.Pool.Exec(ctx, `
			INSERT INTO web_sessions (user_id, token_hash, expires_at, stepup_at, ip, user_agent)
			VALUES ($1, $2, $3, $4, '127.0.0.1', 'test-agent')
		`, adminID, hashToken, time.Now().Add(1*time.Hour), stepupTime)

		req := httptest.NewRequest("POST", "/admin/settings", nil)
		req.AddCookie(&http.Cookie{
			Name:  srv.sessionCookieName(),
			Value: rawToken,
		})
		cReq := req.WithContext(context.WithValue(req.Context(), userContextKey, &UserSession{
			ID:       adminID,
			Username: "stepup_admin_" + uniqueSuffix,
			Role:     "admin",
		}))
		cReq = cReq.WithContext(context.WithValue(cReq.Context(), sessionKey, hashToken))
		return cReq
	}

	// Skenario 1: Belum pernah stepup -> harus ditolak
	{
		sensitiveCalled = false
		req := createReq(nil)
		w := httptest.NewRecorder()
		sensitiveHandler(w, req)

		if sensitiveCalled || w.Code != http.StatusSeeOther {
			t.Errorf("StepUp tanpa timestamp: expected redirect (303), got code %d", w.Code)
		}
	}

	// Skenario 2: Stepup 2 menit yang lalu (validitas 5m) -> harus diizinkan
	{
		sensitiveCalled = false
		stepupAt := time.Now().Add(-2 * time.Minute)
		req := createReq(&stepupAt)
		w := httptest.NewRecorder()
		sensitiveHandler(w, req)

		if !sensitiveCalled || w.Code != http.StatusOK {
			t.Errorf("StepUp aktif (2m lalu): expected OK 200, got code %d", w.Code)
		}
	}

	// Skenario 3: Stepup 6 menit yang lalu (sudah kedaluwarsa) -> harus ditolak
	{
		sensitiveCalled = false
		stepupAt := time.Now().Add(-6 * time.Minute)
		req := createReq(&stepupAt)
		w := httptest.NewRecorder()
		sensitiveHandler(w, req)

		if sensitiveCalled || w.Code != http.StatusSeeOther {
			t.Errorf("StepUp kedaluwarsa (6m lalu): expected redirect (303), got code %d", w.Code)
		}
	}
}

// TestGuard_AdminCLI_ResetTOTP memverifikasi fungsi reset TOTP admin darurat
func TestGuard_AdminCLI_ResetTOTP(t *testing.T) {
	root := findProjectRoot(t)
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)

	pool := testutil.TestDB(t)
	database := &db.DB{Pool: pool}

	ctx := context.Background()
	uniqueSuffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	email := "resettotp_admin_" + uniqueSuffix + "@example.com"

	var adminID int64
	err := database.Pool.QueryRow(ctx, `
		INSERT INTO users (username, display_name, email, password_hash, role, status, referral_code, totp_secret_encrypted, totp_enabled_at, totp_last_step)
		VALUES ($1, $1, $2, 'hash123', 'admin', 'active', $3, 'enc_secret_mock', CURRENT_TIMESTAMP, 12345)
		RETURNING id
	`, "resettotp_admin_"+uniqueSuffix, email, "REF_RST_"+uniqueSuffix).Scan(&adminID)
	if err != nil {
		t.Fatalf("Gagal membuat admin: %v", err)
	}

	// Masukkan kode pemulihan
	_, _ = database.Pool.Exec(ctx, `
		INSERT INTO admin_recovery_codes (user_id, code_hash)
		VALUES ($1, 'hash_sample_code')
	`, adminID)

	// Jalankan ResetAdminTOTP
	resID, err := ResetAdminTOTP(ctx, database.Pool, email)
	if err != nil {
		t.Fatalf("ResetAdminTOTP failed: %v", err)
	}
	if resID != adminID {
		t.Errorf("Expected admin ID %d, got %d", adminID, resID)
	}

	// Verifikasi database: kolom TOTP harus NULL
	var encSecret *string
	var enabledAt *time.Time
	var lastStep *int64
	err = database.Pool.QueryRow(ctx, `
		SELECT totp_secret_encrypted, totp_enabled_at, totp_last_step
		FROM users WHERE id = $1
	`, adminID).Scan(&encSecret, &enabledAt, &lastStep)
	if err != nil {
		t.Fatalf("Gagal query user setelah reset: %v", err)
	}

	if encSecret != nil || enabledAt != nil || lastStep != nil {
		t.Errorf("Expected TOTP fields to be NULL, got enc=%v, enabled=%v, step=%v", encSecret, enabledAt, lastStep)
	}

	// Verifikasi recovery codes terhapus
	var rcCount int
	_ = database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM admin_recovery_codes WHERE user_id = $1", adminID).Scan(&rcCount)
	if rcCount != 0 {
		t.Errorf("Expected 0 recovery codes, got %d", rcCount)
	}

	// Verifikasi audit log tercatat
	var auditAction string
	err = database.Pool.QueryRow(ctx, `
		SELECT action FROM audit_logs 
		WHERE actor_id = $1 AND action = 'reset_totp_cli'
		ORDER BY id DESC LIMIT 1
	`, adminID).Scan(&auditAction)
	if err != nil || auditAction != "reset_totp_cli" {
		t.Errorf("Audit log reset_totp_cli tidak ditemukan: %v", err)
	}
}
