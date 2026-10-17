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

	"goblast/internal/backup"
	"goblast/internal/campaign"
	"goblast/internal/config"
	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/i18n"
	"goblast/internal/platform"
	"goblast/internal/turnstile"
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
		case "reset-totp":
			runResetTOTP(cfg)
			return
		case "restore":
			runRestore(cfg)
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
	waManager.SetEngineStopper(eng.StopAccountWait)

	// H4 & H3: Rekonsiliasi sesi WA saat boot (B1) & pulihkan nomor menggantung (A1) sebelum menerima request
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if recAcc, purgedDev, err := waManager.ReconcileOnBoot(bootCtx); err != nil {
		slog.Error("Gagal menjalankan ReconcileOnBoot saat startup", "err", err)
	} else if recAcc > 0 || purgedDev > 0 {
		slog.Info("ReconcileOnBoot selesai saat boot", "reconciled_accounts", recAcc, "purged_devices", purgedDev)
	}

	if recovered, err := eng.RecoverOnBoot(bootCtx); err != nil {
		slog.Error("Gagal menjalankan RecoverOnBoot saat startup", "err", err)
	} else if recovered > 0 {
		slog.Info("RecoverOnBoot berhasil memulihkan nomor menggantung", "count", recovered)
	}
	bootCancel()

	// H3: Jalankan background refresher settings, supervisor auto-complete, dan pembersih stale claims
	eng.StartSettingsRefresher(context.Background())
	eng.StartSupervisor(context.Background())
	eng.StartCleaner(context.Background())

	srv, err := web.NewServer(database, cfg.AppEnv, waManager, eng)
	if err != nil {
		slog.Error("Failed to initialize server", "err", err)
		os.Exit(1)
	}
	srv.Engine = eng
	srv.EncryptionKey = cfg.EncryptionKey
	srv.EncryptionKeyPrev = cfg.EncryptionKeyPrev

	// H9: Cadangan database otomatis & manajer backup
	var backupMgr *backup.Manager
	if len(cfg.BackupKey) == 32 {
		backupMgr = backup.NewManager(database.Pool, backup.DefaultBackupDir, cfg.BackupKey, cfg.BackupKeyPrev)
		backup.StartScheduler(context.Background(), backupMgr)
	} else {
		slog.Warn("BACKUP_KEY belum dikonfigurasi atau tidak valid. Fitur cadangan database dan penjadwal harian dinonaktifkan.")
	}
	srv.BackupMgr = backupMgr
	srv.AllowWebRestore = cfg.AllowWebRestore

	srv.CSWhatsAppURL = cfg.CSWhatsAppURL
	srv.CSTelegramURL = cfg.CSTelegramURL
	srv.ChannelTelegramURL = cfg.ChannelTelegramURL
	srv.SessionSecret = cfg.SessionSecret
	srv.TrustedProxyCIDRs = cfg.ParsedTrustedCIDRs
	srv.ClientIPHeader = cfg.ClientIPHeader
	if cfg.TurnstileSecretKey != "" {
		srv.Turnstile = turnstile.NewClient(cfg.TurnstileSecretKey, cfg.TurnstileSiteKey, cfg.AppPublicHost)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:              cfg.AppAddr,
		Handler:           srv.WrapGlobalMiddleware(mux),
		MaxHeaderBytes:    16 * 1024,
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
	slog.Info("Menerima sinyal shutdown, memulai penutupan proses secara berurutan...")

	// 1. Tandai draining pada HTTP layer
	srv.SetDraining(true)

	// 2. Engine Drain yang membiarkan kirim in-flight selesai anggun (hingga 20 detik)
	slog.Info("Menghentikan seluruh worker engine secara anggun (Drain maks 20 detik)...")
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Second)
	if err := eng.Drain(drainCtx); err != nil {
		slog.Error("Drain engine selesai dengan catatan", "err", err)
	}
	drainCancel()

	// 3. Hentikan server HTTP dengan batas waktu 15 detik
	slog.Info("Menghentikan HTTP server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server shutdown forced", "err", err)
	}

	// 4. Putus sesi WhatsApp tanpa logout
	slog.Info("Memutus seluruh sesi WhatsApp tanpa logout...")
	waManager.DisconnectAll()

	// 5. Tutup pool database
	slog.Info("Menutup pool koneksi database...")
	database.Pool.Close()
	slog.Info("Shutdown selesai dengan rapi.")
}
