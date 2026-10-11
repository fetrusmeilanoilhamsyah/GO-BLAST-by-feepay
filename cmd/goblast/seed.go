package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"goblast/internal/config"
	"goblast/internal/db"
)

func runSeed(cfg *config.Config) {
	if cfg.AppEnv == "prod" || cfg.AppEnv == "production" {
		fmt.Fprintln(os.Stderr, "Seed ditolak di lingkungan produksi (APP_ENV=prod)")
		os.Exit(1)
	}

	database, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("Database connection failed", "err", err)
		os.Exit(1)
	}
	defer database.Pool.Close()

	seedContent, err := os.ReadFile("db/seed/dev.sql")
	if err != nil {
		slog.Error("Failed to read dev.sql", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if _, err := database.Pool.Exec(ctx, string(seedContent)); err != nil {
		slog.Error("Failed to execute dev.sql", "err", err)
		os.Exit(1)
	}

	slog.Info("Seed development completed successfully")
}
