# GO BLAST (oleh feepay)

Aplikasi web blast pesan WhatsApp multi-pekerja berkinerja tinggi, hemat sumber daya, dan tahan gangguan.

## Prasyarat

- Go 1.26 atau lebih baru (lingkungan pengembangan dan pengujian resmi terverifikasi pada Go 1.27.2)
- PostgreSQL 17
- Nginx (reverse proxy di lingkungan produksi)

## Menjalankan Secara Lokal

1. Salin konfigurasi environment:
   ```bash
   cp .env.example .env
   ```
2. Buat kunci enkripsi 32 byte acak untuk `ENCRYPTION_KEY` dan `BACKUP_KEY`:
   - PowerShell:
     ```powershell
     $b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); [Convert]::ToBase64String($b)
     ```
   - Bash / Linux:
     ```bash
     openssl rand -base64 32
     ```
   Tempelkan string base64 tersebut ke `ENCRYPTION_KEY` dan `BACKUP_KEY` (keduanya harus menggunakan kunci yang berbeda).

3. Jalankan migrasi basis data (Goose):
   ```bash
   go run ./cmd/goblast migrate up
   ```

4. Buat akun admin pertama:
   ```bash
   go run ./cmd/goblast createadmin admin@example.com
   ```
   Perintah ini akan mencetak kata sandi acak awal di konsol. Setelah login pertama kali, admin diwajibkan melakukan penyiapan 2FA TOTP (Google Authenticator / Aegis).

5. Jalankan aplikasi web:
   ```bash
   go run ./cmd/goblast
   ```
   Buka peramban di `http://127.0.0.1:8080`.

## Pengujian

Jalankan seluruh suite pengujian otomatis:
```bash
go test -p 1 ./...
```
*Catatan: Pastikan variabel `TEST_DATABASE_URL` pada `.env` telah mengarah ke database khusus pengujian yang berakhiran `_test`.*

## Catatan Keamanan Singkat

- **Pemisahan Kunci Kriptografi:** `ENCRYPTION_KEY` (data rekening pekerja) dan `BACKUP_KEY` (cadangan `.gbak`) adalah kunci AES-256-GCM 32-byte independen yang tidak boleh sama dan tidak boleh disimpan di database.
- **Isolasi Jaringan:** Di lingkungan produksi, aplikasi menolak binding ke antarmuka publik (`0.0.0.0`) secara bawaan; aplikasi hanya mengikat loopback `127.0.0.1:8080` di belakang Nginx dengan proteksi Cloudflare.
- **Autentikasi 2FA TOTP Admin:** Akun admin dilindungi TOTP RFC 6238 dan step-up authorization 5 menit untuk tindakan sensitif (pengaturan sistem, dompet kas, persetujuan penarikan, reset sandi, dsb).
- **Perlindungan Privasi Customer:** Nomor telepon penerima pesan dianonimkan, tidak pernah dikirim ke browser pekerja, disamarkan dalam log server, dan dilindungi isolasi foreign key `ON DELETE RESTRICT`.

## Struktur Direktori

- `cmd/goblast/`: Titik masuk utama aplikasi (server web, migrasi Goose, CLI admin, restore).
- `internal/`: Logika inti bisnis (auth, backup, campaign, config, db, engine, i18n, money, numbers, platform, ratelimit, safe, turnstile, wa, web, withdraw).
- `web/`: Template HTML server-rendered, CSS tema responsif terang & gelap, aset statis SVG.
- `locales/`: Berkas kamus multibahasa (Indonesia `id.json`, Inggris `en.json`, Mandarin `zh.json`).
- `db/migrations/`: Berkas migrasi database SQL naik dan turun (Goose `0001` s.d. `0024`).
- `docs/`: Dokumentasi arsitektur, panduan keamanan, skema database, dan catatan progres.

## Dokumentasi Lengkap

- [Konsep Aplikasi](docs/DOC_KONSEP.md)
- [Pedoman Mutu & AI Slop Guard](docs/DOC_AI_SLOP.md)
- [Keamanan & Panduan Deployment](docs/DOC_SECURITY_DEPLOY.md)
- [Skema Basis Data](docs/DOC_SKEMA.md)
- [Progres & Catatan Tahap](docs/DOC_PROGRES.md)

## Kepatuhan & Etika

Hormati Ketentuan Layanan (ToS) WhatsApp dan regulasi telekomunikasi setempat; sistem ini ditujukan untuk siaran informasi promosi terarah dengan nomor target yang telah dihimpun sah oleh pemilik sistem.
