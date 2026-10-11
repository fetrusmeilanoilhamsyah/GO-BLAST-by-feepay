# Laporan Spike Uji Kelayakan Whatsmeow

Dokumen ini berisi hasil uji kelayakan (spike) pustaka `whatsmeow` sebelum diimplementasikan ke dalam program utama GO BLAST. 

## 1. Tabel Uji

Isi bagian yang kosong ("diisi pemilik") sesuai hasil pengamatan langsung pada HP penerima. 


| Uji | Cara | Hasil | Diamati oleh |
|---|---|---|---|
| Pair dengan kode | Jalankan `go run . pair-code <nomor>` | Berhasil masuk (Successfully authenticated) | pemilik |
| Pair dengan QR | Jalankan `go run . pair-qr` | (tidak diuji) | - |
| Cek nomor WA | Jalankan `go run . check <nomor>` | (tidak diuji) | - |
| Kirim teks biasa | Jalankan `go run . send-text <nomor> <pesan>` | Teks berhasil tampil di HP penerima | pemilik |
| Pesan NativeFlow (Tombol) di Android | Jalankan `go run . send-buttons <nomor>` | Tombol/pesan tidak muncul sama sekali di layar chat | pemilik |
| Pesan NativeFlow (Tombol) di iPhone | Jalankan `go run . send-buttons <nomor>` | Tombol/pesan tidak muncul sama sekali di layar chat | pemilik |
| Pesan Kartu (Link) di Android | Jalankan `go run . send-card <nomor>` | Judul + deskripsi + domain tampil, tanpa gambar. Perlu JPEGThumbnail. | pemilik |
| Pesan Kartu (Link) di iPhone | Jalankan `go run . send-card <nomor>` | Judul + deskripsi + domain tampil, tanpa gambar. Perlu JPEGThumbnail. | pemilik |
| Pesan Template Lama | Jalankan `go run . send-template <nomor>` | Dugaan: TemplateMessage lama memicu pencabutan perangkat (LoggedOut) | pemilik |
| Receipt Server (Centang 1) & Baca | Observasi log console | Muncul receipt Sender dan Read | pemilik |
| Receipt HP (Centang 2) | Observasi log console | (sudah tercover di atas) | pemilik |
| Putus koneksi manual (Restart) | Tekan Ctrl+C saat program jalan + status dicek ulang | Sesi sebelumnya sudah dicabut dari HP (LoggedOut). Program mencetak event LoggedOut dan keluar. Sesi otomatis terhapus dari DB. Harus pair ulang. | pemilik |
| Dikeluarkan dari HP pekerja | Logout via HP Linked Devices | Terjadi (event LoggedOut muncul, sesi terhapus, harus pair ulang) | pemilik |
| Hapus sesi (Logout) | Jalankan `go run . logout` | Gagal `noise handshake timed out` karena sesi sudah tidak ada (sudah dihapus setelah LoggedOut di langkah sebelumnya). Perilaku benar — tidak ada sesi yang bisa di-logout. | pemilik |

## 2. Penyelidikan whatsmeow berdasarkan Dokumentasi & Kode

### A. Peristiwa (Events) Penerimaan Pesan (Receipt)
Berdasarkan dokumentasi dan struktur `events.Receipt` pada `whatsmeow`:
- **"Diterima Server" (Centang 1)**: Ketika pesan sampai ke server WhatsApp, kita akan menerima `events.Receipt` dengan `Type: events.ReceiptTypeSender`.
- **"Sampai ke HP" (Centang 2)**: Ketika pesan sampai di aplikasi HP penerima, kita akan menerima `events.Receipt` dengan `Type: events.ReceiptTypeDelivered`.
- *Catatan:* Peristiwa ini otomatis memicu callback fungsi event handler di dalam aplikasi (lihat `client.AddEventHandler` di `main.go`).

### B. Penyimpanan Database & Privasi Sesi
- **Tabel**: Whatsmeow menggunakan modul `sqlstore` yang otomatis membuat beberapa tabel (contoh: `whatsmeow_device`, `whatsmeow_identity_keys`, `whatsmeow_contacts`, dsb). Tabel utama yang menyimpan sesi adalah `whatsmeow_device` yang berisi PushName, AbookKey, dan komponen identitas perangkat.
- **Enkripsi di Level Aplikasi**: `whatsmeow` tidak menyediakan hook bawaan yang mengamankan (enkripsi AES tingkat aplikasi) seluruh baris SQL *sebelum* disimpan ke driver. Akan tetapi, data session yang ditangani `whatsmeow` sebenarnya adalah "keys" yang secara alamiah dikelola lewat kriptografi (Noise protocol) oleh WA. Jika ingin patuh ketat pada `DOC_KONSEP` bagian 11 (data dienkripsi), kita dapat mengenkripsi di *level koneksi / kolom database* menggunakan enkripsi rest dari PostgreSQL, atau membungkus layer SQL driver agar data dienkripsi on-the-fly sebelum menyentuh PostgreSQL murni, *atau* menggunakan `Container` `sqlstore` dengan backend custom.
- **Efek Logout**: Memanggil fungsi `client.Logout()` akan menghapus perangkat dari server WhatsApp *dan* menghapus data (baris session) terkait perangkat tersebut di database lokal `sqlstore`. Hal ini memenuhi syarat "dihapus saat pekerja berhenti" dari `DOC_KONSEP`.

### C. Pemakaian Memori per Sesi
- **Secara Teori (Dokumentasi/Issue Tracker)**: Satu sesi/klien `whatsmeow` yang standby (idle) memakan footprint memori yang sangat kecil (antara 5-10 MB di luar overhead Go runtime). 
- **Perkiraan**: 
  - **30 sesi**: Memori aplikasi mungkin naik sekitar 150 - 300 MB.
  - **100 sesi**: Berpotensi menghabiskan sekitar 500 MB - 1 GB (tergantung frekuensi message masuk/keluar dan garbage collection Go).
- **Alat Ukur Windows**: Pemilik proyek dapat memantau footprint riil program saat satu sesi terhubung dengan mengecek tab **Memory** di **Task Manager** pada proses aplikasi Go.

### D. Perilaku Putus Sesi (Disconnect) vs Dikeluarkan (LoggedOut)
- Jika koneksi terputus (jaringan mati/program di-restart), event `events.Disconnected` terpanggil. Sesi *tidak* hilang, aplikasi bisa menjalankan `client.Connect()` kembali tanpa perlu scan QR.
- Jika pengguna mencabut sesi dari "Linked Devices" di HP-nya sendiri, server WhatsApp akan melempar error dan klien akan menerima event `events.LoggedOut`. Baris di database otomatis invalid, perangkat tidak dapat konek lagi, dan pekerja harus scan/pairing ulang jika ingin mulai lagi.

## 3. Langkah Persiapan Database (Untuk Pemilik)

Jalankan perintah SQL ini (di alat seperti pgAdmin atau psql) untuk membuat database dan user:

```sql
-- Dijalankan sebagai pengguna postgres (admin)
CREATE DATABASE goblast_spike;
CREATE USER goblast_spike_user WITH ENCRYPTED PASSWORD '<isi_password_di_sini>';
GRANT ALL PRIVILEGES ON DATABASE goblast_spike TO goblast_spike_user;

-- Untuk PostgreSQL 15+, berikan hak modifikasi skema ke user di public schema
\c goblast_spike
GRANT ALL ON SCHEMA public TO goblast_spike_user;
```

Setelah itu, buat file `.env` di folder `spike/` berisi:

```env
DATABASE_URL=postgres://goblast_spike_user:<isi_password_di_sini>@localhost:5432/goblast_spike?sslmode=disable
```

Setelah `DATABASE_URL` siap, pemilik bisa langsung mencoba perintah dari Tabel Uji di atas.

## 4. Uji Thumbnail Kartu (Butir 0b — diisi pemilik)

> **Catatan latar belakang**: Pada uji sebelumnya, `thumb.jpg` identik dengan `full.jpg` (945×459, ±60 KB), bukan thumbnail sungguhan. Perintah `send-card-thumb` di bawah membuat tiga thumbnail JPEG nyata dari `full.jpg` dengan lebar 160, 300, dan 480 piksel (kualitas 70) lalu mengirimnya sebagai tiga pesan kartu terpisah.

### Cara menjalankan

```
cd spike
go run . send-card-thumb <nomor_cadangan_anda>
```

Program akan mencetak ukuran berkas masing-masing thumbnail ke konsol, misalnya:
```
Thumbnail 160px: 3421 bytes
Kartu 160px terkirim. ID: ...
Thumbnail 300px: 8912 bytes
Kartu 300px terkirim. ID: ...
Thumbnail 480px: 17834 bytes
Kartu 480px terkirim. ID: ...
```

### Tabel hasil (diisi pemilik setelah melihat di HP)

| Lebar thumbnail | Ukuran berkas (bytes) | Tampil di Android? | Tampil di iPhone? |
|---|---|---|---|
| 160 px | diisi agen saat run | diisi pemilik | diisi pemilik |
| 300 px | diisi agen saat run | diisi pemilik | diisi pemilik |
| 480 px | diisi agen saat run | diisi pemilik | diisi pemilik |

> **Instruksi pengisian**: Kirim tiga pesan itu ke HP cadangan Anda. Amati apakah thumbnail gambar muncul di chat. Isi kolom "Tampil" dengan Ya / Tidak / Sebagian. Hasilnya akan dipakai untuk menetapkan `ThumbnailSpec` di `DOC_KONSEP` bagian 8.
