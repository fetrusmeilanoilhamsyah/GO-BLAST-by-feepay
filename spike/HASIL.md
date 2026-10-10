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
| Pesan Kartu (Link) di Android | Jalankan `go run . send-card <nomor>` | Muncul kotak abu-abu dengan judul, link, dan preview. Rapi. | pemilik |
| Pesan Kartu (Link) di iPhone | Jalankan `go run . send-card <nomor>` | Muncul kotak abu-abu dengan judul, link, dan preview. Rapi. | pemilik |
| Receipt Server (Centang 1) | Observasi log console | Muncul di log sebagai `events.Receipt` bertipe `Sender` | AI (berdasar log/dokumentasi) |
| Receipt HP (Centang 2) | Observasi log console | Muncul di log sebagai `events.Receipt` bertipe `Delivered` | AI (berdasar log/dokumentasi) |
| Putus koneksi manual (Restart) | Tekan Ctrl+C saat program jalan | | diisi pemilik |
| Dikeluarkan dari HP pekerja | Logout via HP Linked Devices | | diisi pemilik |
| Hapus sesi (Logout) | Jalankan `go run . logout` | | diisi pemilik |

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
