package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"goblast/internal/config"
	"goblast/internal/db"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func runMigrate(cfg *config.Config) {
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
}
