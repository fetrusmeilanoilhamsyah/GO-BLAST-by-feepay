# DOC_SECURITY_DEPLOY

Panduan dan aturan mengenai keamanan aplikasi dan prosedur deploy sistem GO BLAST.

## Bagian A. Keamanan

### A1. Injeksi SQL
- Gunakan argumen berparameter pada seluruh query PostgreSQL.
- Dilarang menyusun atau menggabungkan string secara manual untuk membuat query SQL.

### A2. Kata sandi dan sesi
- Gunakan algoritma argon2id untuk hashing kata sandi (parameter awal: memory=64MB, time=1, threads=4, salt=16 byte, key=32 byte).
- Set atribut cookie untuk sesi: `HttpOnly`, `Secure`, `SameSite=Lax`.
- Tentukan masa berlaku sesi admin (misal 24 jam) dan sesi pekerja (misal 7 hari).
- Siapkan rute dan prosedur untuk mencabut sesi dari database secara paksa.

### A3. CSRF
- Wajibkan token CSRF atau perlindungan sejenis (seperti pembatasan SameSite) pada semua permintaan yang mengubah data.

### A4. Pembatasan laju
- Batasi rute login dan pendaftaran. Angka awal yang diusulkan: 5 percobaan per IP per jam untuk pendaftaran, dan 10 percobaan per IP per 15 menit untuk login.
- Batasi rute publik lainnya ke angka yang wajar (misal 100 per IP per menit).

### A5. Enkripsi data
- Enkripsi alamat tujuan penarikan pekerja dan kunci sesi WhatsApp jika disimpan di database.
- Gunakan algoritma AES-256-GCM.
- Ambil kunci enkripsi dari variabel lingkungan `ENCRYPTION_KEY`.
- Sediakan cara rotasi kunci (mendukung pembacaan kunci lama saat menulis ulang data dengan kunci baru).

### A6. Kontrol akses
- Periksa tabel peran (admin, pekerja, publik) terhadap tiap kelompok rute di level server.
- Cegah pekerja mengakses data milik pekerja lain atau mengakses rute admin.

### A7. Privasi nomor customer
- Cegah nomor customer masuk atau dirender ke browser pekerja.
- Samarkan nomor telepon secara default di log aplikasi (misal `62812****123`).
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
- Kirimkan `Content-Security-Policy` di header (nilai awal: `default-src 'self'`).
- Kirimkan `X-Content-Type-Options: nosniff`.
- Kirimkan `Referrer-Policy: strict-origin-when-cross-origin`.
- Kirimkan `Strict-Transport-Security: max-age=31536000; includeSubDomains`.

### A11. Insiden
- Cabut seluruh sesi pekerja dan admin segera jika server terindikasi bocor.
- Ganti `ENCRYPTION_KEY` dan kata sandi terkait.
- Beri tahu pekerja mengenai insiden secara transparan.

## Bagian B. Deploy

### B1. Isolasi di VPS bersama
- Gunakan pengguna sistem `goblast` tanpa akses root untuk menjalankan aplikasi.
- Sediakan pengguna database terpisah di PostgreSQL (bukan `postgres`).
- Batasi alokasi memori melalui `MemoryMax=512M` di pengaturan systemd.
- Buka port internal (misal 8080) hanya untuk `127.0.0.1`.

### B2. Nginx
- Siapkan server block reverse proxy di Nginx.
  ```nginx
  server {
      listen 80;
      server_name <domain>;
      location / {
          proxy_pass http://127.0.0.1:8080;
          proxy_set_header Host $host;
          proxy_set_header X-Real-IP $remote_addr;
      }
  }
  ```

### B3. SSL dan domain
- Pasang SSL Let's Encrypt / certbot pada block Nginx di atas.
- Catatan: Domain `feepay.web.id` tidak boleh disentuh atau dikonfigurasi ulang secara sembarangan karena terkait aplikasi lain yang ada.

### B4. Variabel lingkungan
| Nama Variabel | Fungsi | Contoh Nilai | Sifat |
|---|---|---|---|
| `PORT` | Port aplikasi | `8080` | Opsional (default 8080) |
| `DATABASE_URL` | String koneksi psql | `postgres://<username>:<password>@localhost:5432/goblast` | Wajib |
| `ENCRYPTION_KEY` | Kunci enkripsi | `32-byte-hex-string` | Wajib |

### B5. Migrasi
- Jalankan perintah goose (misal `goose -dir migrations postgres "$DATABASE_URL" up`) sebelum memulai versi biner baru.

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

### B9. Health check
- Sediakan rute endpoint `/health` tanpa autentikasi yang membalas status 200 OK.
- Gunakan endpoint tersebut untuk pemantauan sederhana atau deteksi crash otomatis.

### B10. Cloudflare
- Keputusan pemakaian Cloudflare adalah di tangan pemilik proyek (diseting sendiri dari panel Cloudflare).
- Pro: menyembunyikan IP VPS asli dari serangan DDoS, menyimpan cache aset statis.
- Kontra: proxy agresif kadang dapat memutus secara prematur koneksi yang butuh timeout panjang atau polling lambat, dan menyulitkan pembacaan IP asli pekerja jika tidak diproses lewat header khusus Cloudflare.
