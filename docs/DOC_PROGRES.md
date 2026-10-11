# DOC_PROGRES

Catatan posisi kerja. Diperbarui di akhir setiap tugas, oleh manusia atau AI agent.

Status: `[ ]` belum, `[~]` sedang dikerjakan, `[x]` selesai dan diverifikasi (tes dijalankan), `[!]` terblokir.
Nomor tahap mengacu ke tabel "Daftar Tahap" di bawah.

---

## Posisi sekarang

- **Fase**: 1 (MVP)
- **Terakhir dikerjakan**: Tahap 11 (Panel pekerja: layout simetris responsif, sidebar kiri, pemisahan Beranda, Akun WA, Penarikan, dan Referral) selesai dan lulus verifikasi.
- **Lanjut dari sini**: Tahap 12 (Panel admin: laporan, pengaturan, dompet).
- **Blocker**: tidak ada.

---

## TODO / Perlu Keputusan
- Terkait keputusan terbuka #1: Definisi "terkirim" DeliveryLevel saat ini bernilai "server_ack" (centang satu). Menunggu konfirmasi apakah perlu centang dua.
- Terkait keputusan terbuka #4: Saat ini form pendaftaran pekerja tidak memuat pilihan mata uang. Kolom currency pada tabel `users` dibuat konstan `IDR` saat pendaftaran. Menunggu keputusan final pemilik proyek apakah mata uang dipilih saat pendaftaran.
- **Tahap 12**: Cek dompet saat upload nomor ditunda ke Tahap 12 (jangan buat fungsi kosong sekarang).
- **Fleksibilitas Data Penerima (Nomor Saja vs Nomor+Nama/USN/ID)**: Sistem harus fleksibel mendukung upload nomor polos tanpa nama, maupun nomor yang disertai nama/username/ID kustom (format `nomor,nama`, `nomor:nama`, kolom CSV/XLSX), serta template variabel seperti `{nama}` dengan fallback aman jika data kosong.

## Persiapan

- [x] DOC_KONSEP.md
- [x] DOC_AI_SLOP.md
- [x] DOC_PROGRES.md
- [x] DOC_PROMPTS.md (disimpan pemilik di luar folder proyek, dikirim ke agen satu tahap per sesi)
- [x] Go terpasang (versi: go1.27.2 windows/amd64)
- [x] PostgreSQL terpasang (versi: 17.11)
- [x] DOC_SECURITY_DEPLOY.md (Tahap 2)
- [x] DOC_SKEMA.md (Tahap 5)
- [x] Repo git dan `.gitignore` (Tahap 2)
- [x] `.env.example` (Tahap 4)
- [x] AGENTS.md (aturan tetap yang dibaca agen)

## Daftar Tahap

> Catatan log dan temuan historis Tahap 0 sampai 5 telah diarsipkan di [ARSIP_PROGRES.md](ARSIP_PROGRES.md).

Daftar resmi. Agen hanya boleh mengubah kolom Status, dan hanya untuk tahap yang sedang dikerjakan. Jangan mengubah nama, urutan, atau menambah baris. Tahap dikirim ke agen satu per satu oleh pemilik proyek.

| # | Nama Tahap | Status |
|---|---|---|
| 0 | Orientasi dan cek lingkungan | Selesai |
| 1 | Rapikan DOC_KONSEP (dikerjakan di sesi rapat, hasilnya DOC_KONSEP versi 2) | Selesai |
| 2 | Repo git dan DOC_SECURITY_DEPLOY | Selesai |
| 3 | Uji kelayakan whatsmeow (spike) | Selesai |
| 4 | Kerangka proyek Go, konfigurasi, i18n, tema | Selesai |
| 5 | Skema database dan migrasi | Selesai |
| 6 | Autentikasi admin dan pekerja | Selesai |
| 7 | Upload nomor, normalisasi, duplikat per campaign | Selesai |
| 8 | Template dan campaign | Selesai |
| 9 | Pengelola sesi WhatsApp | Selesai |
| 10 | Mesin kirim: klaim nomor, kecepatan, buku besar | Selesai |
| 11 | Panel pekerja | Selesai |
| 12 | Panel admin: laporan, pengaturan, dompet | Belum |
| 13 | Halaman publik | Belum |
| 14 | Penarikan manual | Belum |
| 15 | Review keamanan dan uji beban | Belum |
| 16 | Deploy ke VPS | Belum |
| 17 | Review gabungan sebelum rilis | Belum |
| 18 | Referral (fase 2) | Belum |
| 19 | Penanda klik (fase 2) | Belum |
| 20 | Anti-curang dan batas multi-nomor (fase 2) | Belum |

## Keputusan terbuka

Salinan dari DOC_KONSEP bagian 15. Agen tidak boleh memutuskan sendiri.

- [ ] 1. Definisi "terkirim": centang satu atau centang dua
- [ ] 2. Batas harian per kecepatan (jeda sudah ada: 1, 5, 15, 30 detik, bisa diubah di panel)
- [ ] 3. Jumlah percobaan ulang sebelum `gagal`
- [ ] 4. Cara menentukan mata uang akun pekerja (usulan: dipilih saat daftar)
- [ ] 5. Cara menghitung dompet dengan dua mata uang (usulan: satu kurs acuan dari admin)
- [ ] 6. Nilai awal batas nomor per pekerja dan batas total sesi aktif (setelah pengukuran Tahap 3 dan 15)
- [x] 7. Mode bawaan template: diputuskan `kartu_link` (hasil uji Tahap 3 membuktikan `tombol_asli` tidak dirender oleh aplikasi WA penerima).
- [ ] 8. Penanda klik unik: fase 1 atau fase 2
- [ ] 9. Pencegahan referral palsu
- [ ] 10. Batas waktu atau jumlah bonus referral
- [ ] 11. Font: pemilik memeriksa di DevTools browser (tab Computed, bagian Rendered Fonts) apakah font di go.dev font web atau font sistem

Sudah diputuskan di rapat: multi-nomor per pekerja diizinkan (batas lewat pengaturan), tombol tidak boleh tampil sebagai link polos, rate IDR dan USDT diatur di panel, nomor internasional didukung, kode negara default untuk nomor berawalan 0 adalah Indonesia (bisa diubah di panel), dan aturan nomor hanya berlaku per campaign (tidak lintas campaign) supaya nomor tes bisa dipakai berulang. Nama produk GO BLAST (teks kecil "by feepay"), tema adaptif terang/gelap mengikuti perangkat dengan warna tosca, maskot gopher di panel.

## Keputusan yang sudah diambil

Terbaru di atas. Agen menambah baris di sini setiap ada keputusan baru, dari pemilik maupun pilihan teknis dari agen.

| Tanggal | Keputusan | Alasan | Oleh |
|---|---|---|---|
| 2026-10-11 | UI Panel Pekerja Offcanvas Drawer Ganda (Sidebar Kiri: Profil & Keamanan; Sidebar Kanan: Navigasi, Bahasa & Mode Tampilan) | Desain terinspirasi ZenMux modern, responsif untuk mobile & desktop, drawer dapat disembunyikan/dibuka dengan animasi halus, bebas emoji (100% SVG), variabel warna adaptif | pemilik dan agen |
| 2026-10-11 | Panel pekerja simetris & pemisahan 4 modul halaman (Beranda, Akun WA, Penarikan, Referral) dengan sidebar kiri (desktop) dan horizontal tabs (mobile) | Memenuhi UI simetris dan rapi di semua ukuran perangkat sesuai DOC_AI_SLOP (tanpa emoji, tanpa hardcoded colors, sistem font, identitas GO BLAST by feepay) | pemilik dan agen |
| 2026-10-11 | Enkripsi data tujuan penarikan dengan AES-256-GCM (destination_encrypted) dan proteksi saldo negatif berbasis DB trigger | Menjaga kerahasiaan nomor rekening pekerja serta menjamin penarikan tidak bisa membuat saldo negatif (DOC_AI_SLOP bagian 6) | agen |
| 2026-10-11 | Definisi "terkirim" ditaruh sebagai konstanta DeliveryLevel = "server_ack" (TODO keputusan terbuka #1) | Bawaan aman: centang satu dari server WA. Menunggu keputusan apakah perlu centang dua (delivered) | agen |
| 2026-10-11 | ThumbnailSpec kartu_link sementara: lebar 300px, kualitas JPEG 70 | Pilihan sementara untuk implementasi Tahap 9 & 10 sambil menunggu konfirmasi uji send-card-thumb | pemilik dan agen |
| 2026-10-11 | Penyimpanan kunci perangkat whatsmeow: Opsi A — enkripsi at-rest di level PostgreSQL/disk VPS, tanpa wrapper aplikasi | Data sesi WA sudah dilindungi kriptografis oleh Noise protocol. Enkripsi disk VPS + TLS koneksi DB cukup untuk fase 1. Wrapper driver (B) dan sqlstore kustom (C) over-engineering dan berisiko salah implementasi. | pemilik (memilih "terbaik untuk produksi", agen merekomendasikan A) |
| 2026-10-11 | Fleksibilitas data penerima: mendukung nomor polos, nomor+nama, serta atribut kustom (USN/ID) | Kebutuhan broadcast dinamis ke depan; template pesan mendukung variabel {nama} dengan fallback aman jika data kosong | pemilik |
| 2026-10-11 | Restrukturisasi dokumen ke `docs/` dan arsip log Tahap 0-5 ke `docs/ARSIP_PROGRES.md` | Menjaga kebersihan root repo dan mempercepat pembacaan progres harian | agen dan pemilik |
| 2026-10-11 | Opsi `tombol_asli` dinonaktifkan (`disabled`) dengan label jujur; `kartu_link` jadi mode utama | Temuan spike whatsmeow: native buttons tidak didukung WhatsApp untuk akun biasa/non-Cloud API | agen dan pemilik |
| 2026-10-11 | Tema GO BLAST mengikuti pengaturan sistem (`prefers-color-scheme`) dan menyediakan pemilih manual via cookie Lax | Memenuhi kenyamanan pengguna mengikuti referensi go.dev tanpa pustaka eksternal | pemilik |
| 2026-10-11 | Kata 'FEEPAY' dan elemen CS tidak lagi wajib | Atas permintaan pemilik proyek | pemilik |
| 2026-10-11 | Nilai i18n Select Language disisipkan lewat data struct saat render template | Lebih aman dan simpel tanpa menyuntikkan template.FuncMap secara dinamis | agen |
| 2026-10-11 | Mode bawaan `kartu_link` untuk form campaign diambil dari pengaturan DB, tapi tidak memaksakan DB query error jika key tidak ada | Menjamin UI fallback lancar walau settings kosong awal | agen |
| 2026-10-10 | Mode bawaan template adalah `kartu_link` | Uji Tahap 3 membuktikan pesan tombol (`tombol_asli`) tidak tampil di aplikasi penerima karena pembatasan dari WhatsApp | agen dan pemilik |
| 2026-10-10 | Nama produk GO BLAST, teks kecil "by feepay", tema gelap tosca ala go.dev | Identitas dan referensi tampilan yang disukai pemilik | pemilik |
| 2026-10-10 | Upload nomor mendukung CSV, TXT, XLSX | Nomor dibersihkan dulu lewat botcv yang mengeluarkan format itu | pemilik |
| 2026-10-10 | Aturan nomor hanya per campaign, tidak lintas campaign | Nomor tes harus bisa dipakai berulang saat debugging | pemilik |
| 2026-10-10 | Rate upah dan referral dalam IDR dan USDT, diatur manual di panel | Pekerja lokal dan internasional | pemilik |
| 2026-10-10 | Satu pekerja boleh banyak nomor WhatsApp, batas lewat pengaturan | Jumlah nomor tiap pekerja tidak diketahui | pemilik |
| 2026-10-10 | Tombol harus tampil rapi, tidak boleh link polos: `tombol_asli` atau `kartu_link` | Tampilan pesan penting bagi pemilik | pemilik |
| 2026-10-10 | Backend Go, database PostgreSQL, antarmuka server-rendered | Hemat memori untuk sesi yang hidup terus, cocok untuk VPS bersama | pemilik |
| 2026-10-10 | Satu program, bukan microservices | Dikerjakan sendirian, beban kecil, klaim nomor dan buku besar butuh transaksi database tunggal | disepakati di rapat |

## Catatan temuan

Hasil uji dan hal yang ditemukan saat bekerja (Tahap 0 - 5 diarsipkan di [ARSIP_PROGRES.md](ARSIP_PROGRES.md)). Terbaru di atas.

- 2026-10-11 (Tahap 11 — Panel Pekerja): Selesai dan diverifikasi penuh. UI panel pekerja dimutakhirkan dengan arsitektur **Offcanvas Drawer Ganda** yang simetris dan responsif terinspirasi ZenMux (Sidebar Kanan untuk Beranda, Akun WA, Penarikan, Referral, Pilihan Bahasa ID/EN/ZH, dan Saklar Tema Terang/Gelap/Auto; Sidebar Kiri untuk Profil Pekerja, Pengaturan Nama, Keamanan/Ubah Kata Sandi, dan Logout). Kedua sidebar dapat disembunyikan/dibuka secara interaktif dengan backdrop transparan dan keyboard ESC. Halaman Beranda (/worker), Akun WhatsApp (/worker/accounts) dengan modal QR & pairing interaktif, Penarikan (/worker/withdrawals) terenkripsi AES-256-GCM, dan Referral (/worker/referral) bekerja selaras. Seluruh guard test (TestNoEmojiGuard, TestNoHardcodedColorsInTemplates, TestThemeContrast) dan flow test lulus 100%.
- 2026-10-11 (Tahap 10 — Mesin Kirim): Selesai dan diverifikasi penuh. Uji klaim atomik konkuren 50 akun mengklaim 1000 nomor berhasil (0 duplikat, semua terklaim tepat 1 kali). Uji buku besar idempoten lulus (ON CONFLICT DO NOTHING via unique index). Uji pelepasan nomor saat putus sesi dan pembersih stale numbers (make_interval) lulus. Uji rate_snapshot immutability dan empty settings rejection lulus. Catatan race detector: `go test -race` di Windows membutuhkan cgo dan C compiler (`gcc`) yang belum terpasang di %PATH% mesin pengembang.
- 2026-10-11 (Tahap 9 — Pengelola Sesi WA): Selesai dan diverifikasi. `internal/wa.Manager` memegang peta sesi aktif, membatasi akun per pekerja dan total sesi aktif dari settings, menangani QR via `go-qrcode` dan pairing phone code, mengabaikan event pesan masuk untuk privasi. Handlers dan template HTML (`worker_accounts.html`, `worker_account_new.html`) selesai dengan polling JSON 3 detik. Migrasi 0006 menambahkan kolom auto-stop dan device ID.

### Lembar Uji Manual Tahap 9 (diisi pemilik)

Langkah uji sambung dan putus satu nomor cadangan:
1. Jalankan aplikasi: `go run ./cmd/goblast/`
2. Daftar sebagai pekerja, login
3. Buka /worker/accounts/new, pilih mode Kode Pairing (atau Pindai QR)
4. Masukkan nomor cadangan, klik Kirim
5. Catat kode 8 digit yang muncul
6. Buka WhatsApp di HP, Linked Devices → Link a Device → Link with phone number, masukkan kode
7. Amati status berubah dari menunggu_scan → terhubung otomatis tanpa refresh
8. Klik tombol Putus Koneksi
9. Amati status berubah ke terputus, nomor dikirim (jika ada) kembali tersedia
10. Klik tombol Berhenti (logout)
11. Amati status berubah ke berhenti, sesi terhapus dari whatsmeow

| Langkah | Hasil yang diharapkan | Hasil aktual |
|---|---|---|
| Status menunggu_scan | Terlihat di daftar akun | diisi pemilik |
| Status terhubung setelah pair | Terlihat otomatis tanpa refresh | diisi pemilik |
| Putus koneksi → terputus | Status berubah, nomor kembali tersedia | diisi pemilik |
| Berhenti → berhenti | Status berubah, sesi terhapus dari whatsmeow | diisi pemilik |

- 2026-10-11 (Tahap 9 — butir 0b): Perintah `send-card-thumb` ditambahkan ke `spike/main.go`. Menghasilkan tiga thumbnail JPEG dari `full.jpg` (lebar 160, 300, 480 px; kualitas 70) menggunakan `golang.org/x/image/draw.BiLinear`. Build lulus. Tabel hasil di `spike/HASIL.md` bagian 4 menunggu diisi pemilik setelah mengirim ke HP cadangan. Ukuran berkas aktual akan tercetak di konsol saat perintah dijalankan.
- 2026-10-11 (Tahap 9 — prasyarat): Ditemukan tiga halangan: (1) keputusan penyimpanan kunci perangkat whatsmeow belum ada di tabel Keputusan; (2) dua baris uji di HASIL.md (putus koneksi manual dan hapus sesi) belum diisi pemilik; (3) perintah `send-card-thumb` belum ada — sudah diselesaikan. Tahap 9 ditunda menunggu jawaban pemilik untuk halangan 1 dan 2, dan hasil uji thumbnail untuk halangan 3.
- 2026-10-11: Variabel pesan & data penerima: Software checker/Wati membaca PushName profil WA, mention @JID, atau kolom CSV nama/ID. GO BLAST dirancang fleksibel agar bisa menerima nomor polos (tanpa nama) maupun nomor dengan atribut nama/USN/ID, dengan fallback aman saat data nama kosong.
- 2026-10-11: Tema terang dan gelap diimplementasikan dengan rasio kontras WCAG 2.1 >= 4.5:1. Nilai danger pada mode gelap disesuaikan ke `#ff5252` (rasio 5.00:1) agar lulus standar aksesibilitas.
- 2026-10-11: spike whatsmeow: native buttons tidak dirender oleh WhatsApp pada nomor biasa/non-Cloud API. Di form campaign, opsi `tombol_asli` dinonaktifkan (`disabled`) dengan label jujur.
- 2026-10-11: Tema yang sebelumnya hanya gelap disesuaikan dengan preferensi sistem; template form dan campaign memakai variabel warna agar teks, input, dan border tetap kontras dalam mode terang.

## Log kerja

Terbaru di atas. Format: `tanggal: ringkasan satu baris` (Log Tahap 0 - 5 diarsipkan di [ARSIP_PROGRES.md](ARSIP_PROGRES.md)).

- 2026-10-11: Tahap 11 selesai: Panel pekerja simetris dan responsif dengan sidebar kiri rapi sesuai DOC_AI_SLOP. Pemisahan halaman Beranda (/worker), Akun WhatsApp (/worker/accounts), Penarikan (/worker/withdrawals), dan Referral (/worker/referral), modul enkripsi withdraw (AES-256-GCM), dan tes lulus 100%.
- 2026-10-11: Tahap 10 selesai: Mesin kirim internal/engine (klaim atomik FOR UPDATE SKIP LOCKED, jeda kecepatan ±20%, daily_limits, buku besar idempoten, pembersih stale numbers, dan auto-stop). Tes lulus 100%.
- 2026-10-11: Tahap 9 selesai: Pengelola sesi WhatsApp internal/wa (Manager, RealSender, FakeSender, Preview kartu_link, generator thumbnail 300px), handler worker accounts, migrasi 0006_wa_sessions.sql, template HTML dan polling status 3 detik.
- 2026-10-11 (Tahap 9 butir 0b): Tambah `send-card-thumb` ke `spike/main.go` + `golang.org/x/image` ke spike/go.mod. Tambah tabel uji thumbnail di `spike/HASIL.md` bagian 4. Build lulus. Menunggu pemilik: isi hasil uji thumbnail, isi dua baris uji lama, dan putuskan penyimpanan kunci perangkat whatsmeow.
- 2026-10-11: Implementasi fleksibilitas data penerima (nomor polos, nomor+nama, usn/id) dan template variabel ({nama}, {{name}}, fallback {nama|Kak}): migrasi 0005_recipient_data.sql, parser numbers.Read (TXT, CSV, XLSX), ReplaceVariables, dan update preview & bulk insert.
- 2026-10-11: Koreksi Tahap 7 dan 8 selesai: validasi template (utf8 runes, host URL, normalisasi nomor), penolakan tombol_asli, render baris CS bersyarat, transisi status campaign (draf/berjalan/selesai), preview gambar & upload MIME, previewID regex, data/tmp 0600 & cleaner 24 jam, breakdown kode negara, deteksi sel numerik XLSX, CSV variable fields, currentUser helper, dan fix IP spoofing di getIP.
- 2026-10-11: Perapian repo (Bagian A, A2, B, C): bersihkan file nyasar, pisah main.go, hapus emoji/simbol UI, label jujur tombol_asli, tema adaptif terang & gelap, restrukturisasi docs/.
- 2026-10-11: Tema adaptif terang/gelap diimplementasikan melalui `prefers-color-scheme` sesuai keputusan pemilik.
- 2026-10-11: Tahap 8 selesai (Template dan campaign: list, form create/edit, preview, validation mode/url/image, logic Render format pesan).
- 2026-10-11: Revisi aturan Tahap 7: Nomor tanpa awalan tidak lagi dianggap ambigu. Sistem langsung mem-parse (mendukung `49...` untuk Jerman, `1...` untuk US, dll) sesuai permintaan pemilik.
- 2026-10-11: Tahap 7 selesai (Upload nomor, normalisasi E.164, filter duplikat per campaign, preview CSV, handler).
- 2026-10-11: Menambahkan TODO untuk dompet di DOC_PROGRES.
- 2026-10-11: Tahap 6 selesai (Autentikasi admin dan pekerja, form daftar, middleware rate limit & CSRF, tes auth, dan command createadmin).
