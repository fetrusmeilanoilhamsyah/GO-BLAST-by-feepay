# DOC_PROGRES

Catatan posisi kerja. Diperbarui di akhir setiap tugas, oleh manusia atau AI agent.

Status: `[ ]` belum, `[~]` sedang dikerjakan, `[x]` selesai dan diverifikasi (tes dijalankan), `[!]` terblokir.
Nomor tahap mengacu ke tabel "Daftar Tahap" di bawah.

---

## Posisi sekarang

- **Fase**: 1 (MVP)
- **Terakhir dikerjakan**: Tahap 7 selesai (Upload nomor, normalisasi, duplikat per campaign).
- **Lanjut dari sini**: Tahap 8 (Template dan campaign).
- **Blocker**: tidak ada.

---

## TODO / Perlu Keputusan
- Terkait keputusan terbuka #4: Saat ini form pendaftaran pekerja tidak memuat pilihan mata uang. Kolom currency pada tabel `users` dibuat konstan `IDR` saat pendaftaran. Menunggu keputusan final pemilik proyek apakah mata uang dipilih saat pendaftaran.
- **Tahap 12**: Cek dompet saat upload nomor ditunda ke Tahap 12 (jangan buat fungsi kosong sekarang).

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
| 8 | Template dan campaign | Belum |
| 9 | Pengelola sesi WhatsApp | Belum |
| 10 | Mesin kirim: klaim nomor, kecepatan, buku besar | Belum |
| 11 | Panel pekerja | Belum |
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

Sudah diputuskan di rapat: multi-nomor per pekerja diizinkan (batas lewat pengaturan), tombol tidak boleh tampil sebagai link polos, rate IDR dan USDT diatur di panel, nomor internasional didukung, kode negara default untuk nomor berawalan 0 adalah Indonesia (bisa diubah di panel), dan aturan nomor hanya berlaku per campaign (tidak lintas campaign) supaya nomor tes bisa dipakai berulang. Nama produk GO BLAST (teks kecil "by feepay"), tema gelap tosca ala go.dev dengan nilai warna di DOC_KONSEP bagian 17, maskot gopher di panel.

## Keputusan yang sudah diambil

Terbaru di atas. Agen menambah baris di sini setiap ada keputusan baru, dari pemilik maupun pilihan teknis dari agen.

| Tanggal | Keputusan | Alasan | Oleh |
|---|---|---|---|
| 2026-10-11 | Nilai i18n Select Language disisipkan lewat data struct saat render template | Lebih aman dan simpel tanpa menyuntikkan template.FuncMap secara dinamis | agen |
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

Hasil uji dan hal yang ditemukan saat bekerja. Terbaru di atas.

- 2026-10-11: .env dari pemilik berhasil dibaca. Perintah `migrate status` merespons koneksi sukses tapi tidak ada file migrasi. Endpoint `/healthz` berjalan dan membalas sukses ({"status":"ok","db":"ok"}).
- 2026-10-11: Pemilik proyek memperbarui Go menjadi 1.27.2. Tahap 4 dilanjutkan: direktori dibuat, .env dan config diatur, go get sukses, theme.css diimplementasi dengan variabel warna sesuai bagian 17, MaskPhone dengan uji kasus ditulis, dan tes translasi berlalu dengan fallback ke id.
- 2026-10-11: Proses `go get` untuk `github.com/jackc/pgx/v5` dan `github.com/pressly/goose/v3` terblokir karena pustaka meminta Go versi >= 1.25.0 dan >= 1.26.0, sedangkan versi terpasang adalah 1.22.2. Eksekusi dihentikan sesuai aturan di DOC_AI_SLOP bagian 14.
- 2026-10-10: Berdasarkan dokumentasi whatsmeow, event receipt dibedakan menjadi TypeSender (centang 1) dan TypeDelivered (centang 2). Memori per sesi diprediksi sangat kecil (5-10MB idle), tapi bisa naik signifikan untuk 100 sesi jika ada antrean pesan. Perintah client.Logout() akan menghapus sesi lokal di database (sqlstore).
- 2026-10-10: Repo git diinisialisasi dengan struktur .gitignore yang melarang direktori data/unggahan. DOC_SECURITY_DEPLOY siap dengan panduan rate-limiting, proteksi privasi customer, dan arsitektur VPS.
- 2026-10-10: Cek lingkungan berhasil. Go (go1.22.2 windows/amd64), PostgreSQL (17.11), dan Git (2.53.0.windows.1) terdeteksi. pg_isready menerima koneksi di port 5432.
- (kosong)

## Log kerja

Terbaru di atas. Format: `tanggal: ringkasan satu baris`.

- 2026-10-11: Revisi aturan Tahap 7: Nomor tanpa awalan tidak lagi dianggap ambigu. Sistem langsung mem-parse (mendukung `49...` untuk Jerman, `1...` untuk US, dll) sesuai permintaan pemilik.
- 2026-10-11: Tahap 7 selesai (Upload nomor, normalisasi E.164, filter duplikat per campaign, preview CSV, handler).
- 2026-10-11: Menambahkan TODO untuk dompet di DOC_PROGRES.
- 2026-10-11: Tahap 6 selesai (Autentikasi admin dan pekerja, form daftar, middleware rate limit & CSRF, tes auth, dan command createadmin).
- 2026-10-11: Tahap 5 selesai (Skema database, migrasi, seed, DOC_SKEMA, dan test validasi skema constraint selesai).
- 2026-10-11: Koreksi Tahap 4 dan 2: gofmt, perbaikan validasi lang+cookie Lax, teks i18n di index.html, dan pembaruan DOC_SECURITY_DEPLOY sesuai hasil rapat.

- 2026-10-11: Tahap 4 selesai (Go 1.27.2 berhasil dipasang oleh pemilik proyek, konfigurasi dan dependensi beres).
- 2026-10-11: Tahap 4 dihentikan (terblokir) karena pustaka pgx/v5 dan goose/v3 meminta Go versi yang lebih tinggi dari yang terpasang.
- 2026-10-10: Tahap 3 selesai: program spike whatsmeow (pair, send, NativeFlowMessage) ditulis dan disusun laporan analisanya di HASIL.md.
- 2026-10-10: Tahap 2 selesai: buat repo git lokal, commit awal, dan penulisan dokumen .gitignore serta DOC_SECURITY_DEPLOY.md.
- 2026-10-10: Tahap 0 selesai: orientasi dokumen dan cek versi lingkungan (Go, PostgreSQL, Git).
- 2026-10-10: DOC_PROMPTS ditulis ulang dengan gaya rinci (butir bernomor, file dan nilai persis, larangan, verifikasi, commit, laporan tabel). Ditambahkan AGENTS.md, DOC_AI_SLOP bagian 14 (pustaka yang diizinkan), dan tabel Daftar Tahap resmi di DOC_PROGRES.
- 2026-10-10: Ditambahkan bagian Keputusan yang sudah diambil di DOC_PROGRES, aturan build dan seluruh tes sebelum selesai, serta DOC_AI_SLOP bagian 13 (git: satu commit per tugas, tanpa push otomatis).
- 2026-10-10: Nama produk GO BLAST dan tema gelap tosca ditetapkan. DOC_KONSEP bagian 17 (identitas dan tampilan) ditambahkan, DOC_AI_SLOP bagian 12, P4, dan P13 disesuaikan.
- 2026-10-10: DOC_AI_SLOP bagian 12 (tampilan dan aset) ditambahkan: warna sebagai variabel, font sistem, aturan maskot gopher dan atribusi.
- 2026-10-10: Format upload nomor ditetapkan CSV, TXT, XLSX (DOC_KONSEP bagian 5 dan 16, P7, DOC_AI_SLOP).
- 2026-10-10: Aturan nomor diubah dari "sekali seumur hidup" menjadi "sekali per campaign" (DOC_KONSEP bagian 2, 5, 6, 7, 16; DOC_AI_SLOP; P5 dan P7).
- 2026-10-10: DOC_KONSEP ditulis ulang ke versi 2: mode tombol (`tombol_asli` atau `kartu_link`), empat kecepatan, dua mata uang, multi-nomor dan sesi dihitung per nomor aktif, nomor internasional, bagian arah ke depan, keputusan terbuka dirapikan dan diberi nomor ulang.
- 2026-10-10: DOC_KONSEP direvisi pemilik (multi-nomor, tombol asli, penarikan USDT, empat kecepatan, tiga bahasa). DOC_AI_SLOP, DOC_PROGRES, DOC_PROMPTS dibuat.
- 2026-10-10: DOC_KONSEP pertama ditulis dari rapat konsep.
