package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"goblast/internal/auth"
	"goblast/internal/db"
)

func setupTestServer(t *testing.T) (*Server, *db.DB) {
	database, err := db.Connect(context.Background(), "postgres://postgres:postgres@localhost:5432/goblast?sslmode=disable")
	if err != nil {
		t.Skip("Skipping because DB is not available", err)
	}

	// Clean up tables
	database.Pool.Exec(context.Background(), "DELETE FROM audit_logs")
	database.Pool.Exec(context.Background(), "DELETE FROM web_sessions")
	database.Pool.Exec(context.Background(), "DELETE FROM users")

	srv, err := NewServer(database, "test")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	return srv, database
}

func getCSRFToken(ts *httptest.Server, client *http.Client, targetUrl string) (string, string) {
	resp, _ := client.Get(targetUrl)
	var csrfCookie, sessionCookie string
	for _, c := range resp.Cookies() {
		if c.Name == "gb_csrf" {
			csrfCookie = c.Value
		}
		if c.Name == "gb_session" {
			sessionCookie = c.Value
		}
	}
	return csrfCookie, sessionCookie
}

func TestAuthFlows(t *testing.T) {
	srv, database := setupTestServer(t)
	if srv == nil {
		return
	}
	defer database.Pool.Close()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	t.Run("Without login redirected to /login", func(t *testing.T) {
		resp, err := client.Get(ts.URL + "/worker")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("Expected redirect 303, got %d", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if loc != "/login" {
			t.Errorf("Expected redirect to /login, got %s", loc)
		}
	})

	t.Run("POST without CSRF rejected", func(t *testing.T) {
		form := url.Values{}
		form.Add("email", "test@example.com")
		resp, _ := client.PostForm(ts.URL+"/login", form)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden without CSRF, got %d", resp.StatusCode)
		}
	})

	t.Run("Password below 10 chars rejected in registration", func(t *testing.T) {
		csrf, _ := getCSRFToken(ts, client, ts.URL+"/register")

		form := url.Values{}
		form.Add("email", "short@example.com")
		form.Add("password", "short") // < 10 chars
		form.Add("language", "en")

		req, _ := http.NewRequest("POST", ts.URL+"/register", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "gb_csrf", Value: csrf})
		req.Header.Set("X-CSRF-Token", csrf)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		if resp.StatusCode != http.StatusOK { // Returns same page with error message, not redirect
			t.Errorf("Expected 200 OK (error rendered), got %d", resp.StatusCode)
		}

		// check if user created
		var count int
		database.Pool.QueryRow(context.Background(), "SELECT count(*) FROM users WHERE email='short@example.com'").Scan(&count)
		if count > 0 {
			t.Error("User with short password was created!")
		}
	})

	t.Run("Register a worker, then login, then check roles", func(t *testing.T) {
		csrf, _ := getCSRFToken(ts, client, ts.URL+"/register")

		form := url.Values{}
		form.Add("email", "worker@example.com")
		form.Add("password", "ValidPassw0rd")
		form.Add("language", "id")

		req, _ := http.NewRequest("POST", ts.URL+"/register", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "gb_csrf", Value: csrf})
		req.Header.Set("X-CSRF-Token", csrf)

		resp, _ := client.Do(req)

		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("Expected redirect after registration, got %d", resp.StatusCode)
		}

		// Login
		form = url.Values{}
		form.Add("email", "worker@example.com")
		form.Add("password", "ValidPassw0rd")

		req, _ = http.NewRequest("POST", ts.URL+"/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "gb_csrf", Value: csrf})
		req.Header.Set("X-CSRF-Token", csrf)

		resp, _ = client.Do(req)

		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("Expected redirect after login, got %d", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if loc != "/worker" {
			t.Errorf("Expected redirect to /worker, got %s", loc)
		}

		// get session cookie
		var sessionCookie string
		for _, c := range resp.Cookies() {
			if c.Name == "gb_session" {
				sessionCookie = c.Value
			}
		}
		if sessionCookie == "" {
			t.Fatal("Session cookie not set")
		}

		// Access /worker
		req, _ = http.NewRequest("GET", ts.URL+"/worker", nil)
		req.AddCookie(&http.Cookie{Name: "gb_session", Value: sessionCookie})
		resp, _ = client.Do(req)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200 for /worker, got %d", resp.StatusCode)
		}

		// Access /admin as worker
		req, _ = http.NewRequest("GET", ts.URL+"/admin", nil)
		req.AddCookie(&http.Cookie{Name: "gb_session", Value: sessionCookie})
		resp, _ = client.Do(req)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected 403 for /admin as worker, got %d", resp.StatusCode)
		}
	})

	t.Run("Expired session rejected", func(t *testing.T) {
		csrf, _ := getCSRFToken(ts, client, ts.URL+"/login")

		form := url.Values{}
		form.Add("email", "worker@example.com")
		form.Add("password", "ValidPassw0rd")

		req, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "gb_csrf", Value: csrf})
		req.Header.Set("X-CSRF-Token", csrf)

		resp, _ := client.Do(req)
		var sessionCookie string
		for _, c := range resp.Cookies() {
			if c.Name == "gb_session" {
				sessionCookie = c.Value
			}
		}

		hash := auth.HashSessionToken(sessionCookie)
		database.Pool.Exec(context.Background(), "UPDATE web_sessions SET expires_at = $1 WHERE token_hash = $2", time.Now().Add(-1*time.Hour), hash)

		req, _ = http.NewRequest("GET", ts.URL+"/worker", nil)
		req.AddCookie(&http.Cookie{Name: "gb_session", Value: sessionCookie})
		resp, _ = client.Do(req)

		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("Expected redirect for expired session, got %d", resp.StatusCode)
		}
	})

	t.Run("Login rate limit works", func(t *testing.T) {
		csrf, _ := getCSRFToken(ts, client, ts.URL+"/login")

		form := url.Values{}
		form.Add("email", "ratelimit@example.com")
		form.Add("password", "WrongPassword")

		for i := 0; i < 5; i++ {
			req, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "gb_csrf", Value: csrf})
			req.Header.Set("X-CSRF-Token", csrf)

			resp, _ := client.Do(req)
			if resp.StatusCode == http.StatusTooManyRequests {
				t.Errorf("Failed early on iteration %d", i)
			}
		}

		req, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "gb_csrf", Value: csrf})
		req.Header.Set("X-CSRF-Token", csrf)

		resp, _ := client.Do(req)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("Expected 429 Too Many Requests, got %d", resp.StatusCode)
		}
	})
}
