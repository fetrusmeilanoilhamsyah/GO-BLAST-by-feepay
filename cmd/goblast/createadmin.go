package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"goblast/internal/auth"
	"goblast/internal/config"
	"goblast/internal/db"
)

func runCreateAdmin(cfg *config.Config) {
	if len(os.Args) < 4 || os.Args[2] != "--email" {
		fmt.Println("Usage: go run ./cmd/goblast createadmin --email <email>")
		os.Exit(1)
	}
	email := os.Args[3]

	fmt.Print("Enter admin password (min 10 chars): ")
	var password string
	fmt.Scanln(&password)

	if len(password) < 10 {
		fmt.Println("Password must be at least 10 characters")
		os.Exit(1)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		fmt.Printf("Error hashing password: %v\n", err)
		os.Exit(1)
	}

	database, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fmt.Printf("Database connection failed: %v\n", err)
		os.Exit(1)
	}
	defer database.Pool.Close()

	// generate new referral code for admin too, just to satisfy the unique constraint easily
	newRefCode, _, _ := auth.GenerateSessionToken()
	newRefCode = newRefCode[:8]

	username := "admin"
	if parts := strings.Split(email, "@"); len(parts) > 0 && parts[0] != "" {
		username = strings.ToLower(parts[0])
	}

	var adminID int64
	err = database.Pool.QueryRow(context.Background(), `
		INSERT INTO users (email, username, password_hash, role, display_name, language, currency, referral_code)
		VALUES ($1, $2, $3, 'admin', 'Admin', 'id', 'IDR', $4) RETURNING id
	`, email, username, hash, newRefCode).Scan(&adminID)

	if err != nil {
		fmt.Printf("Error creating admin (email might exist): %v\n", err)
		os.Exit(1)
	}

	// Audit log
	_, _ = database.Pool.Exec(context.Background(), `
		INSERT INTO audit_logs (actor_id, action, entity, entity_id, meta)
		VALUES ($1, 'create_admin', 'users', $2, '{}')
	`, adminID, email)

	fmt.Println("Admin created successfully.")
}
