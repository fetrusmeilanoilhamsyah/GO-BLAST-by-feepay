# RUNBOOK OPERASIONAL & TANGGAP DARURAT GO BLAST

Dokumen operasional satu halaman untuk administrator server GO BLAST by Feepay di VPS Linux.

---

## 1. Lokasi Penting & Port Sistem

| Komponen | Lokasi / Nilai | Keterangan |
|---|---|---|
| **Direktori Rilis Aktif** | `/opt/goblast/current` | Symlink ke rilis aktif di `/opt/goblast/releases/vX.Y.Z` |
| **Biner Aplikasi** | `/opt/goblast/current/goblast` | Biner tunggal Go (kompilasi Linux amd64) |
| **Port Internal** | `127.0.0.1:8090` | Terikat hanya di loopback lokal, tidak terbuka ke publik |
| **Berkas Konfigurasi & Kunci** | `/etc/goblast/goblast.env` | Izin `0640` (`root:goblast`), memuat kunci rahasia terenkripsi |
| **Daftar IP Cloudflare** | `/etc/goblast/cloudflare-ips.conf` | Subnet IP resmi Cloudflare untuk proteksi origin Nginx |
| **Data WhatsApp & Sesi** | `/opt/goblast/data` | Folder data terisolasi (izin `0700` `goblast:goblast`) |
| **Berkas Unggahan** | `/opt/goblast/uploads` | Berkas CSV/XLSX nomor dan gambar kampanye (`0700`) |
| **Berkas Cadangan (.gbak)** | `/opt/goblast/backups` | Arsip database terenkripsi AES-256-GCM (`0700`) |
| **Konfigurasi Nginx** | `/etc/nginx/sites-available/goblast.conf` | Reverse proxy, proteksi origin, dan rate limiting |
| **Unit Systemd** | `/etc/systemd/system/goblast.service` | Manajemen proses, isolasi, dan batas memori 600M |
| **Alat Rawat Cepat** | `/usr/local/bin/goblastctl` | Skrip CLI manajemen server resmi |

---

## 2. Cheat Sheet Perintah Operasional (`goblastctl`)

```bash
# Periksa status aplikasi, memori RSS, dan uptime proses
goblastctl status

# Jalankan audit kesehatan menyeluruh (10 titik pemeriksaan)
goblastctl doctor

# Pantau log sistem secara langsung (nomor customer disamarkan otomatis)
goblastctl logs -f
goblastctl logs --since 1h
goblastctl logs -n 50

# Kontrol layanan
goblastctl restart      # Restart anggun & verifikasi respon /readyz
goblastctl stop
goblastctl start

# Deploy rilis baru (zero-downtime & auto rollback jika gagal smoke test)
goblastctl deploy /tmp/goblast-v1.1.0.tar.gz

# Rollback ke versi sebelumnya jika rilis baru bermasalah
goblastctl rollback

# Perbarui daftar subnet resmi Cloudflare untuk Nginx
goblastctl refresh-cf-ips

# Lihat daftar dan verifikasi berkas cadangan database
goblastctl backups
goblastctl verify-backup
```

---

## 3. Prosedur Tanggap Darurat (Incident Runbook)

### Kasus A: Situs Mati / Nginx Mengembalikan "502 Bad Gateway"
1. Cek status unit:
   ```bash
   goblastctl status
   ```
2. Cek apakah proses mati karena kehabisan memori (OOM) atau panic:
   ```bash
   journalctl -u goblast -n 50 --no-pager
   ```
3. Nyalakan kembali aplikasi:
   ```bash
   goblastctl restart
   ```
4. Jika PostgreSQL mati:
   ```bash
   systemctl status postgresql
   systemctl restart postgresql
   goblastctl restart
   ```

### Kasus B: Penggunaan Memori Naik Mendekati Batas MemoryMax (600M)
1. Cek penggunaan memori proses:
   ```bash
   ps -o pid,user,%mem,rss,command -p $(pidof goblast)
   ```
2. Cek jumlah akun WhatsApp yang sedang aktif mengirim:
   Buka menu `/admin` $\rightarrow$ periksa total sesi WhatsApp yang terhubung.
3. Turunkan nilai `MAX_ACTIVE_SESSIONS` di `/etc/goblast/goblast.env` jika lonjakan disebabkan terlalu banyak sesi WhatsApp aktif.
4. Lakukan restart anggun untuk membersihkan akumulasi buffer memori:
   ```bash
   goblastctl restart
   ```

### Kasus C: Ruang Disk Bebas Rendah (< 15%)
1. Cek direktori yang memakan ruang terbesar:
   ```bash
   du -sh /opt/goblast/* /var/log/*
   ```
2. Bersihkan berkas sementara di folder data:
   Aplikasi secara otomatis membersihkan potongan upload > 1 jam. Lakukan pembersihan manual jika perlu:
   ```bash
   find /opt/goblast/data/tmp -type f -mmin +60 -delete
   ```
3. Batasi retensi log journald systemd:
   ```bash
   journalctl --vacuum-size=100M
   ```
4. Pindahkan berkas cadangan `.gbak` lama ke komputer lokal via `deploy/pull-backup.ps1`.

### Kasus D: Sesi WhatsApp Putus Massal / Server Habis Reboot
1. Sesuai **Keputusan B1**: Sesi WhatsApp **sengaja tidak dipulihkan otomatis saat restart server** demi keamanan akun pekerja.
2. Saat aplikasi boot ulang, modul `ReconcileOnBoot` otomatis mereset seluruh akun menjadi status `'terputus'`.
3. Nomor customer yang sedang dipegang pekerja saat server mati otomatis ditandai `gagal` dengan `last_error = 'hasil_tidak_pasti'`.
4. Pekerja cukup membuka dashboard pekerja dan menautkan ulang WhatsApp-nya.
5. Admin dapat mengantrekan ulang nomor berstatus `hasil_tidak_pasti` melalui tombol "Antrekan Ulang" di halaman detail kampanye panel admin.

### Kasus E: Admin Terkunci TOTP 2FA
Jika ponsel admin hilang atau aplikasi authenticator terhapus dan kehabisan kode pemulihan:
1. Masuk ke SSH server VPS:
   ```bash
   cd /opt/goblast/current
   sudo -u goblast ./goblast reset-totp --email admin@domain.com
   ```
2. Kunci TOTP dihapus seketika, dicatat di log audit keamanan, dan admin dapat login kembali dengan password lalu melakukan setup TOTP baru di `/admin/totp/setup`.

### Kasus F: Akses Langsung Menerobos Cloudflare Ditolak (Error 403)
1. Seluruh trafik wajib melalui domain Cloudflare (`fetrusmeilanoilhamsyah.my.id`).
2. Jika Cloudflare mengubah daftar subnet IP publiknya dan menyebabkan error 403 bagi pengunjung sah:
   ```bash
   goblastctl refresh-cf-ips
   ```
