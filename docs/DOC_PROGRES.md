# DOC_PROGRES

Catatan posisi kerja. Diperbarui di akhir setiap tugas, oleh manusia atau AI agent.

Status: `[ ]` belum, `[~]` sedang dikerjakan, `[x]` selesai dan diverifikasi (tes dijalankan), `[!]` terblokir.
Nomor tahap mengacu ke tabel "Daftar Tahap" di bawah.

---

## Posisi sekarang

- **Fase**: 1 (MVP)
- **Terakhir dikerjakan**: Tahap 8 selesai (Template dan campaign).
- **Lanjut dari sini**: Tahap 9 (Pengelola sesi WhatsApp).
- **Blocker**: tidak ada.

---

## TODO / Perlu Keputusan
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

Sudah diputuskan di rapat: multi-nomor per pekerja diizinkan (batas lewat pengaturan), tombol tidak boleh tampil sebagai link polos, rate IDR dan USDT diatur di panel, nomor internasional didukung, kode negara default untuk nomor berawalan 0 adalah Indonesia (bisa diubah di panel), dan aturan nomor hanya berlaku per campaign (tidak lintas campaign) supaya nomor tes bisa dipakai berulang. Nama produk GO BLAST (teks kecil "by feepay"), tema adaptif terang/gelap mengikuti perangkat dengan warna tosca, maskot gopher di panel.

## Keputusan yang sudah diambil

Terbaru di atas. Agen menambah baris di sini setiap ada keputusan baru, dari pemilik maupun pilihan teknis dari agen.

| Tanggal | Keputusan | Alasan | Oleh |
|---|---|---|---|
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

- 2026-10-11: Variabel pesan & data penerima: Software checker/Wati membaca PushName profil WA, mention @JID, atau kolom CSV nama/ID. GO BLAST dirancang fleksibel agar bisa menerima nomor polos (tanpa nama) maupun nomor dengan atribut nama/USN/ID, dengan fallback aman saat data nama kosong.
- 2026-10-11: Tema terang dan gelap diimplementasikan dengan rasio kontras WCAG 2.1 >= 4.5:1. Nilai danger pada mode gelap disesuaikan ke `#ff5252` (rasio 5.00:1) agar lulus standar aksesibilitas.
- 2026-10-11: spike whatsmeow: native buttons tidak dirender oleh WhatsApp pada nomor biasa/non-Cloud API. Di form campaign, opsi `tombol_asli` dinonaktifkan (`disabled`) dengan label jujur.
- 2026-10-11: Tema yang sebelumnya hanya gelap disesuaikan dengan preferensi sistem; template form dan campaign memakai variabel warna agar teks, input, dan border tetap kontras dalam mode terang.

## Log kerja

Terbaru di atas. Format: `tanggal: ringkasan satu baris` (Log Tahap 0 - 5 diarsipkan di [ARSIP_PROGRES.md](ARSIP_PROGRES.md)).

- 2026-10-11: Implementasi fleksibilitas data penerima (nomor polos, nomor+nama, usn/id) dan template variabel ({nama}, {{name}}, fallback {nama|Kak}): migrasi 0005_recipient_data.sql, parser numbers.Read (TXT, CSV, XLSX), ReplaceVariables, dan update preview & bulk insert.
- 2026-10-11: Koreksi Tahap 7 dan 8 selesai: validasi template (utf8 runes, host URL, normalisasi nomor), penolakan tombol_asli, render baris CS bersyarat, transisi status campaign (draf/berjalan/selesai), preview gambar & upload MIME, previewID regex, data/tmp 0600 & cleaner 24 jam, breakdown kode negara, deteksi sel numerik XLSX, CSV variable fields, currentUser helper, dan fix IP spoofing di getIP.
- 2026-10-11: Perapian repo (Bagian A, A2, B, C): bersihkan file nyasar, pisah main.go, hapus emoji/simbol UI, label jujur tombol_asli, tema adaptif terang & gelap, restrukturisasi docs/.
- 2026-10-11: Tema adaptif terang/gelap diimplementasikan melalui `prefers-color-scheme` sesuai keputusan pemilik.
- 2026-10-11: Tahap 8 selesai (Template dan campaign: list, form create/edit, preview, validation mode/url/image, logic Render format pesan).
- 2026-10-11: Revisi aturan Tahap 7: Nomor tanpa awalan tidak lagi dianggap ambigu. Sistem langsung mem-parse (mendukung `49...` untuk Jerman, `1...` untuk US, dll) sesuai permintaan pemilik.
- 2026-10-11: Tahap 7 selesai (Upload nomor, normalisasi E.164, filter duplikat per campaign, preview CSV, handler).
- 2026-10-11: Menambahkan TODO untuk dompet di DOC_PROGRES.
- 2026-10-11: Tahap 6 selesai (Autentikasi admin dan pekerja, form daftar, middleware rate limit & CSRF, tes auth, dan command createadmin).
