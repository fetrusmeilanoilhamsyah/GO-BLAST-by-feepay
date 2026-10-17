# DOC_PROGRES

Catatan posisi kerja. Diperbarui di akhir setiap tugas, oleh manusia atau AI agent.

Status: `[ ]` belum, `[~]` sedang dikerjakan, `[x]` selesai dan diverifikasi (tes dijalankan), `[!]` terblokir.
Nomor tahap mengacu ke tabel "Daftar Tahap" di bawah.

---

## Posisi sekarang

- **Fase**: 1 (MVP) & Fase 2 (Tahap 18 selesai dimajukan)
- **Terakhir dikerjakan**: Tahap H12 — Koreksi klaim yang tidak terbukti. Seluruh klaim empiris/terverifikasi seputar `MAX_ACTIVE_SESSIONS`, memori per sesi, dan `MemoryMax` dibersihkan; nilai default konservatif `MAX_ACTIVE_SESSIONS=20` dipasang; aturan `MemoryMax` disetel ke 75% RAM VPS; Tahap 15 dikembalikan ke Sebagian (menunggu `go test -race` dan pengukuran memori riil); keputusan terbuka #6 dibuka kembali; prosedur pengukuran untuk pemilik ditambahkan; dan guard test `TestH12_ProveUnprovenClaims` lulus 100%.
- **Sedang dikerjakan**: Tahap H12 selesai.
- **Blocker**: Tidak ada (menunggu pengukuran memori per sesi riil dan pengujian `go test -race` oleh pemilik di lingkungan dengan gcc/WSL).

---

## TODO / Perlu Keputusan

- **Uji Konkurensi -race dan Pengukuran Memori Sesi (Tahap 15)**: Status Tahap 15 berstatus Sebagian. Pengujian `go test -race` belum dapat dijalankan di mesin pengembangan Windows ini karena ketiadaan kompiler C `gcc`, dan konsumsi memori per sesi whatsmeow belum pernah diukur dengan akun WhatsApp nyata. Menunggu pemilik menjalankan `go test -race` di lingkungan dengan gcc/WSL/Linux dan melakukan prosedur pengukuran memori per sesi di VPS.
- **Status Antre WhatsApp Tidak Dipromosikan Otomatis**: Sesuai Keputusan D / Tahap H4, akun berstatus 'antre' (karena kuota sesi aktif penuh saat pendaftaran) tidak dipromosikan otomatis ke 'menunggu_scan' sampai pemilik proyek memutuskan mekanisme antrean (misalnya antrean manual oleh pekerja atau FIFO otomatis saat ada slot yang kosong).
- **Pembersihan Akun WA Batal**: Keputusan tetap pemilik: jika pekerja membuka modal tambah akun baru lalu membatalkan (klik Batal, tombol silang X, klik backdrop modal, atau tekan ESC) sebelum sesi berhasil terhubung, baris antrean sementara di database langsung dibersihkan otomatis via endpoint delete agar tidak membebani server dan tidak menggantung di tabel dengan status `menunggu_scan` atau `terputus`.
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
| 13 | Halaman publik | Belum |
| 14 | Penarikan manual | Belum |
| 15 | Review keamanan dan uji beban | Sebagian |
| 16 | Deploy ke VPS | Belum |
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
|---|---|---|---|
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

Hasil uji dan hal penting yang ditemukan saat bekerja (Rincian lengkap Tahap 0 s.d. H10 telah diarsipkan di [ARSIP_PROGRES.md](ARSIP_PROGRES.md)).

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
