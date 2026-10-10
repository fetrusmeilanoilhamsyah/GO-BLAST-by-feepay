# DOC_SKEMA: Skema Database GO BLAST

Dokumen ini menjelaskan struktur database dan relasi antar tabel untuk aplikasi GO BLAST.

## Gambaran Relasi
- `users`: Tabel pusat yang menyimpan admin dan pekerja. Pekerja bisa memiliki banyak `wa_accounts`.
- `web_sessions`: Menyimpan sesi login pengguna, merujuk ke `users(id)`.
- `campaigns`: Kampanye yang dibuat admin.
- `number_batches`: Mengelompokkan nomor yang diunggah ke suatu kampanye, merujuk ke `campaigns(id)` dan `users(id)` (pengunggah).
- `campaign_numbers`: Nomor spesifik dalam kampanye, merujuk ke `campaigns(id)` dan `number_batches(id)`.
- `wa_accounts`: Akun WhatsApp pekerja, merujuk ke `users(id)`.
- `ledger_entries`: Buku besar catatan finansial pekerja, merujuk ke `users(id)`. Merujuk ke `campaign_numbers(id)` untuk upah.
- `wallet_entries`: Riwayat isi ulang dompet admin.
- `withdrawals`: Pengajuan penarikan dana oleh pekerja, merujuk ke `users(id)`.
- `audit_logs`: Catatan aktivitas penting, merujuk ke `users(id)`.

## Tabel dan Kolom Penting

### `users`
Menyimpan data pengguna (admin dan pekerja).
- `email`: Unik dan huruf kecil.
- `password_hash`: Kata sandi yang di-hash.
- `role`: `admin` atau `worker`.
- `currency`: Mata uang preferensi (`IDR` atau `USDT`).
- `referral_code`: Kode referral unik.
- `referred_by`: Merujuk ke `users(id)` orang yang mengajak (nullable).

### `web_sessions`
Menyimpan sesi login dari web.
- `token_hash`: Hash dari token sesi untuk keamanan.
- `expires_at`: Waktu kedaluwarsa sesi.

### `settings`
Menyimpan pengaturan aplikasi yang diubah melalui panel admin.
- `key`: Kunci pengaturan (contoh: `default_country_code`).
- `value`: Nilai JSONB.

### `campaigns`
Kampanye pengiriman pesan.
- `status`: `draf`, `berjalan`, atau `selesai`.
- `button_mode`: `tombol_asli` atau `kartu_link`.
- `elements`: JSONB untuk mengaktifkan/menonaktifkan elemen pesan.

### `number_batches`
Riwayat unggahan file nomor.
- `filename`: Nama file yang diunggah.
- Kolom statistik: `total`, `accepted`, `dup_in_file`, `dup_in_campaign`, `invalid`, `ambiguous`.

### `campaign_numbers`
Nomor tujuan per kampanye.
- `phone_e164`: Nomor telepon dalam format internasional tanpa `+`.
- `status`: `tersedia`, `dikirim`, `terkirim`, `tidak_terdaftar`, `gagal`.
- `claimed_account_id`: Merujuk ke `wa_accounts(id)` saat sedang diproses.

### `wa_accounts`
Sesi WhatsApp pekerja.
- `jid_hash`: Hash JID pekerja untuk privasi.
- `speed`: Kecepatan kirim (`fast`, `medium`, `slow`, `over_slow`).
- `status`: `menunggu_scan`, `terhubung`, `berhenti`, `terputus`, `antre`.

### `ledger_entries`
Buku besar untuk saldo pekerja (append-only).
- `kind`: `upah`, `referral`, `penarikan`, `koreksi`.
- `amount`: Nilai transaksi (positif untuk pendapatan, negatif untuk penarikan).
- `rate_snapshot`: Rate yang berlaku saat itu.
- `reverses_entry_id`: Merujuk ke entri yang dikoreksi.

### `wallet_entries`
Riwayat isi ulang saldo oleh admin.

### `withdrawals`
Penarikan saldo oleh pekerja.
- `method`: `dana`, `seabank`, `bank`, `usdt_bep20`.
- `destination_encrypted`: Tujuan transfer terenkripsi.

### `audit_logs`
Catatan aktivitas (audit trail).

## Constraint Wajib dan Alasannya
- `check_referred_by_not_self`: Pada `users`, memastikan pengguna tidak bisa memakai kode referral milik sendiri.
- `unique_campaign_phone`: Pada `campaign_numbers`, memastikan nomor telepon (E.164) hanya ada satu per kampanye, mencegah pengiriman ganda di level database.
- `CHECK` pada `currency` dan `status` di banyak tabel: Menjamin integritas data agar selalu valid sesuai DOC_KONSEP.
- `idx_ledger_entries_upah_unique`: Indeks unik parsial di `ledger_entries` memastikan satu `campaign_number_id` hanya bisa membayar upah maksimal satu kali.
- `trg_prevent_ledger_update_delete`: Trigger di `ledger_entries` menolak perintah UPDATE dan DELETE agar buku besar selalu append-only.
- `trg_check_withdrawal_balance`: Trigger pada INSERT `ledger_entries` jenis `penarikan` yang mengunci baris `users` (FOR UPDATE) dan memastikan saldo tidak menjadi negatif setelah penarikan.

## Indeks Penting dan Alasannya
- `idx_campaign_numbers_claim`: Indeks parsial di `campaign_numbers (campaign_id, id) WHERE status = 'tersedia'`. Ini mempercepat klaim antrean menggunakan `FOR UPDATE SKIP LOCKED`.
- `idx_ledger_entries_user_curr_time`: Mempercepat kalkulasi saldo (`user_balance`) dan riwayat.
- `idx_audit_logs_created_at`: Mempercepat query riwayat aktivitas.
- `idx_web_sessions_user_id`: Mempercepat pencarian dan penghapusan semua sesi milik satu pengguna.
