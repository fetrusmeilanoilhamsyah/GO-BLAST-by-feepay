package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"goblast/internal/auth"
	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/i18n"
	"goblast/internal/platform"
	"goblast/internal/web"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	platform.InitLog(cfg.AppEnv)

	// Subcommands
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if len(os.Args) < 3 {
			fmt.Println("Usage: go run ./cmd/goblast migrate <up|down|status>")
			os.Exit(1)
		}
		command := os.Args[2]

		database, err := db.Connect(context.Background(), cfg.DatabaseURL)
		if err != nil {
			slog.Error("Database connection failed", "err", err)
			os.Exit(1)
		}
		defer database.Pool.Close()

		// Get underlying sql.DB for goose
		stdlibDB, err := goose.OpenDBWithDriver("postgres", cfg.DatabaseURL)
		if err != nil {
			slog.Error("Goose db connection failed", "err", err)
			os.Exit(1)
		}
		defer stdlibDB.Close()

		if err := goose.SetDialect("postgres"); err != nil {
			slog.Error("Failed to set dialect", "err", err)
			os.Exit(1)
		}

		dir := "db/migrations"
		var gooseErr error
		switch command {
		case "up":
			gooseErr = goose.Up(stdlibDB, dir)
		case "down":
			gooseErr = goose.Down(stdlibDB, dir)
		case "status":
			gooseErr = goose.Status(stdlibDB, dir)
		default:
			fmt.Printf("Unknown migrate command: %s\n", command)
			os.Exit(1)
		}

		if gooseErr != nil {
			slog.Error("Migration failed", "err", gooseErr)
			os.Exit(1)
		}
		slog.Info("Migration completed successfully")
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "createadmin" {
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

		var adminID int64
		err = database.Pool.QueryRow(context.Background(), `
			INSERT INTO users (email, password_hash, role, display_name, language, currency, referral_code)
			VALUES ($1, $2, 'admin', 'Admin', 'id', 'IDR', $3) RETURNING id
		`, email, hash, newRefCode).Scan(&adminID)

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
		return
	}

	// Normal startup
	if err := i18n.Load("locales"); err != nil {
		slog.Error("Failed to load i18n", "err", err)
		os.Exit(1)
	}

	database, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("Database connection failed", "err", err)
		os.Exit(1)
	}
	defer database.Pool.Close()

	srv, err := web.NewServer(database, cfg.AppEnv)
	if err != nil {
		slog.Error("Failed to initialize server", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	slog.Info("Starting server", "addr", cfg.AppAddr)
	if err := http.ListenAndServe(cfg.AppAddr, mux); err != nil {
		slog.Error("Server failed", "err", err)
		os.Exit(1)
	}
}
