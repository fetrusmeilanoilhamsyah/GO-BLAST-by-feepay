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
- `dikirim`: sedang dipegang satu sesi pekerja (dikunci, tidak bisa dipegang sesi lain).
- `terkirim`: sukses dikirim. Final. Dibayar.
- `tidak_terdaftar`: nomor tidak punya WhatsApp. Final. Tidak dibayar.
- `gagal`: gagal permanen setelah beberapa kali percobaan. Final. Tidak dibayar.

Kalau sesi pekerja terputus saat sebuah nomor berstatus `dikirim`, nomor itu otomatis kembali ke `tersedia`. Gagal karena masalah sementara (jaringan, sesi putus) bukan gagal permanen. Jumlah percobaan maksimum belum diputuskan (bagian 15).

Aturan keras: dalam satu campaign, satu nomor tidak boleh berstatus `terkirim` dua kali, apa pun yang terjadi. Ini ditegakkan di level database, bukan hanya di kode aplikasi.

## 7. Aturan nomor: satu kali per campaign

- Dalam satu campaign, setiap nomor hanya boleh dikirimi pesan satu kali.
- Tidak ada pengecekan lintas campaign. Nomor yang sama boleh dipakai lagi di campaign lain, termasuk nomor tes. Ini keputusan sadar pemilik proyek, supaya nomor tes bisa dipakai berulang selama pengembangan. Konsekuensinya, admin sendiri yang menjaga agar file nomor yang sama tidak dikirim berulang ke orang yang sama.
- Saat upload, nomor diseragamkan dulu ke format baku internasional (E.164 tanpa tanda `+`, misal `62812...` untuk Indonesia atau `1415...` untuk Amerika) sebelum dicocokkan. Nomor dari negara mana pun bisa diproses selama kode negaranya valid. Kode negara disimpan bersama nomor.
- Nomor yang ditulis berawalan `0` (format lokal) tidak membawa kode negara, jadi dianggap memakai kode negara default. Angka `0` di depan dibuang lalu diganti kode negara: `0812...` menjadi `62812...`, bukan `620812...`. Kode negara default adalah pengaturan di panel admin, nilai awalnya Indonesia (`62`).
- Nomor yang duplikat di dalam file, atau sudah ada di campaign yang sama, dilewati dan dilaporkan ke admin.

## 8. Template pesan

Admin bisa mengubah template kapan saja. Elemen berikut bisa dinyalakan atau dimatikan lewat panel tanpa mengubah kode:

- Teks pesan (wajib)
- Foto (opsional, teks menjadi caption foto)
- Aksi ke web (opsional)
- Aksi CS (opsional, bawaan formulir: tidak aktif)
- Aksi telepon (opsional)
- Aksi claim promo (opsional)

**Tampilan harus rapi.** Pesan tidak boleh tampil sebagai teks dan link polos tanpa tata letak. Tiap campaign memilih satu dari dua mode:

1. `tombol_asli`: tombol interaktif sungguhan (link web, telepon atau CS, claim promo), memakai `NativeFlowMessage` lewat whatsmeow. **Temuan spike:** tombol asli tidak muncul di Android dan iPhone (sudah diuji pemilik); kartu link menampilkan judul, deskripsi, domain tanpa gambar karena JPEG thumbnail kecil belum tersedia. Di antarmuka admin, opsi `tombol_asli` dinonaktifkan (`disabled`) dengan label jujur.
2. `kartu_link`: mode default dan andalan. Pesan berformat rapi (judul tebal, baris pendek, pemisah). `kartu_link` dikirim sebagai pesan teks dengan data preview (judul, deskripsi, thumbnail JPEG kecil) yang disusun oleh server, karena aplikasi WhatsApp penerima tidak mengambil Open Graph sendiri. Aman dari sisi protokol dan tampil profesional.

Penyebutan kata "FEEPAY" dan pengisian elemen CS bersifat opsional (tidak diwajibkan secara kaku saat penyimpanan campaign). Default form campaign baru adalah CS tidak aktif (`CSActive: false`).

Preview di panel hanyalah simulasi, bukan jaminan tampilan di HP penerima.

## 9. Upah, referral, dan dompet

**Mata uang.** Rate upah dan rate referral ada dalam dua mata uang, rupiah (IDR) dan USDT. Keduanya diatur terpisah oleh admin di panel, tanpa kurs otomatis. Penghasilan seorang pekerja dicatat dalam satu mata uang, dan satu pesan dihitung memakai rate mata uang itu. Cara menentukan mata uang akun pekerja masih terbuka (bagian 15).

**Upah pekerja.** Dihitung per pesan berstatus `terkirim`. Pesan gagal atau tidak terdaftar tidak dibayar. Rate hari ini ditampilkan di halaman publik.

**Referral.** Setiap pekerja punya link referral unik. Kalau pekerja A mendaftar lewat link pekerja B, maka setiap pesan terkirim oleh A memberi B sebuah bonus per pesan. Contoh: A mengirim 10 pesan, bonus B = 10 x rate referral. Rate referral diatur admin. Satu tingkat saja, bukan berantai.

**Rate dicatat per pesan.** Rate yang berlaku saat pesan terkirim disimpan bersama pesan itu. Kalau admin mengubah rate besok, penghasilan kemarin tidak berubah.

**Catatan saldo.** Penghasilan dicatat sebagai buku besar (setiap pesan menambah satu baris), bukan sebagai satu angka saldo yang ditimpa. Setiap perubahan bisa ditelusuri. Tiap baris menyimpan mata uangnya.

**Dompet admin.** Jumlah nomor yang boleh dimasukkan dibatasi oleh dompet. Biaya terburuk per nomor = upah + bonus referral. Sistem menolak nomor yang melebihi dompet, sehingga kewajiban upah tidak pernah melebihi uang yang dialokasikan. Cara menghitung dompet dengan dua mata uang masih terbuka (bagian 15).

**Penarikan.** Pekerja mengajukan tarik saldo, masuk antrean, admin transfer manual, lalu menandai selesai (bukti transfer opsional). Ada riwayat penarikan per pekerja.
Metode penarikan yang didukung:
- **Lokal (Indonesia):** DANA, Seabank, dan seluruh bank lokal.
- **Internasional:** USDT jaringan BEP20.

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
  - Data sesi disimpan terenkripsi, dan dihapus ketika pekerja berhenti atau sesi terputus. Pekerja scan ulang tiap kali kerja.
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
