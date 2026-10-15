# DOC_KONSEP: GO BLAST (Sistem Broadcast Iklan FEEPAY)

Dokumen ini adalah sumber kebenaran untuk konsep sistem. Semua pekerjaan (manusia maupun AI agent) mengacu ke sini. Kalau ada fitur yang tidak tertulis di sini, jangan dibuat tanpa izin pemilik proyek.

Status: Dokumen hidup (living document), terus disinkronkan dengan keputusan proyek.

---

## 1. Tujuan

Sistem ini adalah tim iklan untuk FEEPAY. Tujuannya satu: menaikkan trafik ke web FEEPAY lewat pesan WhatsApp yang dikirim ke nomor-nomor yang sudah dikumpulkan admin.

Pesan dikirim oleh banyak pekerja lepas. Tiap pekerja menghubungkan WhatsApp pribadinya ke sistem, lalu sistem yang mengirim pesan otomatis dari akun itu. Pekerja dibayar per pesan yang berhasil terkirim. Pembayaran dilakukan manual oleh admin.

Sistem dibuat murah: jumlah nomor yang dikirim menyesuaikan uang yang tersedia di dompet admin. Kalau uang belum ada, admin cukup memasukkan sedikit nomor.

Pasar awal adalah Indonesia (kampung, kampus, dan pembeli pulsa). Sistem dirancang agar nomor dan pekerja dari negara lain bisa dipakai tanpa mengubah desain dasar.

## 2. Yang sengaja tidak dibuat

Daftar ini sama pentingnya dengan daftar fitur. Jangan ditambahkan tanpa izin.

- Pembacaan atau pemrosesan balasan dari customer. Sistem hanya mengirim.
- Fitur berhenti otomatis (opt-out). Kalau penerima membalas "stop", sistem tidak membaca dan tidak mencatatnya. Sebagai gantinya: admin yang menjaga agar nomor yang sama tidak dimasukkan lagi ke campaign berikutnya (bagian 7). Kata FEEPAY dan elemen CS sekarang opsional (bukan wajib).
- Beberapa campaign berjalan paralel dengan prioritas. Untuk sekarang satu per satu.
- Role customer, login Google, PWA, Redis, WebSocket.
- Pengiriman otomatis uang ke pekerja. Pembayaran tetap manual.
- Konversi kurs otomatis antara rupiah dan USDT. Semua rate diatur manual oleh admin.
- Pesan berisi link polos tanpa tata letak (lihat bagian 8).

## 3. Aktor

**Admin** (pemilik proyek). Membuat campaign, upload nomor, mengatur template dan rate, melihat laporan, membayar pekerja secara manual.

**Pekerja**. Mendaftar, lalu menghubungkan satu atau banyak nomor WhatsApp lewat QR atau kode pairing. Jumlah nomor yang dimiliki tiap pekerja tidak diketahui sebelumnya, jadi sistem tidak membatasi secara kaku. Batasnya adalah pengaturan yang diubah admin di panel (bagian 13). Untuk tiap nomor, pekerja memilih kecepatan, lalu mulai dan berhenti kerja. Penghasilan semua nomor milik satu pekerja diakumulasikan ke satu akun. Pekerja tidak pernah melihat nomor customer, dan nomor customer tidak pernah dikirim ke browser pekerja. Pekerja bisa berasal dari negara mana pun.

**Publik** (termasuk calon pekerja). Melihat halaman yang menampilkan sisa total nomor tersedia (live), rate hari ini, dan ajakan mendaftar.

## 4. Komponen

1. **Panel admin**. Halaman web server-rendered.
2. **Panel pekerja**. Halaman web ringan, mobile-first.
3. **Halaman publik**. Angka sisa nomor, rate, tombol daftar.
4. **Mesin kirim** (broadcast engine). Mengelola sesi WhatsApp para pekerja, membagikan nomor ke sesi yang aktif, mengirim pesan, mencatat hasil.
5. **Database** PostgreSQL.

Semua berjalan di satu program Go di VPS yang ada, di belakang Nginx.

## 5. Alur data

**Admin menyiapkan campaign**
1. Admin membuat campaign: isi pesan, foto (opsional), mode tombol, dan elemen template yang diaktifkan (bagian 8).
2. Admin upload database nomor (file CSV, TXT satu nomor per baris, atau XLSX, atau input manual). File biasanya dibersihkan dulu di luar sistem memakai botcv, alat milik pemilik proyek yang tidak terhubung ke sistem ini. Sistem tetap memeriksa ulang semuanya.
3. Sistem menyeragamkan format nomor, membuang nomor tidak valid dan nomor duplikat di dalam file atau di dalam campaign yang sama (bagian 7), lalu memberi admin laporan berapa yang masuk dan berapa yang dilewati.
4. Nomor yang lolos masuk ke campaign dengan status tersedia. Kebutuhan dompet dihitung ulang (bagian 9).

**Pekerja bekerja**
1. Pekerja membuka panel, menghubungkan WhatsApp (QR atau pairing), memilih kecepatan, menekan mulai.
2. Mesin kirim memberikan nomor ke sesi pekerja itu satu per satu sesuai kecepatan, tanpa pernah memberikan nomor yang sama ke dua sesi.
3. Setiap hasil dicatat: terkirim, gagal, atau tidak terdaftar di WhatsApp.
4. Setiap pesan terkirim menambah penghasilan pekerja, dan menambah bonus referral kalau pekerja itu didaftarkan lewat link orang lain.
5. Pekerja menekan berhenti, atau sesi terputus. Sesi WhatsApp dihapus dari server. Nomor yang belum terkirim kembali ke antrean.

**Campaign selesai** ketika nomor tersedia habis. Bulan berikutnya admin membuat campaign baru dengan database nomornya sendiri.

## 6. Status nomor

Setiap nomor customer punya satu status:

- `tersedia`: belum dikirim, menunggu dibagikan.
- `dikirim`: sedang dipegang satu sesi pekerja (dikunci atomik via `ClaimNextNumber`, tidak bisa dipegang sesi lain).
- `terkirim`: sukses dikirim (diakui server WhatsApp via `server_ack`). Final dan kekal (immutable). Dibayar ke buku besar.
- `tidak_terdaftar`: nomor terbukti tidak punya WhatsApp dari pemeriksaan checker kontak. Final dan kekal. Tidak dibayar.
- `gagal`: gagal final setelah mencapai batas percobaan, nomor tidak valid, nomor beracun (panic), atau gangguan di tengah kirim dengan hasil tidak pasti (Keputusan A1). Tidak dibayar.

**Aturan Kepemilikan dan Integritas Status 'dikirim' (Tahap H3, Keputusan A1 & H1):**
1. **Kepemilikan Mutlak:** Hanya goroutine pekerja yang berhasil mengklaim nomor yang berhak mengubah status nomor dari `dikirim` menjadi status akhir. Pihak luar (admin via Stop, manajer WA via disconnect, restart server) TIDAK BOLEH memodifikasi baris `dikirim` secara ad-hoc atau melepasnya massal.
2. **Hasil Pasti Belum Terkirim (`ClassPastiBelumTerkirim`):** Jika sesi belum tersambung, klien nil, atau websocket putus sebelum kirim dipanggil, nomor dikembalikan ke `tersedia` dan `attempts` di-rollback (`attempts = GREATEST(attempts - 1, 0)`) oleh goroutine pemilik.
3. **Hasil Tidak Pasti (Keputusan A1):** Jika terjadi timeout server WhatsApp, koneksi putus di tengah pengiriman, crash, atau restart server, nomor TIDAK BOLEH dikembalikan ke `tersedia` secara otomatis karena berisiko pesan ganda ke customer. Nomor dinyatakan `gagal` dengan `last_error = 'hasil_tidak_pasti'`.
4. **Antrekan Ulang Manual (Keputusan H1):** Nomor yang gagal dengan `last_error = 'hasil_tidak_pasti'` hanya dapat dikembalikan ke antrean `tersedia` secara sadar oleh admin melalui tombol khusus "Antrekan ulang nomor hasil tidak pasti" setelah membaca peringatan risiko pesan ganda. Saat diantrekan ulang oleh admin, `attempts` di-reset ke 0.
5. **Nomor Beracun (Poison Pill):** Jika terjadi panic saat memproses sebuah nomor, goroutine menangkap panic dan memfinalisasi status nomor menjadi `gagal` (`last_error = 'galat_internal'`) serta menaikkan `safe.PanicCount()`.

Aturan keras: dalam satu campaign, satu nomor tidak boleh berstatus `terkirim` dua kali, apa pun yang terjadi. Ini ditegakkan di level database, bukan hanya di kode aplikasi.

## 7. Aturan nomor: satu kali per campaign

- Dalam satu campaign, setiap nomor hanya boleh dikirimi pesan satu kali.
- Tidak ada pengecekan lintas campaign. Nomor yang sama boleh dipakai lagi di campaign lain, termasuk nomor tes. Ini keputusan sadar pemilik proyek, supaya nomor tes bisa dipakai berulang selama pengembangan. Konsekuensinya, admin sendiri yang menjaga agar file nomor yang sama tidak dikirim berulang ke orang yang sama.
- Saat upload, nomor diseragamkan dulu ke format baku internasional (E.164 tanpa tanda `+`, misal `62812...` untuk Indonesia atau `1415...` untuk Amerika) sebelum dicocokkan. Nomor dari negara mana pun bisa diproses selama kode negaranya valid. Kode negara disimpan bersama nomor.
- Nomor yang ditulis berawalan `0` (format lokal) tidak membawa kode negara, jadi dianggap memakai kode negara default. Angka `0` di depan dibuang lalu diganti kode negara: `0812...` menjadi `62812...`, bukan `620812...`. Kode negara default adalah pengaturan di panel admin, nilai awalnya Indonesia (`62`).
- Nomor yang duplikat di dalam file, atau sudah ada di campaign yang sama, dilewati dan dilaporkan ke admin.
- **Fleksibilitas Data Penerima (Nomor Saja, Nama, Username / ID):** Sistem dirancang fleksibel dalam menerima data target. File upload mendukung nomor polos saja (tanpa nama), maupun nomor dengan data pelengkap seperti nama, username (USN), atau ID kustom (misal format `nomor,nama`, `nomor:nama`, atau multi-kolom CSV/XLSX). Sistem tidak boleh kaku mewajibkan adanya nama, namun siap menampung dan menyimpan atribut nama/ID jika disertakan.

## 8. Template pesan

Admin bisa mengubah template kapan saja. Elemen berikut bisa dinyalakan atau dimatikan lewat panel tanpa mengubah kode:

- Teks pesan (wajib)
- Foto (opsional, teks menjadi caption foto)
- Aksi ke web (opsional)
- Aksi CS (opsional, bawaan formulir: tidak aktif)
- Aksi telepon (opsional)
- Aksi claim promo (opsional)
- Personalisasi / Variabel pesan (opsional, misal tag `{nama}`): Jika data nama/USN penerima tersedia, sistem mengisi variabel tersebut. Jika data penerima hanya berupa nomor polos (tanpa nama), sistem memiliki fallback yang aman (salam umum atau tanpa teks rusak), atau alternatif fallback otomatis ke PushName WhatsApp / mention `@`.

**Tampilan harus rapi.** Pesan tidak boleh tampil sebagai teks dan link polos tanpa tata letak. Tiap campaign memilih satu dari dua mode:

1. `tombol_asli`: tombol interaktif sungguhan (link web, telepon atau CS, claim promo), memakai `NativeFlowMessage` lewat whatsmeow. **Keputusan P (Tahap H7):** Dibuka kembali sebagai opsi eksperimental dengan perlindungan pengaman: diberi label `(Eksperimental)`, formulir menampilkan banner peringatan merah mengenai risiko tidak dirender pada perangkat WhatsApp versi lama/tertentu dan risiko banned, checkbox persetujuan sadar risiko wajib dicentang (`agree_experimental_buttons`), dan dicatat dalam audit log. Sesuai keputusan pemilik: tidak dibatasi kuota 20 pesan (`config.MaxRecipientsNativeFlow = 0`), jumlah penerima bebas sesuai kehendak admin. Pengiriman wajib memakai abstraksi `MessageBuilder` dengan `InteractiveMessage.NativeFlowMessage` (protokol usang `TemplateMessage` dilarang keras karena memicu pemutusan sesi instan).
2. `kartu_link`: mode default dan andalan. Pesan berformat rapi (judul tebal, baris pendek, pemisah). `kartu_link` dikirim sebagai pesan teks dengan data preview (judul, deskripsi, thumbnail JPEG kecil) yang disusun oleh server, karena aplikasi WhatsApp penerima tidak mengambil Open Graph sendiri. Aman dari sisi protokol dan tampil profesional. Aturan `ThumbnailSpec`: lebar 300 piksel, kualitas JPEG 70 (dihasilkan server menggunakan pustaka `golang.org/x/image/draw.BiLinear`). Pratinjau di panel admin menampilkan data: URI langsung dari unggahan form atau dari DB tanpa menyimpan ke disk.

Penyebutan kata "FEEPAY" dan pengisian elemen CS bersifat opsional (tidak diwajibkan secara kaku saat penyimpanan campaign). Default form campaign baru adalah CS tidak aktif (`CSActive: false`).

Preview di panel hanyalah simulasi, bukan jaminan tampilan di HP penerima.

## 9. Upah, referral, dan dompet

**Mata uang tunggal.** Semua upah dan bonus referral dihitung dalam mata uang tunggal Rupiah (IDR), satu angka untuk seluruh pekerja. Tidak ada pemilihan mata uang akun saat pendaftaran. USDT hanya didapat lewat konversi eksplisit dari saldo rupiah ("Tukar ke USDT") dengan kurs yang diatur admin di panel (`rate_idr_to_usdt`), dilengkapi batas usia kurs maksimal 24 jam dan `request_token` idempoten.

**Upah pekerja.** Dihitung per pesan berstatus `terkirim` dalam rupiah utuh. Pesan gagal atau tidak terdaftar tidak dibayar. Rate hari ini ditampilkan di halaman pekerja/publik (misal: "Rp 50 per pesan" serta estimasi ekuivalen USDT jika kurs valid < 24 jam).

**Referral.** Setiap pekerja punya link referral unik. Kalau pekerja A mendaftar lewat link pekerja B, maka setiap pesan terkirim oleh A memberi B sebuah bonus per pesan dalam rupiah. Satu tingkat saja, bukan berantai.

**Rate dicatat per pesan.** Rate yang berlaku saat pesan terkirim disimpan bersama pesan itu di buku besar (`rate_snapshot`). Kalau admin mengubah rate besok, penghasilan kemarin tidak berubah.

**Catatan saldo.** Penghasilan dan mutasi dicatat sebagai buku besar append-only di `ledger_entries` (setiap transaksi menambah satu baris), bukan sebagai satu angka saldo yang ditimpa. Setiap baris menyimpan mata uangnya (`IDR` atau `USDT`).

**Dompet admin.** Jumlah nomor yang boleh dimasukkan dibatasi oleh dompet kas admin. Biaya terburuk per nomor = upah + bonus referral. Sistem menolak nomor yang melebihi dompet, sehingga kewajiban upah tidak pernah melebihi uang yang dialokasikan.

**Penarikan.** Penarikan rupiah diambil dari saldo rupiah, sedangkan penarikan USDT diambil dari saldo USDT hasil penukaran. Pekerja mengajukan tarik saldo, saldo langsung dikunci/dikurangi di buku besar, masuk antrean `withdrawals`, admin transfer manual, lalu menandai selesai di panel.
Metode penarikan yang didukung:
- **Lokal (Indonesia - saldo IDR):** DANA, Seabank, BCA, dan transfer bank lokal.
- **Internasional (saldo USDT):** USDT jaringan BEP20.

## 10. Kecepatan dan pengaman akun

Ada empat kecepatan, dengan jeda awal antar pesan:

| Kecepatan | Jeda awal |
|---|---|
| fast | 1 detik |
| medium | 5 detik |
| slow | 15 detik |
| over slow | 30 detik |

Semua nilai jeda diatur admin di panel, tidak ditanam di kode. Jeda sebenarnya ditambah variasi acak kecil. Batas harian per akun untuk tiap kecepatan belum diputuskan (bagian 15).

Pengaman yang wajib ada karena akun WhatsApp pekerja bisa dibanned (ini otomasi tidak resmi dan melanggar ketentuan WhatsApp), terutama di kecepatan fast:
- Batas harian per akun.
- Sesi otomatis berhenti kalau ada tanda masalah.
- Pekerja diberi tahu risiko ini sejak mendaftar, diperingatkan lagi saat memilih kecepatan tinggi, dan disarankan memakai nomor khusus.

## 11. Sesi dan privasi pekerja

- Server secara teknis memegang akses ke akun WhatsApp pekerja selama sesi aktif. Karena itu:
  - Sistem tidak menyimpan isi pesan masuk atau chat pribadi pekerja.
  - Data sesi dilindungi dan dihapus ketika pekerja berhenti atau sesi terputus. Protokol Noise melindungi lalu lintas, bukan kunci yang tersimpan; kunci whatsmeow tersimpan tidak terenkripsi di tabel whatsmeow_* pada database yang sama; mitigasi: dihapus saat berhenti, enkripsi disk VPS, pengguna database terbatas (dikerjakan di Tahap 16). Pekerja scan ulang tiap kali kerja.
  - Admin tidak membuka chat pribadi pekerja. Ini juga tertulis di syarat dan ketentuan.
- Nomor customer tidak pernah ditampilkan ke pekerja. Pekerja hanya melihat angka agregat.

## 12. Laporan

**Admin** melihat per campaign dan keseluruhan:
- Sisa nomor tersedia
- Jumlah terkirim (berhasil), gagal, tidak terdaftar
- Per pekerja: jumlah terkirim dan penghasilan (per mata uang)
- Daftar penarikan yang menunggu

Dashboard diperbarui dengan polling berkala (5 sampai 10 detik), bukan WebSocket.

**Pekerja** melihat penghasilan dan riwayat pesan terkirim miliknya sendiri, tanpa nomor customer.

**Publik** melihat sisa nomor tersedia (live), rate hari ini, dan cara mendaftar.

## 13. Skala dan stack

Asumsi skala:
- Pekerja terdaftar: 100 sampai 1000 orang, tapi tidak online bersamaan.
- Beban server ditentukan oleh jumlah **nomor WhatsApp yang aktif bersamaan**, bukan jumlah pekerja. Perkiraan awal 20 sampai 30 sesi, tapi karena satu pekerja boleh banyak nomor, angkanya bisa lebih besar dan harus diukur (Tahap 3 dan 15).
- Untuk melindungi VPS, ada pengaturan batas total sesi aktif dan batas nomor per pekerja. Kelebihannya mengantre, tidak ditolak mendadak. Nilai awal ditentukan dari hasil pengukuran memori per sesi.
- Database nomor: dimulai dari sekitar 10 ribu, kadang hanya puluhan nomor per hari menyesuaikan dompet.

Stack:
- Backend: Go. Alasan: hemat memori untuk sesi yang hidup terus, satu program tunggal.
- Pustaka WhatsApp: whatsmeow (belum diuji, wajib dicoba dulu sebelum fase 1). Pustaka ini tidak resmi, bisa rusak kalau WhatsApp mengubah protokol.
- Database: PostgreSQL.
- Antarmuka: halaman server-rendered dari Go (template) dengan HTMX seperlunya. Tidak ada React terpisah.
- Bahasa antarmuka: Indonesia, Inggris, dan Mandarin. Teks disimpan di berkas terjemahan, bukan di kode. Terjemahan harus natural, bukan kaku atau terasa seperti hasil mesin. Draf dari AI wajib ditinjau penutur asli sebelum rilis.
- Server: VPS yang sudah ada, Nginx sebagai reverse proxy.

## 14. Arah ke depan (bukan fase 1)

Kalau sistem ini berhasil, kemungkinan FEEPAY mulai menjual produk digital (kode-kode) ke luar negeri. Itu tidak dibangun sekarang, tapi yang perlu dijaga dari awal:
- Nomor disimpan dalam format internasional dengan kode negara (sudah di bagian 7).
- Template mungkin perlu varian per bahasa atau negara, dipilih berdasarkan kode negara nomor. Belum ada di fase 1.
- Mengirim pesan massal ke nomor di negara lain bisa terkena aturan setempat tentang izin menghubungi (misalnya di Eropa dan Amerika). Pemilik proyek perlu memeriksa ini sebelum nomor luar negeri dipakai dalam skala besar.

## 15. Keputusan yang masih terbuka

Jangan diputuskan sendiri oleh AI agent. Tanya pemilik proyek.

1. Definisi "terkirim": cukup diterima server WhatsApp (centang satu), atau harus sampai ke HP penerima (centang dua)? Usulan: centang satu, karena lebih cepat dan tidak bergantung HP penerima, tapi lebih mudah dicurangi.
2. Batas harian per kecepatan.
3. Jumlah percobaan ulang sebelum nomor dianggap `gagal`.
4. Cara menentukan mata uang akun pekerja. Usulan: dipilih pekerja saat daftar dan tidak bisa diganti setelah ada penghasilan.
5. Cara menghitung dompet dengan dua mata uang. Usulan: admin mengisi satu kurs acuan di panel, dipakai hanya untuk menghitung kebutuhan dompet dan laporan, tidak untuk mengonversi saldo pekerja.
6. Nilai awal batas nomor per pekerja dan batas total sesi aktif (setelah pengukuran memori).
7. Mode bawaan template: `tombol_asli` atau `kartu_link` (setelah uji Tahap 3).
8. Penanda klik unik per pekerja di link pesan, untuk melihat pekerja mana yang kirimannya benar-benar menghasilkan kunjungan dan sebagai pengaman dari pekerja yang hanya mengejar angka terkirim. Disarankan, tapi belum diputuskan masuk fase 1.
9. Pencegahan referral palsu (orang mendaftarkan akun kedua miliknya sendiri untuk menggaruk bonus). Minimal: satu nomor WhatsApp hanya bisa terdaftar sekali, dan admin bisa menonaktifkan akun.
10. Apakah bonus referral punya batas waktu atau batas jumlah.

## 16. Fase 1 (MVP)

Hanya ini yang dibuat dulu:
- Upload nomor (CSV, TXT, XLSX) dengan penyeragaman format internasional dan pembuangan duplikat dalam satu campaign
- Template teks dengan foto, mode tombol (`tombol_asli` atau `kartu_link`), elemen bisa dinyalakan atau dimatikan
- Panel admin: campaign, dompet, rate (IDR dan USDT), kecepatan, laporan dasar
- Pekerja: daftar, sambung satu atau banyak nomor WhatsApp, pilih kecepatan, mulai dan berhenti, lihat penghasilan
- Mesin kirim dengan pembagian nomor tanpa bentrok dan pelepasan otomatis saat sesi putus
- Penarikan manual (DANA, Seabank, bank lokal, USDT BEP20)
- Antarmuka tiga bahasa

Referral, penanda klik, dan hal lain di luar daftar ini menyusul setelah fase 1 jalan.

## 17. Identitas dan tampilan

- **Nama produk:** GO BLAST. Di dekat nama, tampil teks kecil "by feepay".
- **Tema:** Mengikuti pengaturan terang/gelap sistem perangkat secara default (`prefers-color-scheme`), serta dapat dipilih manual (auto, terang, gelap) via cookie `theme`. Referensi tampilan adalah go.dev, dengan tosca sebagai warna primer:

| Peran | Variabel CSS | Tema Terang (Bawaan) | Tema Gelap |
|---|---|---|---|
| Latar halaman | `--color-bg` | `#ffffff` | `#202224` |
| Latar hero | `--color-bg-hero` | `#f2f5f6` | `#2d2d2d` |
| Kartu | `--color-card` | `#ffffff` | `#3f4042` |
| Warna utama (tombol, brand) | `--color-primary` | `#007d9c` | `#007d9c` |
| Teks tombol utama | `--color-on-primary` | `#ffffff` | `#ffffff` |
| Aksen (sorotan) | `--color-accent` | `#00add8` | `#00add8` |
| Tautan | `--color-link` | `#007d9c` | `#50b7e0` |
| Teks utama | `--color-text` | `#202224` | `#e0e0e0` |
| Teks muted | `--color-muted` | `#59666c` | `#b0b0b0` |
| Garis batas | `--color-border` | `#d0d7de` | `#555555` |
| Permukaan muted | `--color-surface-muted` | `#f2f5f6` | `#35383a` |
| Sukses | `--color-success` | `#2e7d32` | `#4caf50` |
| Bahaya | `--color-danger` | `#c62828` | `#ff5252` |
| Peringatan | `--color-warning` | `#b25e00` | `#ff9800` |

  Seluruh pasangan kontras warna telah divalidasi dengan rumus rasio WCAG 2.1 ($\ge 4.5:1$). Hijau dipakai hanya untuk penghasilan dan status berhasil, merah hanya untuk gagal dan peringatan banned.
- **Font:** mengikuti tampilan referensi go.dev dengan daftar font sistem di `docs/DOC_AI_SLOP.md` bagian 12.
- **Maskot:** gopher kecil di panel pekerja dan admin. Aturan lisensi dan atribusi ada di `docs/DOC_AI_SLOP.md` bagian 12.
- **Catatan nama:** nama memakai kata "GO". Pemilik proyek memeriksa aturan merek dagang Go (go.dev/brand) sebelum nama ini dipakai di halaman publik.

## 18. Keadaan kode saat ini

Bagian ini mencatat fitur yang sudah ada di kode tetapi belum atau belum sepenuhnya tertulis di bagian-bagian sebelumnya. Diperbarui di Tahap S0 berdasarkan pembacaan langsung kode.

### Sistem notifikasi lonceng per pekerja

Tabel `notifications` (migrasi 0015) menampung tiga jenis notifikasi: `broadcast` (dari admin), `ticket_reply` (balasan tiket CS), dan `system`. Pekerja melihat notifikasi lewat ikon lonceng di navbar yang di-polling setiap 30 detik. Endpoint: `GET /worker/api/notifications`, `POST /worker/notifications/read`, `POST /worker/notifications/{id}/read`. File utama: `internal/web/worker_handlers.go` (handler notifikasi), `web/templates/worker_nav.html` (komponen lonceng).

### Balasan tiket CS oleh admin

Admin dapat menulis pesan balasan saat menyelesaikan tiket di `/admin/tickets`. Balasan disimpan di kolom `reply_message`, `replied_by`, `replied_at` pada tabel `support_tickets` (ditambahkan migrasi 0015) dan sekaligus membuat entri di `notifications` untuk pekerja pengirim tiket. Pekerja bisa membaca balasan dari modal bantuan dan dari lonceng notifikasi. File utama: `internal/web/admin_handlers.go` (HandleAdminTicketResolvePOST).

### Reset kata sandi pekerja oleh admin

Admin dapat mereset kata sandi pekerja dari halaman detail pekerja `/admin/workers/{id}`. Sistem menghasilkan kata sandi sementara acak, menghash-nya dengan argon2id, dan menampilkan kata sandi sementara sekali saja di halaman agar admin bisa menyampaikannya ke pekerja. Sesi aktif pekerja dihapus sekaligus saat reset. File utama: `internal/web/admin_handlers.go` (HandleAdminWorkerResetPasswordPOST).

### Mode pemeliharaan

Admin dapat mengaktifkan `maintenance_mode` lewat `/admin/maintenance`. Saat aktif, middleware mengalihkan semua permintaan pekerja (kecuali logout) ke halaman `/maintenance`. Admin tetap dapat mengakses panel. Engine pengiriman tidak dihentikan secara otomatis saat mode ini aktif (ini adalah penyimpangan yang dicatat; lihat DOC_PROGRES bagian Penyimpangan). Nilai disimpan di tabel `settings`. File utama: `internal/web/admin_handlers.go`, `internal/web/middleware.go`.

### Konversi IDR ke USDT oleh pekerja

Pekerja dapat mengonversi saldo IDR ke USDT lewat `POST /worker/convert`. Konversi menggunakan formula `(amount_idr × 1.000.000) / rate_idr_to_usdt`, dikerjakan dalam transaksi atomik dengan penguncian baris `users` (`FOR UPDATE`), dan dicatat sebagai dua entri buku besar berjenis `koreksi` (satu negatif IDR, satu positif USDT). Kurs `rate_idr_to_usdt` diatur admin di panel pengaturan. File utama: `internal/web/worker_handlers.go` (HandleWorkerConvertPOST).

### Email opsional

Sejak migrasi 0015, kolom `email` pada tabel `users` menjadi nullable. Pekerja boleh mendaftar tanpa mengisi email; login bisa dilakukan hanya dengan username. Admin selalu punya email. Seluruh query yang membaca email menggunakan `COALESCE` untuk menghindari error scan NULL. File utama: `db/migrations/0015_email_optional_notifications.sql`, `internal/web/auth_handlers.go`, `internal/web/middleware.go`.

### Nonaktifkan pekerja

Admin dapat menonaktifkan akun pekerja dari daftar pekerja (`/admin/workers`). Saat dinonaktifkan, kolom `status` pada `users` diubah ke `'inactive'`, semua sesi web pekerja dihapus, dan semua sesi WhatsApp pekerja diputus lewat WaManager. File utama: `internal/web/admin_handlers.go` (HandleAdminWorkerDisablePOST, HandleAdminWorkerEnablePOST).

### Variabel `{nama}` dalam template pesan

Template pesan mendukung variabel `{nama}` yang diisi dengan `recipient_name` dari tabel `campaign_numbers` saat pesan disusun. Jika `recipient_name` kosong, variabel diganti dengan string kosong (fallback aman, tidak ada teks rusak). Logika substitusi ada di engine saat `buildCampaignMessage`. File utama: `internal/engine/engine.go`.

### Kontak CS dari variabel lingkungan

URL kontak CS (WhatsApp, Telegram, Channel Telegram) dibaca dari variabel lingkungan `CS_WHATSAPP_URL`, `CS_TELEGRAM_URL`, `CHANNEL_TELEGRAM_URL` di `internal/config/config.go`, diteruskan ke `Server`, dan disematkan ke template pekerja. Ada nilai fallback hardcode aman jika variabel kosong. File utama: `internal/config/config.go`, `internal/web/worker_handlers.go` (csLinks()).

### Panel detail pekerja untuk admin

Halaman `/admin/workers/{id}` menampilkan profil lengkap pekerja, saldo IDR dan USDT, rekening tujuan penarikan (didekripsi), daftar akun WhatsApp, 10 penarikan terbaru, dan 20 entri buku besar terbaru. Admin dapat mereset kata sandi dan memutus semua sesi WhatsApp dari halaman ini. File utama: `internal/web/admin_handlers.go` (HandleAdminWorkerDetailGET, HandleAdminWorkerDetailDisconnectAllPOST).

### Sistem broadcast pengumuman admin

Admin membuat pengumuman di `/admin/broadcasts`. Saat disimpan atau diaktifkan (`is_active = true`), sistem menyebarkan notifikasi ke tabel `notifications` semua pekerja aktif secara atomik (`INSERT ... SELECT ... WHERE NOT EXISTS`). Auto-sync juga berjalan saat pekerja membuka endpoint notifikasi. File utama: `internal/web/admin_handlers.go` (HandleAdminBroadcastsGET, HandleAdminBroadcastNewPOST), `db/migrations/0012_support_and_broadcasts.sql`.

