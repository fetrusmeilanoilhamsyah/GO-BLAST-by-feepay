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
- `username`: Nama pengguna unik (3-20 karakter, huruf kecil, angka, underscore `_`). Wajib diisi saat pendaftaran pekerja (**migrasi 0018**).
- `email`: Unik dan huruf kecil. **Nullable sejak migrasi 0015** — email opsional saat pendaftaran pekerja. Admin selalu punya email.
- `password_hash`: Kata sandi yang di-hash.
- `role`: `admin` atau `worker`.
- `status`: Status akun (`active`, `suspended`, atau `frozen`). Nilai `'frozen'` ditambahkan pada **migrasi 0018** untuk pembekuan akun pekerja dengan penghentian sesi WhatsApp dan penolakan login/penarikan.
- `freeze_reason`: Alasan pembekuan akun oleh admin (TEXT, nullable, **ditambahkan migrasi 0018**).
- `terms_accepted_at`: Waktu pengguna menyetujui Syarat & Ketentuan saat pendaftaran (TIMESTAMPTZ, nullable, **ditambahkan migrasi 0018**).
- `terms_version`: Versi Syarat & Ketentuan yang disetujui (TEXT, nullable, misal 'v1.0', **ditambahkan migrasi 0018**).
- Kolom `currency` **dihapus pada migrasi 0016** — semua pekerja menerima upah dan bonus referral dalam mata uang tunggal Rupiah (IDR).
- `referral_code`: Kode referral unik.
- `referred_by`: Merujuk ke `users(id)` orang yang mengajak (nullable).
- `withdraw_locked_until`: Waktu penahanan penarikan (TIMESTAMPTZ, nullable, **ditambahkan migrasi 0017**). Diisi saat pekerja mengubah rekening/dompet atau saat admin mereset kata sandi (berlaku 24 jam).
- `payout_dest_hash`: Hash deterministik HMAC-SHA256 dari rekening/alamat wallet tujuan penarikan pekerja (TEXT, nullable, **ditambahkan migrasi 0017**). Digunakan untuk deteksi multi-akun dan pencarian admin tanpa mendekripsi data.
- `must_change_password`: Boolean penanda wajib ganti kata sandi setelah admin mereset kata sandi pekerja (BOOLEAN NOT NULL DEFAULT FALSE, **ditambahkan migrasi 0017**). Sesi lama dicabut dan rute pekerja dialihkan paksa ke ubah kata sandi.

### `web_sessions`
Menyimpan sesi login dari web.
- `token_hash`: Hash dari token sesi untuk keamanan.
- `expires_at`: Waktu kedaluwarsa sesi.

### `settings`
Menyimpan pengaturan aplikasi yang diubah melalui panel admin.
- `key`: Kunci pengaturan.
- `value`: Nilai JSONB.
- Sejak migrasi 0016, pengaturan upah `rate_wage` dan referral `rate_referral` disimpan sebagai bilangan bulat IDR tunggal (bukan map {IDR, USDT}). Kurs `rate_idr_to_usdt` adalah bilangan bulat (1 USDT = berapa rupiah), dan `rate_idr_to_usdt_updated_at` menyimpan waktu ISO-8601/RFC3339 kapan kurs terakhir diperbarui.
- Pada **migrasi 0018**, form `/admin/settings` disederhanakan menjadi 4 isian bisnis utama: `rate_wage`, `rate_referral`, `rate_idr_to_usdt`, dan `max_accounts_per_worker`. Kunci teknis backend terdahulu (`speed_delays`, `daily_limits`, `max_attempts`, `max_consecutive_failures`, `claim_timeout_seconds`, `max_active_sessions`, `default_country_code`) dihapus dari tabel dan dipindahkan ke konstanta Go di `internal/config/limits.go` serta variabel lingkungan `MAX_ACTIVE_SESSIONS`. Kunci saklar darurat `lock_withdrawals`, `lock_linking`, dan `lock_engine` tetap dikelola di halaman pemeliharaan.

### `campaigns`
Kampanye pengiriman pesan.
- `status`: `draf`, `berjalan`, atau `selesai`.
- `button_mode`: `tombol_asli` atau `kartu_link`.
- `elements`: JSONB untuk mengaktifkan/menonaktifkan elemen pesan.
- `archived_at`: TIMESTAMP WITH TIME ZONE (nullable). Diisi saat kampanye diarsipkan oleh admin. Kampanye terarsip bersifat hanya-baca, disembunyikan dari daftar bawaan, dan tidak menerima unggahan nomor baru.

### `number_batches`
Riwayat unggahan file nomor.
- `filename`: Nama file yang diunggah.
- Kolom statistik: `total`, `accepted`, `dup_in_file`, `dup_in_campaign`, `invalid`, `ambiguous`.

### `campaign_numbers`
Nomor tujuan per kampanye.
- `phone_e164`: Nomor telepon dalam format internasional tanpa `+`.
- `recipient_name`: Nama atau username/ID penerima (opsional, nullable).
- `custom_data`: Atribut kustom JSONB tambahan (opsional, nullable).
- `status`: `tersedia`, `dikirim`, `terkirim`, `tidak_terdaftar`, `gagal`.
- `claimed_account_id`: Merujuk ke `wa_accounts(id)` saat sedang diproses.

### `wa_accounts`
Sesi WhatsApp pekerja.
- `jid_hash`: Hash JID pekerja untuk privasi.
- `speed`: Kecepatan kirim (`fast`, `medium`, `slow`, `over_slow`).
- `status`: `menunggu_scan`, `terhubung`, `berhenti`, `terputus`, `antre`.
- `consecutive_failures`: Jumlah kegagalan beruntun untuk proteksi auto-stop (ditambahkan di migrasi 0006).
- `whatsmeow_device_id`: ID perangkat di tabel whatsmeow_devices (ditambahkan di migrasi 0006).

### `ledger_entries`
Buku besar untuk saldo pekerja (append-only).
- `kind`: `upah`, `referral`, `penarikan`, `koreksi`, `konversi` (**'konversi' ditambahkan di migrasi 0016**).
- `amount`: Nilai transaksi (positif untuk pendapatan, negatif untuk penarikan/konversi keluar). Bilangan bulat (BIGINT): IDR dalam rupiah utuh, USDT dalam sepersejuta USDT (contoh: 3000 mewakili 0.003 USDT).
- `rate_snapshot`: Rate yang berlaku saat itu (BIGINT, IDR: rupiah, USDT: sepersejuta USDT).
- `conversion_id`: Merujuk ke `currency_conversions(id)` ON DELETE RESTRICT (**ditambahkan migrasi 0016**).
- `reverses_entry_id`: Merujuk ke entri yang dikoreksi.

### `currency_conversions`
Tabel pencatatan transaksi penukaran saldo IDR ke USDT (**ditambahkan migrasi 0016**).
- `user_id`: Pekerja yang melakukan konversi, merujuk ke `users(id)` ON DELETE RESTRICT.
- `idr_amount`: Nominal rupiah yang ditukar (BIGINT > 0).
- `usdt_micro`: Nominal sepersejuta USDT yang didapatkan (BIGINT > 0, pembulatan ke bawah murni).
- `kurs`: Nilai tukar rupiah per 1 USDT saat transaksi (BIGINT > 0).
- `request_token`: Token acak dari form untuk mencegah konversi ganda (idempoten).
- `created_at`: Waktu transaksi dibuat.

### `support_tickets`
Tiket laporan/bantuan dari pekerja ke admin. Ditambahkan migrasi 0012.
- `user_id`: Pekerja pengirim tiket.
- `category`: Kategori laporan (misal: `koneksi`, `pembayaran`, `lainnya`).
- `message`: Isi laporan.
- `status`: `open` atau `resolved`.
- `admin_notes`: Catatan internal admin.
- `reply_message`: Pesan balasan dari admin ke pekerja (**ditambahkan migrasi 0015**).
- `replied_by`: Admin yang membalas, merujuk ke `users(id)` ON DELETE SET NULL (**migrasi 0015**).
- `replied_at`: Waktu balasan dikirim (**migrasi 0015**).
- `resolved_at`: Waktu tiket diselesaikan.

### `broadcasts`
Pengumuman yang dibuat admin. Ditambahkan migrasi 0012.
- `title`: Judul pengumuman.
- `message`: Isi pengumuman.
- `badge`: Jenis visual — `info`, `warning`, `success`, atau nilai lain.
- `is_active`: Boolean, hanya broadcast aktif yang disebar ke notifikasi pekerja.
- `created_by`: Admin pembuat, merujuk ke `users(id)` ON DELETE SET NULL.

### `notifications`
Notifikasi sistem untuk pekerja (lonceng di panel pekerja). **Ditambahkan migrasi 0015.**
- `user_id`: Penerima notifikasi (pekerja).
- `title`: Judul singkat notifikasi.
- `message`: Isi notifikasi.
- `kind`: Jenis — `broadcast` (dari admin), `ticket_reply` (balasan tiket), `system` (sistem).
- `ref_id`: ID referensi ke entitas asal (opsional, misal ID tiket).
- `read_at`: NULL = belum dibaca. Diisi saat dibaca.

### `wallet_entries`
Riwayat isi ulang saldo oleh admin.

### `withdrawals`
Penarikan saldo oleh pekerja.
- `method`: `dana`, `seabank`, `bank`, `usdt_bep20`.
- `destination_encrypted`: Tujuan transfer terenkripsi AES-256-GCM dengan prefiks versi (`v1:k1:` / `v1:k0:`).
- `request_token`: Token acak form untuk mencegah penarikan ganda / idempotensi (`TEXT NOT NULL`, **ditambahkan migrasi 0017**).
- `payout_reference`: Nomor referensi transfer bank atau TXID blockchain penarikan USDT yang wajib diisi admin saat menyetujui (`TEXT`, nullable, **ditambahkan migrasi 0017**).
- `reject_reason`: Alasan penolakan penarikan yang wajib diisi admin saat menolak (`TEXT`, nullable, **ditambahkan migrasi 0017**).

### `audit_logs`
Catatan aktivitas (audit trail).

## Constraint Wajib dan Alasannya
- `check_referred_by_not_self`: Pada `users`, memastikan pengguna tidak bisa memakai kode referral milik sendiri.
- `unique_campaign_phone`: Pada `campaign_numbers`, memastikan nomor telepon (E.164) hanya ada satu per kampanye, mencegah pengiriman ganda di level database.
- `CHECK` pada `currency` dan `status` di banyak tabel: Menjamin integritas data agar selalu valid sesuai DOC_KONSEP.
- `idx_withdrawals_user_token`: Indeks unik pada `withdrawals(user_id, request_token)` menjamin pengajuan penarikan dengan token yang sama bersifat idempoten (migrasi 0017).
- `idx_currency_conversions_user_token`: Indeks unik pada `currency_conversions(user_id, request_token)` menjamin permintaan konversi dengan token yang sama bersifat idempoten (migrasi 0016).
- `idx_ledger_entries_upah_unique`: Indeks unik parsial di `ledger_entries` memastikan satu `campaign_number_id` hanya bisa membayar upah maksimal satu kali.
- `trg_prevent_ledger_update_delete`: Trigger di `ledger_entries` menolak semua perintah UPDATE dan DELETE secara ketat (append-only tanpa pengecualian).
- `ledger_entries_campaign_number_id_fkey`: Foreign key pada `campaign_number_id` yang merujuk ke `campaign_numbers(id)` dengan aturan `ON DELETE RESTRICT` (migrasi 0014), memastikan nomor kampanye yang sudah tercatat di buku besar tidak dapat dihapus.
- `trg_check_withdrawal_balance`: Trigger pada INSERT `ledger_entries` dengan nominal negatif untuk jenis `penarikan` dan `konversi` (**migrasi 0016**) yang mengunci baris `users` (FOR UPDATE) dan memastikan saldo tidak menjadi negatif setelah penarikan atau konversi.

## Indeks Penting dan Alasannya
- `idx_campaign_numbers_claim`: Indeks parsial di `campaign_numbers (campaign_id, id) WHERE status = 'tersedia'`. Ini mempercepat klaim antrean menggunakan `FOR UPDATE SKIP LOCKED`.
- `idx_ledger_entries_user_curr_time`: Mempercepat kalkulasi saldo (`user_balance`) dan riwayat.
- `idx_currency_conversions_user_token`: Menjamin idempotensi konversi IDR ke USDT per pekerja dan per token.
- `idx_withdrawals_user_token`: Menjamin idempotensi penarikan per pekerja dan per token (migrasi 0017).
- `idx_withdrawals_user_pending`: Indeks parsial `withdrawals(user_id) WHERE status = 'menunggu'` untuk validasi batas maksimal 1 antrean penarikan pending (migrasi 0017).
- `idx_users_payout_dest_hash`: Indeks parsial `users(payout_dest_hash) WHERE payout_dest_hash IS NOT NULL` untuk pencarian pekerja berdasarkan nomor rekening/wallet tujuan (migrasi 0017).
- `idx_audit_logs_created_at`: Mempercepat query riwayat aktivitas.
- `idx_web_sessions_user_id`: Mempercepat pencarian dan penghapusan semua sesi milik satu pengguna.
- `idx_wa_accounts_worker_status`: Mempercepat penghitungan dan validasi batas akun aktif per pekerja.
- `idx_wa_accounts_status`: Mempercepat pemantauan status seluruh akun dan total batas sesi aktif sistem.
- `idx_campaign_numbers_sent_at`: (Migrasi 0011) Mempercepat query laporan tren harian 14 hari di dashboard admin (`sent_at >= $1`).
- `idx_wallet_entries_currency`: (Migrasi 0011) Mempercepat kalkulasi saldo dompet admin per mata uang (`currency, created_at`) dan riwayat isi ulang.
- `idx_support_tickets_status`: (Migrasi 0012) Mempercepat filter tiket berdasarkan status.
- `idx_support_tickets_user_id`: (Migrasi 0012) Mempercepat query tiket per pekerja.
- `idx_notifications_user_unread`: (Migrasi 0015) Indeks parsial `(user_id, created_at DESC) WHERE read_at IS NULL` — mempercepat query notifikasi belum dibaca di lonceng pekerja.
- `idx_notifications_user_id`: (Migrasi 0015) Mempercepat query semua notifikasi per pekerja.
