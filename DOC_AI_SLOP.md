# DOC_AI_SLOP: Aturan untuk AI Agent

Wajib dibaca sebelum mengerjakan tugas apa pun di proyek ini. Tujuannya mencegah hasil yang kelihatan jadi padahal asal: fitur tambahan yang tidak diminta, skema yang berubah diam-diam, data palsu, dan laporan "selesai" untuk hal yang tidak pernah dijalankan.

---

## 1. Urutan baca (setiap sesi)

1. `DOC_PROGRES.md`: untuk tahu posisi terakhir dan tugas berikutnya.
2. `DOC_KONSEP.md`: sumber kebenaran konsep.
3. `DOC_AI_SLOP.md`: dokumen ini.
4. `DOC_SKEMA.md` dan `DOC_SECURITY_DEPLOY.md` kalau sudah ada, untuk tugas yang menyentuh database, keamanan, atau deploy.

Kalau dokumen saling bertentangan, atau tugas tidak cocok dengan DOC_KONSEP: berhenti dan tanya. Jangan memilih sendiri.

## 2. Ruang lingkup

- Dilarang membuat fitur di luar `DOC_KONSEP.md` tanpa izin pemilik proyek, termasuk yang "sekalian saja biar lengkap". Daftar "Yang sengaja tidak dibuat" mengikat.
- Satu tugas dalam satu waktu. Setelah selesai: perbarui progres, laporkan, berhenti. Jangan lanjut ke tugas berikutnya sendiri.
- Dilarang refactor atau mengubah file yang tidak berkaitan dengan tugas.
- Dilarang menambah dependensi baru tanpa izin. Kalau butuh, sebutkan nama, alasan, dan alternatif tanpa dependensi.
- Keputusan di `DOC_KONSEP.md` bagian "Keputusan yang masih terbuka" tidak boleh diputuskan agen. Tanyakan. Sementara menunggu, pakai nilai yang bisa diubah lewat pengaturan atau panel, jangan ditanam di kode.

## 3. Database

- Dilarang mengubah skema tanpa migrasi baru dan pembaruan `DOC_SKEMA.md` di sesi yang sama.
- Migrasi hanya maju. Jangan mengedit migrasi lama yang sudah dijalankan.
- Dilarang menghapus atau mengubah data produksi tanpa izin tertulis dari pemilik proyek. Migrasi yang merusak data harus ditanyakan dulu.
- Aturan bisnis yang penting ditegakkan di database (unique, constraint, transaksi), bukan hanya di kode aplikasi.
- Uang tidak boleh disimpan atau dihitung dengan angka desimal mengambang (float). Pakai bilangan bulat atau desimal berpresisi tetap.

## 4. Data dummy dan data asli

- Dilarang menanam data dummy sebagai data produksi.
- Data contoh hanya boleh ada di seed untuk pengembangan dan tes, dipisah dari kode produksi, diberi penanda jelas, dan tidak dijalankan di produksi.
- Dilarang memakai nomor telepon asli di kode, tes, log, contoh, atau dokumentasi. Pakai nomor fiktif.

## 5. Aturan khusus proyek

Tidak boleh dilanggar dengan alasan apa pun:

- Nomor customer tidak boleh sampai ke browser pekerja, ke respons API pekerja, atau ke log (kalau perlu dicatat, disamarkan).
- Sistem tidak menyimpan isi chat atau pesan masuk milik pekerja.
- Data sesi WhatsApp disimpan terenkripsi, dan dihapus saat pekerja berhenti atau sesi terputus.
- Dalam satu campaign, satu nomor customer tidak boleh berstatus `terkirim` dua kali. Ditegakkan di database. Tidak ada aturan lintas campaign: nomor yang sama (termasuk nomor tes) boleh dipakai di campaign lain.
- Buku besar penghasilan hanya menambah baris. Koreksi dilakukan dengan baris pembalik, bukan dengan mengubah atau menghapus baris lama.
- Rate upah dan rate referral dicatat bersama setiap pesan saat pesan itu terkirim.
- Penerimaan hasil kirim harus idempoten: hasil yang sama dilaporkan dua kali tidak boleh membayar dua kali.
- Setiap endpoint memeriksa peran dan kepemilikan di sisi server. Menyembunyikan tombol di UI tidak dianggap pengamanan.
- Rahasia (kunci enkripsi, kata sandi, token) tidak boleh masuk repo. Pakai variabel lingkungan. `.env` ada di `.gitignore`, dan `.env.example` tidak berisi nilai asli.
- Tidak ada fitur yang menampilkan atau mengekspor daftar nomor customer ke pekerja.

## 6. Tes wajib untuk fitur sensitif

Fitur di bawah ini belum dianggap selesai tanpa tes otomatis yang benar-benar dijalankan:

- **Klaim nomor**: banyak sesi mengklaim sekaligus, tidak ada nomor yang didapat dua sesi. Nomor yang dipegang sesi yang putus kembali ke antrean.
- **Normalisasi dan duplikat nomor**: format campuran (`0812...`, `+62812...`, `62812...`) dikenali sebagai nomor yang sama, duplikat dalam satu campaign dilewati, dan nomor yang sama di campaign lain tidak diblokir. File XLSX dengan sel berformat angka juga diuji.
- **Buku besar penghasilan**: jumlah sesuai rate saat pesan terkirim, tidak ganda jika hasil dilaporkan dua kali.
- **Dompet**: nomor yang melebihi dompet ditolak.
- **Penarikan**: saldo tidak bisa jadi negatif, satu saldo tidak bisa ditarik dua kali.
- **Referral** (fase 2): bonus benar, tidak ganda, tidak bisa referral ke diri sendiri.
- **Akses antar peran**: pekerja tidak bisa membaca data pekerja lain atau data admin.

## 7. Kejujuran laporan

- Jangan menulis "selesai" atau "berhasil" untuk sesuatu yang tidak dijalankan. Bedakan tiga hal: dijalankan dan lulus, ditulis tapi belum dijalankan, tidak bisa diuji di lingkungan ini.
- Jangan menelan error. Jangan mengganti error dengan data palsu supaya kelihatan jalan.
- Jangan menulis fungsi kosong atau TODO yang berpura-pura bekerja. Setiap TODO dicatat di `DOC_PROGRES.md`.
- Jangan mengklaim perilaku pustaka WhatsApp (misalnya tombol tampil di HP penerima) tanpa uji nyata. Hasil uji dicatat apa adanya.
- Kalau tidak tahu, katakan tidak tahu.

## 8. Teks dan bahasa

- Semua teks antarmuka dan teks template bawaan disimpan di berkas terjemahan, bukan ditanam di kode. Bahasa yang didukung: Indonesia, Inggris, Mandarin.
- Gaya tulisan: natural, singkat, seperti orang menulis biasa. Hindari kalimat generik ala iklan, slogan, dan emoji sebagai hiasan daftar.
- Terjemahan Inggris dan Mandarin ditulis agen sebagai draf dan ditandai "perlu review penutur asli" di berkas sampai pemilik proyek menyetujui. Agen tidak boleh mengklaim terjemahannya sudah sempurna.

## 9. Definisi selesai

Sebuah tugas selesai kalau semua ini benar:

- Berjalan di lingkungan pengembangan.
- Proyek bisa dibangun (build) tanpa error, dan seluruh tes yang sudah ada dijalankan ulang, bukan hanya tes tugas ini. Tes yang relevan untuk tugas ini sudah ditulis dan dijalankan. Hasilnya dilaporkan apa adanya, termasuk tes yang gagal.
- Tidak ada fitur di luar lingkup tugas.
- Kalau skema berubah: ada migrasi dan `DOC_SKEMA.md` diperbarui.
- `DOC_PROGRES.md` diperbarui: status tugas, posisi sekarang, catatan temuan, dan log kerja.
- Keputusan yang diambil di sesi ini dicatat di bagian "Keputusan yang sudah diambil" di `DOC_PROGRES.md`: apa yang diputuskan, alasannya, dan siapa yang memutuskan (pemilik atau agen). Pilihan teknis kecil yang tidak ada di dokumen, misalnya memilih nama paket atau struktur folder, juga dicatat.
- Perubahan di-commit ke git sesuai bagian 13.

## 10. Format laporan akhir

Setiap tugas ditutup dengan laporan pendek berisi:

1. Yang dikerjakan (daftar file yang dibuat atau diubah).
2. Yang dijalankan dan hasilnya.
3. Yang belum dijalankan atau tidak bisa diuji, dan alasannya.
4. Pertanyaan atau keputusan yang butuh jawaban pemilik proyek.
5. Titik lanjut berikutnya.
6. Commit yang dibuat (pesan commit), dan apakah sudah di-push.

## 11. Berhenti dan tanya kalau

- Dokumen saling bertentangan.
- Tugas butuh keputusan yang masih terbuka.
- Akan menghapus data atau menjalankan migrasi yang merusak.
- Butuh dependensi baru.
- Perubahan menyentuh sesi WhatsApp, buku besar, atau penarikan dengan cara yang tidak ada di dokumen.
- Tugas ternyata terlalu besar untuk satu sesi. Pecah dan usulkan pembagiannya, jangan dikerjakan setengah-setengah.

## 12. Tampilan dan aset

- **Warna.** Semua warna disimpan sebagai variabel di satu berkas. Dilarang menulis kode warna langsung di halaman atau komponen. Nilai palet ada di `DOC_KONSEP.md` bagian 17. Jangan mengarang identitas visual sendiri.
- **Font.** Pakai daftar font sistem, tanpa mengunduh font dari luar (tidak ada Google Fonts atau CDN font). Alasannya halaman harus ringan di HP dengan RAM kecil dan koneksi lambat, dan font web untuk huruf Mandarin berukuran sangat besar. Daftar bawaan: `system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans", "PingFang SC", "Microsoft YaHei", sans-serif`. Dilarang menambah font web tanpa izin.
- **Maskot gopher.** Hanya di panel pekerja dan panel admin, berukuran kecil, untuk keadaan kosong, loading, atau pesan sukses. Satu atau dua pose, format SVG. Dilarang menjadi elemen utama di halaman publik, dan dilarang dipakai di pesan yang dikirim ke customer.
- **Nama produk.** Semua antarmuka memakai nama GO BLAST, dengan teks kecil "by feepay" di dekatnya.
- **Halaman publik.** Nama GO BLAST menjadi elemen utama, dengan "by feepay" sebagai teks kecil. Logo "Go" bertulisan tidak dipakai sebagai bagian identitas.
- **Sumber gopher.** Ambil dari grafis resmi atau repositori grafis komunitas Go, bukan dari gambar di halaman go.dev. Pemilik proyek memeriksa lisensi tiap file. Agen mencatat asal dan lisensi tiap file di `assets/CREDITS.md`.
- **Atribusi.** Footer panel menampilkan atribusi sesuai lisensi file yang dipakai, misalnya "Gopher oleh Renée French (CC BY 3.0)".
- **Logo "Go" bertulisan** tidak dipakai sebagai elemen desain. Aturan merek dagangnya diperiksa pemilik proyek di go.dev/brand.
- **Gambar umum.** SVG atau gambar kecil yang terkompresi. Tidak ada gambar latar besar.

## 13. Git dan repo

- Repo dibuat di Tahap 2. Sebelum itu belum ada commit.
- Satu commit per tugas, dibuat setelah build dan seluruh tes lulus. Format pesan: `P<nomor>: ringkasan singkat`.
- Agen tidak melakukan push, kecuali pemilik proyek memerintahkannya dengan jelas. Dilarang force push, menulis ulang riwayat, atau menghapus branch.
- Repo yang di-push harus privat.
- Tidak boleh ikut ke git: `.env`, kunci enkripsi, data sesi WhatsApp, file nomor customer (CSV, TXT, XLSX), cadangan database, dan file unggahan admin atau pekerja. Semuanya masuk `.gitignore` sejak Tahap 2. Kalau ada yang terlanjur ter-commit, berhenti dan laporkan ke pemilik proyek. Jangan menghapusnya dari riwayat sendiri.
- Seed dan contoh memakai nomor fiktif (lihat bagian 4).

## 14. Pustaka yang diizinkan

Pemilik proyek sudah menyetujui daftar ini. Pustaka di luar daftar tidak boleh dipasang tanpa izin (lihat bagian 2).

| Pustaka | Dipakai untuk | Mulai tahap |
|---|---|---|
| Pustaka standar Go (`net/http`, `html/template`, `encoding/csv`, `crypto/*`, `log/slog`, `testing`) | Server, templat, CSV, kriptografi, log, tes | Tahap 4 |
| `github.com/jackc/pgx/v5` | Koneksi PostgreSQL | Tahap 3 |
| `github.com/pressly/goose/v3` | Migrasi database | Tahap 4 |
| `github.com/joho/godotenv` | Membaca `.env` saat pengembangan | Tahap 4 |
| `golang.org/x/crypto` | Hash kata sandi argon2id | Tahap 6 |
| `golang.org/x/time` | Pembatasan laju | Tahap 6 |
| `github.com/nyaruka/phonenumbers` | Normalisasi dan validasi nomor E.164 | Tahap 7 |
| `github.com/xuri/excelize/v2` | Membaca file XLSX | Tahap 7 |
| `go.mau.fi/whatsmeow` | Koneksi WhatsApp | Tahap 3 |
| `github.com/skip2/go-qrcode` | Menggambar QR untuk pairing | Tahap 9 |

Tidak ada framework web, ORM, atau pustaka sesi. Router memakai `net/http`, SQL ditulis langsung dengan parameter.

**Versi Go dan toolchain.** Pustaka di daftar ini bisa meminta versi Go yang lebih baru dari yang terpasang. Kalau itu terjadi, berhenti dan laporkan versi yang diminta dan versi yang terpasang. Jangan membiarkan Go mengunduh toolchain sendiri: di terminal yang dipakai agen, atur `GOTOOLCHAIN=local` sebelum menjalankan perintah `go` (PowerShell: `$env:GOTOOLCHAIN = 'local'`), supaya ketidakcocokan versi muncul sebagai error yang jelas.
