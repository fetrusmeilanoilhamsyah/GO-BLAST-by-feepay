# GO BLAST (oleh feepay)

Aplikasi web blast pesan WhatsApp multi-pekerja dengan dukungan pelacakan kartu link (open graph simulation) dan rotasi nomor terdistribusi.

## Prasyarat

- Go 1.27.x
- PostgreSQL 16+

## Menjalankan Secara Lokal

1. Salin konfigurasi environment:
   ```bash
   cp .env.example .env
   # Sesuaikan DATABASE_URL dan konfigurasi lainnya di .env
   ```

2. Jalankan migrasi basis data:
   ```bash
   go run ./cmd/goblast migrate up
   ```

3. Buat akun admin pertama (opsional):
   ```bash
   go run ./cmd/goblast create-admin admin@example.com
   ```

4. Jalankan aplikasi web:
   ```bash
   go run ./cmd/goblast
   ```
   Buka peramban di `http://localhost:8080`.

## Struktur Direktori

- `cmd/goblast/`: Titik masuk utama aplikasi (server web, migrasi, cli).
- `internal/`: Logika inti bisnis (auth, campaign, db, i18n, numbers, web).
- `web/`: Aset antarmuka (template HTML, CSS tema terang & gelap, aset statis).
- `db/migrations/`: Berkas migrasi database SQL (goose).
- `docs/`: Dokumentasi lengkap konsep, keamanan, arsitektur, dan kemajuan.

## Dokumentasi

Dokumentasi detail arsitektur dan aturan pengembangan tersedia di direktori `docs/`:
- [Konsep Aplikasi](docs/DOC_KONSEP.md)
- [Pedoman Mutu & AI Slop Guard](docs/DOC_AI_SLOP.md)
- [Progres & Catatan Tahap](docs/DOC_PROGRES.md)
- [Keamanan & Panduan Deployment](docs/DOC_SECURITY_DEPLOY.md)
- [Skema Basis Data](docs/DOC_SKEMA.md)

## Kepatuhan & Etika

Hormati Ketentuan Layanan (ToS) WhatsApp dan regulasi telekomunikasi setempat; jangan gunakan aplikasi ini untuk tindakan spam atau pengiriman tanpa persetujuan penerima.
