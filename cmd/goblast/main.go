package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/i18n"
	"goblast/internal/platform"
	"goblast/internal/wa"
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
		case "seed":
			runSeed(cfg)
			return
		}
	}

	// Normal startup
	campaign.StartTmpCleaner(context.Background())

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

	settingsReader := wa.NewDBSettingsReader(database)
	waManager := wa.NewManager(database, settingsReader, cfg)
	sender := wa.NewRealSender(waManager)

	eng := engine.New(database, sender, waManager, settingsReader)
	waManager.SetDisconnectCallback(eng.StopAccount)
	eng.StartCleaner(context.Background())

	srv, err := web.NewServer(database, cfg.AppEnv, waManager, eng)
	if err != nil {
		slog.Error("Failed to initialize server", "err", err)
		os.Exit(1)
	}
	srv.EncryptionKey = cfg.EncryptionKey
	srv.EncryptionKeyPrev = cfg.EncryptionKeyPrev
	srv.CSWhatsAppURL = cfg.CSWhatsAppURL
	srv.CSTelegramURL = cfg.CSTelegramURL
	srv.ChannelTelegramURL = cfg.ChannelTelegramURL

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:              cfg.AppAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	stopCtx, stopCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopCancel()

	go func() {
		slog.Info("Starting server", "addr", cfg.AppAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-stopCtx.Done()
	slog.Info("Menerima sinyal shutdown, menghentikan proses...")

	// 1. Panggil eng.StopAll
	eng.StopAll()

	// 2. Hentikan server dengan batas waktu 15 detik
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server shutdown forced", "err", err)
	}

	// 3. Tutup pool database
	database.Pool.Close()
	slog.Info("Shutdown selesai dengan rapi.")
}
