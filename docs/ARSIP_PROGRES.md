# ARSIP_PROGRES: GO BLAST

Arsip log eksekusi dan catatan temuan historis untuk Tahap 0 sampai Tahap 5.
Dokumen aktif dan log terkini (Tahap 6 ke atas) tetap berada di [DOC_PROGRES.md](DOC_PROGRES.md).

---

## Catatan Temuan Historis (Tahap 0 - 5)

- 2026-10-11: .env dari pemilik berhasil dibaca. Perintah `migrate status` merespons koneksi sukses tapi tidak ada file migrasi. Endpoint `/healthz` berjalan dan membalas sukses ({"status":"ok","db":"ok"}).
- 2026-10-11: Pemilik proyek memperbarui Go menjadi 1.27.2. Tahap 4 dilanjutkan: direktori dibuat, .env dan config diatur, go get sukses, theme.css diimplementasi dengan variabel warna sesuai bagian 17, MaskPhone dengan uji kasus ditulis, dan tes translasi berlalu dengan fallback ke id.
- 2026-10-11: Proses `go get` untuk `github.com/jackc/pgx/v5` dan `github.com/pressly/goose/v3` terblokir karena pustaka meminta Go versi >= 1.25.0 dan >= 1.26.0, sedangkan versi terpasang adalah 1.22.2. Eksekusi dihentikan sesuai aturan di DOC_AI_SLOP bagian 14.
- 2026-10-10: Berdasarkan dokumentasi whatsmeow, event receipt dibedakan menjadi TypeSender (centang 1) dan TypeDelivered (centang 2). Memori per sesi diprediksi sangat kecil (5-10MB idle), tapi bisa naik signifikan untuk 100 sesi jika ada antrean pesan. Perintah client.Logout() akan menghapus sesi lokal di database (sqlstore).
- 2026-10-10: Repo git diinisialisasi dengan struktur .gitignore yang melarang direktori data/unggahan. DOC_SECURITY_DEPLOY siap dengan panduan rate-limiting, proteksi privasi customer, dan arsitektur VPS.
- 2026-10-10: Cek lingkungan berhasil. Go (go1.22.2 windows/amd64), PostgreSQL (17.11), dan Git (2.53.0.windows.1) terdeteksi. pg_isready menerima koneksi di port 5432.

---

## Log Kerja Historis (Tahap 0 - 5)

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
