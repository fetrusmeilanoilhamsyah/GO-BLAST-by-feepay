package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/i18n"
	"goblast/internal/platform"
	"goblast/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	platform.InitLog(cfg.AppEnv)

	// Subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			runMigrate(cfg)
			return
		case "createadmin":
			runCreateAdmin(cfg)
			return
		}
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
