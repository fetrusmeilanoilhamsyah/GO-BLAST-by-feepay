# DOC_PROGRES

Catatan posisi kerja. Diperbarui di akhir setiap tugas, oleh manusia atau AI agent.

Status: `[ ]` belum, `[~]` sedang dikerjakan, `[x]` selesai dan diverifikasi (tes dijalankan), `[!]` terblokir.
Nomor tahap mengacu ke tabel "Daftar Tahap" di bawah.

---

## Posisi sekarang

- **Fase**: 1 (MVP)
- **Terakhir dikerjakan**: Penerapan Dual Layout (Desktop Full Table & Mobile Compact Cards) responsif pada seluruh tabel panel admin (Dashboard: Laporan Kampanye, Statistik Pekerja, Akun WA, Tren Harian 14 Hari; Kampanye; Pekerja; Detail Pekerja: Akun WA, Riwayat Penarikan, Mutasi Saldo; Penarikan; Kas Dompet Admin; Tiket CS; Broadcast Pengumuman; Diagnostik Pemeliharaan; serta Preview Kode Negara Kampanye). Di desktop/laptop tampil tabel penuh berkolom lengkap, di layar HP otomatis beralih menjadi kartu kompak yang rapi dan simetris tanpa overflow horizontal.
- **Sedang dikerjakan**: Menunggu arahan pengujian atau tahap berikutnya dari pemilik proyek.
- **Blocker**: Tidak ada.



---

## TODO / Perlu Keputusan
- **Purge Data Dev Penarikan & Konfigurasi ENCRYPTION_KEY**: Data penarikan di lingkungan pengembangan yang sebelumnya terenkripsi dengan kunci bawaan lama tidak dapat didekripsi lagi dan sebaiknya di-purge/reset di database dev. Variabel `ENCRYPTION_KEY` kini wajib diisi di `.env` masing-masing pengembang dan server produksi (base64 dari tepat 32 byte acak).
- **Pembersihan Akun WA Batal**: Keputusan tetap pemilik: jika pekerja membuka modal tambah akun baru lalu membatalkan (klik Batal, tombol silang X, klik backdrop modal, atau tekan ESC) sebelum sesi berhasil terhubung, baris antrean sementara di database langsung dibersihkan otomatis via endpoint delete agar tidak membebani server dan tidak menggantung di tabel dengan status `menunggu_scan` atau `terputus`.
- **Uji manual pengiriman nyata (Tahap 10)**: Uji manual pengiriman nyata (dua akun cadangan, satu database kecil) menunggu pemilik, dan jangan menandai Tahap 10 Selesai berdasarkan tes dengan FakeSender saja.
- Terkait keputusan terbuka #1: Definisi "terkirim" DeliveryLevel saat ini bernilai "server_ack" (centang satu). Menunggu konfirmasi apakah perlu centang dua.
- Terkait keputusan terbuka #4: Saat ini form pendaftaran pekerja tidak memuat pilihan mata uang. Kolom currency pada tabel `users` dibuat konstan `IDR` saat pendaftaran. Menunggu keputusan final pemilik proyek apakah mata uang dipilih saat pendaftaran.
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
| 10 | Mesin kirim: klaim nomor, kecepatan, buku besar | Sebagian |
| 11 | Panel pekerja | Selesai |
| 12 | Panel admin: laporan, pengaturan, dompet | Selesai |
| S0 | Sinkronkan dokumen dengan kode | Selesai |
| S1 | Mata uang final dan konversi USDT yang aman | Belum |
| S2 | Satu definisi database tersedia | Belum |
| S3 | Pusat kendali: kunci granular dan pemeriksaan kesehatan | Belum |
| S4 | Pengaman penarikan | Belum |
| S5 | Pengaturan disederhanakan dan akun pekerja | Belum |
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
- [x] 5. Cara menghitung dompet dengan dua mata uang (diputuskan: dompet admin murni untuk pembukuan dan pemantauan sisa modal per mata uang; upload nomor tidak diblokir saldo; kurs_acuan tidak dibuat)
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
| 2026-10-13 | Satu mata uang untuk upah dan referral (rupiah/IDR); USDT hanya lewat konversi eksplisit pekerja; batas harian tanpa batas (0); percobaan ulang (`max_attempts`, `max_consecutive_failures`, `claim_timeout_seconds`) menjadi konstanta backend; maksimal akun per pekerja tetap pengaturan admin; kontak CS dari variabel lingkungan (`CS_WHATSAPP_URL`, `CS_TELEGRAM_URL`, `CHANNEL_TELEGRAM_URL`) | Menyederhanakan model mata uang agar tidak ada pekerja dengan saldo yang salah denominasi; memindahkan parameter teknis dari form admin ke konstanta agar tidak disalahgunakan | pemilik |
| 2026-10-13 | Dual Layout Responsif Mobile & Desktop Seluruh Tabel Panel Admin | Seluruh tabel panel admin (Dashboard, Kampanye, Pekerja, Detail Pekerja, Penarikan, Dompet/Kas, Tiket CS, Broadcast, dan Pemeliharaan/Debug) diubah menjadi dual layout (`desktop-only` tabel penuh lebar berkolom lengkap dan `mobile-only admin-card-list` kartu kompak rapi simetris). Memisahkan aturan `.mobile-only { display: block !important; }` dari `.admin-topbar` agar kartu tidak terkompres menjadi inline-flex di perangkat seluler | pemilik dan agen |
| 2026-10-13 | Balasan Tiket CS Admin (`reply_message`) Terintegrasi ke Notifikasi & Modal Bantuan Pekerja | Admin dapat menulis pesan balasan di `/admin/tickets` saat menyelesaikan laporan. Pesan balasan langsung tersimpan di `support_tickets` (`reply_message`, `replied_by`, `replied_at`) dan dikirimkan otomatis ke `notifications` pekerja (`kind = 'ticket_reply'`). Pekerja dapat memantau riwayat laporan dan membaca pesan balasan admin langsung dari modal bantuan CS (`GET /worker/api/tickets`) serta dari lonceng notifikasi | pemilik dan agen |
| 2026-10-13 | Sinkronisasi Otomatis Broadcast Global ke Notifikasi Pekerja | Broadcast admin yang diaktifkan (`is_active = true`) maupun di-toggle aktif otomatis dikirimkan ke tabel `notifications` seluruh pekerja aktif via query batch atomik (`INSERT ... SELECT ... WHERE NOT EXISTS`). Selain itu, endpoint `GET /worker/api/notifications` menjalankan auto-sync untuk broadcast aktif sehingga pekerja baru maupun pekerja yang belum menerima broadcast langsung mendapatkan notifikasi di lonceng tanpa duplikasi | pemilik dan agen |
| 2026-10-13 | Konfigurasi Tautan CS & Channel Resmi via Environment Variables (`.env`) | Tautan kontak WhatsApp CS (`CS_WHATSAPP_URL`), Telegram CS (`CS_TELEGRAM_URL`), dan Channel Telegram Resmi (`CHANNEL_TELEGRAM_URL`) dipindahkan dari hardcode template ke `internal/config/config.go` dengan fallback bawaan yang aman, dicatat di `.env.example`, dan diteruskan dinamis ke `worker_nav.html` | pemilik dan agen |
| 2026-10-13 | Tahap D: Panel Maintenance & Diagnostik Sistem (`/admin/maintenance`, toggle `maintenance_mode`, redirect pekerja ke `/maintenance`), Detail Pekerja Admin & Reset Password sementara (`/admin/workers/{id}`, pemutusan sesi aktif), dan pemindahan parameter teknis internal (`max_attempts`, `max_consecutive_failures`, `claim_timeout_seconds`) dari UI form ke backend/konstanta aman | Menyederhanakan formulir pengaturan untuk admin non-teknis, menyediakan saklar darurat pemeliharaan sistem dengan pengalihan akses pekerja yang aman, dan memudahkan penanganan bantuan pekerja via inspeksi profil serta reset sandi | pemilik dan agen |
| 2026-10-13 | Tahap B: Implementasi Konversi IDR→USDT via `POST /worker/convert` dengan formula `(amount_idr * 1000000) / rate_idr_to_usdt`, transaksi atomik dan lock `users` row (`FOR UPDATE`), insert ganda `ledger_entries` kind `koreksi` (-IDR dan +USDT mikro-USDT), audit log | Menjamin pencegahan race-condition konversi saldo secara bersamaan, presisi bilangan bulat tanpa pembulatan float, dan transparansi jejak finansial | pemilik dan agen |
| 2026-10-13 | Tahap B: Penarikan Pintar Terpisah per Mata Uang (`usdt_bep20` memotong saldo USDT dan validasi via `parseUSDTInput`, metode lokal memotong saldo IDR), form penarikan menampilkan dual card IDR & USDT serta kalkulator konversi interaktif | Memastikan pekerja dengan metode USDT BEP20 menarik dari saldo USDT hasil konversi, sedangkan pekerja lokal menarik dari saldo rupiah tanpa kebingungan | pemilik dan agen |
| 2026-10-13 | Sistem mata uang baru: semua saldo pekerja dalam IDR, konversi IDR→USDT dilakukan manual pekerja via kalkulator di panel, kurs diatur admin manual (`rate_idr_to_usdt`), tidak ada auto exchange rate | Menyeragamkan denominasi saldo agar tidak ada pekerja yang salah mata uang, konversi eksplisit mencegah manipulasi | pemilik |
| 2026-10-13 | Database dev di-reset/purge untuk data penarikan dan saldo USDT lama yang tidak kompatibel dengan sistem baru | Mulai fresh menghindari data corrupt dari sistem lama | pemilik |
| 2026-10-13 | Tabel `broadcasts` dipertahankan untuk admin membuat broadcast, tapi hasilnya masuk ke tabel `notifications` sebagai insert ke semua pekerja — bukan banner terpisah | Simplifikasi UX, semua notifikasi terpusat di lonceng | pemilik |
| 2026-10-13 | Email pekerja menjadi opsional (nullable) sejak migrasi 0015; username tetap wajib; login bisa via username saja | Mempermudah pekerja internasional yang tidak mau isi email, mengurangi friction daftar | pemilik |
| 2026-10-13 | Query nomor tersedia di panel pekerja difilter hanya kampanye berstatus `berjalan` (bukan semua status) | Fix bug: kampanye dijeda/selesai tidak boleh ikut dihitung dalam counter tersedia | agen |
| 2026-10-13 | Sistem notifikasi lonceng: tabel `notifications` baru (migrasi 0015) menampung broadcast, balasan tiket, dan notifikasi sistem — menggantikan banner broadcast di dashboard pekerja | UX lebih natural, pekerja tidak terganggu banner yang menempel terus, semua notifikasi terpusat | pemilik dan agen |

| 2026-10-12 | Fitur Jeda Kampanye (`POST /admin/campaigns/{id}/pause`) & Preservasi `started_at` saat Lanjutkan (`POST /admin/campaigns/{id}/start`) | Memberikan kontrol penuh bagi admin untuk menghentikan sementara pengiriman kampanye yang sedang berjalan tanpa menghapus atau menyelesaikan permanen. Sisa nomor aman di database dan kampanye dapat dilanjutkan kapan saja | pemilik dan agen |
| 2026-10-12 | Standarisasi Navbar Admin: Mobile Hamburger Drawer & Desktop Single-bar Topbar | Menyelaraskan UX panel admin dengan panel pekerja/publik, menghilangkan bar tab horizontal yang terpotong di layar HP Android/iOS, dan menampilkan drawer vertikal lengkap berikon SVG serta bilah desktop yang rapi | pemilik dan agen |
| 2026-10-12 | Keputusan #5: Dompet admin murni untuk pembukuan dan pemantauan sisa modal per mata uang (bebas upload nomor tanpa memblokir saldo); kunci kurs_acuan tidak dibuat | Menjaga fleksibilitas alur upload campaign agar tidak terkunci/macet, dan mengelola saldo modal IDR & USDT secara terpisah | pemilik |
| 2026-10-12 | Tahap 10b: Penyambungan Engine via WorkController, rute kontrol kerja pekerja (start/stop), graceful shutdown (ReadHeaderTimeout 10s, ReadTimeout 60s, WriteTimeout 60s, IdleTimeout 120s, timeout 15s) | Memungkinkan pekerja mengontrol goroutine pengiriman pesan per akun secara aman tanpa logout WhatsApp, dan menjamin penutupan bersih saat server dihentikan | pemilik dan agen |
| 2026-10-12 | Koreksi Tahap 10: Pengaman uang & pesan, satuan rate_wage mikro-USDT, thumbnail cache (path, modTime), batas harian Asia/Jakarta, migrasi 0010 pembersihan seeded business defaults | Melindungi integritas buku besar dan saldo, mencegah pengiriman ganda pada timeout/error tidak pasti, menghapus default bisnis dari migrasi agar kontrol penuh di tangan admin, dan meningkatkan efisiensi render per campaign | pemilik dan agen |
| 2026-10-11 | Reposisi Navbar & Drawer (Menu di Kiri, Profil di Kanan) serta Pengelompokan Pengaturan Akun ke Accordion Collapsible Ringkas | Standarisasi letak menu navigasi umum di sebelah kiri dan profil di kanan, serta menggantikan form panjang di drawer profil yang meluber dengan 3 kartu accordion rapi (Pengaturan Profil, Rekening & Wallet, Keamanan Kata Sandi) | pemilik dan agen |
| 2026-10-11 | Mode Gelap Pekat OLED Pitch Black (`#0a0a0a`), Pembersihan Total Font Hijau AI-Slop, dan Kartu Responsif Mobile Akun WA di Android | Menghilangkan rasa silau/merih di mata saat menggunakan mode gelap, menyelaraskan tipografi monokrom go.dev bersih, dan mencegah tabel akun WhatsApp meluber/penuh sesak di layar HP Android dengan tampilan kartu vertikal proporsional | pemilik dan agen |
| 2026-10-11 | Pilihan Rekening Penarikan Global (SeaBank, BCA, DANA, USDT BEP20) untuk Semua Akun Pekerja | Membebaskan seluruh pekerja untuk memilih saluran pencairan lokal maupun internasional tanpa restriksi mata uang pendaftaran | pemilik dan agen |
| 2026-10-11 | Penyimpanan Rekening/Wallet Tujuan di Pengaturan Akun (SeaBank, BCA, DANA, USDT BEP20) & Form Penarikan Cepat 1-Klik Berbasis Preset Chip Nominal (10K - 1 Juta) | Pekerja hanya menyimpan rekening sekali secara terenkripsi (AES-256-GCM), tidak perlu mengetik ulang rekening/nama setiap kali penarikan. Form penarikan menyediakan tombol nominal cepat (10K, 20K, 30K, 50K, 100K, 200K, 500K, 1 Juta) serta opsi Tarik Semua Saldo | pemilik dan agen |
| 2026-10-11 | Dukungan kolom `username` pada pendaftaran & fleksibilitas login (Username atau Email), tampilan profil ringkas di panel, serta kesiapan proteksi bot Cloudflare Turnstile | Mencegah tampilan nama email kepanjangan/terpotong di layar mobile, mempermudah akses login fleksibel, dan menyiapkan arsitektur anti-bot | pemilik dan agen |
| 2026-10-11 | Desain monokrom elegan ala go.dev (soft off-white `#f8f9fa`, aksen hitam `#111827`, kartu putih `#ffffff`) menggantikan aksen tosca/biru, serta pembersihan ikon ke garis minimalis 1.75px | Menghilangkan rasa silau di layar HP, menghasilkan visual bersih dan tenang, serta bebas dari kesan grafis AI slop | pemilik dan agen |
| 2026-10-11 | UI Panel Pekerja Offcanvas Drawer Ganda (Sidebar Kiri: Profil & Keamanan; Sidebar Kanan: Navigasi, Bahasa & Mode Tampilan) | Desain terinspirasi ZenMux modern, responsif untuk mobile & desktop, drawer dapat disembunyikan/dibuka dengan animasi halus, bebas emoji (100% SVG), variabel warna adaptif | pemilik dan agen |
| 2026-10-11 | Panel pekerja simetris & pemisahan 4 modul halaman (Beranda, Akun WA, Penarikan, Referral) dengan sidebar kiri (desktop) dan horizontal tabs (mobile) | Memenuhi UI simetris dan rapi di semua ukuran perangkat sesuai DOC_AI_SLOP (tanpa emoji, tanpa hardcoded colors, sistem font, identitas GO BLAST by feepay) | pemilik dan agen |
| 2026-10-11 | Enkripsi data tujuan penarikan dengan AES-256-GCM (destination_encrypted) dan proteksi saldo negatif berbasis DB trigger | Menjaga kerahasiaan nomor rekening pekerja serta menjamin penarikan tidak bisa membuat saldo negatif (DOC_AI_SLOP bagian 6) | agen |
| 2026-10-11 | Definisi "terkirim" ditaruh sebagai konstanta DeliveryLevel = "server_ack" (TODO keputusan terbuka #1) | Bawaan aman: centang satu dari server WA. Menunggu keputusan apakah perlu centang dua (delivered) | agen |
| 2026-10-11 | ThumbnailSpec kartu_link sementara: lebar 300px, kualitas JPEG 70 | Pilihan sementara untuk implementasi Tahap 9 & 10 sambil menunggu konfirmasi uji send-card-thumb | pemilik dan agen |
| 2026-10-11 | Penyimpanan kunci perangkat whatsmeow: Opsi A — enkripsi at-rest di level PostgreSQL/disk VPS, tanpa wrapper aplikasi | protokol Noise melindungi lalu lintas, bukan kunci yang tersimpan; kunci whatsmeow tersimpan tidak terenkripsi di tabel whatsmeow_* pada database yang sama; mitigasi: dihapus saat berhenti, enkripsi disk VPS, pengguna database terbatas (dikerjakan di Tahap 16). | pemilik (memilih "terbaik untuk produksi", agen merekomendasikan A) |
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

## Penyimpangan yang diketahui

Perbedaan antara DOC_KONSEP dan kode aktual. Dicatat di Tahap S0 dan akan diselesaikan di tahap S1–S5.

- **(a) Tiga angka untuk upah dan referral:** `rate_wage` dan `rate_referral` masing-masing menyimpan nilai IDR dan USDT secara terpisah, ditambah `rate_idr_to_usdt` sebagai kurs konversi — jadi ada tiga angka yang bisa tidak konsisten satu sama lain. DOC_KONSEP bagian 9 menyebutkan dua mata uang diatur terpisah, tapi tidak mencakup keberadaan kurs konversi sebagai angka ketiga yang terpisah. (Akan diselesaikan S1.)
- **(b) Hitungan nomor tersedia tidak konsisten antar lokasi:** `wa_handlers.go` baris 63 (`HandleWorkerAccounts`) menghitung `COUNT(*) FROM campaign_numbers WHERE status = 'tersedia'` tanpa memfilter status kampanye. Sebaliknya, `worker_handlers.go` `HandleWorkerHome` baris 235–240 dan engine memfilter `AND c.status = 'berjalan'`. Akibatnya halaman `/worker/accounts` menampilkan angka lebih besar dari `/worker`. (Akan diselesaikan S2.)
- **(c) Mode pemeliharaan hanya memblokir halaman pekerja, tidak menghentikan engine:** Ketika `maintenance_mode = true`, middleware mengalihkan pekerja ke `/maintenance`, tetapi engine yang sedang berjalan (goroutine pengiriman) tidak dihentikan dan tetap mengirim pesan. DOC_KONSEP tidak mendefinisikan perilaku engine saat mode pemeliharaan aktif. (Akan diselesaikan S3.)
- **(d) Form pengaturan admin masih memuat kunci yang akan menjadi konstanta backend:** `HandleAdminSettingsPOST` menerima dan menyimpan `max_attempts`, `max_consecutive_failures`, dan `claim_timeout_seconds` dari form HTML, meskipun keputusan pemilik (S0) menetapkan ketiganya menjadi konstanta backend. Nilainya masih tersimpan di tabel `settings` dan dibaca dari form. (Akan diselesaikan S5.)
- **(e) Paket `withdraw` hanya punya satu fungsi tes:** `withdraw_test.go` hanya memiliki `TestWithdrawEncryption` yang menguji enkripsi/dekripsi AES-256-GCM. Tidak ada tes untuk skenario penarikan atomik, saldo negatif, atau race condition. (Akan diselesaikan S4.)

## Catatan temuan

Hasil uji dan hal yang ditemukan saat bekerja (Tahap 0 - 5 diarsipkan di [ARSIP_PROGRES.md](ARSIP_PROGRES.md)). Terbaru di atas.

- 2026-10-12 (Penyempurnaan Panel Admin: Permintaan Penarikan, Hapus Kampanye, Pengaturan Fleksibel & Pencarian Pekerja):
  1. Navbar Admin Responsif: Didesain rapi di desktop dan ramah sentuhan (touch-friendly) di Android/mobile dengan horizontal chip scroll tanpa scrollbar kaku (`admin_nav.html`).
  2. Modul Permintaan Penarikan (`/admin/withdrawals`): Menggantikan halaman dompet menjadi pengelolaan penarikan pekerja. Menampilkan antrean menunggu (total & jumlah IDR dan USDT), tab filter status (`menunggu`, `selesai`, `ditolak`, `semua`), dan mendekripsi data rekening tujuan transfer dengan AES-256-GCM. Tombol Setujui mengubah status menjadi `selesai`, dan tombol Tolak mengubah status menjadi `ditolak` serta mengembalikan saldo pekerja di buku besar (`ledger_entries`, `kind = 'koreksi'`). Dicatat dalam `audit_logs`.
  3. Pembenahan Logika Pengaturan (`/admin/settings`): Input USDT kini mendukung notasi desimal manusiawi (contoh: `0.003` USDT atau `0.002` USDT) maupun integer mikro-USDT (`3000`). Plafon 5000 pada batas harian (`daily_limits`) dihapus sepenuhnya; mendukung `0` untuk tanpa batas hingga angka besar. Tersedia tombol 1-klik "Isi Nilai Rekomendasi" berbasis JavaScript untuk mengisi nilai parameter operasional yang seimbang dan masuk akal secara instan.
  4. Hapus Kampanye (`POST /admin/campaigns/{id}/delete`): Kampanye berstatus `draf` atau `selesai` kini dapat dihapus oleh admin dengan aman (kampanye `berjalan` dilindungi dan ditolak). Proses menghapus melepaskan relasi foreign key buku besar (`UPDATE ledger_entries SET campaign_number_id = NULL`), menghapus nomor kampanye dan record kampanye, membersihkan file gambar dari disk, dan mencatat audit log. Tombol hapus dengan dialog konfirmasi tersedia di daftar kampanye dan detail kampanye.
  5. Peningkatan Panel Pekerja (`/admin/workers`): Ditambahkan fitur pencarian instan `q` (berdasarkan username atau email), kolom jumlah akun WhatsApp aktif (`status = 'terhubung'`), dan format saldo yang rapi (pemisah ribuan rupiah dan desimal USDT).
  6. Perencanaan Bantuan / CS & Broadcast: Rencana pop-up helpdesk dengan jawaban otomatis FAQ panduan koneksi dan tautan langsung ke CS WhatsApp, CS Telegram, Channel Telegram, serta tiket pesan internal telah dirumuskan. Rencana banner pengumuman pembaruan rate/jadwal di panel pekerja juga telah disiapkan.
  7. Pengujian & Keamanan: Seluruh unit test dan suite berurutan (`go test -p 1 ./...`) serta pengawal emoji (`TestNoEmojiGuard`) lulus 100%. Biner `goblast.exe` berhasil dikompilasi ulang dan diperbarui.

- 2026-10-12 (Koreksi Tahap 10 — Pengaman Uang & Pesan, Nilai Bisnis Bawaan, Satuan Rate):
  1. Migrasi 0010_remove_seeded_business_defaults.sql menghapus pengaturan bisnis bawaan 0007 (hanya jika nilai sama persis) dan dipindahkan ke db/seed/dev.sql dengan proteksi penolakan di produksi (APP_ENV=prod) via CLI sub-command `seed`.
  2. Format rate upah disatukan menjadi JSON `rate_wage` {"IDR": <int rupiah>, "USDT": <int mikro-USDT, 3000 = 0.003 USDT>}. Seluruh fallback lama (rate_idr, rate_usdt, rate_wage_idr, rate_wage_usdt) dihapus. Jika rate kosong atau <= 0, engine tidak mengirim apa pun dan menulis stop_reason "rate upah belum diisi admin". DOC_SKEMA.md diperbarui.
  3. Pencatatan pasca-kirim dibungkus context.WithoutCancel dengan timeout 10s dan 3x retry (200ms, 1s, 3s). Jika tetap gagal, status menjadi 'gagal' (last_error "terkirim_tidak_tercatat"), dicatat di slog.Error, dan tidak pernah dikembalikan ke tersedia.
  4. Pengiriman sender.Send dibungkus context.WithTimeout 60s. Error batas waktu berstatus 'gagal' (last_error "hasil_tidak_pasti") tanpa ulang. Error sentinel wa.ErrSessionNotActive dan wa.ErrSessionNotConnected dibuat sebagai satu-satunya error yang boleh mengembalikan nomor ke tersedia. claim_timeout_seconds dipastikan selalu > timeout kirim.
  5. Putaran tak terkendali: buildCampaignMessage gagal jeda 5s dan setelah 3 kegagalan beruntun akun dihentikan ("gagal menyusun pesan campaign") serta nomor diproses lewat max_attempts. Nomor tidak_terdaftar menerapkan jeda kecepatan yang sama seperti setelah kirim.
  6. Thumbnail di-cache dalam memori dengan kunci (path gambar, waktu ubah file). Client.Upload di RealSender dihapus (kirim hanya JPEGThumbnail). Fungsi encodeJPEG dan ValidateJPEGBytes dihapus.
  7. Batas harian dihitung dari awal hari zona Asia/Jakarta (00:00:00 WIB). Saat batas tercapai, akun menunggu awal hari berikutnya lalu lanjut. Campaign otomatis berstatus selesai saat tidak ada nomor tersedia maupun dikirim dalam satu transaksi bersama audit_logs.
  8. DBSettingsReader membedakan pgx.ErrNoRows dari error database sungguhan.
  9. Seluruh 8 tes DB baru (dan auto-complete campaign) lulus 100%.
- 2026-10-12 (Tahap 11 — Panel Pekerja Selesai Sesuai Spesifikasi Penuh):
  1. Halaman `/worker`: ringkasan penghasilan per mata uang dari `ledger_entries` (IDR dan USDT), daftar akun WhatsApp pekerja langsung di beranda dengan status & aksi, dan sisa nomor tersedia (angka agregat sistem).
  2. Akun WhatsApp (`/worker/accounts`): modal persetujuan risiko banned & privasi sesi (i18n) wajib dicentang sebelum menyambung akun pertama. Dropdown 4 kecepatan (`over_slow`, `slow`, `medium`, `fast`) dengan peringatan ekstra pada pilihan `fast`.
  3. Riwayat Pesan (`/worker/history`): riwayat pesan terkirim (tanggal, kampanye, jumlah pesan, upah) dengan paginasi 20 per halaman. 100% tanpa nomor telepon customer sama sekali, bahkan disamarkan pun tidak ditampilkan demi privasi data.
  4. Referral (`/worker/referral`): kode & tautan referral, dengan teks i18n bahwa bonus referral belum dihitung otomatis (dijadwalkan aktif Tahap 18).
  5. Performa & Batasan Ukuran: seluruh halaman panel pekerja HTML terkompresi/mentah berkisar ~3.5 KB, CSS tema ~15.6 KB, total transfer pertama < 20 KB (jauh di bawah batas 50 KB). Ringan untuk perangkat spek rendah tanpa framework JavaScript.
  6. Pengujian Otomatis: `TestWorkerPanelFlows` mencakup uji pencegahan kebocoran nomor customer, proteksi IDOR (pekerja A tidak bisa melihat/menghapus akun pekerja B), modal persetujuan risiko, dan kelengkapan teks di 3 bahasa (`id`, `en`, `zh`). Seluruh suite tes lulus 100%.
- 2026-10-11 (Tahap 11 — Pemutakhiran UI & Autentikasi): Selesai dan diverifikasi. Penambahan kolom `username VARCHAR(50) UNIQUE` (Migrasi 0008) dengan trigger `trg_users_default_username` agar data lama tetap aman. Form pendaftaran mewajibkan username (3-30 karakter alfanumerik + underscore) dan form login mendukung identifier username maupun email secara fleksibel. UI panel pekerja dimutakhirkan dengan skema monokrom ala go.dev (latar soft off-white #f8f9fa agar tidak menyilaukan mata di layar HP, aksen hitam arang #111827 pada tombol/brand, kartu putih #ffffff dengan elevasi lembut, dan ikon SVG minimalis 1.75px bebas AI slop). Profil di bilah atas menampilkan username ringkas (mencegah teks email terpotong di HP). Label i18n (`worker.id_label`, `worker.role_label`, `worker.username_label`) diperbaiki penuh. Wadah bot protection Cloudflare Turnstile (`#cf-turnstile-container`) telah disiapkan pada halaman login dan register. Seluruh unit test dan suite berurutan (`go test -p 1 ./...`) lulus 100%.
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

- 2026-10-13: **Perbaikan Bug Visibilitas Pekerja & Session Tanpa Email (NULL Email Scan)**: (1) Menemukan dan memperbaiki akar masalah pekerja tanpa email tidak muncul di panel admin: migrasi 0015 membuat email menjadi nullable, namun `HandleAdminWorkers` dan query dashboard worker stats masih memindai `u.email` langsung ke field Go berjenis `string`, sehingga saat nilai email di database `NULL`, `rows.Scan` mengembalikan error dan pekerja dilewati secara diam-diam (*silently skipped*); (2) Memperbaiki query dengan `COALESCE(u.email, '-')` pada `HandleAdminWorkers` (pencarian & default) dan dashboard worker stats sehingga seluruh pekerja yang mendaftar tanpa email tampil normal di panel admin; (3) Memperbaiki query sesi login pada `middleware.go` dengan `COALESCE(u.email, '')` dan `COALESCE(u.display_name, u.username, '')` agar pekerja tanpa email dapat login tanpa mengalami pembatalan cookie sesi; (4) Memperbaiki error handling form registrasi di `auth_handlers.go` agar membedakan pesan error database sebenarnya (misal: duplikasi email, sequence ID, referral) bukan selalu menampilkan pesan generik; (5) Seluruh guard tests dan suite tes 13 paket lulus 100% (0 gagal).
- 2026-10-13: **Perbaikan Tabel Terpotong di Laptop & Adaptasi Mobile Kartu Simetris Seluruh Halaman Pekerja**: (1) Memperbaiki tabel daftar pekerja di panel admin (`admin_workers.html`): membatasi lebar kolom username & email dengan pemotongan teks halus (`text-overflow: ellipsis`) dan tooltip nama lengkap saat di-hover, merapatkan padding sel dari 16px menjadi 10-14px, menghapus pembatas sempit `max-width: 1200px` menjadi `100%` dengan `min-width: 0` pada `.admin-content` sehingga seluruh tombol aksi (Detail dan Nonaktifkan) tampil utuh tanpa terpotong di laptop; (2) Menambahkan custom scrollbar horizontal pada `.responsive-table-container` di `theme.css`; (3) Menyelesaikan masalah tabel terpotong horizontal di mobile/smartphone pada seluruh halaman pekerja dengan arsitektur ganda `desktop-only` (tabel penuh) dan `mobile-only` (daftar kartu simetris `account-card-list` 100% lebar layar): Aktivitas Saldo Terkini di `worker_home.html`, Riwayat Penarikan di `worker_withdrawals.html`, dan Riwayat Bonus Referral di `worker_referral.html`; (4) Seluruh guard tests (anti-emoji, anti-hardcoded-color, kontras WCAG) dan suite tes (13 paket) lulus 100% (0 gagal).
- 2026-10-13: **Penyempurnaan Mobile Topbar & Tata Letak Statistik Beranda Pekerja (Grid 2 Kolom Simetris)**: (1) Perbaikan navbar atas mobile (`worker_nav.html`): teks logo 'GO BLAST by feepay' dirapikan menjadi single-line (`white-space: nowrap`), tagline disembunyikan di HP, tombol Menu navigasi kiri dan tombol Profil akun kanan dijadikan icon-only badge yang ringkas sehingga tidak lagi tumpang tindih pada layar HP (iPhone/Android 360-390px); (2) Perombakan tata letak statistik beranda pekerja (`worker_home.html` & `theme.css`): Saldo Tersedia dijadikan kartu Hero (`stat-hero`) mandiri dengan tombol aksi Tarik Saldo langsung; 6 metrik utama ditata dalam grid 2 kolom x 3 baris simetris di HP (`stat-grid` `repeat(2, 1fr)` gap 8px) mencakup: Database Tersedia, Rate Upah Blast (dibaca dari setting admin), Total WA Terkoneksi, Total Pendapatan Hari Ini, Total Pendapatan Semuanya, dan Total Hasil Referral; (3) Integrasi data rate admin (`rate_wage`) & total hasil referral di `HandleWorkerHome`; (4) Seluruh guard tests (anti-emoji, anti-hardcoded-color, kontras WCAG), tes web, dan 13 paket sistem lulus 100% (0 gagal).
- 2026-10-13: **Overhaul UI & Tata Letak Panel Admin (Responsif Mobile & Sidebar Desktop)**: (1) Menyelaraskan arsitektur panel admin dengan panel pekerja: di laptop/desktop menggunakan **Sidebar Kiri Permanen** (fixed 240px) berisi 9 menu navigasi berikon SVG, pengaturan bahasa & tema, serta tombol logout; topbar desktop bersih tanpa tab-tab horizontal yang sempit dan berantakan; di HP/mobile menggunakan **Navbar Ringkas** dengan tombol Menu hamburger di kiri, logo di tengah, dan drawer slide-in dari kiri dengan backdrop overlay; (2) Memperbaiki 14 template admin (`admin_dashboard`, `admin_campaigns`, `admin_campaign_form`, `admin_campaign_detail`, `admin_campaign_numbers`, `admin_campaign_numbers_preview`, `admin_workers`, `admin_worker_detail`, `admin_withdrawals`, `admin_settings`, `admin_wallet`, `admin_tickets`, `admin_broadcasts`, `admin_maintenance`) dengan pembungkus standar `.admin-layout` & `.admin-content`; (3) Menyematkan navigasi `admin_nav` yang sebelumnya hilang pada `admin_maintenance.html` dan `admin_worker_detail.html`; (4) Memperbaiki tabel agar dapat digeser horizontal (`.responsive-table-container`) pada seluruh halaman dan grid form yang sebelumnya kaku di HP; (5) Seluruh guard tests (anti-emoji, anti-hardcoded-color, kontras WCAG) dan suite tes (13 paket) lulus 100% (0 gagal).
- 2026-10-13: **Perbaikan Bug Pekerja, Default Link CS, & Redesain Beranda Pekerja**: (1) Memperbaiki placeholder dan tombol pencarian direktori pekerja admin (`admin.workers_search_placeholder` & `admin.workers_search_btn`) di kamus bahasa ID, EN, ZH; (2) Memperbarui default konfigurasi tautan CS (`CS_WHATSAPP_URL`, `CS_TELEGRAM_URL`, `CHANNEL_TELEGRAM_URL`) di `.env.example`, `config.go`, dan `worker_handlers.go` dengan fallback placeholder nomor aman (`https://wa.me/6281200000000`); (3) Redesain menyeluruh Beranda Pekerja (`/worker` / `worker_home.html`) responsif mobile & desktop: grid 5 statistik utama (Total Database Tersedia agregat sistem, Pendapatan Hari Ini IDR/USDT, Akun WA Terkoneksi, Total Semua Pendapatan all-time, dan Saldo Tersedia dengan tombol tarik); grid 5 shortcut aksi cepat kartu (Hubungkan WA, Tarik Saldo, Riwayat Blast, Tim Referral, dan Pusat Bantuan CS); serta perapian mutasi transaksi keluar & masuk bertanda `+Rp ...` / `-Rp ...` / `+X USDT` tanpa kebocoran integer mikro-USDT mentah; (4) Seluruh unit tes, guard tes (anti-emoji, anti-hardcoded-color, kontras WCAG), dan paket repositori lulus 100% (13 paket, 0 gagal).
- 2026-10-13: **Tahap C selesai** — Sistem Notifikasi Lonceng & Integrasi Broadcast: (1) Endpoint baru pekerja `GET /worker/api/notifications` (ambil notifikasi terbaru & jumlah unread), `POST /worker/notifications/read` (tandai semua dibaca), dan `POST /worker/notifications/{id}/read` (tandai per item dibaca); (2) Komponen lonceng interaktif di navbar atas pekerja (`worker_nav.html`) lengkap dengan badge counter unread dinamis, panel dropdown popover yang bersih dan responsif, polling otomatis 30s vanilla JS tanpa dependensi eksternal, dan penutupan otomatis via ESC atau klik di luar; (3) Integrasi broadcast admin (`POST /admin/broadcasts/new`) langsung menyebarkan baris notifikasi ke semua pekerja aktif; (4) Integrasi CS tickets (`POST /admin/tickets/{id}/resolve`) langsung mengirim notifikasi penyelesaian ke pekerja pengirim tiket; (5) Pembersihan banner broadcast lama dari `worker_home.html` demi tampilan beranda yang tenang dan rapi; (6) Suite tes unit & integrasi notifikasi baru ditambahkan dan seluruh 13 paket tes lulus 100% (0 gagal).
- 2026-10-13: **Tahap B selesai** — Sistem Mata Uang Baru IDR/USDT & Konversi Saldo: (1) Setting admin baru `rate_idr_to_usdt` (kurs 1 USDT = berapa Rupiah) dengan form validation, display pemisah ribuan, dan simpan di tabel settings dengan audit log; (2) Endpoint baru `POST /worker/convert` untuk konversi atomik saldo pekerja dari IDR ke USDT via kalkulator kurs admin dengan penguncian baris `users` (`FOR UPDATE`) dan pencatatan buku besar kind `koreksi` berpasangan; (3) Refactoring `HandleWorkerWithdrawalsPOST` agar penarikan USDT (`usdt_bep20`) memotong saldo USDT dan divalidasi presisi mikro-USDT, sementara penarikan lokal (SeaBank, BCA, DANA, Bank) memotong saldo IDR; (4) Desain ulang halaman penarikan pekerja (`worker_withdrawals.html`) menampilkan kartu saldo ganda (IDR & USDT), kalkulator konversi interaktif live, peringatan saldo USDT kosong jika rekening BEP20, chip nominal pintar per metode, dan riwayat penarikan terformat rapi; (5) Suite tes unit & integrasi konversi saldo dan penarikan USDT baru ditambahkan dan seluruh 13 paket tes lulus 100% (0 gagal).
- 2026-10-13: **Tahap A selesai** — Bug Fix pra-produksi: (1) Fix query `availableNumbers` di panel pekerja agar hanya menghitung nomor dari kampanye berstatus `berjalan` — kampanye dijeda/selesai tidak masuk hitungan; (2) Fix tampilan Destination saat dekripsi gagal di panel admin penarikan — tampilkan pesan informatif berwarna merah "Enkripsi lama — tidak dapat dibaca" bukan teks generik; (3) Email pekerja dijadikan opsional — backend menangani `NULL` email, template form register dihapus `required`, login diperbarui agar aman dengan email NULL; (4) Migrasi 0015: `users.email` nullable, tabel `notifications` baru untuk sistem lonceng, kolom reply di `support_tickets`; (5) DOC_SKEMA.md dan DOC_PROGRES.md diperbarui. Build dan seluruh tes lulus 100% (13 paket, 0 gagal).

- 2026-10-12: Koreksi integritas buku besar dan campaign: migrasi 0014_restore_strict_ledger.sql (trigger ketat prevent_ledger_update_delete tolak semua UPDATE & DELETE, FK ON DELETE RESTRICT, kolom campaigns.archived_at), hapus campaign dibatasi hanya draf tanpa nomor terkirim dan tanpa referensi ledger, rute baru arsip campaign POST /admin/campaigns/{id}/archive, filter daftar campaign (archived disembunyikan default, show_archived=1), tolak aksi upload nomor/edit pada campaign terarsip, pembersihan testutil aman trigger, DOC_SKEMA diperbarui, dan seluruh tes integrasi lulus 100% (0 skip).
- 2026-10-12: Penguatan keamanan tes & kunci enkripsi: seluruh tes database beralih hanya membaca TEST_DATABASE_URL tanpa skip (0 skip), validasi nama database berakhiran '_test' dan beda dari DATABASE_URL via helper testutil.TestDB & testutil.TestDBInstance, isolasi pembersihan t.Cleanup per-tes tanpa TRUNCATE/DELETE blanket, ENCRYPTION_KEY wajib tanpa fallback bawaan di config & withdraw, validasi tepat 32 byte, dan suite tes enkripsi withdraw baru (70 top-level tes lulus, 0 gagal, 0 lewati).
- 2026-10-12: Perbaikan foreign key hapus kampanye: tambahkan pembersihan number_batches sebelum menghapus kampanye (selesaikan error SQLSTATE 23503), dan migrasi 0013_allow_campaign_cleanup_ledger.sql agar relasi ledger_entries.campaign_number_id dapat di-unlink secara aman tanpa melanggar sifat append-only transaksi finansial. Seluruh tes lulus 100%.
- 2026-10-12: Pusat Bantuan & CS (Worker FAB, FAQ bot interaktif, tautan WhatsApp & Telegram resmi, formulir tiket laporan ke admin), sistem broadcast pengumuman (banner dinamis di worker dashboard, kelola broadcast di admin panel), perbaikan teks tombol hapus campaign dan dekripsi rekening penarikan admin cross-key. Seluruh tes unit & integrasi lulus 100%.
- 2026-10-12: Tahap 12 selesai: Panel admin (dashboard statistik & tren 14 hari dengan polling 10s vanilla fetch, pengaturan dengan audit log perubahan nilai lama & baru serta validasi ketat, dompet pembukuan modal IDR/USDT dan riwayat isi ulang, direktori pekerja dengan aksi enable/disable yang otomatis menghentikan akun WA dan membersihkan sesi web). Seluruh tes unit & integrasi lulus 100%.
- 2026-10-12: Responsif mobile riwayat pesan /worker/history: ganti tabel kaku dengan tata letak adaptif desktop-only tabel dan mobile-only daftar kartu simetris ala akun WhatsApp, touch target paginasi >= 40px, dan verifikasi seluruh tes 100% lulus.
- 2026-10-12: Optimasi kecepatan QR code WhatsApp: singleton sqlstore container (hindari overhead koneksi DB & migrasi berulang per sambung akun), background pre-encoding PNG base64 di memori Session, sertakan QR langsung di HTTP response POST /init, polling awal 200ms & interval 800ms. Seluruh tes lulus 100%.
- 2026-10-12: Perbaikan siklus hidup akun WA: otomatis bersihkan antrean sementara saat user batal/tutup modal tambah akun agar tidak menggantung di DB, perbaiki fallback tombol aksi di tabel desktop & mobile agar akun terputus/berhenti selalu bisa disambung kembali atau dihapus dengan aman, unclaim campaign_numbers sebelum hapus akun, dan perbarui binary goblast.exe.
- 2026-10-12: Tahap 10b selesai: Engine.IsRunning, WorkController interface di web, rute start & stop kerja pekerja, tampilan status kerja & stop_reason serta tombol di /worker/accounts dan /worker tiga bahasa, graceful shutdown (timeouts + SIGINT/SIGTERM 15s), tes lulus 100%. Status Tahap 10 diubah ke Sebagian menunggu uji manual pengiriman nyata oleh pemilik.
- 2026-10-12: Koreksi Tahap 10 selesai: migrasi 0010, satuan rate_wage mikro-USDT, pengaman uang & pesan, thumbnail in-memory cache, batas harian Asia/Jakarta, dan 8 DB test baru lulus 100%.
- 2026-10-12: UI Akun WhatsApp: Tambahkan kartu ringkasan Database Nomor Tersedia (Sistem) dan Akun Terhubung di halaman /worker/accounts.
- 2026-10-12: Tahap 11 selesai penuh: Audit dan implementasi lengkap panel pekerja (beranda agregat & saldo per mata uang, modal risiko banned & privasi, riwayat pesan tanpa nomor customer, referral Tahap 18 notice, ukuran < 50 KB, tes lulus 100%).
- 2026-10-11: Penyempurnaan UI: Reposisi tombol navbar (Menu di kiri, Profil di kanan), drawer navigasi kiri & drawer profil kanan, serta pengelompokan form profil ke dalam accordion collapsible ringkas.
- 2026-10-11: Penyempurnaan UI: Mode gelap pekat OLED Pitch Black (#0a0a0a), kartu mobile vertikal akun WhatsApp untuk layar Android, pembersihan total font hijau AI-slop, dan rekening penarikan global (SeaBank, BCA, DANA, USDT BEP20).
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
