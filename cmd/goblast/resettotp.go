package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/web"
)

func runResetTOTP(cfg *config.Config) {
	if len(os.Args) < 4 || os.Args[2] != "--email" {
		fmt.Println("Usage: go run ./cmd/goblast reset-totp --email <email>")
		os.Exit(1)
	}
	email := strings.TrimSpace(os.Args[3])
	if email == "" {
		fmt.Println("Error: --email cannot be empty")
		os.Exit(1)
	}

	database, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fmt.Printf("Database connection failed: %v\n", err)
		os.Exit(1)
	}
	defer database.Pool.Close()

	ctx := context.Background()
	_, err = web.ResetAdminTOTP(ctx, database.Pool, email)
	if err != nil {
		fmt.Printf("Gagal reset TOTP: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Sukses: TOTP untuk admin %s berhasil di-reset. Admin wajib melakukan setup TOTP kembali pada login berikutnya.\n", email)
}

