# DOC_SECURITY_DEPLOY

Panduan dan aturan mengenai keamanan aplikasi dan prosedur deploy sistem GO BLAST.

## Bagian A. Keamanan

### A1. Injeksi SQL
- Gunakan argumen berparameter pada seluruh query PostgreSQL.
- Dilarang menyusun atau menggabungkan string secara manual untuk membuat query SQL.

### A2. Kata sandi dan sesi
- Gunakan algoritma argon2id untuk hashing kata sandi (parameter: memory=64MB, time=1, threads=4, salt=16 byte, key=32 byte).
- Panjang kata sandi dibatasi minimal 10 karakter dan maksimal 128 byte untuk mencegah serangan Denial of Service (DoS) memory exhaustion.
- Seluruh verifikasi dan pembuatan hash argon2id dikawal oleh `ArgonGate` (`ArgonConcurrencyLimit = 2`, `ArgonQueueLimit = 8`, `ArgonMaxWait = 2s`). Jika server sibuk/antrean penuh, permintaan verifikasi langsung ditolak dengan HTTP 503 (`Retry-After: 2`) tanpa menghabiskan RAM server.
- Mitigasi timing attack pada login: jika akun tidak ditemukan di database, sistem tetap mengeksekusi komputasi argon2id menggunakan `ShadowHash` konstan.
- Status pembekuan akun (`frozen`) hanya dicek SETELAH verifikasi kata sandi cocok, untuk mencegah kebocoran status eksistensi/pembekuan akun ke penyerang.
- Atribut cookie sesi:
  - Produksi: `__Host-gb_session` dengan flag `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`.
  - Pengembangan: `gb_session` dengan flag `HttpOnly`, `SameSite=Lax`, `Path=/`.
- Batas akumulasi sesi: Setiap kali pengguna login, sesi kedaluwarsa dibersihkan, dan sesi aktif dibatasi maksimal 5 sesi simultan per akun (sesi paling tua otomatis dicabut). Pembersih latar belakang (`startSessionCleaner`) berjalan berkala tiap jam untuk membersihkan sesi basi.
- Masa berlaku sesi: Admin 24 jam, Pekerja 7 hari. Pembaruan `last_seen_at` di database dibatasi maksimal 1 kali per menit per sesi untuk mencegah *write thrashing*.
- Prosedur pencabutan sesi: Pengguna dapat mencabut seluruh sesi lain saat mengganti kata sandi di menu keamanan pekerja (`POST /worker/security`). Admin dapat mencabut sesi dari tabel maintenance/worker detail.

### A3. CSRF
- Wajibkan token CSRF pada semua permintaan state-changing (POST).
- Cookie CSRF: `__Host-gb_csrf` di produksi (`Secure`, `SameSite=Lax`), `gb_csrf` di pengembangan.
- Token CSRF ditandatangani menggunakan HMAC-SHA256 (`SESSION_SECRET`) dan divalidasi dengan `subtle.ConstantTimeCompare`.
- Pengamanan lapis ganda CSRF: Pemeriksaan header browser `Sec-Fetch-Site` (menolak `cross-site`) dan verifikasi kecocokan header `Origin` dengan host publik server.

### A4. Pembatasan laju (Rate Limiting) & Proteksi Bot
- Menggunakan rate limiter token bucket berbasis `golang.org/x/time/rate` di memori (`internal/ratelimit`) dengan pengusiran LRU (`maxKeys = 10000`) dan pembersihan berkala tiap 10 menit dengan panic recovery.
- Arsitektur rate limiting login multi-lapis (Temuan K1):
  - Lapis 1 (Global): 100 request/menit untuk seluruh sistem.
  - Lapis 2 (IP Klien): 20 kegagalan per 15 menit per IP klien.
  - Lapis 3 (Identifier Akun): 10 kegagalan per 15 menit per username/email.
  - Lapis 4 (Pasangan IP + Identifier): 5 kegagalan per 15 menit. Berhasil login me-reset pembatasan pasangan.
- Pembatasan laju pendaftaran: 10 pendaftaran per jam per IP, dan batas harian 100 per hari per IP.
- Perlindungan bot tanpa JavaScript berat:
  - Form Timing Token (HMAC timestamp): Formulir login dan registrasi menolak pengiriman yang terlalu cepat (< 2 detik) atau kedaluwarsa (> 2 jam).
  - Honeypot Field (`website_hp`): Field tersembunyi via CSS yang jika diisi bot akan langsung ditolak tanpa eksekusi database atau argon2.
- Integrasi Cloudflare Turnstile (Keputusan F1):
  - Verifikasi CAPTCHA modern di form login dan registrasi via Cloudflare siteverify endpoint (timeout 3 detik).
  - Mode fail-closed untuk form registrasi (menolak jika Cloudflare outage) dan fail-open terkontrol untuk login hanya jika Cloudflare API benar-benar mengalami gangguan jaringan/5xx.
- Pembebasan pembatasan oleh Admin: Halaman `/admin/maintenance` menyediakan daftar pembatasan aktif (`ActiveRestrictions`) dengan penyamaran kunci IP/identitas, tombol cabut pembatasan per kunci, dan tombol cabut semua (`release-all`). Tidak ada blokir permanen.

### A5. Enkripsi data dan rotasi kunci
- Enkripsi alamat tujuan penarikan pekerja dan data sensitif menggunakan algoritma AES-256-GCM.
- Format ciphertext terenkripsi menyertakan penanda versi:
  - `v1:k1:<base64>`: Terenkripsi dengan kunci aktif saat ini (`ENCRYPTION_KEY`).
  - `v1:k0:<base64>`: Terenkripsi dengan kunci rotasi sebelumnya (`ENCRYPTION_KEY_PREV`).
  - Fallback aman: Mendukung ciphertext legacy tanpa prefiks atau format sejenis.
- Ambil kunci enkripsi dari variabel lingkungan `ENCRYPTION_KEY` (wajib, 32 byte base64).
- Dukungan rotasi kunci dua generasi (`ENCRYPTION_KEY` dan `ENCRYPTION_KEY_PREV`):
  1. Pasang kunci lama ke variabel `ENCRYPTION_KEY_PREV`.
  2. Generate 32 byte acak baru dan pasang ke `ENCRYPTION_KEY`.
  3. Aplikasi otomatis membaca/mendekripsi data lama dengan k1, fallback ke k0 jika gagal, dan selalu menulis/mengenkripsi data baru menggunakan k1.
  4. Seluruh log aplikasi dilarang keras mencetak plaintext, ciphertext utuh, maupun kunci enkripsi.
- Pemisahan cadangan kunci dari database:
  - Kunci enkripsi pada berkas `.env` TIDAK BOLEH disimpan bersama cadangan database (`pg_dump`).
  - Jika cadangan database dicuri atau bocor, data rekening dan tujuan penarikan pekerja tetap tidak dapat didekripsi tanpa berkas kunci terpisah. Simpan salinan darurat berkas `.env` di brankas password / secret manager terisolasi.

### A6. Kontrol akses
- Periksa tabel peran (admin, pekerja, publik) terhadap tiap kelompok rute di level server.
- Cegah pekerja mengakses data milik pekerja lain atau mengakses rute admin.

### A7. Privasi nomor customer
- Cegah nomor customer masuk atau dirender ke browser pekerja.
- Samarkan nomor telepon secara default di log aplikasi (contoh `6281****678`).
- Dilarang memasukkan berkas nomor (CSV/TXT/XLSX) ke git.
- Cegah berkas cadangan dikirim keluar server tanpa dienkripsi terlebih dahulu.

### A8. Log audit
- Catat pembuatan atau perubahan campaign beserta ratenya.
- Catat penarikan manual, transfer, atau perubahan uang di buku besar.
- Catat aktivitas masuk dan keluarnya sesi admin.

### A9. Unggahan
- Batasi tipe file yang diizinkan (CSV, TXT, XLSX, JPG, PNG).
- Tetapkan ukuran maksimum file unggahan (misal 5 MB).
- Buat nama file acak di server untuk setiap unggahan.
- Simpan file unggahan di luar folder yang dilayani langsung oleh server web.

### A10. Header keamanan
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
  - Seluruh script di halaman HTML wajib memuat `nonce="{{ .Nonce }}"`.
  - Seluruh inline event handler (`onclick=`, `onsubmit=`, `onchange=`, dll) dilarang dan dieliminasi total dari seluruh berkas template (diverifikasi oleh `TestGuard_CSP_TemplatesStaticCheck`).
- `X-Content-Type-Options: nosniff`.
- `X-Frame-Options: DENY`.
- `Referrer-Policy: strict-origin-when-cross-origin`.
- `Permissions-Policy: camera=(), microphone=(), geolocation=()`.
- `Strict-Transport-Security: max-age=31536000; includeSubDomains` (dikirim saat request HTTPS di produksi).
- `Cache-Control: no-store, no-cache, must-revalidate` pada seluruh endpoint terautentikasi dan form login/register untuk mencegah riwayat formulir dan token tersimpan di cache perantara atau browser publik.

### A11. Insiden
- Cabut seluruh sesi pekerja dan admin segera jika server terindikasi bocor.
- Ganti `ENCRYPTION_KEY`, `SESSION_SECRET`, dan kata sandi terkait.
- Beri tahu pekerja mengenai insiden secara transparan.

## Bagian B. Deploy

### B1. Isolasi di VPS bersama
- Gunakan pengguna sistem `goblast` tanpa akses root untuk menjalankan aplikasi.
- Sediakan pengguna database terpisah di PostgreSQL (bukan `postgres`).
- Batasi alokasi memori melalui `MemoryMax` di pengaturan systemd, nilainya ditentukan setelah pengukuran memori (keputusan terbuka nomor 6).
- Buka port internal (misal 8080) hanya untuk `127.0.0.1` (loopback). Pada lingkungan `production`, aplikasi menolak bind publik (`0.0.0.0` atau tanpa IP) secara default; bind publik hanya diizinkan jika `ALLOW_PUBLIC_BIND=true` disetel eksplisit.

### B2. Nginx
- Siapkan server block reverse proxy di Nginx yang meneruskan header Cloudflare ke aplikasi Go:
  ```nginx
  server {
      listen 80;
      server_name <domain>;
      location / {
          proxy_pass http://127.0.0.1:8080;
          proxy_set_header Host $host;
          proxy_set_header X-Real-IP $remote_addr;
          proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
          proxy_set_header X-Forwarded-Proto $scheme;
          proxy_set_header CF-Connecting-IP $http_cf_connecting_ip;
      }
  }
  ```

### B3. SSL dan domain
- Pasang SSL Let's Encrypt / certbot pada block Nginx di atas, atau gunakan mode SSL Full/Strict Cloudflare.
- Catatan: Domain `feepay.web.id` tidak boleh disentuh atau dikonfigurasi ulang secara sembarangan karena terkait aplikasi lain yang ada.

### B4. Variabel lingkungan
| Nama Variabel | Fungsi | Contoh Nilai | Sifat |
|---|---|---|---|
| `APP_ENV` | Lingkungan aplikasi | `production` / `dev` | Wajib |
| `APP_ADDR` | Alamat bind aplikasi | `127.0.0.1:8080` (wajib loopback di produksi) | Wajib |
| `ALLOW_PUBLIC_BIND` | Mengizinkan bind publik di produksi jika `true` | `false` | Opsional |
| `DATABASE_URL` | String koneksi PostgreSQL | `postgres://<user>:<pass>@localhost:5432/goblast` | Wajib |
| `DB_MAX_CONNS` | Batas maksimum koneksi pool DB aplikasi | `25` (default 25) | Opsional |
| `DB_MIN_CONNS` | Batas minimum koneksi pool DB aplikasi | `5` (default 5) | Opsional |
| `SESSION_SECRET` | Rahasia cookie sesi dan tanda tangan CSRF (32 byte base64) | `(base64 dari 32 byte acak)` | Wajib |
| `ENCRYPTION_KEY` | Kunci enkripsi data rekening penarikan (32 byte base64) | `(base64 dari 32 byte acak)` | Wajib |
| `ENCRYPTION_KEY_PREV` | Kunci enkripsi versi sebelumnya (rotasi kunci) | `(base64 dari 32 byte)` | Opsional |
| `TRUSTED_PROXY_CIDRS` | Daftar CIDR proxy tepercaya untuk parsing IP asli klien (Cloudflare & Loopback) | `127.0.0.1/32,::1/128,173.245.48.0/20,...` | Opsional (default internal/Cloudflare) |
| `CLIENT_IP_HEADER` | Nama header IP klien asli dari reverse proxy | `CF-Connecting-IP` | Opsional (default CF-Connecting-IP) |
| `TURNSTILE_SITE_KEY` | Site key Cloudflare Turnstile publik | `0x4AAAAAA...` | Opsional |
| `TURNSTILE_SECRET_KEY` | Secret key Cloudflare Turnstile | `0x4AAAAAA...` | Opsional |
| `APP_PUBLIC_HOST` | Host publik aplikasi untuk validasi Origin CSRF | `goblast.feepay.web.id` | Opsional |

#### Rumus Kapasitas Koneksi Database:
Untuk mencegah kegagalan pool koneksi database di produksi karena kehabisan koneksi PostgreSQL, kapasitas koneksi harus memenuhi rumus:
$$\text{DB\_MAX\_CONNS} + \text{batas pool whatsmeow} + 10 < \text{max\_connections PostgreSQL}$$

Penjelasan:
- `DB_MAX_CONNS`: batas pool pgxpool aplikasi utama (default: 25).
- `batas pool whatsmeow`: batas pool whatsmeow sqlstore (`SetMaxOpenConns`, default: 10).
- `10`: margin pengaman untuk migrasi skema database, koneksi administrasi, pemantauan pg_stat, dan psql ad-hoc.
- `max_connections PostgreSQL`: batas maksimal koneksi di PostgreSQL (default umumnya 100).
- Contoh konfigurasi default: $25 + 10 + 10 = 45 < 100$ (Aman).

### B5. Migrasi
- Jalankan migrasi (misal `go run ./cmd/goblast migrate up`) menggunakan folder db/migrations sebelum memulai versi biner baru.

### B6. Restart
- Lakukan deploy atau restart hanya di jam sepi, dan persiapkan pemberitahuan ke pekerja.
- Restart aplikasi menyebabkan seluruh sesi WhatsApp terputus; pekerja wajib melakukan scan ulang untuk menghubungkan akun.

### B7. Cadangan
- Lakukan ekspor `pg_dump` secara harian.
- Pertahankan masa penyimpanan (retention) selama 7 hari.
- Siapkan langkah memulihkan data (misal `psql "$DATABASE_URL" < backup.sql`).
- Uji pemulihan cadangan berkala di database terpisah/kosong.

### B8. Rollback
- Simpan tiga biner build terakhir yang berhasil di-deploy.
- Lakukan langkah mengembalikan versi dengan menghentikan service, mengubah symlink biner lama, dan menyalakan kembali service.
- Jangan melakukan rollback aplikasi jika migrasi database pada versi terbaru tidak kompatibel mundur (tidak bisa di-rollback secara aman).

### B9. Health check & Readiness probe
- `/healthz` (Liveness probe): membalas status `200 OK` murni untuk mendeteksi apakah proses web hidup (tetap `200 OK` walau server sedang dalam proses *draining* atau DB lambat).
- `/readyz` (Readiness probe): membalas `200 OK` hanya jika database berhasil di-*ping* dan server tidak sedang *draining*. Mengembalikan `503 Service Unavailable` saat server sedang proses shutdown (*draining*) atau database terputus, sehingga reverse proxy/load balancer tidak lagi mengirimkan trafik baru.

### B10. Cloudflare (Keputusan G1 & F1)
- Konfigurasi produksi: Cloudflare berada di depan Nginx, Nginx meneruskan header `CF-Connecting-IP` ke aplikasi Go.
- Pembacaan IP klien asli (`getIP` / `getClientIP`):
  1. Remote address koneksi TCP wajib berasal dari `TRUSTED_PROXY_CIDRS` (subnet IP resmi Cloudflare dan loopback server internal).
  2. Nilai header `CF-Connecting-IP` disanitasi dan divalidasi keabsahan formatnya menggunakan `net.ParseIP`.
  3. Jika remote address bukan proxy tepercaya atau format IP di header tidak valid, header tersebut diabaikan dan sistem menggunakan remote address TCP murni untuk mencegah spoofing IP rate limit.
- CAPTCHA Turnstile (Keputusan F1): Diaktifkan pada formulir publik (`login.html` dan `register.html`) untuk menyaring bot sebelum komputasi password hashing. Di lingkungan produksi (`production`), sistem menolak penggunaan Turnstile Test Key dummy milik Cloudflare.
