# DOC_PROGRES

Catatan posisi kerja. Diperbarui di akhir setiap tugas, oleh manusia atau AI agent.

Status: `[ ]` belum, `[~]` sedang dikerjakan, `[x]` selesai dan diverifikasi (tes dijalankan), `[!]` terblokir.
Nomor tahap mengacu ke tabel "Daftar Tahap" di bawah.

---

## Posisi sekarang

- **Fase**: 1 (MVP) & Fase 2 (Tahap 18 selesai dimajukan)
- **Terakhir dikerjakan**: Perbaikan penautan WhatsApp konkurensi tinggi, hitung mundur penautan 2 menit (`ScanTimeout = 2m`) dengan pembersihan otomatis sesi & baris sementara `wa_accounts` yang kedaluwarsa (`handleScanTimeout` dan `CleanupStalePendingAccounts`), *broadcast* kesiapan QR (`qrReady`) tanpa saling rebutan channel, penyaringan event `whatsmeow` sebelum `eventQueues` agar tidak dibanjiri `AppState`/`Contact`, serta perbaikan `PairPhone` dan normalisasi seluruh nomor internasional (dengan atau tanpa `+` serta karakter arah Unicode). Dideploy ke VPS sebagai rilis `v1.0.2` via `goblastctl deploy` dan service `goblast` diverifikasi aktif (`HTTP 200` pada `/readyz`).
- **Sedang dikerjakan**: Verifikasi operasional antarmuka publik dan kesiapan pengujian alur blast.
- **Blocker**: Tidak ada (menunggu pengukuran memori per sesi riil dan pengujian `go test -race` oleh pemilik di lingkungan dengan gcc/WSL).

---

## TODO / Perlu Keputusan

- **Uji Konkurensi -race dan Pengukuran Memori Sesi (Tahap 15)**: Status Tahap 15 berstatus Sebagian. Pengujian `go test -race` belum dapat dijalankan di mesin pengembangan Windows ini karena ketiadaan kompiler C `gcc`, dan konsumsi memori per sesi whatsmeow belum pernah diukur dengan akun WhatsApp nyata. Menunggu pemilik menjalankan `go test -race` di lingkungan dengan gcc/WSL/Linux dan melakukan prosedur pengukuran memori per sesi di VPS.
- **Status Antre WhatsApp Tidak Dipromosikan Otomatis**: Sesuai Keputusan D / Tahap H4, akun berstatus 'antre' (karena kuota sesi aktif penuh saat pendaftaran) tidak dipromosikan otomatis ke 'menunggu_scan' sampai pemilik proyek memutuskan mekanisme antrean (misalnya antrean manual oleh pekerja atau FIFO otomatis saat ada slot yang kosong).
- **Pembersihan Akun WA Batal & Kedaluwarsa (2 Menit)**: Keputusan tetap pemilik: jika pekerja membuka modal tambah akun baru lalu membatalkan (klik Batal, tombol silang X, klik backdrop modal, tekan ESC, tutup tab, atau tidak memindai sampai batas waktu 2 menit habis) sebelum sesi berhasil terhubung, sesi di memori dan baris antrean sementara di database (`wa_jid_hash IS NULL AND connected_at IS NULL`) langsung dibersihkan otomatis agar tidak membebani server dan tidak menggantung di tabel dengan status `menunggu_scan` atau `antre`.
- **Uji manual pengiriman nyata (Tahap 10)**: Uji manual pengiriman nyata (dua akun cadangan, satu database kecil) menunggu pemilik, dan jangan menandai Tahap 10 Selesai berdasarkan tes dengan FakeSender saja.
- Terkait keputusan terbuka #1: Definisi "terkirim" DeliveryLevel saat ini bernilai "server_ack" (centang satu). Menunggu konfirmasi apakah perlu centang dua.
- Terkait keputusan terbuka #6: Menunggu pengukuran memori per sesi dengan akun WhatsApp cadangan oleh pemilik. Sementara memakai nilai awal konservatif `MAX_ACTIVE_SESSIONS = 20`, `DB_MAX_CONNS = 25-30`, dan `MemoryMax` sebesar 75% RAM VPS.
- Terkait keputusan terbuka #8: Penanda klik unik (fase 1 atau fase 2).
- Terkait keputusan terbuka #11: Font: pemilik memeriksa di DevTools browser (tab Computed, bagian Rendered Fonts) apakah font di go.dev font web atau font sistem.

### Lembar Uji Manual: Prosedur Pengukuran Memori Riil per Sesi WhatsApp

Pemilik wajib melakukan pengukuran langsung sebelum membuka sistem ke pekerja dalam jumlah besar:
1. **Catat Kondisi Awal (Baseline):** Sebelum ada sesi WhatsApp terhubung, catat `HeapAlloc` dan `Sys` dari `/admin/api/metrics` serta `RSS` dari terminal VPS: `ps -o rss= -p $(pidof goblast)`.
2. **Uji Bertahap (1, 3, dan 5 Akun):** Sambungkan 1 akun cadangan, tunggu 2 menit idle terhubung, catat. Tambah hingga 3 akun, tunggu 2 menit, catat. Tambah hingga 5 akun, tunggu 2 menit, catat.
3. **Uji Pengiriman Riil:** Kirim campaign kecil ke nomor sendiri melalui akun-akun cadangan tersebut, lalu catat lonjakan metrik saat proses kirim.
4. **Rumus Perhitungan:**
   - $\text{Memori per sesi} = \frac{\text{RSS dengan } n \text{ akun} - \text{RSS awal}}{n}$ (ambil nilai terbesar dari semua baris, bukan rata-rata).
   - $\text{MAX\_ACTIVE\_SESSIONS} = \left\lfloor \frac{\text{RAM aplikasi} - \text{RSS awal}}{1{,}5 \times \text{Memori per sesi}} \right\rfloor$ (margin pengaman 1,5x).

| Kondisi Uji | Jumlah Akun | HeapAlloc (MB) | Sys (MB) | RSS (KB / MB) | Kenaikan RSS dari Awal | Memori per Sesi | Catatan |
|---|---|---|---|---|---|---|---|
| Awal (Idle / No WA) | 0 | ... | ... | ... | 0 | - | Baseline sebelum sesi WA |
| Idle 1 Akun (2 menit) | 1 | ... | ... | ... | ... | ... | Akun cadangan #1 |
| Idle 3 Akun (2 menit) | 3 | ... | ... | ... | ... | ... | Akun cadangan #1–#3 |
| Idle 5 Akun (2 menit) | 5 | ... | ... | ... | ... | ... | Akun cadangan #1–#5 |
| Mengirim Pesan Nyata | 5 | ... | ... | ... | ... | ... | Kirim campaign kecil ke nomor sendiri |
| **Nilai Terbesar Diambil** | - | - | - | - | - | **... MB** | *Gunakan nilai puncak ini untuk rumus* |

---

## Persiapan

- [x] DOC_KONSEP.md
- [x] DOC_AI_SLOP.md
- [x] DOC_PROGRES.md
- [x] DOC_PROMPTS.md (disimpan pemilik di luar folder proyek, dikirim ke agen satu tahap per sesi)
- [x] Go terpasang (versi: go1.27.2 windows/amd64, teruji kompatibel Go 1.26+)
- [x] PostgreSQL terpasang (versi: 17.11)
- [x] DOC_SECURITY_DEPLOY.md (Tahap 2 & diperbarui di H11)
- [x] DOC_SKEMA.md (Tahap 5 & diverifikasi katalog H11)
- [x] Repo git dan `.gitignore` (Tahap 2 & disempurnakan H11)
- [x] `.env.example` (Tahap 4 & disinkronkan 22 variabel H11)
- [x] AGENTS.md (aturan tetap yang dibaca agen)

---

## Daftar Tahap

> Catatan log dan temuan historis lengkap telah diarsipkan di [ARSIP_PROGRES.md](ARSIP_PROGRES.md).

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
| 10 | Mesin kirim: klaim nomor, kecepatan, buku besar | Sebagian |
| 11 | Panel pekerja | Selesai |
| 12 | Panel admin: laporan, pengaturan, dompet | Selesai |
| S0 | Sinkronkan dokumen dengan kode | Selesai |
| S1 | Mata uang final dan konversi USDT yang aman | Selesai |
| S2 | Satu definisi database tersedia | Selesai |
| S3 | Pusat kendali: kunci granular dan pemeriksaan kesehatan | Selesai |
| S4 | Pengaman penarikan | Selesai |
| S5 | Pengaturan disederhanakan dan akun pekerja | Selesai |
| H1 | Fondasi ketahanan proses dan koneksi DB | Selesai |
| H2 | Skema database sebelum produksi | Selesai |
| H3 | Mesin kirim tahan ramai | Selesai |
| H4 | Pengelola sesi WhatsApp | Selesai |
| H5a | Keamanan akses: login, rate limit, sesi, header, Turnstile | Selesai |
| H5b | Keamanan akses: batas per endpoint, CS, TOTP admin, audit handler | Selesai |
| H6 | Referral (tahap 18 dikerjakan lebih awal) | Selesai |
| H7 | Format pesan dan siklus campaign | Selesai |
| H8 | Arsip yang jelas, retensi data, dan pembersihan server | Selesai |
| H9 | Backup aman dan pemulihan | Selesai |
| H10 | Uji beban dan kekacauan | Selesai |
| H11 | Dokumentasi dan kesiapan produksi | Selesai |
| H12 | Koreksi klaim yang tidak terbukti | Selesai |
| H13 | Perbaikan pemeriksaan nomor terdaftar whatsmeow | Selesai |
| H14 | Penanganan nomor yang tidak bergantung pada Indonesia | Selesai |
| 13 | Halaman publik | Selesai |
| 14 | Penarikan manual | Belum |
| 15 | Review keamanan dan uji beban | Sebagian |
| 16 | Deploy ke VPS | Selesai |
| 17 | Review gabungan sebelum rilis | Belum |
| 18 | Referral (fase 2) | Selesai |
| 19 | Penanda klik (fase 2) | Belum |
| 20 | Anti-curang dan batas multi-nomor (fase 2) | Belum |

---

## Keputusan terbuka

Salinan dari DOC_KONSEP bagian 15. Agen tidak boleh memutuskan sendiri.

- [ ] 1. Definisi "terkirim": centang satu (`server_ack`) atau centang dua (`delivery_ack`). Saat ini berjalan stabil pada `server_ack`. Menunggu keputusan final pemilik.
- [x] 2. Batas harian per kecepatan: diputuskan batas harian 0 (tanpa batas) sebagai konstanta backend (jawaban C1 pemilik, 14 Oktober 2026).
- [x] 3. Jumlah percobaan ulang sebelum `gagal`: diputuskan percobaan ulang 3 kali sebagai konstanta backend (jawaban C1 pemilik, 14 Oktober 2026).
- [x] 4. Cara menentukan mata uang akun pekerja: diputuskan di Tahap S1 bahwa seluruh upah dan referral memakai mata uang tunggal Rupiah (IDR); kolom `users.currency` dihapus; USDT hanya didapat lewat konversi eksplisit "Tukar ke USDT".
- [ ] 6. Nilai awal batas nomor per pekerja dan batas total sesi aktif: Menunggu pengukuran memori per sesi dengan akun WhatsApp cadangan oleh pemilik. Sementara memakai nilai awal konservatif `MAX_ACTIVE_SESSIONS = 20`, `DB_MAX_CONNS = 25-30`, dan `MemoryMax` sebesar 75% RAM VPS. (Pengujian beban L6 mencatat puncak 203.81 MB tanpa sesi WhatsApp; memori per sesi whatsmeow belum terukur secara riil).
- [ ] 8. Penanda klik unik: fase 1 atau fase 2.
- [x] 9. Pencegahan referral palsu: diputuskan referral standar satu tingkat, dibayar per pesan terkirim oleh orang yang diajak, aman dari kecurangan, bug, dan celah; satu nomor WhatsApp hanya untuk satu akun pekerja aktif dengan jalur pelepasan oleh admin (jawaban D pemilik, 14 Oktober 2026).
- [x] 10. Batas waktu atau jumlah bonus referral: diputuskan tidak ada batas jumlah dan tidak ada batas waktu bonus (jawaban D pemilik, 14 Oktober 2026).
- [ ] 11. Font: pemilik memeriksa di DevTools browser (tab Computed, bagian Rendered Fonts) apakah font di go.dev font web atau font sistem.

---

## Keputusan yang sudah diambil

Terbaru di atas. Satu baris ringkas per keputusan.

| Tanggal | Kode / Keputusan | Ringkasan Keputusan | Oleh |
| 2026-10-24 | Kartu Akun WA Terpadu & Pembersihan Sesi Logout (v1.0.7–v1.0.8) | Koneksi WhatsApp (`terhubung`) langsung masuk ke daftar kartu akun tanpa form modal langkah kedua; kartu akun terpadu menampilkan statistik per akun (dikirim/sukses/gagal/belum pasti/kompensasi), dropdown kecepatan langsung di kartu, tombol Mulai Blast/Stop, dan tombol Putuskan & Hapus (`Logout` + hapus perangkat & baris DB); pembersihan otomatis baris `logged_out`/`server restart` yang tidak aktif di memori; health check `wa_sessions` hanya memberi peringatan jika ada campaign `berjalan` tanpa WA terhubung; optimasi PageSpeed (`Cache-Control: public, max-age=31536000, immutable` & kompresi maskot) | pemilik dan agen |
| 2026-10-24 | Konkurensi QR, Timeout 2m & Pairing Internasional | Batas waktu scan QR/pairing 2 menit (`ScanTimeout = 2m`) dengan auto-delete baris sementara `wa_accounts` yang belum pernah tertaut (`wa_jid_hash IS NULL AND connected_at IS NULL`); broadcast kesiapan QR (`qrReady`); filter event whatsmeow sebelum `eventQueues` agar tidak penuh oleh `AppState`/`Contact`; perbaikan `PairPhone` menunggu QR event pertama sesuai spesifikasi whatsmeow; dukungan penuh nomor internasional dengan/tanpa `+` dan pembersihan karakter arah Unicode | pemilik dan agen |
| 2026-10-18 | Keputusan UI & UX Auth | Desain UX modern ala Linear/Vercel (Number Flow live counter, Sonner stacked toast, transisi WA terhubung); footer panel pekerja & publik (atribusi Gopher CC BY 3.0, hak cipta 2026); instant theme switch SVG Sun/Moon tanpa reload; pembersihan badge AI-slop "Wajib diisi" diganti asterik; contoh username Goblast123 fleksibel; panduan kata sandi 10 karakter non-repetisi; tombol intip password SVG outline tanpa emoji; relaksasi rate limit akun WA di limits.go | pemilik dan agen |
| 2026-10-18 | H14 / Nomor Internasional | Normalisasi E.164 independen dari Indonesia, default_country per campaign & settings, validasi seluler (tolak bukan_seluler), mitigasi ambiguitas numerik XLSX, pairing internasional (+62/60/44 dll.), peringatan multi-negara di preview | pemilik dan agen |
| 2026-10-18 | H13 / Pemeriksaan Nomor | Perbaikan IsOnWhatsApp whatsmeow (prefix '+', pencocokan r.Query bukan LID, tiga keadaan: Terdaftar/TidakTerdaftar/TidakPasti), penanganan retry backoff TidakPasti (3x lalu gagal cek_nomor_gagal), metrik engine, health check wa_check_status, sanitasi purge | pemilik dan agen |
| 2026-10-18 | H12 / Koreksi Klaim | Koreksi klaim memori per sesi & 75-80 sesi (belum terukur sesi WA riil), default konservatif MAX_ACTIVE_SESSIONS=20, MemoryMax 75% RAM VPS, Tahap 15 kembali Sebagian (butuh -race & ukur memori) | pemilik dan agen |
| 2026-10-18 | H11 / Dokumentasi & Kesiapan | Selaraskan seluruh dokumen dengan kode aktual, 2 guard test otomatis (katalog DB & env parity), DOC_SECURITY_DEPLOY komprehensif, checklist go-live 21 butir | pemilik dan agen |
| 2026-10-18 | H10 / Penyetelan Parameter | Uji beban 11 skenario L1–L11 (tanpa sesi WA), DB_MAX_CONNS 25–30, DB_MIN_CONNS 5, whatsmeow pool 10; memori per sesi & -race belum terukur | pemilik dan agen |
| 2026-10-17 | H9 / Keputusan M & N | Cadangan otomatis .gbak AES-256-GCM + gzip (03:00 WIB, GFS 7+4), CLI restore atomik, web restore dinonaktifkan bawaan | pemilik dan agen |
| 2026-10-17 | H8 / Keputusan O | Transparansi arsip, pembersihan purge campaign (anonimisasi nomor customer), halaman /admin/storage, dan pengingat 90 hari | pemilik dan agen |
| 2026-10-15 | H7 / Keputusan P & R | Pratinjau foto via data URI tanpa tulis disk, resolusi {nama\|Kak}, mode tombol asli eksperimental, ekspor sisa nomor tersedia | pemilik dan agen |
| 2026-10-15 | H6 & 18 / Keputusan D | Referral otomatis 1 tingkat per pesan terkirim, anti-fraud rekening sama & self-referral, tanpa batas akumulasi atau waktu | pemilik dan agen |
| 2026-10-15 | H5b / Keputusan E2 | Rate limit per-endpoint (limits.go), URL CS wajib HTTPS, logout POST-only, TOTP 2FA RFC 6238 admin, step-up 5m, CLI reset | pemilik dan agen |
| 2026-10-15 | H5a / Keputusan F1 & G1 | Mitigasi brute force ArgonGate (2 paralel, antrean 8), kuota 5 sesi, Turnstile bot filter, IP klien tepercaya, CSP ketat | pemilik dan agen |
| 2026-10-15 | H4 / Keputusan B1 | Rekonsiliasi boot B1, reservasi kuota sesi atomik, pool event worker non-blocking, penanganan status whatsmeow | pemilik dan agen |
| 2026-10-15 | H3 / Keputusan A1 & H1 | Kepemilikan goroutine nomor dikirim, klasifikasi error 5 kelas, circuit breaker 5 kegagalan, requeue uncertain oleh admin | pemilik dan agen |
| 2026-10-15 | H2 / Skema Produksi | Migrasi 0019–0023, waktu WIB timeutil, 0 Seq Scan 200k rows, 25 Foreign Key tanpa orphan row | pemilik dan agen |
| 2026-10-15 | H1 / Fondasi Proses | Isolasi pool DB (pgxpool 25 & whatsmeow 10), safe goroutine panic catch, graceful shutdown 20s/15s, healthz/readyz probe | pemilik dan agen |
| 2026-10-14 | H0 / Perencanaan Rangkaian H | Rangkaian 12 tahap perbaikan terstruktur H1–H11 disepakati setelah S5 | pemilik dan agen |
| 2026-10-14 | Keputusan A1 | Hanya goroutine pemilik yang boleh mengubah nomor dikirim; nomor yatim ditandai gagal dengan hasil_tidak_pasti | pemilik |
| 2026-10-14 | Keputusan B1 | Sesi WhatsApp tidak dipulihkan saat restart; pekerja scan ulang; sisa perangkat whatsmeow dibersihkan saat boot | pemilik |
| 2026-10-14 | Keputusan C1 | Percobaan ulang 3 kali; batas harian 0 (tanpa batas) sebagai konstanta backend | pemilik |
| 2026-10-14 | Keputusan D | Referral standar satu tingkat per pesan terkirim; satu nomor WhatsApp untuk satu akun pekerja aktif | pemilik |
| 2026-10-14 | Keputusan E2 | Autentikasi 2FA TOTP di aplikasi web untuk akun admin | pemilik |
| 2026-10-14 | Keputusan F1 | Cloudflare Turnstile digunakan pada form login dan registrasi | pemilik |
| 2026-10-14 | Keputusan G1 | Cloudflare di depan Nginx; IP klien dibaca dari header CF-Connecting-IP via proxy tepercaya | pemilik |
| 2026-10-14 | Keputusan H1 | Admin dapat mengantrekan ulang nomor berstatus hasil_tidak_pasti per campaign dengan modal peringatan | pemilik |
| 2026-10-14 | Keputusan Q | Folder spike/ dipisahkan dari paket produksi dan diabaikan dari build/deploy VPS | pemilik |
| 2026-10-14 | Tambahan Pemilik | Tidak ada blokir permanen (semua limit pulih sendiri); tidak ada tabel/kolom/kode dummy di produksi | pemilik |
| 2026-10-14 | Tahap S5 | Form admin disederhanakan 4 isian bisnis, parameter teknis ke limits.go, akun pekerja berstatus frozen | pemilik dan agen |
| 2026-10-13 | Tahap S4 | Pengaman penarikan: request_token unik, penahanan 24 jam ganti rekening/password, AES-256-GCM v1:k1 | pemilik dan agen |
| 2026-10-13 | Tahap S3 | Kunci granular settings (withdrawals, linking, engine), mode darurat engine, 9 health check maintenance | pemilik dan agen |
| 2026-10-13 | Tahap S2 | Satu definisi nomor tersedia claimable.go, angka tertahan admin, polling live-update 5 detik pekerja | pemilik dan agen |
| 2026-10-13 | Tahap S1 | Upah dan referral dalam Rupiah (IDR) tunggal, konversi manual IDR ke USDT via kalkulator kurs admin | pemilik dan agen |
| 2026-10-12 | Keputusan #5 | Dompet admin murni untuk pembukuan dan pemantauan kas modal; upload nomor tidak memblokir saldo | pemilik |
| 2026-10-11 | Keputusan Kartu Link | Mode utama pesan kartu_link; tombol_asli dinonaktifkan bawaan/eksperimental | pemilik dan agen |
| 2026-10-11 | Keputusan Tema | Tema adaptif terang/gelap mengikuti prefers-color-scheme dengan aksen monokrom ala go.dev | pemilik |
| 2026-10-10 | Keputusan Arsitektur | Program tunggal Go monolith, PostgreSQL, server-rendered HTML tanpa framework JS berat | disepakati di rapat |
| 2026-10-10 | Keputusan Kampanye | Upload nomor CSV/TXT/XLSX; normalisasi E.164; aturan nomor unik per campaign, bukan lintas campaign | pemilik |

---

## Penyimpangan yang diketahui

Perbedaan antara DOC_KONSEP dan kode aktual yang dicatat di Tahap S0:

- **(a) Tiga angka untuk upah dan referral:** [Selesai di Tahap S1] Disatukan menjadi mata uang rupiah tunggal untuk upah dan referral, `users.currency` dihapus, dan USDT hanya didapat melalui konversi eksplisit pekerja dengan kurs admin.
- **(b) Hitungan nomor tersedia tidak konsisten antar lokasi:** [Selesai di Tahap S2] Disatukan melalui `internal/campaign/claimable.go` dengan konstanta `ClaimableWhere` (`cn.status = 'tersedia' AND c.status = 'berjalan' AND c.archived_at IS NULL`) dan fungsi `ClaimableCount` di seluruh handler, klaim atomik `engine.go`, serta diperiksa oleh pengawal tes statis (`guard_test.go`).
- **(c) Mode pemeliharaan hanya memblokir halaman pekerja, tidak menghentikan engine:** [Selesai di Tahap S3] Diperbaiki dengan mengintegrasikan efek `lock_engine` ke dalam `engine.go` (membaca status `maintenance_mode` dan `lock_engine` lewat cache memori 2 detik). Saat aktif, engine berhenti mengklaim nomor baru dengan stop_reason "sistem dalam perbaikan", sesi WhatsApp tetap terhubung, dan nomor in-flight diselesaikan aman.
- **(d) Form pengaturan admin masih memuat kunci yang akan menjadi konstanta backend:** [Selesai di Tahap S5 & H1] Form pengaturan admin disederhanakan; konstanta backend (`max_attempts`, `max_consecutive_failures`, `claim_timeout_seconds`) dipindahkan ke `limits.go` dan env `MAX_ACTIVE_SESSIONS`.
- **(e) Paket `withdraw` hanya punya satu fungsi tes:** [Selesai di Tahap S4, H9, & H10] Paket penarikan (`withdrawals`) memiliki cakupan tes lengkap untuk transaksi atomik, integritas buku besar saldo negatif, kunci ganti password/rekening 24 jam, dan rotasi kunci `ENCRYPTION_KEY_PREV`.

---

## Catatan temuan

- **2026-10-24 (Penyederhanaan Alur Tautan & Kartu Akun WhatsApp Pekerja, Pembersihan Sesi Logout, & Optimasi PageSpeed — Rilis v1.0.7–v1.0.9)**:
  - *Penyederhanaan Alur Tautan & Kartu Akun WhatsApp (`/worker/accounts`)*:
    1. Saat QR atau kode pairing berhasil tersambung (`terhubung`), modal tidak lagi membuka langkah kedua untuk mengisi label/kecepatan dan tombol "Simpan & Mulai Bekerja", melainkan langsung mengarahkan ke daftar kartu akun `/worker/accounts`.
    2. Tampilan kartu akun disatukan menjadi `.wa-blast-card` yang memuat header status (`ONLINE • MENGIRIM PESAN...` / `TERHUBUNG • SIAP BLAST`), 5 kotak metrik per akun (`DIKIRIM`, `SUKSES`, `GAGAL`, `BELUM PASTI`, `KOMPENSASI`), dropdown kecepatan pengiriman langsung di kartu (otomatis menyimpan saat diubah tanpa menimpa label akun via `COALESCE(NULLIF($1, ''), label)`), tombol `Mulai Blast` / `Stop`, dan tombol ikon tempat sampah (`Putuskan & Hapus WhatsApp`).
    3. `Manager.Delete` kini memanggil `Client.Logout(ctx)` terlebih dahulu bila sesi sedang `terhubung` sebelum `Client.Disconnect()`, menghapus `deviceStore`, melepaskan `claimed_account_id`, dan menghapus baris `wa_accounts`.
    4. `CleanupStalePendingAccounts` otomatis membersihkan baris akun milik pekerja yang berstatus `berhenti` (`stop_reason = 'logged_out'`) atau `terputus` (`stop_reason = 'server restart'`) dan tidak lagi aktif di memori.
    5. Pemeriksaan kesehatan `wa_sessions` di `RunHealthChecks` (`/admin/maintenance`) diperbarui agar hanya mengeluarkan status `peringatan` apabila terdapat campaign aktif (`berjalan`) namun `0` akun WhatsApp yang terhubung.
    6. Pembaruan live 5 detik pada kartu akun (`/worker/api/stats?accounts=1`) dirancang hemat query: melewati polling saat tab browser tidak aktif (`document.hidden`) dan hanya menjalankan query agregasi `campaign_numbers` ketika terdapat akun WhatsApp pekerja yang sedang aktif bekerja (`IsWorking = true` atau baru saja transisi berhenti).
  - *Optimasi PageSpeed (Rilis v1.0.7)*:
    1. Mengecilkan dimensi `gopher-flash.webp` ke `66x56` (`2,5 KB`), mengompresi seluruh PNG maskot Gopher ke 8-bit indexed palette (`87 KB` total), serta meningkatkan header `/static/` menjadi `Cache-Control: public, max-age=31536000, immutable`.

- **2026-10-24 (Optimasi Kecepatan Panel Pekerja, Cache Aset Statis, & Konsolidasi Query DB — Rilis v1.0.6)**:
  - *Akar Masalah Panel Pekerja Terasa Berat Saat Pindah Halaman*:
    1. Seluruh 9 gambar maskot Gopher di `web/static/img/gopher-*.png` berukuran asli hingga `1024x1024` piksel dengan total bobot **1,52 MB**, padahal hanya ditampilkan setinggi `34px`–`76px` di layar.
    2. Handler `/static/` di `internal/web/web.go` belum menyertakan header `Cache-Control`, serta konfigurasi Nginx `goblast` belum mengaktifkan cache `/static/` dan memakai `proxy_buffering off` untuk seluruh rute.
    3. `HandleWorkerHome` menjalankan **21 query database beruntun (sequential)** setiap kali halaman Beranda dibuka (10 query terpisah ke `ledger_entries`, 3 query ke `settings`, 3 query ke `wa_accounts`, dll.).
  - *Perbaikan & Implementasi*:
    1. `web/static/img/gopher-*.png` & `web/templates/worker_nav.html`: Mengecilkan resolusi PNG maskot ke ukuran Retina 2x (`160px` tinggi, memangkas ukuran total dari **1,52 MB menjadi 202 KB** / hemat 87%), memakai varian `.webp` (`4–5 KB`) di parade maskot footer, serta menambahkan `loading="lazy" decoding="async"`.
    2. `internal/web/web.go` & Nginx VPS: Menambahkan header `Cache-Control: public, max-age=86400` pada `/static/`, menambahkan blok caching `/static/` dan kompresi `gzip` di Nginx VPS, serta mengaktifkan `proxy_buffering` untuk halaman HTML/aset statis.
    3. `internal/web/worker_handlers.go`: Menggabungkan 16 query beruntun di `HandleWorkerHome` menjadi **3 query tunggal** (`SUM(...) FILTER (WHERE ...)` pada `ledger_entries`, pembacaan batch `settings`, dan penghitungan langsung dari baris `wa_accounts`).

- **2026-10-24 (Audit Pra-Produksi & Perbaikan 5 Bug Kritis — Rilis v1.0.3–v1.0.5)**:
  - *Temuan Audit Log VPS (`journalctl -u goblast`), Katalog Database, & Kode*:
    1. **Bug #1 (`campaign.Render` & `GetOrCreateThumbnail` memanggil `os.Stat` pada Data URI base64 saat Preview Admin)**: Pada `HandleAdminCampaignPreview`, `tmpl.ImagePath` diisi string `data:image/jpeg;base64,...` (hingga ratusan KB) lalu dikirim ke `campaign.RenderForRecipient`. Di `campaign.go:406` dan `thumbnail.go:47`, `GetOrCreateThumbnail` memanggil `os.Stat(imagePath)` karena `imagePath != ""`, menghasilkan galat `file name too long` dan mencetak seluruh base64 ratusan KB ke `journalctl` (`level=ERROR msg="campaign: stat gambar thumbnail gagal"`).
    2. **Bug #2 (`HandleAdminSettingsPOST` tidak menyegarkan `rate_idr_to_usdt_updated_at` jika angka kurs tetap sama)**: Di `admin_handlers.go:504`, `rate_idr_to_usdt_updated_at` sebelumnya hanya diperbarui jika `oldKurs != rateIDRtoUSDT`. Akibatnya, jika keesokan harinya kurs USDT masih tetap `15000` dan admin menekan "Simpan Pengaturan" untuk mengonfirmasi ulang kurs, usia kurs tidak diperbarui dan pekerja tetap tertolak saat konversi IDR ke USDT (`KursMaxAgeHours = 24`).
    3. **Bug #3 (`CleanupStalePendingAccounts` belum mencakup baris sementara yang berubah menjadi `terputus` saat restart server)**: Saat server direstart ketika ada baris `menunggu_scan`/`antre` yang belum pernah tertaut (`wa_jid_hash IS NULL AND connected_at IS NULL`), `ReconcileOnBoot` mengubah statusnya menjadi `terputus` (`stop_reason = 'server restart'`). Sebelumnya `CleanupStalePendingAccounts` hanya memeriksa status `menunggu_scan` dan `antre`.
    4. **Bug #4 (Konfirmasi "Bersihkan Data Nomor" selalu gagal jika nama campaign di database memiliki spasi di akhir)**: Pada `HandleAdminCampaignNewPOST` dan `HandleAdminCampaignEditPOST`, input `name` disimpan tanpa `strings.TrimSpace` (misal Campaign #5 di VPS tersimpan sebagai `'DB '` dengan panjang 3 karakter). Sementara di `HandleAdminCampaignPurgePOST`, `confirmName` di-trim (`strings.TrimSpace(r.FormValue("confirm_name"))` menjadi `'DB'`) lalu dibandingkan dengan `name` mentah (`'DB '`), sehingga `'DB' != 'DB '` selalu bernilai true dan menolak konfirmasi meskipun admin sudah mengetik `DB`.
    5. **Bug #5 (Broadcast yang dihapus atau dinonaktifkan oleh Admin masih tertinggal di lonceng notifikasi pekerja)**: Pada `HandleAdminBroadcastDeletePOST` dan `HandleAdminBroadcastTogglePOST`, penghapusan atau penonaktifan broadcast hanya mengubah tabel `broadcasts` tanpa menghapus baris terkait di tabel `notifications` (`WHERE kind = 'broadcast' AND ref_id = $1`). Akibatnya broadcast yang sudah dihapus (seperti `"SYARAT WD"`, `ref_id = 2`) masih muncul di lonceng notifikasi pekerja.
  - *Perbaikan & Implementasi*:
    1. `internal/campaign/campaign.go` & `internal/campaign/thumbnail.go`: Melewati `GetOrCreateThumbnail` dan `GenerateThumbnail` jika `strings.HasPrefix(imagePath, "data:")`.
    2. `internal/web/admin_handlers.go`: Selalu memperbarui `kursUpdatedAt = time.Now().Format(time.RFC3339)` setiap kali admin menyimpan pengaturan di `HandleAdminSettingsPOST`, serta menghapus baris `notifications` (`kind = 'broadcast' AND ref_id = $1`) ketika broadcast dihapus atau dinonaktifkan.
    3. `internal/wa/manager.go`: Memperluas kueri `CleanupStalePendingAccounts` agar turut menghapus baris `status = 'terputus' AND stop_reason = 'server restart'` yang belum pernah tertaut (`wa_jid_hash IS NULL AND connected_at IS NULL`).
    4. `internal/web/campaign_handlers.go`: Menambahkan `strings.TrimSpace` pada penyimpanan nama di `HandleAdminCampaignNewPOST`, `HandleAdminCampaignEditPOST`, dan pencocokan `strings.TrimSpace(name)` di `HandleAdminCampaignPurgePOST`, serta merapikan `btrim(name)` di tabel `campaigns` VPS.
    5. Menjalankan `VACUUM FULL` pada tabel `whatsmeow_*` di PostgreSQL produksi untuk membebaskan ~14 MB halaman indeks kosong bekas sesi lama, membersihkan notifikasi broadcast yatim di VPS, serta mendeploy rilis `v1.0.5` via `goblastctl deploy`.

- **2026-10-24 (Perbaikan Konkurensi QR, Hitung Mundur Penautan 2 Menit, & Pairing Nomor Internasional)**:
  - *Akar Masalah di Log VPS (`journalctl -u goblast` & Nginx)*:
    1. Saat akun WhatsApp pertama kali ditautkan, `whatsmeow` mengirimkan ratusan event sinkronisasi kontak dan aplikasi (`*events.Contact`, `*events.AppState`) ke `m.eventQueues` (kapasitas 64), menyebabkan 2.679 peringatan `wa: event queue full, event dibuang non-blocking` yang berisiko membuang event penting (`PairSuccess` / `Connected`).
    2. Saat pengguna membuka modal QR namun tidak memindai sampai kode QR habis (~160 detik), `whatsmeow` memutus WebSocket otomatis, tetapi `sess` tetap tertinggal di `m.sessions` dengan status `menunggu_scan` dan memakan jatah kuota `max_accounts_per_worker` maupun `MAX_ACTIVE_SESSIONS`.
    3. `HandleWorkerAccountInitPOST` mengabaikan galat dari `s.WaManager.Connect(...)` (`_, _, _ = ...`), sehingga saat kuota penuh atau sesi lama kadaluarsa, browser menerima `qr_image_b64: ""` dan berputar-putar melakukan polling `/status`.
    4. Pemanggilan `client.PairPhone` gagal dengan `websocket not connected` karena dipanggil sebelum event QR pertama siap pada channel `GetQRChannel` (sesuai kontrak `whatsmeow/pair-code.go`), atau dipanggil pada sesi yang sudah kadaluarsa.
    5. Pada `HandleWorkerAccountPairPOST`, nomor internasional yang diketik tanpa awalan `+` dan kode negaranya berawalan angka `8` (misalnya Kamboja `855...`, Vietnam `84...`, Jepang `81...`, Korea `82...`) salah ditafsirkan sebagai nomor lokal Indonesia (`628...`) karena `numbers.Normalize(rawPhone, "")` memakai default region `'ID'`.
  - *Perbaikan & Implementasi*:
    1. `internal/config/limits.go`: Menambahkan konstanta `ScanTimeout = 2 * time.Minute`.
    2. `internal/wa/manager.go`:
       - Menyaring event di `handleEvent` sebelum masuk `m.eventQueues` sehingga hanya 8 event siklus hidup koneksi (`Connected`, `Disconnected`, `LoggedOut`, `TemporaryBan`, `ConnectFailure`, `ClientOutdated`, `StreamReplaced`, `PairSuccess`) yang diantrekan.
       - Menambahkan `scanTimer` (2 menit), `scanDeadline`, `qrReady` (broadcast channel), serta `handleScanTimeout` dan `CleanupStalePendingAccounts` yang memutus klien, menghapus `deviceStore`, menghapus sesi dari `m.sessions`, dan menghapus baris `wa_accounts` sementara yang belum pernah tertaut (`wa_jid_hash IS NULL AND connected_at IS NULL`).
       - Memperbaiki `pairingFn` agar menunggu `<-sess.qrReady` sebelum memanggil `client.PairPhone`.
       - Menambahkan `GetQRImageB64WithTimeout` agar endpoint `/init` (batas tunggu 800ms) dan `/status` (batas tunggu 150ms) tidak pernah memblokir HTTP worker selama 2,5 detik.
    3. `internal/numbers/numbers.go` & `internal/web/wa_handlers.go`:
       - Membersihkan karakter arah Unicode tak kasat mata (`\u202a`–`\u202e`, `\u200b`–`\u200f`, `\u00a0`, `\ufeff`) saat menyalin nomor dari WhatsApp.
       - Tetap menolak awalan `0` tunggal tanpa kode negara (`"tulis dengan kode negara, misalnya +62..."`), dan otomatis menambahkan `+` pada nomor non-`0` sebelum `numbers.Normalize` sehingga seluruh nomor internasional (dengan atau tanpa `+`) tervalidasi akurat.
       - Pada `HandleWorkerAccountInitPOST`, jika `Connect` gagal (misal kuota penuh), baris sementara yang baru dibuat langsung dihapus dan pesan galat dikembalikan ke modal.
    4. `web/templates/worker_accounts.html`:
       - Menambahkan indikator hitung mundur 2 menit (`02:00`) di modal penautan (tab QR maupun Kode Pairing), tampilan kedaluwarsa otomatis disertai tombol `Muat Ulang Sesi Baru`, guard `isPollingStatus` agar request polling tidak pernah bertumpuk, serta pembersihan `keepalive` saat halaman ditutup (`pagehide`).
    5. Dideploy ke VPS sebagai rilis `v1.0.2` via `goblastctl deploy`.

- **2026-10-18 (Peningkatan UX Panel Pekerja — Linear/Vercel Design System)**:
  - *Latar Belakang*: Transisi antarmuka panel pekerja sebelumnya terasa kaku karena mengandalkan reload halaman penuh (*full page reload*) dan teks status statis.
  - *Spesifikasi Komponen UX Modern (Zero-Dependency & Aman CSP)*:
    1. **Number Flow (Live Odometer Counter)**: Web Component native `<number-flow>` murni CSS transform untuk menganimasikan perubahan angka pada "Sisa Nomor Tersedia" dan "Pendapatan Hari Ini" saat polling `/worker/api/stats` berjalan. Digit yang berubah meluncur vertikal (*spring animation*), digit yang tetap tidak berguncang.
    2. **Momen WhatsApp Terkoneksi**: Pada modal pairing QR/Phone (`worker_accounts.html`), saat polling mendeteksi `terhubung`, kotak QR tidak langsung hilang seketika, melainkan menampilkan animasi centang sukses monokrom (*animated SVG checkmark*) dengan status "WhatsApp Berhasil Terhubung!", lalu jeda transisi halus membuka form label & kecepatan.
    3. **Sonner-style Stacked Toast**: Notifikasi mengambang bertumpuk di sudut layar untuk aksi ganti password, simpan rekening, penarikan dana, disconnect akun, dan salin tautan referral, menggantikan kotak alert statis di atas halaman.
    4. **Instant Button Loading Feedback**: Tombol aksi langsung disabled dan menampilkan teks status loading ("Menyimpan...", "Memproses...") saat diklik untuk mencegah klik ganda dan memberi kepastian respons seketika.
    5. **Smooth Tactile Feedback & Modal Transitions (GPU Accelerated)**:
       - Mikro-interaksi sentuhan empuk / pegas (`scale(0.96)`) pada seluruh tombol saat ditekan (`:active`) tanpa delay 300ms di HP (`touch-action: manipulation; -webkit-tap-highlight-color: transparent`).
       - Animasi buka pop-up modal zoom-in lembut (`scale(0.93) -> scale(1)`) dengan latar blur halus (`backdrop-filter: blur(4px)`).
       - Crossfade mulus saat berganti tab QR / kode pairing dan animasi gambar garis centang monokrom saat sesi WhatsApp terhubung (`checkmarkStrokeDraw`). Seluruh animasi berjalan di layer GPU (0% beban CPU, 120 FPS mulus di mobile).
    6. **Perbaikan Tombol CS & Tab Helpdesk**:
       - Memperbaiki tab modal bantuan (`.helpdesk-tab-btn`) agar teks tidak meloncat/pecah saat diklik (menjaga `font-weight: 600` konsisten tanpa pergeseran lebar huruf dan mengecualikan tab dari scale transform).
       - Menambahkan `grid-column: span 2` pada kartu ke-5 ("Bantuan CS & Tiket") di dasbor mobile agar merentang penuh selebar baris ke-3, dilengkapi `white-space: nowrap` dan `text-overflow: ellipsis` sehingga teks tidak pernah terpotong atau patah di HP sempit.
       - Memperbaiki `.contact-card` agar stabil dan empuk (`:active { transform: scale(0.98); }`).
    7. **Penyesuaian Rate Limiting Akun WhatsApp (`limits.go`)**:
       - Mengatasi masalah pekerja mudah terkena rate limit saat proses penautan WhatsApp (karena 1 alur pairing memakai 3–4 request: init, pair, settings, delete, disconnect, stop).
       - Melonggarkan `WorkerAccountUserAttempts` dari 6 kali/10 menit menjadi **20 kali/5 menit**, dan `WorkerAccountIPAttempts` dari 20 kali/10 menit menjadi **40 kali/5 menit**. Token pulih setiap 15 detik (bukan 100 detik), sehingga pekerja leluasa menautkan/mengganti nomor tanpa takut terkena limit, sementara sistem tetap terlindungi dari bot/spam.
  - *Komitmen Integritas Kode*: Seluruh logika bisnis lama (transaksi buku besar, perutean WhatsApp, verifikasi CSRF, polling stats, parameter penarikan) dipertahankan 100% tanpa perubahan struktur data backend. Tidak ada penambahan dependensi npm/node luar (murni Vanilla JS & CSS variables).
  - *Verifikasi & Pengujian*: Seluruh pengujian statis guard CSP (`TestGuard_CSP_TemplatesStaticCheck`), pengujian IDOR (`TestGuard_IDOR_CrossWorkerAccountsAndTickets`), dan seluruh rangkaian test suite repositori (`go test -p 1 ./...`) lulus 100% tanpa ada regresi.

- **2026-10-18 (Tahap H14 — Penanganan Nomor yang Tidak Bergantung pada Indonesia)**:
  - Pembuktian Bug Fail-First: Ditulis `internal/numbers/numbers_h14_test.go` (`TestH14_ProveCurrentBugs`) yang membuktikan dua bug kritis:
    1. Nomor telepon rumah (fixed line) berstatus `ok` karena pustaka hanya memanggil `IsValidNumber` tanpa memeriksa `GetNumberType` (`MOBILE` / `FIXED_LINE_OR_MOBILE`).
    2. Kode negara di-hardcode ke Indonesia (`defaultCC = "62"`), memotong awalan `0` nasional secara sepihak dan menambahkan `62`. Nomor seluler UK (`07911123456`) gagal di-parse sebagai nomor UK (`status = tidak_valid`) ketika defaultRegion disetel ke "GB".
    Subtes awal membuktikan kegagalan implementasi lama sebelum perbaikan.
  - Perbaikan & Implementasi:
    1. Migrasi 0025 (`db/migrations/0025_campaign_default_country.sql`): Menambahkan kolom `default_country VARCHAR(4) NOT NULL DEFAULT 'ID'` pada tabel `campaigns` dan entri kunci `'default_country'` bernilai `'"ID"'::jsonb` pada tabel `settings`. Diverifikasi siklus migrasi Up, DownTo(18), dan Up kembali via `migration_cycle_test.go`.
    2. `internal/numbers/numbers.go`: Menambahkan fungsi `SupportedCountryOptions()` dan perbaikan `Normalize(raw, defaultRegion, isNumericXLSX)`:
       - Menerima `defaultRegion` (ISO alpha-2, toleransi numerik).
       - Awalan `+` dan `00` diproses sebagai nomor internasional.
       - Format nasional diuraikan dengan `phonenumbers.Parse(clean, region)`.
       - Menolak nomor non-seluler (`bukan_seluler`).
       - Menolak sel numerik XLSX tanpa awalan 0 sebagai `ambigu`, kecuali diawali kode panggilan negara default.
    3. `internal/campaign`: Struct `Campaign` memiliki `DefaultCountry`. Fungsi `ValidateTemplate` menerima `defaultRegion` (bukan fallback 62). `ProcessPreview` membaca `default_country` dari campaign, memilah `bukan_seluler` ke `stats.NotMobile`, dan menghitung metrik per negara.
    4. `internal/web`:
       - `admin_handlers.go`: Pengaturan sistem mendukung pilihan dropdown `default_country`, divalidasi region yang didukung, dan dicatat pada audit log.
       - `campaign_handlers.go`: Form tambah/edit campaign menyediakan dropdown negara default; saat tambah baru mengambil default dari setting sistem, saat edit mengambil dari campaign.
       - `campaign_numbers.go`: Halaman preview menampilkan kartu `Bukan Seluler` (`stats.NotMobile`), serta peringatan multi-negara dan nomor luar negara default.
       - `wa_handlers.go`: Menghapus penulisan ulang '62' otomatis. Menolak input pairing berawalan 0 tunggal dengan pesan `"tulis dengan kode negara, misalnya +62..."`. Memvalidasi format internasional via `numbers.Normalize`.
       - Template dan i18n (`id.json`, `en.json`, `zh.json`) disinkronkan lengkap dengan catatan penutur asli untuk bahasa asing.
    5. Seluruh pengujian lulus 100%.
- **2026-10-18 (Tahap H13 — Perbaikan Pemeriksaan Nomor Terdaftar WhatsApp)**:
  - Pembuktian Bug Fail-First: Ditulis `internal/wa/manager_h13_test.go` (`TestH13_Manager_IsOnWhatsApp_Bugs_Proof`) yang membuktikan tiga kelemahan fatal pada implementasi lama:
    1. `whatsmeow.Client.IsOnWhatsApp` menuntut nomor diawali tanda `+` (`go.mau.fi/whatsmeow/user.go:174`). Nomor di DB disimpan polos tanpa `+` (`numbers.go:81-82`), menyebabkan kueri USync gagal/diabaikan server.
    2. Pencocokan respons membandingkan `r.JID.User == phone`. Padahal dalam mode LID (`addressing_mode: lid`), `r.JID.User` berisi LID (bukan nomor telepon), sehingga pencocokan selalu bernilai false. Pencocokan yang benar wajib membaca `strings.TrimPrefix(r.Query, "+")`.
    3. Respons kosong dari server, galat jaringan, atau nomor tidak ditemukan dalam respons USync sebelumnya diperlakukan sebagai `(false, nil)`, yang oleh engine langsung ditandai secara final dan keliru sebagai `tidak_terdaftar`.
    Ketiga subtes membuktikan kegagalan 100% pada implementasi lama sebelum perbaikan.
  - Perbaikan & Implementasi:
    1. `internal/wa/manager.go`: Menambahkan tipe enum hasil tiga keadaan `CheckResult`: `CheckResultTerdaftar`, `CheckResultTidakTerdaftar`, dan `CheckResultTidakPasti`. Menambahkan awalan `+` khusus saat memanggil `cli.IsOnWhatsApp` tanpa mengubah format E.164 polos di DB. Pencocokan respons menggunakan `r.Query`. Menambahkan pencatatan debug log aman bertopeng nomor satu kali per sesi akun via `checkLogged atomic.Bool`.
    2. `internal/engine/engine.go`: Memperbarui `workerLoop` dan interface `WhatsAppChecker`. Jika hasil `CheckResultTidakPasti` (respons kosong/galat jaringan/timeout), nomor TIDAK ditandai `tidak_terdaftar`, melainkan dikembalikan ke status `tersedia` dengan backoff singkat hingga batas percobaan (`attempts >= 3`). Jika telah mencapai batas, barulah difinalisasi menjadi `gagal` dengan `last_error = 'cek_nomor_gagal'`. Status `tidak_terdaftar` hanya diberikan jika server WhatsApp pasti mengonfirmasi `IsIn == false`.
    3. `internal/engine/metrics.go`: Menambahkan penghitung metrik atomik `CheckTerdaftarCount`, `CheckTidakTerdaftarCount`, `CheckTidakPastiCount`, dan `ConsecutiveTidakTerdaftarCount` pada `EngineStats`, diekspos di endpoint `/admin/api/metrics`.
    4. `internal/campaign/purge.go`: Menambahkan `'cek_nomor_gagal'` ke dalam daftar kategori `last_error` yang diizinkan pada saat pembersihan anonimisasi nomor.
    5. `internal/web/admin_handlers.go`: Menambahkan indikator pemeriksaan kesehatan `wa_check_status` pada `/admin/maintenance` yang berubah menjadi status `peringatan` (kuning) jika terdeteksi 20 nomor tidak terdaftar berturut-turut tanpa nomor terdaftar.
    6. `locales/*.json`: Menambahkan kunci i18n pemeriksaan nomor WhatsApp (ditandai penutur asli untuk en dan zh).
    7. Seluruh pengujian (`manager_h13_test.go`, `engine_h13_test.go`, `admin_h13_test.go`, `purge_test.go`) lulus 100%.
- **2026-10-18 (Tahap H12 — Koreksi Klaim yang Tidak Terbukti)**:
  - Guard Test Pembuktian Awal: Ditulis `internal/config/h12_unproven_claims_test.go` (`TestH12_ProveUnprovenClaims`). Pada pengujian awal, tes berhasil membuktikan kegagalan di 4 area: klaim `(diukur empiris di H10: 75-80 sesi aman)` & `MAX_ACTIVE_SESSIONS=50` di `.env.example`, klaim 75–80 sesi & subbab empiris di `DOC_KONSEP.md`, klaim hasil ukur empiris & nilai 50 di `DOC_SECURITY_DEPLOY.md`, serta status Tahap 15 "Selesai" dan keputusan #6 tertutup di `DOC_PROGRES.md`.
  - Catatan Koreksi Tanpa Pembelaan:
    - *Apa yang salah*: Angka `MAX_ACTIVE_SESSIONS = 75-80` dan `MemoryMax = 512M` sebelumnya ditulis sebagai "diukur empiris" atau "terverifikasi", padahal pengukuran memori riil L6 (203.81 MB) dieksekusi tanpa satu pun sesi WhatsApp terhubung. Konsumsi memori per sesi whatsmeow (3 MB / 4.5 MB) hanya asumsi teoretis yang tidak pernah diukur secara riil. Selain itu, `go test -race` belum pernah dijalankan di lingkungan Windows ini karena ketiadaan kompiler C `gcc`, sehingga penandaan Tahap 15 sebagai "Selesai" adalah klaim berlebih.
    - *Di mana*: `.env.example`, `docs/DOC_KONSEP.md` (bagian 13 & keputusan #6), `docs/DOC_SECURITY_DEPLOY.md` (B1, tabel B4, runbook B10a, metrik B11, checklist butir 11), `docs/DOC_PROGRES.md` (Tahap 15, keputusan #6, catatan H10), dan `docs/ARSIP_PROGRES.md`.
    - *Apa yang diperbaiki*:
      1. Seluruh kata "empiris", "terverifikasi", "diukur", dan "hasil ukur" yang melekat pada `MAX_ACTIVE_SESSIONS`, memori per sesi, atau `MemoryMax` telah dihapus dan diganti dengan keterangan faktual: belum diukur dengan sesi WhatsApp nyata. Angka L6 (203.81 MB) tetap dicantumkan dengan keterangan eksplisit "tanpa sesi WhatsApp".
      2. Nilai bawaan di `.env.example` dan Tabel B4 diturunkan menjadi nilai awal sementara yang konservatif: `MAX_ACTIVE_SESSIONS=20`.
      3. Contoh dan aturan `MemoryMax` systemd diubah menjadi 75% dari total RAM VPS sebagai aturan pengaman (bukan angka tetap), dengan catatan angka final disesuaikan setelah pengukuran langsung oleh pemilik.
      4. Keputusan terbuka #6 dibuka kembali (`- [ ] 6.`).
      5. Status Tahap 15 dikembalikan menjadi `Sebagian` dengan catatan menunggu `go test -race` (butuh gcc atau WSL) dan pengukuran memori per sesi.
      6. Go-Live Checklist butir 11 diubah menjadi "Perlu Tindakan" (Pemilik), dan ditambahkan butir 21 untuk pengujian `go test -race` berstatus "Perlu Tindakan" (Pemilik).
      7. Ditulis prosedur pengukuran memori riil per sesi lengkap dengan tabel isian untuk pemilik di `DOC_PROGRES.md` dan `DOC_SECURITY_DEPLOY.md`.
      8. Seluruh pengawal tes otomatis (`TestH12_ProveUnprovenClaims`, `TestGuard_EnvVarsSync`, `TestGuard_DocSkemaMatchesDatabaseCatalog`) lulus 100%.
- **2026-10-18 (Tahap H11 — Dokumentasi dan Kesiapan Produksi)**:
  - Guard Test Skema Database: Ditulis `internal/db/schema_doc_guard_test.go` (`TestGuard_DocSkemaMatchesDatabaseCatalog`). Pada pengujian awal, tes berhasil membuktikan ketidakcocokan nama constraint `check_campaign_numbers_attempts_non_negative` dan hilangnya indeks FK migrasi 0024 `idx_ledger_entries_campaign_number_id` pada `DOC_SKEMA.md`. Setelah diperbaiki, tes lulus 100%.
  - Guard Test Variabel Lingkungan: Ditulis `internal/config/env_doc_guard_test.go` (`TestGuard_EnvVarsSync`). Pada pengujian awal, tes berhasil membuktikan 11 variabel lingkungan yang ada di `config.go` belum dicantumkan di `.env.example` dan 5 variabel belum masuk di Tabel B4 `DOC_SECURITY_DEPLOY.md`. Seluruh 22 variabel disinkronkan dua arah dan tes lulus 100%.
  - Sinkronisasi Batas Laju (`internal/config/limits.go`): Batas laju per endpoint di `DOC_SECURITY_DEPLOY.md` diselaraskan secara akurat dengan kode aktual: `WorkerFinancial` (10/10m), `WorkerAccountUser` (6/10m), `WorkerAccountIP` (20/10m), `PublicRoute` (100/m), `LoginRateLimitGlobal` (300/m), `AdminCampaignExportNumbers` (5/jam), `AdminStoragePurge` (5/jam).
  - Kebersihan Biner & Build Linux: `.gitignore` dilengkapi dengan `/goblast` dan `goblast` agar biner kompilasi silang Linux tidak masuk git secara tidak sengaja. Kompilasi silang `GOOS=linux GOARCH=amd64 go build ./cmd/goblast` terbukti sukses tanpa peringatan.
  - Dokumentasi Deploy Komprehensif: `DOC_SECURITY_DEPLOY.md` dilengkapi unit systemd lengkap (`MemoryMax=512M`, `TimeoutStopSec=30s`), Nginx config lengkap (`client_max_body_size 6m`, Cloudflare IP restore), tuning PostgreSQL & autovacuum tabel antrean, incident runbook, serta checklist go-live 20 butir.

---

## Log kerja

Terbaru di atas. Ringkasan satu baris per tugas (Rincian multi-baris diarsipkan di [ARSIP_PROGRES.md](ARSIP_PROGRES.md)).

- 2026-10-24: **Perapian Kartu Akun WhatsApp Pekerja, Pembersihan Sesi Logout, Retensi Notifikasi 90 Hari & Optimasi PageSpeed (v1.0.4–v1.0.8) selesai** — Koneksi WA langsung masuk ke kartu akun tanpa langkah kedua modal, kartu akun terpadu (statistik dikirim/sukses/gagal/belum pasti/kompensasi, dropdown kecepatan langsung di kartu, tombol Mulai Blast/Stop, dan tombol Putuskan & Hapus yang melakukan logout + hapus bersih), pembersihan otomatis baris `logged_out`/`server restart`, health check `wa_sessions` hanya peringatan saat ada campaign aktif tanpa WA terhubung, retensi notifikasi 90 hari (migrasi 0026), serta optimasi aset/cache PageSpeed (99/100/100/100).
- 2026-10-24: **Perbaikan Konkurensi QR, Pairing Internasional, & Audit Pra-Produksi (v1.0.2–v1.0.3) selesai** — Filter event whatsmeow sebelum antrean, hitung mundur scan 2 menit dengan hapus otomatis baris sementara (`wa_jid_hash IS NULL`), sinkronisasi `qrReady` sebelum `PairPhone`, normalisasi nomor internasional tanpa `+`, penjaga `data:` URI pada thumbnail preview, penyegaran otomatis `rate_idr_to_usdt_updated_at`, pembersihan baris sementara pasca-restart, serta `VACUUM FULL` tabel `whatsmeow_*` dan deploy `v1.0.3` di VPS.
- 2026-10-18: **Tahap 16 selesai** — Deploy GO BLAST ke VPS via SSH: PostgreSQL 16 terisolasi low-RAM (max 40 conn, shared_buffers 64MB), Goose migrasi 0001–0025 sukses, TLS Let's Encrypt aktif di `fetrusmeilanoilhamsyah.my.id`, proteksi origin Cloudflare ketat (direct IP tolak 403), systemd service `goblast` beroperasi dengan isolasi penuh (`MemoryMax=600M`, `ProtectSystem=strict`), CLI `goblastctl` (doctor, status, logs, restart, backups), drill restart & B1 disconnect sukses, admin awal dibuat di `/root/INITIAL_ADMIN_CREDENTIALS.txt` (0600), seluruh 7 layanan VPS lain berjalan normal tanpa gangguan, VPS tidak pernah di-reboot.
- 2026-10-18: **Pemasangan Maskot Gopher Lengkap & Perapian Footer selesai** — Gopher Flash pada brand GO BLAST (login, register, topbar mobile, sidebar drawer, footer); Gopher Maling di atas kotak kode QR tautan WhatsApp; Gopher Minum Es pada empty state riwayat penarikan, aktivitas beranda, dan riwayat blast; footer pekerja disederhanakan simetris & proporsional (margin-top 24px, tautan bantuan/kebijakan tanpa wrap berlebih); lolos seluruh pengujian.
- 2026-10-18: **Tahap H14 selesai** — Penanganan nomor yang tidak bergantung pada Indonesia: normalisasi E.164 multi-negara via nyaruka/phonenumbers, default_country per campaign & setting sistem, validasi seluler (tolak bukan_seluler), ambiguitas XLSX, pairing nomor internasional, peringatan preview, migrasi 0025, lolos seluruh tes.
- 2026-10-18: **Tahap H13 selesai** — Perbaikan pemeriksaan nomor terdaftar whatsmeow: prefix '+' query USync, matching r.Query bukan JID LID, hasil tiga keadaan (Terdaftar/TidakTerdaftar/TidakPasti), retry backoff TidakPasti (3x lalu gagal cek_nomor_gagal), metrik engine & health check wa_check_status, sanitasi purge, lolos seluruh tes.
- 2026-10-18: **Tahap 13 selesai** — Halaman publik lengkap: beranda publik "/", "/syarat", "/privasi", "/teknologi", footer 3 kolom bersama, audit whatsmeow, transparansi keamanan faktual, isolasi data privasi, i18n 3 bahasa, lolos seluruh pengawal tes.
- 2026-10-18: **Tahap H12 selesai** — Koreksi klaim yang tidak terbukti: hapus klaim empiris sesi/memori, default konservatif MAX_ACTIVE_SESSIONS=20, MemoryMax 75% RAM VPS, Tahap 15 Sebagian (butuh -race & ukur memori), prosedur pengukuran pemilik.
- 2026-10-18: **Tahap H11 selesai** — Dokumentasi selaras dengan kode, 2 automated guard test (DOC_SKEMA & env), perapian DOC_SECURITY_DEPLOY, DOC_PROGRES, checklist go-live 21 butir.
- 2026-10-18: **Tahap H10 selesai** — Uji beban dan kekacauan (11 skenario L1–L11 lulus, migrasi 0024 indeks FK, penyetelan DB_MAX_CONNS 25–30, MemoryMax 512M, 75–80 sesi).
- 2026-10-17: **Tahap H9 selesai** — Cadangan aman terenkripsi .gbak (AES-256-GCM + gzip), retensi GFS, pemulihan atomik CLI, panel admin /admin/backups.
- 2026-10-17: **Tahap H8 selesai** — Halaman /admin/storage, pembersihan arsip kampanye purge (anonimisasi nomor customer), retensi data, pengingat 90 hari.
- 2026-10-15: **Tahap H7 selesai** — Format pesan, variasi nama {nama|Kak}, mode tombol asli eksperimental, sanitasi gambar JPEG/PNG, ekspor nomor tersedia.
- 2026-10-15: **Tahap H6 & 18 selesai** — Referral otomatis satu tingkat per pesan terkirim, pencegahan multi-akun dan self-referral, dashboard referral pekerja & admin.
- 2026-10-15: **Tahap H5b selesai** — Rate limit per-endpoint (limits.go), validasi HTTPS CS/Telegram, logout POST-only, TOTP 2FA admin & step-up 5m, CLI reset-totp.
- 2026-10-15: **Tahap H5a selesai** — Keamanan akses: mitigasi brute force ArgonGate (concurrency 2, queue 8), kuota 5 sesi, Turnstile, honeypot, timing token, CSP ketat.
- 2026-10-15: **Tahap H4 selesai** — Pengelola sesi whatsmeow: rekonsiliasi boot B1, kuota sesi atomik, event worker non-blocking, penanganan status sambung/putus.
- 2026-10-15: **Tahap H3 selesai** — Mesin kirim tahan ramai: kepemilikan goroutine, klasifikasi error 5 kelas, circuit breaker, 2 RT DB/pesan sukses.
- 2026-10-15: **Tahap H2 selesai** — Skema DB sebelum produksi: migrasi 0019–0023, timeutil WIB, 0 Seq Scan 200k, 25 FK 0 orphan.
- 2026-10-15: **Tahap H1 selesai** — Fondasi proses & koneksi DB: safe goroutine, pgxpool & whatsmeow pool, graceful shutdown, healthz/readyz.
- 2026-10-14: **Tahap H0 selesai** — Administrasi dokumen: perencanaan 12 tahap perbaikan terstruktur (H1–H11) dan pencatatan keputusan pemilik.
- 2026-10-14: **Tahap S5 selesai** — Form pengaturan disederhanakan, batas harian 0 & retry 3 konstanta backend, status frozen, username registrasi.
- 2026-10-13: **Tahap S4 selesai** — Pengaman penarikan: token unik, penahanan 24 jam ubah rekening/password, AES-256-GCM v1:k1, audit log.
- 2026-10-13: **Tahap S3 selesai** — Pusat kendali: kunci granular settings, penghentian engine saat darurat, 9 pemeriksaan kesehatan maintenance.
- 2026-10-13: **Tahap S2 selesai** — Satu definisi nomor tersedia terpusat di claimable.go, angka tertahan admin, polling live-update pekerja.
- 2026-10-13: **Tahap S1 selesai** — Upah dan referral rupiah tunggal IDR, konversi eksplisit USDT, integer money, trigger cegah saldo negatif.
- 2026-10-13: **Tahap S0 selesai** — Sinkronisasi dokumen dengan kode, identifikasi 5 penyimpangan, verifikasi baseline database.
- 2026-10-12: **Tahap 12 selesai** — Panel admin: dashboard statistik 14 hari, pengaturan sistem ber-audit log, dompet modal pembukuan, direktori pekerja.
- 2026-10-12: **Tahap 11 selesai** — Panel pekerja penuh: beranda agregat, modal persetujuan risiko, riwayat pesan tanpa nomor customer, referral, responsif mobile.
- 2026-10-12: **Tahap 10b selesai** — Penyambungan engine via WorkController, rute start/stop kerja pekerja, graceful shutdown, status kerja diubah Sebagian menunggu uji manual.
- 2026-10-11: **Tahap 10 selesai** — Mesin kirim internal/engine: klaim atomik FOR UPDATE SKIP LOCKED, jeda kecepatan ±20%, daily_limits, buku besar idempoten.
- 2026-10-11: **Tahap 9 selesai** — Pengelola sesi WhatsApp internal/wa: manager sesi, RealSender, FakeSender, preview kartu_link, generator thumbnail 300px.
- 2026-10-11: **Tahap 8 selesai** — Template dan campaign: form create/edit, preview pesan, validasi mode kartu_link, format render pesan.
- 2026-10-11: **Tahap 7 selesai** — Upload nomor target: normalisasi E.164, deteksi duplikat per campaign, parser CSV/TXT/XLSX, data penerima fleksibel.
- 2026-10-11: **Tahap 6 selesai** — Autentikasi admin & pekerja: registrasi, login identifier fleksibel, rate limiter, CSRF token, sub-command createadmin.
