package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"goblast/internal/backup"
	"goblast/internal/config"
	"goblast/internal/db"
)

func runRestore(cfg *config.Config) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	filePath := fs.String("file", "", "Path ke berkas cadangan .gbak yang akan dipulihkan")
	forceNonEmpty := fs.Bool("force-nonempty", false, "Izinkan pemulihan ke database yang tidak kosong dengan konfirmasi ketik")
	stoppedTheApp := fs.Bool("i-stopped-the-app", false, "Lewati pemeriksaan koneksi aplikasi aktif jika aplikasi sudah dipastikan mati")

	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "Penggunaan argumen restore tidak valid: %v\n", err)
		os.Exit(1)
	}

	if *filePath == "" {
		fmt.Fprintln(os.Stderr, "Kesalahan: Flag --file <path> wajib disertakan.")
		fmt.Fprintln(os.Stderr, "Contoh: goblast restore --file backups/20261017_030000_otomatis_a1b2c3d4.gbak")
		os.Exit(1)
	}

	if len(cfg.BackupKey) != 32 {
		fmt.Fprintln(os.Stderr, "Kesalahan: BACKUP_KEY belum dikonfigurasi di environment atau berkas .env.")
		os.Exit(1)
	}

	// Buka koneksi database dengan application_name goblast_restore
	database, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("Gagal menghubungkan ke database", "err", err)
		os.Exit(1)
	}
	defer database.Pool.Close()

	ctx := context.Background()

	// Jika --force-nonempty digunakan pada database yang ada isinya, minta konfirmasi interaktif
	if *forceNonEmpty {
		var userCount int64
		_ = database.Pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&userCount)
		if userCount > 0 {
			fmt.Printf("PERINGATAN: Database saat ini memuat %d pengguna dan data lainnya.\n", userCount)
			fmt.Print("Pemulihan akan MENGHAPUS SEMUA DATA yang ada saat ini dan menimpanya dari cadangan.\n")
			fmt.Print("Ketik 'RESTORE' untuk melanjutkan: ")

			reader := bufio.NewReader(os.Stdin)
			text, _ := reader.ReadString('\n')
			text = strings.TrimSpace(text)
			if text != "RESTORE" {
				fmt.Println("Pemulihan dibatalkan oleh pengguna.")
				os.Exit(0)
			}
		}
	}

	fmt.Println("Memulai proses pemulihan basis data dari:", *filePath)

	res, err := backup.Restore(ctx, database.Pool, *filePath, cfg.BackupKey, backup.RestoreOptions{
		ForceNonEmpty: *forceNonEmpty,
		StoppedTheApp: *stoppedTheApp,
		EncryptionKey: cfg.EncryptionKey,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nPEMULIHAN GAGAL: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n=== PEMULIHAN DATABASE SELESAI ===")
	fmt.Printf("Tabel Dipulihkan : %d\n", res.RestoredTables)
	fmt.Printf("Total Baris      : %d\n", res.RestoredRows)
	fmt.Printf("Berkas Uploads   : %d\n", res.RestoredFiles)
	fmt.Printf("Status Enkripsi  : %s\n", res.EncryptionKeyCheck)
	fmt.Println("\nCATATAN (Keputusan B1):")
	fmt.Println("- Sesi WhatsApp pekerja tidak dipulihkan. Seluruh akun diatur ke status 'terputus'.")
	fmt.Println("- Pekerja wajib memindai ulang QR code untuk menghubungkan kembali sesi WhatsApp mereka.")
}
