# DOC_SECURITY_DEPLOY

Panduan dan aturan mengenai keamanan aplikasi dan prosedur deploy sistem GO BLAST.

---

## Bagian A. Keamanan

### A1. Injeksi SQL
- Gunakan argumen berparameter pada seluruh query PostgreSQL melalui library `pgx/v5`.
- Dilarang menyusun atau menggabungkan string secara manual (`fmt.Sprintf`, string concatenation) untuk membuat query SQL.
- Seluruh query antrean menggunakan klausa `FOR UPDATE SKIP LOCKED` pada transaksi atomik untuk mencegah *race condition* antar worker.

### A2. Kata Sandi dan Sesi
- **Algoritma Hashing**: Gunakan algoritma argon2id untuk hashing kata sandi (parameter: memory=64MB, time=1, threads=4, salt=16 byte, key=32 byte).
- **Batas Panjang Input**: Panjang kata sandi dibatasi minimal 10 karakter dan maksimal 128 byte (`MaxPasswordBytes`) untuk mencegah serangan Denial of Service (DoS) memory exhaustion.
- **ArgonGate Concurrency Throttle**: Seluruh verifikasi dan pembuatan hash argon2id dikawal oleh `ArgonGate` (`ArgonConcurrencyLimit = 2`, `ArgonQueueLimit = 8`, `ArgonMaxWait = 2s`). Jika server sibuk atau antrean penuh, permintaan verifikasi langsung ditolak dengan HTTP 503 (`Retry-After: 2`) tanpa membebani RAM server.
- **Mitigasi Timing Attack**: Jika akun tidak ditemukan di database pada saat login, sistem tetap mengeksekusi komputasi argon2id menggunakan `ShadowHash` konstan untuk mencegah deteksi keberadaan akun melalui perbedaan durasi respons.
- **Pemeriksaan Akun Beku**: Status pembekuan akun (`frozen`) hanya dicek SETELAH verifikasi kata sandi cocok, untuk mencegah kebocoran status eksistensi/pembekuan akun ke penyerang.
- **Atribut Cookie Sesi**:
  - Lingkungan Produksi: Nama cookie `__Host-gb_session` dengan flag `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`.
  - Lingkungan Pengembangan: Nama cookie `gb_session` dengan flag `HttpOnly`, `SameSite=Lax`, `Path=/`.
- **Batas Akumulasi & Kuota Sesi**: Maksimal 5 sesi web aktif simultan per akun (`MaxUserWebSessions = 5`). Sesi paling tua otomatis dicabut saat login ke-6 berhasil.
- **Pembersih Sesi Latar Belakang**: `startSessionCleaner` berjalan otomatis tiap jam untuk membersihkan sesi yang telah kedaluwarsa dari tabel `web_sessions`.
- **Masa Berlaku Sesi**: Admin 24 jam, Pekerja 7 hari.
- **Throttling Pembaruan `last_seen_at`**: Pembaruan kolom `last_seen_at` di database dibatasi maksimal 1 kali per menit per sesi untuk mencegah *write thrashing* pada koneksi database.
- **Prosedur Pencabutan Sesi**:
  - Pengguna dapat mencabut seluruh sesi lain saat mengganti kata sandi di menu keamanan pekerja (`POST /worker/security`).
  - Admin dapat mencabut sesi pengguna tertentu dari panel `/admin/maintenance` atau detail pekerja.
  - Skenario darurat: seluruh sesi dapat dicabut seketika via perintah SQL `DELETE FROM web_sessions;`.

### A3. CSRF (Cross-Site Request Forgery)
- Wajibkan token CSRF pada semua permintaan perubahan data (POST/PUT/DELETE).
- Cookie CSRF: `__Host-gb_csrf` di produksi (`Secure`, `SameSite=Lax`, `Path=/`), `gb_csrf` di pengembangan.
- Token CSRF ditandatangani menggunakan HMAC-SHA256 (`SESSION_SECRET`) dan divalidasi dengan `subtle.ConstantTimeCompare`.
- **Pengamanan Lapis Ganda CSRF**:
  - Pemeriksaan header browser `Sec-Fetch-Site` (menolak `cross-site`).
  - Verifikasi kecocokan header `Origin` dengan host publik server (`APP_PUBLIC_HOST` atau `Host`).

### A4. Pembatasan Laju (Rate Limiting) & Proteksi Bot
- Menggunakan rate limiter token bucket berbasis `golang.org/x/time/rate` di memori (`internal/ratelimit`) dengan pengusiran LRU (`MaxLimiterKeys = 10000`) dan pembersihan berkala tiap 10 menit dengan proteksi panic recovery.
- **Arsitektur Rate Limiting Login Multi-Lapis**:
  - Lapis 1 (Global): 300 request/menit untuk seluruh sistem (`LoginRateLimitGlobalAttempts = 300`).
  - Lapis 2 (IP Klien): 20 kegagalan per 15 menit per IP klien (`LoginRateLimitIPAttempts = 20`).
  - Lapis 3 (Identifier Akun): 10 kegagalan per 15 menit per username/email (`LoginRateLimitIdentifierAttempts = 10`).
  - Lapis 4 (Pasangan IP + Identifier): 5 kegagalan per 15 menit (`LoginRateLimitPairAttempts = 5`). Berhasil login me-reset pembatasan pasangan.
- **Pembatasan Laju Pendaftaran Akun**:
  - Per jam: 10 pendaftaran per jam per IP (`RegisterRateLimitHourlyAttempts = 10`).
  - Per hari: 100 pendaftaran per hari per IP (`RegisterRateLimitDailyAttempts = 100`).
- **Pembatasan Laju Log Audit Gagal**:
  - Maksimal pencatatan audit log `login_failed` adalah 20 entri per IP per menit (`AuditLoginFailedRateLimitAttempts = 20`) guna mencegah serangan flooding disk log audit.
- **Pembatasan Laju per Endpoint Spesifik (`internal/config/limits.go`)**:
  - `WorkerTicket10MinAttempts` (3/10m per user) dan `WorkerTicketDailyAttempts` (10/hari per user) untuk `POST /worker/tickets/new`.
  - `WorkerSecurityAttempts` (5/15m per user) untuk `POST /worker/security` (ganti kata sandi).
  - `WorkerFinancialAttempts` (10/10m per user) untuk penarikan (`POST /worker/withdrawals`), konversi saldo (`POST /worker/convert`), dan ubah rekening.
  - `WorkerAccountUserAttempts` (6/10m per user) dan `WorkerAccountIPAttempts` (20/10m per IP) untuk manajemen akun WhatsApp (tambah, init, pair, hapus).
  - `WorkerWorkCtrlAttempts` (30/menit per user) untuk aksi start/stop kerja WhatsApp.
  - `WorkerAPIPollingAttempts` (120/menit per IP/user, burst 30) untuk polling statistik, lonceng notifikasi, dan status akun WhatsApp.
  - `PublicRouteAttempts` (100/menit per IP) untuk rute publik umum dan `POST /logout`.
  - `StaticRouteAttempts` (300/menit per IP) untuk aset statis frontend.
  - `AdminGlobalAttempts` (300/menit per user/IP) membungkus seluruh panel admin.
  - `AdminTOTPVerifyAttempts` (5 kegagalan per 15 menit) untuk verifikasi kode 2FA.
  - `AdminCampaignExportNumbersAttempts` (5 per jam per admin) untuk ekspor sisa nomor kampanye.
  - `AdminStoragePurgeAttempts` (5 per jam per admin) untuk pembersihan penyimpanan arsip kampanye.
- **Sifat Pembatasan**: Tidak ada pemblokiran IP permanen. Pembatasan pulih secara otomatis setelah jendela waktu kedaluwarsa atau token bucket terisi kembali. Admin dapat mencabut pembatasan aktif (`ActiveRestrictions`) secara manual di `/admin/maintenance`.
- **Perlindungan Bot Tanpa JavaScript Berat**:
  - Form Timing Token (HMAC timestamp): Formulir login dan registrasi menolak pengiriman yang terlalu cepat (< 2 detik) atau kedaluwarsa (> 2 jam).
  - Honeypot Field (`website_hp`): Field tersembunyi via CSS yang jika diisi bot akan langsung ditolak tanpa eksekusi database atau argon2id.
- **Integrasi Cloudflare Turnstile**:
  - Verifikasi CAPTCHA modern di form login dan registrasi via Cloudflare siteverify endpoint (timeout 3 detik).
  - Mode fail-closed untuk formulir registrasi (menolak jika Cloudflare outage) dan mode fail-open terkontrol untuk formulir login hanya jika Cloudflare API mengalami gangguan jaringan/5xx.
  - Di lingkungan produksi, penggunaan dummy test keys Cloudflare ditolak saat booting.

### A5. Enkripsi Data dan Rotasi Kunci
- Enkripsi alamat tujuan penarikan pekerja dan data sensitif menggunakan algoritma AES-256-GCM authenticated encryption.
- **Format Ciphertext Bervolume Multi-Generasi**:
  - `v1:k1:<base64>`: Terenkripsi dengan kunci aktif saat ini (`ENCRYPTION_KEY`).
  - `v1:k0:<base64>`: Terenkripsi dengan kunci rotasi sebelumnya (`ENCRYPTION_KEY_PREV`).
  - Fallback aman: Mendukung ciphertext legacy tanpa prefiks atau format sejenis.
- Ambil kunci enkripsi dari variabel lingkungan `ENCRYPTION_KEY` (wajib, 32 byte base64).
- **Dukungan Rotasi Kunci Dua Generasi (`ENCRYPTION_KEY` dan `ENCRYPTION_KEY_PREV`)**:
  1. Pasang kunci lama ke variabel `ENCRYPTION_KEY_PREV`.
  2. Generate 32 byte acak baru dan pasang ke `ENCRYPTION_KEY`.
  3. Aplikasi membaca/mendekripsi data lama dengan k1, fallback ke k0 jika gagal, dan selalu menulis/mengenkripsi data baru menggunakan k1.
  4. Seluruh log aplikasi dilarang keras mencetak plaintext, ciphertext utuh, maupun kunci enkripsi.
- **Pemisahan Cadangan Kunci dari Database**:
  - Kunci enkripsi pada berkas `.env` TIDAK BOLEH disimpan bersama cadangan database (`pg_dump` atau `.gbak`).
  - Jika cadangan database bocor, data rekening pekerja tetap tidak dapat didekripsi tanpa berkas kunci terpisah. Simpan salinan darurat berkas `.env` di brankas password / secret manager terisolasi.

### A6. Kontrol Akses & TOTP Admin (Keputusan E2)
- Periksa peran (admin, pekerja, publik) terhadap tiap kelompok rute di level middleware HTTP server.
- Cegah pekerja mengakses data milik pekerja lain (IDOR) pada akun WhatsApp, tiket laporan, riwayat penarikan, maupun buku besar saldo.
- **Penegakan Logout POST-Only**: Rute `GET /logout` mengembalikan HTTP `405 Method Not Allowed`. Logout hanya diproses via `POST /logout` dengan verifikasi token CSRF dan rate limiting publik.
- **Validasi Tautan CS & Channel Resmi**: Tautan CS WhatsApp (`CS_WHATSAPP_URL`), CS Telegram (`CS_TELEGRAM_URL`), dan Channel Telegram (`CHANNEL_TELEGRAM_URL`) wajib diawali dengan skema `https://`. Skema berbahaya seperti `javascript:`, `data:`, `http:`, atau format non-HTTPS ditolak saat konfigurasi dimuat.
- **Autentikasi Dua Faktor (2FA) TOTP Admin (RFC 6238)**:
  - Algoritma HMAC-SHA1, periode 30 detik, 6 digit angka, toleransi window $\pm 1$ langkah waktu (90 detik total) untuk mencegah kegagalan akibat desinkronisasi jam perangkat.
  - **Anti-Replay Attack**: kolom `users.totp_last_step` menyimpan langkah waktu terakhir yang berhasil diverifikasi sehingga kode TOTP yang sama tidak dapat digunakan kembali dalam jendela waktu yang sama.
  - **Kode Pemulihan Darurat**: Saat setup, sistem menerbitkan 10 kode pemulihan sekali pakai (format `XXXXX-XXXXX`). Kode di-hash menggunakan SHA-256 dan disimpan pada tabel `admin_recovery_codes`. Kode pemulihan langsung dihapus dari database begitu berhasil digunakan.
  - **Alur Setup Wajib**: Admin yang belum mengonfigurasi TOTP otomatis dialihkan ke `/admin/totp/setup` dan diblokir dari seluruh rute panel admin lainnya.
  - **Step-Up Verification (Masa Berlaku 5 Menit / `AdminStepUpDuration`)**: Middleware `RequireStepUp` mewajibkan verifikasi ulang TOTP untuk 10 aksi sensitif admin:
    1. Perubahan pengaturan sistem (`POST /admin/settings`)
    2. Pengisian dompet modal kas (`POST /admin/wallet/topup`)
    3. Persetujuan penarikan (`POST /admin/withdrawals/{id}/approve`)
    4. Penolakan penarikan (`POST /admin/withdrawals/{id}/reject`)
    5. Reset kata sandi pekerja (`POST /admin/workers/{id}/reset-password`)
    6. Pembekuan pekerja (`POST /admin/workers/{id}/freeze`)
    7. Pembukaan pembekuan pekerja (`POST /admin/workers/{id}/unfreeze`)
    8. Penonaktifan pekerja (`POST /admin/workers/{id}/disable`)
    9. Pengaktifan pekerja (`POST /admin/workers/{id}/enable`)
    10. Pengantrean ulang nomor tidak pasti (`POST /admin/campaigns/{id}/requeue-uncertain`)
  - **Pemulihan Darurat CLI**: Jika admin kehilangan authenticator dan kehabisan kode pemulihan, reset TOTP dapat dilakukan dari terminal server:
    ```bash
    ./goblast reset-totp --email admin@domain.com
    ```
    Perintah ini menghapus rahasia TOTP, membersihkan kode pemulihan tersisa, dan mencatat audit log `admin_totp_reset_cli`.

### A7. Privasi Nomor Customer
- Nomor customer tidak pernah dirender atau dikirimkan ke antarmuka browser pekerja.
- Penyamaran otomatis di seluruh log aplikasi (contoh: `6281****678`).
- Dilarang memasukkan berkas nomor (CSV/TXT/XLSX) ke repositori git.
- Cadangan database wajib dienkripsi penuh menggunakan AES-256-GCM (`.gbak`) sebelum diunduh atau dipindahkan ke penyimpanan luar.

### A8. Log Audit & Sanitasi Error Internal
- Mencatat seluruh peristiwa krusial: pembuatan campaign, rate upah, penarikan dana, perubahan buku besar saldo, aktivitas login/logout admin, perubahan pengaturan sistem, aksi pembekuan akun, dan reset TOTP CLI.
- **Sanitasi Error Internal (Zero Error Leakage)**:
  - Dilarang keras menampilkan pesan galat internal (`err.Error()`, nama database, nama tabel, kolom, sintaks SQL, atau stack trace) pada HTTP response body maupun URL query string (`?err=...`).
  - Seluruh kegagalan internal disajikan sebagai pesan ramah pengguna (pesan i18n atau deskripsi generik yang aman).
  - Rincian teknis galat dicatat secara aman ke log aplikasi server melalui `slog.Error` dengan konteks relevan tanpa mengekspos kredensial atau nomor telepon customer.

### A9. Unggahan Berkas
- Batasi tipe berkas yang diizinkan: CSV, TXT, XLSX (daftar nomor), serta JPG, PNG (gambar kampanye).
- **Verifikasi Magic Bytes (File Signature)**:
  - Berkas XLSX diverifikasi memiliki header arsip ZIP (`PK\x03\x04`).
  - Berkas gambar kampanye diverifikasi menggunakan `http.DetectContentType` (hanya `image/jpeg` dan `image/png`).
- Batas ukuran unggahan: 1 MB untuk gambar kampanye, 6 MB untuk berkas nomor CSV/TXT/XLSX.
- Penamaan acak: Setiap berkas yang disimpan di server diberi nama UUID unik.
- Isolasi berkas: Berkas unggahan disimpan di luar folder web root publik (`neuteredFileSystem` dan direktori data terisolasi).

### A10. Header Keamanan HTTP
Aplikasi secara ketat mengirimkan header keamanan pada seluruh respons HTTP:
- `Content-Security-Policy`:
  - `default-src 'self'`
  - `script-src 'self' 'nonce-{nonce}' https://challenges.cloudflare.com`
  - `style-src 'self' 'unsafe-inline'`
  - `img-src 'self' data: https:`
  - `font-src 'self'`
  - `connect-src 'self' https://challenges.cloudflare.com`
  - `frame-src https://challenges.cloudflare.com`
  - `object-src 'none'`
  - `base-uri 'self'`
  - `form-action 'self'`
  - `frame-ancestors 'none'`
  - Seluruh elemen `<script>` di template HTML wajib menyertakan atribut `nonce="{{ .Nonce }}"`.
  - Seluruh inline JavaScript event handler (`onclick=`, `onsubmit=`, `onchange=`, dll) dilarang dan telah dieliminasi total dari berkas template (diverifikasi oleh tes guard `TestGuard_CSP_TemplatesStaticCheck`).
- `X-Content-Type-Options: nosniff`.
- `X-Frame-Options: DENY`.
- `Referrer-Policy: strict-origin-when-cross-origin`.
- `Permissions-Policy: camera=(), microphone=(), geolocation=()`.
- `Strict-Transport-Security: max-age=31536000; includeSubDomains` (dikirim otomatis saat request berstatus HTTPS di produksi).
- `Cache-Control: no-store, no-cache, must-revalidate` pada seluruh endpoint terautentikasi dan formulir login/register untuk mencegah caching browser pada data sensitif.

### A11. Penanganan IP Klien Tepercaya
- IP klien asli dibaca dari header yang ditentukan di `CLIENT_IP_HEADER` (default: `CF-Connecting-IP`).
- Validasi IP hanya mempercayai header jika koneksi TCP perantara (`RemoteAddr`) berasal dari daftar subnet tepercaya pada `TRUSTED_PROXY_CIDRS` (subnet IP resmi Cloudflare dan alamat loopback lokal).
- Nilai header IP divalidasi keabsahan formatnya menggunakan `net.ParseIP`. Jika tidak valid atau proxy perantara bukan anggota subnet tepercaya, header diabaikan dan sistem menggunakan remote address TCP murni guna mencegah spoofing IP pada mekanisme rate limiting.

---

## Bagian B. Deploy

### B1. Isolasi di VPS Linux & Unit Systemd
- Gunakan pengguna sistem tanpa privilese root (`goblast:goblast`) untuk mengoperasikan aplikasi.
- Aplikasi wajib di-bind ke loopback lokal `127.0.0.1:8090`. Bind publik `0.0.0.0` di produksi ditolak oleh server kecuali jika `ALLOW_PUBLIC_BIND=true` disetel secara eksplisit.
- Batasi alokasi memori proses menggunakan aturan MemoryMax sebesar 600M (75% dari sisa aman RAM VPS 2GB setelah dikurangi buffer OS, PostgreSQL, dan proyek lain).
- Berikan batas waktu drain `TimeoutStopSec=30s` agar proses memiliki waktu menyelesaikan transaksi berjalan saat service dimatikan.

#### Berkas Unit Lengkap `/etc/systemd/system/goblast.service`:
```ini
[Unit]
Description=GO BLAST by Feepay WhatsApp Engine
Documentation=https://github.com/fetrusmeilanoilhamsyah/GO-BLAST-by-feepay
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
User=goblast
Group=goblast
WorkingDirectory=/opt/goblast/current
ExecStart=/opt/goblast/current/goblast run

# Penanganan kegagalan & restart terkontrol
Restart=on-failure
RestartSec=5s
StartLimitIntervalSec=60s
StartLimitBurst=5

# Manajemen memori dan penghentian anggun (graceful shutdown)
MemoryMax=600M
TimeoutStopSec=30s
KillSignal=SIGTERM
LimitNOFILE=65535

# Pemuatan konfigurasi rahasia produksi (izin berkas wajib 0640 root:goblast)
EnvironmentFile=/etc/goblast/goblast.env

# Pengamanan proses sistem Linux (Hardening)
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
NoNewPrivileges=true
ReadWritePaths=/opt/goblast/data /opt/goblast/backups /opt/goblast/uploads

# Standar output diarahkan ke systemd journal
StandardOutput=journal
StandardError=journal
SyslogIdentifier=goblast
LogRateLimitIntervalSec=30s
LogRateLimitBurst=500

[Install]
WantedBy=multi-user.target
```

Perintah aktivasi service di VPS:
```bash
sudo systemctl daemon-reload
sudo systemctl enable goblast
sudo systemctl start goblast
sudo systemctl status goblast
```

### B2. Konfigurasi Nginx Reverse Proxy
Konfigurasi Nginx di bawah ini mencakup proteksi batas ukuran unggahan 6 MB, penerusan header IP Cloudflare, serta pembatasan akses direktori internal.

#### Berkas Konfigurasi Lengkap `/etc/nginx/sites-available/goblast.conf`:
```nginx
# Konfigurasi Subnet Resmi Cloudflare untuk Restorasi IP Klien Asli
set_real_ip_from 173.245.48.0/20;
set_real_ip_from 103.21.244.0/22;
set_real_ip_from 103.22.200.0/22;
set_real_ip_from 103.31.4.0/22;
set_real_ip_from 141.101.64.0/18;
set_real_ip_from 108.162.192.0/18;
set_real_ip_from 190.93.240.0/20;
set_real_ip_from 188.114.96.0/20;
set_real_ip_from 197.234.240.0/22;
set_real_ip_from 198.41.128.0/17;
set_real_ip_from 162.158.0.0/15;
set_real_ip_from 104.16.0.0/13;
set_real_ip_from 104.24.0.0/14;
set_real_ip_from 172.64.0.0/13;
set_real_ip_from 131.0.72.0/22;
set_real_ip_from 2400:cb00::/32;
set_real_ip_from 2606:4700::/32;
set_real_ip_from 2803:f800::/32;
set_real_ip_from 2405:b500::/32;
set_real_ip_from 2405:8100::/32;
set_real_ip_from 2a06:98c0::/29;
set_real_ip_from 2c0f:f248::/32;
set_real_ip_from 127.0.0.1;
real_ip_header CF-Connecting-IP;

server {
    listen 80;
    listen [::]:80;
    server_name goblast.feepay.web.id;

    # Pengalihan otomatis HTTP ke HTTPS
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name goblast.feepay.web.id;

    # Sertifikat SSL (Let's Encrypt / Cloudflare Origin CA)
    ssl_certificate /etc/ssl/certs/goblast.crt;
    ssl_certificate_key /etc/ssl/private/goblast.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;

    # Batas ukuran payload unggahan (sesuai batas file nomor 6 MB)
    client_max_body_size 6m;

    # Isolasi berkas sensitif dari web
    location ~ /\.(env|git|gbak|sql) {
        deny all;
        return 404;
    }

    # Optional: Batasi akses IP khusus untuk rute admin (jika diinginkan pemilik)
    # location ^~ /admin {
    #     allow 203.0.113.50; # IP kantor / VPN pemilik
    #     deny all;
    #     proxy_pass http://127.0.0.1:8080;
    #     proxy_set_header Host $host;
    #     proxy_set_header X-Real-IP $remote_addr;
    #     proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    #     proxy_set_header X-Forwarded-Proto $scheme;
    #     proxy_set_header CF-Connecting-IP $http_cf_connecting_ip;
    # }

    # Rute utama aplikasi Go
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        # Header penerusan identitas klien dan protokol
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header CF-Connecting-IP $http_cf_connecting_ip;

        # Timeout proxy yang wajar untuk operasi upload dan polling
        proxy_connect_timeout 10s;
        proxy_read_timeout 90s;
        proxy_send_timeout 90s;

        # Nonaktifkan buffering proxy untuk respons cepat
        proxy_buffering off;
    }
}
```

Aktivasi Nginx:
```bash
sudo ln -s /etc/nginx/sites-available/goblast.conf /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

### B3. SSL dan Domain
- Pasang sertifikat SSL gratis via Certbot (`certbot --nginx -d goblast.feepay.web.id`) atau pasang Cloudflare Origin CA Certificate dengan mode enkripsi Cloudflare *Full (Strict)*.
- **Peringatan Domain**: Subdomain `feepay.web.id` digunakan bersama oleh aplikasi lain. Dilarang mengubah root record domain atau sertifikat domain utama tanpa koordinasi dengan pengelola sistem Feepay lainnya.

### B4. Variabel lingkungan
Tabel variabel lingkungan berikut menyelaraskan seluruh variabel yang dibaca di kode (`internal/config/config.go`), `.env.example`, dan dokumentasi deployment:

| Nama Variabel | Fungsi | Contoh Nilai | Sifat |
|---|---|---|---|
| `APP_ENV` | Lingkungan aplikasi | `production` / `dev` | Wajib |
| `APP_ADDR` | Alamat bind aplikasi | `127.0.0.1:8080` (wajib loopback di produksi) | Wajib |
| `ALLOW_PUBLIC_BIND` | Mengizinkan bind publik di produksi jika `true` | `false` | Opsional |
| `APP_PUBLIC_HOST` | Host publik aplikasi untuk validasi Origin CSRF | `goblast.feepay.web.id` | Opsional |
| `DATABASE_URL` | String koneksi PostgreSQL | `postgres://<user>:<pass>@localhost:5432/goblast` | Wajib |
| `TEST_DATABASE_URL` | String koneksi database PostgreSQL untuk pengujian | `postgres://<user>:<pass>@localhost:5432/goblast_test` | Wajib untuk tes |
| `DB_MAX_CONNS` | Batas maksimum koneksi pool DB aplikasi | `25` (default 25) | Opsional |
| `DB_MIN_CONNS` | Batas minimum koneksi pool DB aplikasi | `5` (default 5) | Opsional |
| `SESSION_SECRET` | Rahasia cookie sesi dan tanda tangan CSRF (32 byte base64) | `(base64 dari 32 byte acak)` | Wajib |
| `ENCRYPTION_KEY` | Kunci enkripsi data rekening penarikan (32 byte base64) | `(base64 dari 32 byte acak)` | Wajib |
| `ENCRYPTION_KEY_PREV` | Kunci enkripsi versi sebelumnya (rotasi kunci) | `(base64 dari 32 byte)` | Opsional |
| `BACKUP_KEY` | Kunci enkripsi cadangan database (.gbak) AES-256-GCM (32 byte base64) | `(base64 dari 32 byte acak)` | Wajib untuk fitur backup |
| `BACKUP_KEY_PREV` | Kunci cadangan versi sebelumnya untuk rotasi kunci cadangan | `(base64 dari 32 byte)` | Opsional |
| `ALLOW_WEB_RESTORE` | Mengizinkan pemulihan database melalui antarmuka panel admin | `false` (default false) | Opsional |
| `MAX_ACTIVE_SESSIONS` | Batas maksimal sesi WhatsApp aktif simultan | `20` (nilai awal sementara; sesuaikan setelah pemilik mengukur memori riil) | Wajib di prod |
| `CS_WHATSAPP_URL` | Tautan WhatsApp Customer Service resmi | `https://wa.me/6281200000000` | Opsional |
| `CS_TELEGRAM_URL` | Tautan Telegram Customer Service resmi | `https://t.me/feepay_cs` | Opsional |
| `CHANNEL_TELEGRAM_URL` | Tautan Channel Telegram resmi | `https://t.me/goblast_channel` | Opsional |
| `CLIENT_IP_HEADER` | Nama header IP klien asli dari reverse proxy | `CF-Connecting-IP` | Opsional (default CF-Connecting-IP) |
| `TRUSTED_PROXY_CIDRS` | Daftar CIDR proxy tepercaya untuk parsing IP asli klien (Cloudflare & Loopback) | `127.0.0.1/32,::1/128,173.245.48.0/20,...` | Opsional (default internal/Cloudflare) |
| `TURNSTILE_SITE_KEY` | Site key Cloudflare Turnstile publik | `0x4AAAAAA...` | Opsional |
| `TURNSTILE_SECRET_KEY` | Secret key Cloudflare Turnstile | `0x4AAAAAA...` | Opsional |

#### Rumus Kapasitas Koneksi Database:
Untuk mencegah kegagalan pool koneksi database di produksi akibat kehabisan koneksi PostgreSQL, kapasitas koneksi harus memenuhi rumus:
$$\text{DB\_MAX\_CONNS} + \text{batas pool whatsmeow} + 10 < \text{max\_connections PostgreSQL}$$

Penjelasan parameter:
- `DB_MAX_CONNS`: batas pool `pgxpool` aplikasi utama (default: 25).
- `batas pool whatsmeow`: batas pool whatsmeow sqlstore (`SetMaxOpenConns`, default: 10).
- `10`: margin pengaman untuk proses migrasi database, koneksi administrasi psql, dan pemantauan `pg_stat`.
- `max_connections PostgreSQL`: batas maksimal koneksi di PostgreSQL (default umumnya 100).
- Contoh konfigurasi default: $25 + 10 + 10 = 45 < 100$ (Aman).

### B4.1 Prosedur Pengukuran Memori Riil per Sesi WhatsApp

Batas sesi aktif (`MAX_ACTIVE_SESSIONS`) dan alokasi `MemoryMax` tidak boleh diasumsikan tanpa pengukuran. Pemilik wajib melakukan pengukuran langsung di lingkungan VPS menggunakan akun WhatsApp cadangan sebelum membuka sistem ke banyak pekerja.

#### Langkah-Langkah Pengukuran:
1. **Catat Kondisi Awal (Baseline):**
   Sebelum ada sesi WhatsApp yang terhubung, catat metrik memori proses:
   - `HeapAlloc` dan `Sys` dari endpoint `/admin/api/metrics`.
   - `RSS` (Resident Set Size) dari terminal VPS: `ps -o rss= -p $(pidof goblast)`.
2. **Uji Bertahap (1, 3, dan 5 Akun):**
   - Sambungkan 1 akun WhatsApp cadangan, tunggu 2 menit dalam keadaan idle terhubung, lalu catat metrik.
   - Sambungkan akun ke-2 dan ke-3 (total 3 akun), tunggu 2 menit, catat metrik.
   - Sambungkan akun ke-4 dan ke-5 (total 5 akun), tunggu 2 menit, catat metrik.
3. **Uji Pengiriman Riil:**
   - Buat kampanye kecil dan kirim pesan ke nomor milik sendiri melalui akun-akun cadangan tersebut, lalu catat lonjakan metrik saat proses kirim berlangsung.
4. **Rumus Perhitungan:**
   - **Memori per sesi**:
     $$\text{Memori per sesi} = \frac{\text{RSS dengan } n \text{ akun} - \text{RSS awal}}{n}$$
     *Gunakan nilai terbesar dari seluruh baris pengukuran, bukan nilai rata-rata.*
   - **Kapasitas MAX_ACTIVE_SESSIONS**:
     $$\text{MAX\_ACTIVE\_SESSIONS} = \left\lfloor \frac{\text{RAM yang dialokasikan untuk aplikasi} - \text{RSS awal}}{1{,}5 \times \text{Memori per sesi}} \right\rfloor$$
     *Faktor pengali 1,5 memberikan margin pengaman 50% untuk lonjakan sinkronisasi data WhatsApp dan buffer jaringan.*

#### Lembar Tabel Isian Pengukuran Pemilik:

| Kondisi Uji | Jumlah Akun | HeapAlloc (MB) | Sys (MB) | RSS (KB / MB) | Kenaikan RSS dari Awal | Memori per Sesi | Catatan |
|---|---|---|---|---|---|---|---|
| Awal (Idle / No WA) | 0 | ... | ... | ... | 0 | - | Baseline sebelum sesi WA |
| Idle 1 Akun (2 menit) | 1 | ... | ... | ... | ... | ... | Akun cadangan #1 |
| Idle 3 Akun (2 menit) | 3 | ... | ... | ... | ... | ... | Akun cadangan #1–#3 |
| Idle 5 Akun (2 menit) | 5 | ... | ... | ... | ... | ... | Akun cadangan #1–#5 |
| Mengirim Pesan Nyata | 5 | ... | ... | ... | ... | ... | Kirim campaign kecil ke nomor sendiri |
| **Nilai Terbesar Diambil** | - | - | - | - | - | **... MB** | *Gunakan nilai puncak ini untuk rumus* |


### B5. Konfigurasi dan Tuning PostgreSQL
Untuk server VPS ramping (RAM 1 GB – 2 GB), sesuaikan berkas konfigurasi `/etc/postgresql/17/main/postgresql.conf`:

```ini
# Koneksi dan Memori
max_connections = 100
shared_buffers = 256MB                  # 512MB jika RAM VPS 2 GB
work_mem = 4MB
maintenance_work_mem = 64MB
effective_cache_size = 768MB

# Write Ahead Log (WAL) & Checkpoint
checkpoint_completion_target = 0.9
wal_buffers = 16MB
default_statistics_target = 100

# Timeouts Pengaman Aplikasi
statement_timeout = 30000               # 30 detik
lock_timeout = 5000                     # 5 detik
idle_in_transaction_session_timeout = 30000 # 30 detik
```

#### Pengaturan Autovacuum Agresif untuk Tabel Antrean:
Tabel `campaign_numbers` mengalami ratusan operasi `UPDATE` klaim dan status per menit, yang dapat menimbulkan bloat dead tuples jika tidak segera dibersihkan. Jalankan perintah SQL berikut di database produksi:
```sql
ALTER TABLE campaign_numbers SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02,
    autovacuum_vacuum_cost_limit = 1000
);
```

### B6. Migrasi Database
- Seluruh migrasi berada di direktori `db/migrations/` dan dieksekusi maju menggunakan perintah CLI resmi:
  ```bash
  /opt/goblast/current/goblast migrate up
  ```
- Periksa versi migrasi saat ini:
  ```bash
  /opt/goblast/current/goblast migrate version
  ```
- Dilarang keras mengedit berkas migrasi lama yang telah diterapkan di produksi (`0001` s.d. `0025`). Seluruh penyesuaian skema baru wajib dibuat sebagai file migrasi baru dengan bagian `Up` dan `Down`.

### B7. Cadangan Database dan Prosedur Pemulihan (Tahap H9)
Sistem dilengkapi modul cadangan otomatis terenkripsi penuh yang beroperasi tanpa membocorkan kunci enkripsi ke berkas cadangan:

1. **Jadwal dan Retensi Otomatis**:
   - Cadangan harian dieksekusi otomatis setiap pukul **03:00 WIB**.
   - Skema retensi GFS: menyimpan 7 cadangan harian dan 4 cadangan mingguan.
   - Pengecekan otomatis ruang disk: minimal 2x ukuran cadangan terakhir atau 100 MB ruang kosong sebelum pembuatan cadangan dimulai.
   - Cadangan terverifikasi terakhir dijamin **tidak pernah dihapus** oleh sistem retensi maupun penghapusan manual.
   - Berkas cadangan fisik disimpan di folder `/opt/goblast/backups/` dalam format `.gbak` (AES-256-GCM 64 KB streaming chunks + kompresi gzip + trailer integritas).
   - Seluruh tabel `whatsmeow_*`, `web_sessions`, dan `goose_db_version` **dikecualikan secara ketat**.

2. **Penyalinan Salinan Luar Server (Off-Site Backup)**:
   Pemilik wajib menyimpan salinan di luar server VPS secara berkala:
   - **Manual melalui Panel Admin**:
     Buka menu `/admin/backups`, pilih berkas cadangan terbaru, lalu klik **Unduh**. Aksi ini dilindungi oleh Step-Up TOTP 2FA.
   - **Otomatis via rclone (direkomendasikan)**:
     Pasang `rclone` di server VPS dan arahkan sinkronisasi ke penyimpanan cloud terpisah (seperti Cloudflare R2, AWS S3, atau Wasabi):
     ```bash
     # Contoh sinkronisasi harian pukul 04:00 WIB via cron:
     rclone sync /opt/goblast/backups/ remote-s3:goblast-offsite-backups/ --fast-list
     ```
   - **Otomatis via scp ke komputer/laptop pribadi pemilik**:
     ```bash
     scp -P 22 deploy@vps.goblast.id:/opt/goblast/backups/*.gbak ~/Backups/goblast/
     ```

3. **Prosedur Pemulihan Bencana (Disaster Recovery Drill)**:
   Pemulihan basis data dieksekusi secara atomik menggunakan perintah CLI resmi.

   **Skenario A: Pemulihan ke Database Kosong (Bencana Total)**
   ```bash
   # 1. Hentikan aplikasi GO BLAST
   sudo systemctl stop goblast

   # 2. Siapkan database PostgreSQL target baru yang bersih
   createdb -h localhost -U postgres goblast

   # 3. Jalankan migrasi hingga versi terbaru
   /opt/goblast/current/goblast migrate up

   # 4. Eksekusi pemulihan dari berkas .gbak
   /opt/goblast/current/goblast restore --file /opt/goblast/backups/20261017_030000_otomatis_abc123.gbak

   # 5. Nyalakan kembali aplikasi
   sudo systemctl start goblast
   ```

   **Skenario B: Pemulihan ke Database yang Tidak Kosong**
   ```bash
   # Gunakan bendera --force-nonempty (sistem meminta konfirmasi ketik kata 'RESTORE')
   /opt/goblast/current/goblast restore --file /opt/goblast/backups/20261017_030000_otomatis_abc123.gbak --force-nonempty
   ```

4. **Verifikasi Pasca-Pemulihan**:
   Proses pemulihan secara otomatis memvalidasi integritas data:
   - Menghitung kecocokan baris dengan metadata header.
   - Mereset seluruh sesi WhatsApp ke status `'terputus'` (pekerja menghubungkan ulang akun secara aman).
   - Mereset seluruh sequence ID ke `MAX(id)+1`.
   - Menguji dekripsi sampel rekening penarikan dengan `ENCRYPTION_KEY` aktif.
   - Memastikan 0 saldo minus, 0 duplikat upah/referral, dan 0 foreign key orphan rows.

5. **Latihan Uji Coba Pemulihan (Restoration Drill)**:
   Lakukan uji coba pemulihan pada mesin lokal atau server staging minimal sekali setiap bulan:
   1. Unduh berkas `.gbak` terbaru dari panel `/admin/backups`.
   2. Pasang environment pengujian dengan `BACKUP_KEY` dan `ENCRYPTION_KEY` yang sama.
   3. Jalankan `./goblast restore --file <berkas.gbak> --force-nonempty` pada database pengujian.
   4. Periksa log hasil pemulihan untuk memastikan seluruh invarian data berstatus lulus (`OK`).

### B8. Prosedur Deploy Rilis Baru
Lakukan deploy pada jam sepi trafik dan berikan pemberitahuan jeda layanan kepada pekerja:

1. **Kompilasi Biner Linux & Siapkan Paket Rilis**:
   Kompilasi biner pada mesin build lokal dan kemas bersama aset (templates, static, locales, migrations):
   ```bash
   GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o deploy/goblast ./cmd/goblast
   tar -czf deploy/goblast-v1.0.0.tar.gz -C deploy/stage .
   sha256sum deploy/goblast-v1.0.0.tar.gz > deploy/goblast-v1.0.0.tar.gz.sha256
   ```
2. **Kirim Paket Rilis ke Server**:
   ```bash
   scp deploy/goblast-v1.0.0.tar.gz* deploy@vps:/tmp/
   ```
3. **Eksekusi Deploy Atomik via `goblastctl`**:
   Utilitas `goblastctl deploy` secara otomatis memverifikasi SHA256, mengekstrak ke `/opt/goblast/releases/v1.0.0`, menghubungkan symlink folder persisten (`data`, `uploads`, `backups`), mengeksekusi migrasi database, mengalihkan symlink `/opt/goblast/current`, merestart service, serta memverifikasi kesehatan `/readyz` (otomatis rollback jika gagal):
   ```bash
   goblastctl deploy /tmp/goblast-v1.0.0.tar.gz
   ```
4. **Verifikasi Diagnostik Pasca-Deploy**:
   ```bash
   goblastctl doctor
   goblastctl status
   ```
5. **Konsekuensi Arsitektur Sesi WhatsApp (Keputusan B1)**:
   Proses shutdown mematikan koneksi soket whatsmeow di memori. Sesi WhatsApp terputus dan status akun direset ke `terputus`. Pekerja wajib melakukan scan ulang QR code atau tautkan ulang perangkat untuk memulai sesi kerja baru.

### B9. Prosedur Rollback
Jika rilis versi baru mengalami galat fatal atau regresi:

1. **Rollback Cepat via `goblastctl`**:
   Perintah ini mengalihkan symlink `/opt/goblast/current` kembali ke rilis sebelumnya dan merestart service secara anggun:
   ```bash
   goblastctl rollback
   ```
2. **Rollback Migrasi Database (Jika Diperlukan)**:
   Hanya lakukan jika migrasi versi baru memiliki skrip `Down` yang aman dan perlu diturunkan:
   ```bash
   cd /opt/goblast/current && ./goblast migrate down
   ```
3. **Verifikasi Status**:
   ```bash
   goblastctl doctor
   goblastctl status
   ```
5. **Verifikasi Status**:
   ```bash
   sudo systemctl status goblast
   curl -I http://127.0.0.1:8080/readyz
   ```

### B10. Incident Runbook Singkat

#### (a) Memori Membengkak / Mendekati Batas MemoryMax
- **Gejala**: RSS memori mendekati batas `MemoryMax`, log systemd mencatat peringatan cgroup memory, atau proses dihentikan oleh OOM killer.
- **Penyebab**: Lonjakan sesi WhatsApp aktif melebihi kapasitas memori server, atau antrean unggahan nomor berukuran besar.
- **Tindakan**:
  1. Restart proses untuk merebut kembali memori heap: `sudo systemctl restart goblast`.
  2. Turunkan sementara nilai `MAX_ACTIVE_SESSIONS` di file `.env` (misal dari 20 menjadi 10).
  3. Muat ulang konfigurasi: `sudo systemctl restart goblast`.

#### (b) Disk Server / Database Penuh
- **Gejala**: PostgreSQL menolak transaksi baru dengan galat `could not extend file: No space left on device`.
- **Tindakan**:
  1. Periksa ruang disk: `df -h`.
  2. Bersihkan berkas sementara lama: `find /var/www/goblast/data/tmp/ -type f -mmin +60 -delete`.
  3. Bersihkan log journald lama: `sudo journalctl --vacuum-size=200M`.
  4. Jalankan vacuum manual pada tabel antrean: `psql -U goblast -d goblast -c "VACUUM (ANALYZE) campaign_numbers;"`.

#### (c) Kunci Enkripsi Bocor (`ENCRYPTION_KEY` / `BACKUP_KEY`)
- **Tindakan Rotasi Darurat**:
  1. Generate kunci acak baru 32 byte base64: `openssl rand -base64 32`.
  2. Edit berkas `/var/www/goblast/.env`:
     - Pindahkan nilai `ENCRYPTION_KEY` lama ke `ENCRYPTION_KEY_PREV`.
     - Masukkan kunci acak baru ke `ENCRYPTION_KEY`.
     - Lakukan hal serupa pada `BACKUP_KEY` dan `BACKUP_KEY_PREV` jika kunci cadangan turut bocor.
  3. Restart aplikasi: `sudo systemctl restart goblast`.
  4. Aplikasi otomatis mendekripsi data lama menggunakan `k0` dan menulis ulang data baru menggunakan `k1`.
  5. Periksa log aplikasi untuk memastikan tidak ada galat dekripsi.

#### (d) Nomor Customer Terindikasi Bocor
- **Tindakan Isolasi**:
  1. Buka menu `/admin/campaigns` dan ubah status seluruh kampanye aktif menjadi `paused`.
  2. Periksa log audit pengunduhan sisa nomor: `SELECT * FROM audit_logs WHERE action = 'admin_campaign_export_numbers' ORDER BY created_at DESC LIMIT 20;`.
  3. Bekukan akun admin atau pekerja yang terindikasi mencurigakan via panel admin atau perintah SQL.

#### (e) Pencabutan Sesi Admin & Pekerja Massal
- **Tindakan Cabut Sesi Seketika**:
  Jika token sesi atau cookie dicuri secara massal:
  1. Eksekusi query penghapusan seluruh sesi web dari psql:
     ```sql
     DELETE FROM web_sessions;
     ```
  2. Putus seluruh sesi WhatsApp yang sedang terhubung:
     ```bash
     sudo systemctl restart goblast
     ```
  3. Seluruh admin dan pekerja otomatis ter-logout dan wajib memasukkan kredensial baru.
  4. *(Rencana CLI TODO)*: Sediakan perintah CLI `goblast revoke-sessions --all` agar tindakan ini dapat dijalankan langsung tanpa membuka psql.

### B11. Pemantauan dan Tanda Bahaya (Monitoring)

#### Metrik Utama yang Wajib Dipantau:
1. **Penggunaan Memori (RSS)**: Wajib dipantau agar berada di bawah 80% dari batas `MemoryMax` cgroup.
2. **Koneksi Database Aktif**: Periksa via `SELECT count(*) FROM pg_stat_activity WHERE datname = 'goblast';`. Jumlah koneksi tidak boleh mendekati 100.
3. **Ruang Disk Bebas**: Partisi root `/` wajib memiliki ruang bebas minimal 2 GB untuk menampung cadangan harian.

#### Pola Log Tanda Bahaya di `journalctl -u goblast -f`:
- `cipher: message authentication failed`: Indikasi kunci enkripsi salah atau korupsi data rekening.
- `ArgonGate rejected: queue full`: Indikasi serangan brute-force login massal yang sedang membebani server.
- `pool exhausted`: Waktu tunggu koneksi database habis; segera cek query yang terkunci atau naikkan kapasitas pool.
- `whatsmeow connect error`: Gangguan jaringan ke server WhatsApp; pantau apakah terjadi massal atau pada nomor tertentu.

---

## Bagian C. VPS Tetap Ramping

### C1. Daftar Berkas di Server Produksi
Untuk menjaga keamanan dan meminimalkan jejak penyimpanan di server VPS:

- **HANYA BERKAS INI YANG BOLEH ADA DI SERVER PRODUKSI**:
  - Direktori rilis terisolasi: `/opt/goblast/releases/<versi>/` (biner `goblast`, `web/templates/`, `web/static/`, `locales/`, `db/migrations/`).
  - Symlink rilis aktif: `/opt/goblast/current -> /opt/goblast/releases/<versi>`.
  - Direktori penyimpanan data operasional persisten: `/opt/goblast/data/`, `/opt/goblast/uploads/`, `/opt/goblast/backups/`.
  - Berkas konfigurasi rahasia: `/etc/goblast/goblast.env` (izin akses ketat `0640`, pemilik `root:goblast`).
  - Berkas daftar IP Cloudflare: `/etc/goblast/cloudflare-ips.conf`.
  - Berkas alat operasional CLI: `/usr/local/bin/goblastctl`.
- **DILARANG KERAS BERADA DI SERVER PRODUKSI**:
  - Seluruh kode sumber Go (`*.go`).
  - Repositori git (`.git/`, `.gitignore`).
  - Kompiler Go dan toolchain pengembangan (`go`, `gopls`, `gcc`).
  - Direktori spike dan prototipe (`spike/`).
  - Data pengujian dan mock (`testdata/`, berkas pengujian `*_test.go`).
  - Berkas nomor telepon mentah (`*.csv`, `*.txt`, `*.xlsx`).

### C2. Tabel Direktori yang Bertumbuh

| Direktori | Peruntukan | Perkiraan Pertumbuhan | Penanggung Jawab | Mekanisme Pembersihan |
|---|---|---|---|---|
| `data/tmp/` | Berkas sementara ekspor dan pemrosesan impor | 10–50 MB / hari | Aplikasi backend | Dibersihkan otomatis oleh pembersih berkas sementara untuk file berusia > 1 jam (`TmpFilesMaxAge`). |
| `uploads/` | Lampiran gambar kampanye pengiriman | 1–5 MB per kampanye | Admin & Aplikasi | Dihapus otomatis saat admin mengeksekusi pembersihan penyimpanan arsip kampanye lama (`AdminStoragePurge`). |
| `backups/` | Berkas cadangan database terenkripsi (`.gbak`) | 5–20 MB per cadangan harian | Aplikasi backend (Modul H9) | Dikelola otomatis oleh skema retensi GFS (7 harian, 4 mingguan). Cadangan terverifikasi terakhir dilindungi dari penghapusan. |
| Log Systemd (`journald`) | Log output aplikasi Go dan Nginx | 50–200 MB / minggu | OS Linux (`journald`) | Dibatasi otomatis melalui konfigurasi `/etc/systemd/journald.conf` dengan `SystemMaxUse=500M`. |

---

## Bagian D. Daftar Kesiapan Produksi (Go-Live Checklist)

Tabel berikut memuat 21 butir verifikasi kesiapan sistem sebelum GO BLAST dibuka untuk trafik publik dan pekerja:

| No | Butir Periksa | Kategori | Status | Cara Membuktikan | Penanggung Jawab |
|---|---|---|---|---|---|
| 1 | 22 Variabel Lingkungan Lengkap | Konfigurasi | Siap | Jalankan unit test `TestGuard_EnvVarsSync`; periksa kelengkapan file `.env` produksi. | Admin / Sistem |
| 2 | Kunci Enkripsi Acak 32-Byte | Keamanan | Siap | Periksa panjang decoding base64 dari `ENCRYPTION_KEY`, `BACKUP_KEY`, dan `SESSION_SECRET` (wajib tepat 32 byte, bukan nilai contoh). | Admin |
| 3 | Migrasi Skema DB Versi Terbaru | Database | Siap | Eksekusi `./goblast migrate version`; pastikan versi migrasi berada di versi `24` tanpa galat tertunda. | Admin |
| 4 | Admin Pertama & TOTP Aktif | Akses | Perlu Tindakan | Buat akun admin pertama melalui CLI; login dan tuntaskan alur setup TOTP di `/admin/totp/setup`. | Pemilik |
| 5 | Step-Up 2FA Berfungsi | Akses | Siap | Akses rute sensitif (contoh: ubah pengaturan di `/admin/settings`); verifikasi modal permintaan TOTP 5 menit muncul. | Admin |
| 6 | Cloudflare Turnstile Aktif & Teruji | Proteksi Bot | Perlu Tindakan | Buka form login & register; pastikan widget Turnstile muncul dan verifikasi berhasil dengan kunci Cloudflare resmi. | Admin |
| 7 | CSP Bebas Pelanggaran di Browser | Keamanan Web | Siap | Buka seluruh halaman pekerja dan admin dengan console browser terbuka; pastikan 0 pesan CSP violation. | Admin |
| 8 | Logout POST-Only Ditegakkan | Akses | Siap | Akses rute `GET /logout`; pastikan server mengembalikan kode respons HTTP 405 Method Not Allowed. | Sistem |
| 9 | Uji Cadangan & Integritas (.gbak) | Cadangan | Siap | Buat cadangan manual di `/admin/backups`; periksa kemunculan file `.gbak` dan status integritas terverifikasi di database. | Admin |
| 10 | Uji Coba Latihan Pemulihan (Drill) | Bencana | Siap | Jalankan `./goblast restore --file <file.gbak> --force-nonempty` pada database staging; verifikasi seluruh data pulih utuh. | Admin |
| 11 | MemoryMax 75% RAM VPS di Systemd | Infrastruktur | Perlu Tindakan | Hitung 75% RAM VPS dan ukur memori per sesi riil; sesuaikan unit systemd goblast.service. | Pemilik |
| 12 | TimeoutStopSec 30s di Systemd | Infrastruktur | Siap | Jalankan `systemctl show goblast -p TimeoutStopUSec`; pastikan bernilai `30000000` (30s). | Admin |
| 13 | Nginx Proxy Headers & 6m Body Size | Web Server | Siap | Periksa `nginx -T`; verifikasi `client_max_body_size 6m;` dan penerusan header `CF-Connecting-IP`. | Admin |
| 14 | SSL / HTTPS Aktif (Grade A) | Keamanan Web | Siap | Akses domain via HTTPS di browser; verifikasi gembok SSL valid dan header HSTS terpasang. | Admin |
| 15 | Tautan CS & Channel Resmi Valid | Konfigurasi | Siap | Klik tautan CS WhatsApp, CS Telegram, dan Channel di footer; verifikasi mengarah ke URL resmi berawalan `https://`. | Admin |
| 16 | Koneksi WhatsApp Terhubung & Putus Bersih | Integrasi WA | Perlu Tindakan | Tautkan 1 nomor WhatsApp uji coba; verifikasi QR terhubung, status menjadi terhubung, dan tombol putuskan bekerja bersih. | Pemilik |
| 17 | Worker Financial Rate Limit Berfungsi | Keamanan Akses | Siap | Kirim permintaan penarikan beruntun; pastikan permintaan ke-11 dalam 10 menit menerima HTTP 429 Too Many Requests. | Sistem |
| 18 | ArgonGate Melindungi Serangan Brute-Force | Performa | Siap | Verifikasi pembatasan komputasi argon2id aktif dengan `ArgonConcurrencyLimit = 2` dan `ArgonQueueLimit = 8`. | Sistem |
| 19 | Autovacuum Agresif Tabel Antrean | Database | Siap | Query `pg_options` pada tabel `campaign_numbers`; pastikan `autovacuum_vacuum_scale_factor = 0.05`. | Admin |
| 20 | Direktori Bersih Tanpa Kode Sumber | Kebersihan VPS | Siap | Jalankan `find /opt/goblast -name "*.go" -o -name ".git"`; pastikan output kosong di server produksi. | Admin |
| 21 | go test -race lulus untuk paket engine, wa, web, campaign, backup | Pengujian | Perlu Tindakan | Jalankan go test -race ./internal/... di lingkungan dengan gcc atau WSL/Linux. | Pemilik |
