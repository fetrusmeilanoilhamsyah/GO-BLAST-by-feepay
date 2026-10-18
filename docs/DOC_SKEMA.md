# DOC_SKEMA: Skema Database GO BLAST

Dokumen ini adalah sumber kebenaran teknis struktur database PostgreSQL untuk aplikasi GO BLAST by feepay.
Diperbarui pada **Tahap H2** berdasarkan katalog nyata PostgreSQL (`pg_catalog` / `information_schema`) dari migrasi `0001` hingga `0023`.

---

## 1. Gambaran Relasi (17 Tabel)

Aplikasi GO BLAST terdiri dari 17 tabel inti:

1. `users`: Entitas pengguna sistem (admin dan worker).
2. `web_sessions`: Sesi web HTTP pengguna berbasis token cookie, dilengkapi `stepup_at` untuk TOTP admin.
3. `admin_recovery_codes`: Kode pemulihan darurat satu kali pakai untuk admin ber-TOTP (migrasi 0023).
4. `settings`: Konfigurasi global sistem (key-value JSONB).
5. `campaigns`: Kampanye pesan WhatsApp yang dibuat admin, dilengkapi `purged_at` dan `purge_summary` (migrasi 0023).
6. `number_batches`: Pengelompokan file nomor tujuan yang diunggah ke kampanye.
7. `campaign_numbers`: Nomor penerima pesan dalam kampanye, dilengkapi optimasi `fillfactor=85` dan indeks parsial klaim/stale (migrasi 0019).
8. `wa_accounts`: Akun WhatsApp pekerja untuk blasting, menggunakan `wa_jid_hash` (migrasi 0021).
9. `ledger_entries`: Buku besar keuangan pekerja (append-only, tidak dapat diubah/dihapus).
10. `wallet_entries`: Riwayat saldo/top-up dompet kas admin.
11. `withdrawals`: Permintaan penarikan upah pekerja (fiat bank/e-wallet & USDT BEP-20).
12. `currency_conversions`: Riwayat penukaran saldo pekerja dari IDR ke USDT.
13. `audit_logs`: Rekam jejak audit keamanan dan aktivitas administratif.
14. `support_tickets`: Tiket bantuan/keluhan dari pekerja ke tim admin.
15. `broadcasts`: Pengumuman umum dari admin kepada para pekerja.
16. `notifications`: Notifikasi personal pekerja (lonceng dashboard).
17. `backups`: Metadata dan catatan verifikasi berkas cadangan database (migrasi 0023).

---

## 2. Struktur Tabel dan Kolom

### 2.1 `users`
Menyimpan akun administrator dan pekerja (worker).
- `id` BIGSERIAL PRIMARY KEY.
- `username` VARCHAR(50) UNIQUE (nullable saat pendaftaran lama, wajib di 0018). Indeks unik case-insensitive `idx_users_username_lower`.
- `email` VARCHAR(255) NULL. Indeks unik parsial case-insensitive `idx_users_email_lower` (`WHERE email IS NOT NULL`, migrasi 0020). Email opsional bagi pekerja.
- `password_hash` VARCHAR(255) NOT NULL.
- `role` VARCHAR(20) NOT NULL CHECK (`role IN ('admin', 'worker')`).
- `display_name` VARCHAR(100) NOT NULL.
- `status` VARCHAR(50) NOT NULL DEFAULT 'aktif' CHECK (`status IN ('aktif', 'nonaktif', 'dibekukan')`).
  *(Catatan koreksi H2: Nilai CHECK resmi adalah `'aktif'`, `'nonaktif'`, `'dibekukan'`; bukan istilah bahasa Inggris 'active'/'suspended'/'frozen').*
- `freeze_reason` TEXT NULL: Alasan pembekuan worker oleh admin.
- `referral_code` VARCHAR(50) UNIQUE NOT NULL.
- `referred_by` BIGINT NULL REFERENCES `users(id)`: Pengguna yang mengundang (CHECK: `referred_by <> id`). Indeks `idx_users_referred_by` (migrasi 0022).
- `wallet_address` TEXT NULL: Alamat wallet USDT BEP-20 milik worker.
- `bank_name` VARCHAR(50) NULL, `bank_account_number` VARCHAR(50) NULL, `bank_account_holder` VARCHAR(100) NULL.
- `payout_dest_hash` TEXT NULL: Hash deterministik HMAC-SHA256 tujuan penarikan untuk pencegahan multi-akun (migrasi 0017).
- `withdraw_locked_until` TIMESTAMPTZ NULL: Waktu penahanan penarikan 24 jam setelah perubahan profil/rekening.
- `must_change_password` BOOLEAN NOT NULL DEFAULT false.
- `preferred_language` VARCHAR(5) NOT NULL DEFAULT 'id' CHECK (`preferred_language IN ('id', 'en', 'zh')`).
- `terms_accepted_at` TIMESTAMPTZ NULL, `terms_version` TEXT NULL.
- `totp_secret_encrypted` TEXT NULL: Rahasia TOTP terenkripsi AES-256-GCM untuk admin (migrasi 0023 untuk H5b).
- `totp_enabled_at` TIMESTAMPTZ NULL: Waktu aktivasi 2FA TOTP admin (migrasi 0023).
- `totp_last_step` BIGINT NULL: Timestamp step TOTP terakhir untuk proteksi anti-replay (migrasi 0023).
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.2 `web_sessions`
Penyimpanan sesi login web berbasis token.
- `id` BIGSERIAL PRIMARY KEY.
- `token` VARCHAR(255) UNIQUE NOT NULL.
- `user_id` BIGINT NOT NULL REFERENCES `users(id)` ON DELETE CASCADE.
- `stepup_at` TIMESTAMPTZ NULL: Waktu verifikasi step-up TOTP terakhir untuk otorisasi aksi sensitif admin (migrasi 0023 untuk H5b).
- `expires_at` TIMESTAMPTZ NOT NULL. Indeks `idx_web_sessions_expires_at` (migrasi 0022).
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.3 `admin_recovery_codes` (Tabel Baru - Migrasi 0023)
Menyimpan kode pemulihan darurat satu kali pakai untuk admin ber-TOTP.
- `id` BIGSERIAL PRIMARY KEY.
- `user_id` BIGINT NOT NULL REFERENCES `users(id)` ON DELETE CASCADE. Indeks `idx_admin_recovery_codes_user_id`.
- `code_hash` VARCHAR(64) NOT NULL: Hash SHA-256 dari kode pemulihan.
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.
- `used_at` TIMESTAMPTZ NULL: Diisi waktu penggunaan saat kode dipakai; jika NULL berarti belum dipakai.

### 2.4 `settings`
Tabel kunci-nilai konfigurasi global sistem.
- `key` VARCHAR(100) PRIMARY KEY. Termasuk `default_country` (JSON string, default `"ID"`, migrasi 0025).
- `value` JSONB NOT NULL.
- `updated_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.5 `campaigns`
Kampanye pesan siaran WhatsApp yang dibuat oleh admin.
- `id` BIGSERIAL PRIMARY KEY.
- `name` VARCHAR(255) NOT NULL.
- `status` VARCHAR(50) NOT NULL DEFAULT 'draf' CHECK (`status IN ('draf', 'berjalan', 'selesai')`).
- `button_mode` VARCHAR(50) NOT NULL DEFAULT 'kartu_link' CHECK (`button_mode IN ('tombol_asli', 'kartu_link')`).
- `body` TEXT NOT NULL.
- `elements` JSONB NOT NULL DEFAULT '{}'.
- `default_country` VARCHAR(4) NOT NULL DEFAULT 'ID': Kode negara default (ISO 3166-1 alpha-2) untuk menafsirkan nomor lokal (migrasi 0025 untuk H14).
- `archived_at` TIMESTAMPTZ NULL: Waktu pengarsipan kampanye.
- `purged_at` TIMESTAMPTZ NULL: Waktu pembersihan nomor kampanye (migrasi 0023 untuk H8).
- `purge_summary` JSONB NULL: Ringkasan statistik nomor saat kampanye dibersihkan (migrasi 0023 untuk H8).
- `created_by` BIGINT NOT NULL REFERENCES `users(id)`.
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.6 `number_batches`
Riwayat file nomor CSV/TXT yang diunggah ke suatu kampanye.
- `id` BIGSERIAL PRIMARY KEY.
- `campaign_id` BIGINT NOT NULL REFERENCES `campaigns(id)` ON DELETE RESTRICT. Indeks `idx_number_batches_campaign_id` (migrasi 0022).
- `filename` VARCHAR(255) NOT NULL.
- `uploaded_by` BIGINT NOT NULL REFERENCES `users(id)`.
- `uploaded_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.
- `total` INT NOT NULL DEFAULT 0, `accepted` INT NOT NULL DEFAULT 0, `rejected` INT NOT NULL DEFAULT 0.
- `dup_in_file` INT NOT NULL DEFAULT 0, `dup_in_campaign` INT NOT NULL DEFAULT 0.
- `invalid` INT NOT NULL DEFAULT 0, `ambiguous` INT NOT NULL DEFAULT 0.

### 2.7 `campaign_numbers`
Daftar nomor penerima pesan yang diklaim dan dikirim oleh worker.
- Parameter tabel (migrasi 0019): `fillfactor = 85`, `autovacuum_vacuum_scale_factor = 0.05`, `autovacuum_analyze_scale_factor = 0.02`.
- `id` BIGSERIAL PRIMARY KEY.
- `campaign_id` BIGINT NOT NULL REFERENCES `campaigns(id)` ON DELETE RESTRICT.
- `batch_id` BIGINT NOT NULL REFERENCES `number_batches(id)` ON DELETE RESTRICT.
- `phone_e164` VARCHAR(50) NOT NULL.
- `country_code` VARCHAR(10) NOT NULL.
- `recipient_name` VARCHAR(255) NULL.
- `custom_data` JSONB NULL.
- `status` VARCHAR(50) NOT NULL DEFAULT 'tersedia' CHECK (`status IN ('tersedia', 'dikirim', 'terkirim', 'tidak_terdaftar', 'gagal')`).
- `attempts` INT NOT NULL DEFAULT 0 CHECK (`attempts >= 0`).
- `claimed_account_id` BIGINT NULL REFERENCES `wa_accounts(id)`.
- `claimed_at` TIMESTAMPTZ NULL.
- `sent_at` TIMESTAMPTZ NULL.
- `last_error` TEXT NULL.
- Constraint integritas status klaim (migrasi 0019):
  - `check_campaign_numbers_dikirim_has_claim`: `status <> 'dikirim' OR (claimed_account_id IS NOT NULL AND claimed_at IS NOT NULL)`.
  - `check_campaign_numbers_tersedia_unclaimed`: `status <> 'tersedia' OR claimed_account_id IS NULL`.
- Unique constraint: `unique_campaign_phone` UNIQUE (`campaign_id`, `phone_e164`).

### 2.8 `wa_accounts`
Sesi akun WhatsApp worker untuk pengiriman pesan blasting.
- `id` BIGSERIAL PRIMARY KEY.
- `worker_id` BIGINT NOT NULL REFERENCES `users(id)`.
- `label` VARCHAR(100) NOT NULL.
- `phone_number` VARCHAR(50) NULL (kolom historis, lihat audit rilis).
- `wa_jid_hash` VARCHAR(64) NULL: Hash SHA-256 JID WhatsApp aktif (renamed dari `jid_hash`, migrasi 0021). Indeks unik parsial: `idx_wa_accounts_wa_jid_hash_active` (`WHERE status <> 'berhenti' AND wa_jid_hash IS NOT NULL`).
- `status` VARCHAR(50) NOT NULL DEFAULT 'menunggu_scan' CHECK (`status IN ('menunggu_scan', 'terhubung', 'berhenti', 'terputus', 'antre')`).
- `speed` VARCHAR(20) NOT NULL DEFAULT 'medium' CHECK (`speed IN ('fast', 'medium', 'slow', 'over_slow')`).
- `stop_reason` TEXT NULL.
- `consecutive_failures` INT NOT NULL DEFAULT 0.
- `whatsmeow_device_id` BIGINT NULL.
- `connected_at` TIMESTAMPTZ NULL, `stopped_at` TIMESTAMPTZ NULL.
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.9 `ledger_entries`
Buku besar keuangan pekerja (append-only via trigger `trg_prevent_ledger_update_delete`).
- `id` BIGSERIAL PRIMARY KEY.
- `user_id` BIGINT NOT NULL REFERENCES `users(id)`.
- `kind` VARCHAR(50) NOT NULL CHECK (`kind IN ('upah', 'referral', 'penarikan', 'koreksi', 'konversi')`).
- `currency` VARCHAR(10) NOT NULL CHECK (`currency IN ('IDR', 'USDT')`).
- `amount` BIGINT NOT NULL: Nilai mutasi (positif = kredit, negatif = debit).
- `rate_snapshot` BIGINT NULL: Salinan tarif saat transaksi terjadi.
- `campaign_number_id` BIGINT NULL REFERENCES `campaign_numbers(id)` ON DELETE RESTRICT. Indeks FK `idx_ledger_entries_campaign_number_id` (migrasi 0024).
- `source_user_id` BIGINT NULL REFERENCES `users(id)`.
- `reverses_entry_id` BIGINT NULL REFERENCES `ledger_entries(id)`.
- `conversion_id` BIGINT NULL REFERENCES `currency_conversions(id)` ON DELETE RESTRICT.
- `note` TEXT NULL.
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.
- Constraint integritas referral (migrasi 0022):
  - `check_ledger_entries_referral_source`: `kind <> 'referral' OR (source_user_id IS NOT NULL AND campaign_number_id IS NOT NULL)`.
  - `idx_ledger_entries_referral_unique`: UNIQUE (`campaign_number_id`) WHERE `kind = 'referral'`.
  - `idx_ledger_entries_upah_unique`: UNIQUE (`campaign_number_id`) WHERE `kind = 'upah'`.

### 2.10 `wallet_entries`
Catatan saldo isi ulang kas master/dompet admin.
- `id` BIGSERIAL PRIMARY KEY.
- `currency` VARCHAR(10) NOT NULL CHECK (`currency IN ('IDR', 'USDT')`).
- `amount` BIGINT NOT NULL.
- `note` TEXT NULL.
- `created_by` BIGINT NOT NULL REFERENCES `users(id)`.
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.11 `withdrawals`
Catatan permintaan penarikan saldo oleh worker.
- `id` BIGSERIAL PRIMARY KEY.
- `user_id` BIGINT NOT NULL REFERENCES `users(id)`.
- `currency` VARCHAR(10) NOT NULL CHECK (`currency IN ('IDR', 'USDT')`).
- `amount` BIGINT NOT NULL.
- `method` VARCHAR(50) NOT NULL CHECK (`method IN ('dana', 'seabank', 'bank', 'usdt_bep20')`).
- `destination_encrypted` TEXT NOT NULL: Tujuan transfer terenkripsi AES-256-GCM.
- `status` VARCHAR(50) NOT NULL DEFAULT 'menunggu' CHECK (`status IN ('menunggu', 'selesai', 'ditolak')`).
- `request_token` TEXT NOT NULL: Token idempotensi permintaan penarikan. Indeks unik `idx_withdrawals_user_request_token` (`UNIQUE (user_id, request_token)`).
- `payout_reference` TEXT NULL: Nomor referensi bank / hash transaksi blockchain.
- `reject_reason` TEXT NULL: Alasan penolakan penarikan oleh admin.
- `proof_path` TEXT NULL (kolom historis).
- `requested_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.
- `processed_at` TIMESTAMPTZ NULL, `processed_by` BIGINT NULL REFERENCES `users(id)`.

### 2.12 `currency_conversions`
Pencatatan penukaran saldo worker dari IDR ke USDT secara idempoten.
- `id` BIGSERIAL PRIMARY KEY.
- `user_id` BIGINT NOT NULL REFERENCES `users(id)` ON DELETE RESTRICT.
- `idr_amount` BIGINT NOT NULL CHECK (`idr_amount > 0`).
- `usdt_micro` BIGINT NOT NULL CHECK (`usdt_micro > 0`).
- `kurs` BIGINT NOT NULL CHECK (`kurs > 0`).
- `request_token` VARCHAR(255) NOT NULL. Indeks unik `uq_currency_conversions_user_token` (`UNIQUE (user_id, request_token)`).
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.13 `audit_logs`
Pencatatan riwayat audit keamanan dan administrasi.
- `id` BIGSERIAL PRIMARY KEY.
- `actor_id` BIGINT NULL REFERENCES `users(id)`.
- `action` VARCHAR(255) NOT NULL.
- `entity` VARCHAR(255) NOT NULL.
- `entity_id` VARCHAR(255) NOT NULL.
- `meta` JSONB NULL.
- `ip` VARCHAR(45) NULL.
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.14 `support_tickets`
Pusat bantuan tiket pesan dari worker ke admin.
- `id` BIGSERIAL PRIMARY KEY.
- `user_id` BIGINT NOT NULL REFERENCES `users(id)` ON DELETE CASCADE.
- `category` VARCHAR(50) NOT NULL.
- `message` TEXT NOT NULL.
- `status` VARCHAR(20) NOT NULL DEFAULT 'open' CHECK (`status IN ('open', 'resolved')`).
- `admin_notes` TEXT NULL.
- `reply_message` TEXT NULL.
- `replied_by` BIGINT NULL REFERENCES `users(id)` ON DELETE SET NULL.
- `replied_at` TIMESTAMPTZ NULL, `resolved_at` TIMESTAMPTZ NULL.
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.15 `broadcasts`
Pesan siaran pengumuman admin untuk seluruh worker.
- `id` BIGSERIAL PRIMARY KEY.
- `title` VARCHAR(150) NOT NULL.
- `message` TEXT NOT NULL.
- `badge` VARCHAR(30) NOT NULL DEFAULT 'info'.
- `is_active` BOOLEAN NOT NULL DEFAULT true.
- `created_by` BIGINT NULL REFERENCES `users(id)` ON DELETE SET NULL.
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.16 `notifications`
Pemberitahuan personal di panel worker.
- `id` BIGSERIAL PRIMARY KEY.
- `user_id` BIGINT NOT NULL REFERENCES `users(id)` ON DELETE CASCADE.
- `title` VARCHAR(150) NOT NULL.
- `message` TEXT NOT NULL.
- `kind` VARCHAR(30) NOT NULL.
- `ref_id` BIGINT NULL.
- `read_at` TIMESTAMPTZ NULL.
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.

### 2.17 `backups` (Tabel Baru - Migrasi 0023)
Metadata dan pencatatan verifikasi backup database berkala.
- `id` BIGSERIAL PRIMARY KEY.
- `kind` VARCHAR(20) NOT NULL CHECK (`kind IN ('otomatis', 'manual', 'sebelum_restore')`).
- `filename` VARCHAR(255) UNIQUE NOT NULL.
- `created_at` TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP.
- `created_by` BIGINT NULL REFERENCES `users(id)` ON DELETE SET NULL.
- `size_bytes` BIGINT NOT NULL CHECK (`size_bytes >= 0`).
- `sha256` VARCHAR(64) NOT NULL.
- `schema_version` INT NOT NULL.
- `row_counts` JSONB NULL.
- `verified_at` TIMESTAMPTZ NULL.
- `verify_ok` BOOLEAN NULL.
- `note` TEXT NULL.

---

## 3. Matriks Perilaku ON DELETE Foreign Key

Berikut adalah katalog lengkap 25 Foreign Key di GO BLAST beserta perilaku `ON DELETE` dan alasan desainnya:

| No | Tabel Anak (`child`) | Kolom FK | Tabel Induk (`parent`) | Kolom Induk | Aksi `ON DELETE` | Alasan Desain / Invarian Bisnis |
|---|---|---|---|---|---|---|
| 1 | `users` | `referred_by` | `users` | `id` | **NO ACTION** | Mencegah penghapusan pengguna jika ada pengguna lain yang mencantumkan akun tersebut sebagai referrer aktif. |
| 2 | `web_sessions` | `user_id` | `users` | `id` | **CASCADE** | Sesi login web adalah data ephemeral; jika pengguna dihapus, seluruh token login harus langsung musnah. |
| 3 | `admin_recovery_codes` | `user_id` | `users` | `id` | **CASCADE** | Kode darurat pemulihan melekat pada akun admin; jika admin dihapus, seluruh kode pemulihan ikut terhapus demi keamanan. |
| 4 | `campaigns` | `created_by` | `users` | `id` | **NO ACTION** | Menjamin integritas pembuat kampanye siaran; admin tidak dapat dihapus jika masih menjadi pemilik kampanye aktif. |
| 5 | `number_batches` | `campaign_id` | `campaigns` | `id` | **NO ACTION / RESTRICT** | Mencegah penghapusan kampanye jika masih memiliki riwayat batch file unggahan nomor. |
| 6 | `number_batches` | `uploaded_by` | `users` | `id` | **NO ACTION** | Menjamin riwayat penanggung jawab unggahan batch nomor tidak hilang. |
| 7 | `campaign_numbers` | `campaign_id` | `campaigns` | `id` | **NO ACTION / RESTRICT** | Mencegah penghapusan kampanye selama data nomor target masih ada di database. |
| 8 | `campaign_numbers` | `batch_id` | `number_batches` | `id` | **NO ACTION / RESTRICT** | Nomor kampanye tidak boleh kehilangan relasi ke batch asal unggahannya. |
| 9 | `campaign_numbers` | `claimed_account_id` | `wa_accounts` | `id` | **NO ACTION / RESTRICT** | Mencegah penghapusan akun WhatsApp jika nomor sedang berstatus klaim aktif oleh akun tersebut. |
| 10 | `wa_accounts` | `worker_id` | `users` | `id` | **NO ACTION** | Menjaga relasi kepemilikan akun WhatsApp pekerja; pekerja tidak boleh dihapus sembarangan jika memiliki sesi WA. |
| 11 | `ledger_entries` | `user_id` | `users` | `id` | **NO ACTION** | Integritas keuangan mutlak: buku besar finansial tidak boleh kehilangan pemilik saldo. |
| 12 | `ledger_entries` | `campaign_number_id` | `campaign_numbers` | `id` | **RESTRICT** | (Migrasi 0014) Nomor target yang upah pengirimannya telah dicatat di buku besar dilarang keras dihapus dari database. |
| 13 | `ledger_entries` | `source_user_id` | `users` | `id` | **NO ACTION** | Menjaga rekam jejak worker bawahan yang menghasilkan bonus referral bagi pengundangnya. |
| 14 | `ledger_entries` | `reverses_entry_id` | `ledger_entries` | `id` | **NO ACTION** | Menjaga tautan audit antar entri buku besar yang dikoreksi. |
| 15 | `ledger_entries` | `conversion_id` | `currency_conversions` | `id` | **RESTRICT** | (Migrasi 0016) Mencegah penghapusan baris konversi mata uang yang mendasari mutasi kredit/debit buku besar. |
| 16 | `wallet_entries` | `created_by` | `users` | `id` | **NO ACTION** | Menjaga jejak administrator yang mengisi saldo kas/dompet operasional. |
| 17 | `withdrawals` | `user_id` | `users` | `id` | **NO ACTION** | Permintaan penarikan dana pekerja tidak boleh kehilangan relasi ke worker pemohon. |
| 18 | `withdrawals` | `processed_by` | `users` | `id` | **NO ACTION** | Menjaga jejak administrator yang memproses atau menolak permintaan penarikan. |
| 19 | `currency_conversions` | `user_id` | `users` | `id` | **RESTRICT** | (Migrasi 0016) Menjamin transaksi penukaran saldo IDR ke USDT tidak dapat diputus dari pengguna asalnya. |
| 20 | `audit_logs` | `actor_id` | `users` | `id` | **NO ACTION** | Menjamin log audit aktivitas penting tidak kehilangan relasi aktor pelaksana. |
| 21 | `support_tickets` | `user_id` | `users` | `id` | **CASCADE** | (Migrasi 0012) Jika akun worker dihapus, tiket bantuan miliknya ikut dihapus. |
| 22 | `support_tickets` | `replied_by` | `users` | `id` | **SET NULL** | (Migrasi 0015) Jika admin yang menjawab tiket dihapus, balasan tiket tetap utuh dan `replied_by` diubah menjadi NULL. |
| 23 | `broadcasts` | `created_by` | `users` | `id` | **SET NULL** | (Migrasi 0012) Pengumuman sistem tetap dipertahankan meskipun admin pembuatnya dihapus dari sistem. |
| 24 | `notifications` | `user_id` | `users` | `id` | **CASCADE** | (Migrasi 0015) Notifikasi personal di lonceng worker otomatis dihapus jika akun worker dihapus. |
| 25 | `backups` | `created_by` | `users` | `id` | **SET NULL** | (Migrasi 0023) Rekaman log dan riwayat verifikasi cadangan database tetap tersimpan meskipun admin pembuatnya dihapus. |

---

## 4. Constraint Integritas dan Bisnis

1. `check_campaign_numbers_attempts_non_negative`: `attempts >= 0` (migrasi 0019).
2. `check_campaign_numbers_dikirim_has_claim`: `status <> 'dikirim' OR (claimed_account_id IS NOT NULL AND claimed_at IS NOT NULL)` (migrasi 0019).
3. `check_campaign_numbers_tersedia_unclaimed`: `status <> 'tersedia' OR claimed_account_id IS NULL` (migrasi 0019).
4. `check_ledger_entries_referral_source`: `kind <> 'referral' OR (source_user_id IS NOT NULL AND campaign_number_id IS NOT NULL)` (migrasi 0022).
5. `backups_kind_check`: `kind IN ('otomatis', 'manual', 'sebelum_restore')` (migrasi 0023).
6. `check_referred_by_not_self`: `referred_by <> id` (migrasi 0001).
7. `unique_campaign_phone`: UNIQUE (`campaign_id`, `phone_e164`) (migrasi 0002).
8. `uq_currency_conversions_user_token`: UNIQUE (`user_id`, `request_token`) (migrasi 0016).
9. `trg_prevent_ledger_update_delete`: Menolak keras operasi UPDATE dan DELETE pada tabel `ledger_entries` (migrasi 0014).
10. `trg_check_withdrawal_balance`: Mengunci baris `users` (FOR UPDATE) pada INSERT `ledger_entries` bertanda negatif (`penarikan` dan `konversi`) untuk memastikan saldo pekerja tidak pernah minus (migrasi 0014/0016).

---

## 5. Indeks Database dan Query yang Dilayani

Berikut daftar seluruh indeks aktif, alasan teknis pembuatan, dan query spesifik yang dilayani:

| Nama Indeks | Definisi / Kolom | Alasan & Query yang Dilayani |
|---|---|---|
| `idx_campaign_numbers_claim` | `(campaign_id, id) WHERE status = 'tersedia'` | **Query Klaim Worker:** Mempercepat klaim nomor siap kirim per kampanye menggunakan pola `SELECT id FROM campaign_numbers WHERE campaign_id = $1 AND status = 'tersedia' LIMIT 1 FOR UPDATE SKIP LOCKED`. |
| `idx_campaign_numbers_claimed_at_stale` *(0019)* | `(claimed_at) WHERE status = 'dikirim'` | **Query Pembersih Stale:** Mempercepat engine / background cleaner mendeteksi dan mengembalikan nomor menggantung via `UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL WHERE status = 'dikirim' AND claimed_at < NOW() - interval 'X seconds'`. |
| `idx_campaign_numbers_claimed_account_release` *(0019)* | `(claimed_account_id) WHERE status = 'dikirim'` | **Query Pelepasan Akun:** Mempercepat pelepasan nomor saat worker terputus/berhenti via `UPDATE campaign_numbers SET status = 'tersedia', claimed_account_id = NULL, claimed_at = NULL WHERE status = 'dikirim' AND claimed_account_id = $1`. |
| `idx_campaign_numbers_campaign_status` *(0019)* | `(campaign_id, status)` | **Query Agregasi Kampanye:** Mempercepat penghitungan metrik kampanye di panel admin: `SELECT COUNT(*) FROM campaign_numbers WHERE campaign_id = $1 AND status = 'terkirim'/'dikirim'/'tersedia'`. |
| `idx_campaign_numbers_sent_at` *(0011)* | `(sent_at)` | **Query Tren Laporan Admin:** Mempercepat grafik tren pengiriman 14 hari terakhir di dashboard admin (`sent_at >= $1`). |
| `unique_campaign_phone` *(0002)* | `UNIQUE (campaign_id, phone_e164)` | **Integritas Nomor Kampanye:** Menjamin nomor tujuan tidak terduplikasi dalam satu kampanye yang sama. |
| `idx_users_email_lower` *(0020)* | `UNIQUE (LOWER(email)) WHERE email IS NOT NULL` | **Query Autentikasi Email:** Mencegah duplikasi email beda kapitalisasi dan mempercepat lookup login admin/worker via email. |
| `idx_users_username_lower` *(0008)* | `UNIQUE (LOWER(username))` | **Query Autentikasi Username:** Menjamin keunikan case-insensitive nama pengguna worker saat register dan login. |
| `idx_users_referred_by` *(0022)* | `(referred_by) WHERE referred_by IS NOT NULL` | **Query Daftar Referral:** Mempercepat lookup daftar bawahan referral di panel worker dan kalkulasi komisi. |
| `idx_users_payout_dest_hash` *(0017)* | `(payout_dest_hash)` | **Query Deteksi Multi-Akun:** Mempercepat pencarian pekerja yang menggunakan rekening bank / wallet yang sama. |
| `users_email_key` *(0001)* | `UNIQUE (email)` | **Integritas Email Pengguna:** Penegakan keunikan alamat email pada level katalog database. |
| `users_referral_code_key` *(0001)* | `UNIQUE (referral_code)` | **Integritas Kode Referral:** Menjamin setiap pengguna memiliki kode referral unik global. |
| `idx_wa_accounts_wa_jid_hash_active` *(0021)* | `UNIQUE (wa_jid_hash) WHERE status <> 'berhenti' AND wa_jid_hash IS NOT NULL` | **Query Unik Sesi WA Aktif:** Mencegah satu nomor WhatsApp dihubungkan oleh lebih dari satu sesi pekerja aktif sekaligus. |
| `idx_wa_accounts_worker_status` *(0006)* | `(worker_id, status)` | **Query Kuota Akun Worker:** Mempercepat validasi batas maksimal akun terhubung per pekerja (`worker_id = $1 AND status = 'terhubung'`). |
| `idx_wa_accounts_status` *(0006)* | `(status)` | **Query Status Sistem:** Mempercepat pemantauan seluruh akun aktif di engine. |
| `idx_ledger_entries_upah_unique` | `UNIQUE (campaign_number_id) WHERE kind = 'upah'` | **Idempotensi Upah:** Menjamin satu nomor target pesan hanya dapat menghasilkan upah satu kali saja. |
| `idx_ledger_entries_referral_unique` *(0022)* | `UNIQUE (campaign_number_id) WHERE kind = 'referral'` | **Idempotensi Komisi Referral:** Menjamin satu nomor target pesan hanya menghasilkan bonus referral satu kali. |
| `idx_ledger_entries_user_curr_time` | `(user_id, currency, created_at)` | **Query Saldo & Mutasi:** Mempercepat kalkulasi saldo pekerja (`SUM(amount)`) dan pagination riwayat transaksi mutasi. |
| `idx_ledger_entries_campaign_number_id` *(0024)* | `(campaign_number_id)` | **Optimasi FK Purge:** Mempercepat penegakan foreign key ON DELETE RESTRICT saat penghapusan nomor kampanye massal. |
| `idx_number_batches_campaign_id` *(0022)* | `(campaign_id)` | **Query Batch Kampanye:** Mempercepat lookup daftar batch yang diunggah ke suatu kampanye di detail kampanye admin. |
| `idx_web_sessions_user_id` | `(user_id)` | **Query Sesi Pengguna:** Mempercepat pencabutan semua sesi milik satu pengguna saat logout atau reset kata sandi. |
| `idx_web_sessions_expires_at` *(0022)* | `(expires_at)` | **Query Pembersih Sesi Kadaluwarsa:** Mempercepat garbage collection sesi web yang sudah melewati masa berlaku. |
| `web_sessions_token_hash_key` *(0001)* | `UNIQUE (token_hash)` | **Integritas Token Sesi:** Menjamin keunikan hash token sesi web login. |
| `idx_audit_logs_action_created_at` *(0022)* | `(action, created_at)` | **Query Retensi & Filter Audit:** Mempercepat filter log audit berdasarkan aksi dan rotasi/retensi berkala. |
| `idx_audit_logs_created_at` *(0022)* | `(created_at)` | **Query Retensi Audit:** Mempercepat pembersihan log audit lama di atas 30 hari. |
| `idx_notifications_created_at` *(0022)* | `(created_at)` | **Query Retensi Notifikasi:** Mempercepat pembersihan notifikasi usang di atas 90 hari. |
| `idx_notifications_user_unread` *(0015)* | `(user_id, created_at DESC) WHERE read_at IS NULL` | **Query Lonceng Dashboard:** Mempercepat indikator notifikasi belum dibaca di header worker. |
| `idx_notifications_user_id` *(0015)* | `(user_id)` | **Query Daftar Notifikasi:** Mempercepat pemuatan seluruh riwayat notifikasi worker. |
| `idx_withdrawals_user_request_token` *(0017)* | `UNIQUE (user_id, request_token)` | **Idempotensi Penarikan:** Mencegah pengajuan penarikan ganda dengan request token yang sama. |
| `uq_currency_conversions_user_token` *(0016)* | `UNIQUE (user_id, request_token)` | **Idempotensi Konversi:** Mencegah penukaran IDR ke USDT berulang akibat jaringan terputus / klik ganda. |
| `idx_admin_recovery_codes_user_id` *(0023)* | `(user_id)` | **Lookup Kode Darurat TOTP:** Mempercepat verifikasi kode pemulihan admin yang bersangkutan. |
| `backups_filename_key` *(0023)* | `UNIQUE (filename)` | **Keunikan Berkas Backup:** Menjamin berkas dump backup memiliki identitas unik di database. |
| `idx_backups_created_at` *(0023)* | `(created_at DESC)` | **Riwayat Backup Admin:** Mempercepat pengurutan daftar cadangan terbaru di panel admin `/admin/backups`. |
| `idx_support_tickets_status` *(0012)* | `(status)` | **Filter Tiket CS:** Mempercepat pemfilteran tiket berdasarkan status terbuka atau terselesaikan. |
| `idx_support_tickets_user_id` *(0012)* | `(user_id)` | **Riwayat Tiket Worker:** Mempercepat pemuatan tiket laporan milik pekerja tertentu. |
| `idx_wallet_entries_currency` *(0011)* | `(currency, created_at)` | **Laporan Dompet Admin:** Mempercepat agregasi saldo modal kas per mata uang di panel admin. |


---

## 6. Audit Kolom Tak Terpakai & Usulan Rilis Berikutnya

Sesuai hasil audit Tahap H2 terhadap seluruh kode Go (`internal/`, `cmd/`, `web/`) dan file template HTML, ditemukan elemen skema berikut yang saat ini belum atau tidak lagi diakses:

1. **`wa_accounts.phone_number`**:
   - *Status:* Tidak pernah dibaca maupun ditulis dalam kode Go.
   - *Penyebab:* Identitas sesi WhatsApp sepenuhnya dikelola oleh `whatsmeow` menggunakan JID dan ID perangkat (`whatsmeow_device_id`), dengan verifikasi hash `wa_jid_hash`.
   - *Usulan:* Pertahankan jika di masa depan admin ingin melihat nomor telepon raw worker, atau jadwalkan penghapusan kolom ini pada migrasi pasca-stabil jika tidak dibutuhkan.

2. **`ledger_entries.reverses_entry_id`**:
   - *Status:* Dibuat pada migrasi 0003, tidak pernah dibaca/ditulis oleh kode aplikasi.
   - *Penyebab:* Koreksi finansial saat ini menggunakan entri berjenis `'koreksi'` dengan penjelasan rinci pada kolom `note`, bukan relasi FK kaku.
   - *Usulan:* Dapat dibiarkan sebagai cadangan audit masa depan atau diarsipkan.

3. **`withdrawals.proof_path`**:
   - *Status:* Dibuat pada migrasi 0004 untuk menyimpan path gambar bukti transfer penarikan.
   - *Penyebab:* Pengesahan penarikan saat ini mencatat kode referensi/TXID di `payout_reference` dan catatan di `admin_notes`, tanpa modul upload berkas bukti transfer.
   - *Usulan:* Dapat diaktifkan fiturnya di rilis berikutnya atau dihapus.

4. **`wa_accounts.connected_at` & `wa_accounts.stopped_at`**:
   - *Status:* Ditulis saat event koneksi/diskoneksi oleh `internal/wa/manager.go`, tetapi belum ditampilkan di template HTML atau di-query untuk pelaporan.
   - *Usulan:* Pertahankan kolom ini untuk keperluan pelaporan analitik durasi online akun WhatsApp.

5. **Kolom Persiapan Tahap Berikutnya (Sengaja Disiapkan di H2)**:
   - `campaigns.purged_at` & `campaigns.purge_summary`: Diimplementasikan pada **Tahap H8**.
   - `users.totp_secret_encrypted`, `users.totp_enabled_at`, `users.totp_last_step`, `web_sessions.stepup_at`, dan tabel `admin_recovery_codes`: Diimplementasikan pada **Tahap H5b**.
   - Tabel `backups`: Untuk sistem backup dan verifikasi otomatis (**Tahap H9**).

---

## 7. Siklus Hidup Data, Retensi, dan Pembersihan (Tahap H8)

Sesuai arsitektur dan keputusan pemilik (Opsi B: Pembersihan manual tanpa penghapusan otomatis data customer; nomor terkirim dianonimkan demi privasi), siklus hidup data kampanye dan retensi server diatur sebagai berikut:

### 7.1 Status Siklus Kampanye
1. **Aktif / Berjalan (`berjalan`)**: Kampanye aktif mendistribusikan target pesan kepada akun WhatsApp pekerja.
2. **Selesai (`selesai`)**: Seluruh nomor selesai diproses (`finished_at IS NOT NULL`).
3. **Diarsip (`archived_at IS NOT NULL`)**: Kampanye disembunyikan dari daftar utama admin dan dialihkan menjadi status hanya-baca (*read-only*). **Pengarsipan tidak menghapus data apa pun**; semua nomor, riwayat pengiriman, dan berkas CSV tetap utuh hingga admin menjalankan pembersihan manual.
4. **Dibersihkan (`purged_at IS NOT NULL`)**: Admin secara manual mengeksekusi aksi *Purge* setelah masa retensi atau setelah kampanye selesai/diarsip.

### 7.2 Prosedur Pembersihan Kampanye (*Purge*, Opsi B)
Aksi pembersihan kampanye dijalankan melalui `POST /admin/campaigns/{id}/purge` dengan perlindungan Step-Up TOTP dan validasi nama kampanye:
- **Pencatatan Snapshot Ringkasan (`campaigns.purge_summary`)**:
  Sebelum mutasi data, sistem menghitung dan merekam snapshot statistik ke dalam kolom JSONB:
  `{"total": N, "terkirim": N, "gagal": N, "tidak_terdaftar": N, "dihapus_tersedia": N}`.
- **Penghapusan Nomor Tersedia**:
  Hanya nomor dengan status `'tersedia'` (belum pernah diambil/diproses) yang dihapus secara fisik dari database.
- **Anonimisasi Data Customer**:
  Nomor dengan status `'terkirim'`, `'gagal'`, dan `'tidak_terdaftar'` **tidak dihapus** melainkan disamarkan:
  - `phone_e164` diubah menjadi `'x' || id` (pseudonim deterministik unik).
  - `country_code` diubah menjadi `'XX'`.
  - `recipient_name` dikosongkan (`NULL`).
  - `custom_data` dikosongkan (`NULL`).
- **Sanitasi Pesan Kesalahan (`last_error`)**:
  - Pada nomor `'terkirim'` dan `'tidak_terdaftar'`, `last_error` dikosongkan (`NULL`).
  - Pada nomor `'gagal'`, `last_error` disanitasi: jika memuat nilai whitelist (`hasil_tidak_pasti`, `terkirim_tidak_tercatat`, `galat_internal`), nilai dipertahankan; selain itu disederhanakan menjadi `'lainnya'` untuk menghindari kebocoran nomor atau data sensitif di pesan error WhatsApp.
- **Integritas Finansial & Audit Ledger**:
  Constraint `FOREIGN KEY (campaign_number_id) REFERENCES campaign_numbers(id) ON DELETE RESTRICT` pada tabel `ledger_entries` tetap terjaga utuh. Catatan upah pekerja, komisi referral, dan saldo dompet tidak mengalami perubahan maupun fragmentasi.
- **Pembersihan Berkas Batch**:
  Kolom `number_batches.filename` dikosongkan (`''`), dan berkas gambar template kampanye di folder `uploads/` dapat dihapus jika dipilih admin dan tidak digunakan kampanye aktif lain.
- **Waktu Purge**:
  Kolom `campaigns.purged_at` diisi `CURRENT_TIMESTAMP`.

### 7.3 Retensi Server & Pembersihan Rutin (/admin/storage)
Halaman admin `/admin/storage` menyediakan metrik visual ukuran database, tabel utama, dan folder server serta 4 aksi pembersihan mandiri (dengan pembatasan laju 5 kali/jam per admin dan proteksi Step-Up TOTP):
1. **Berkas Unggahan Sementara (`data/tmp/`)**:
   Membersihkan berkas pecahan CSV dan metadata upload yang kedaluwarsa lebih dari 1 jam (`TmpFilesMaxAge = 1 hour`).
2. **File Gambar Yatim (`uploads/`)**:
   Memeriksa disk terhadap database kampanye, lalu menghapus gambar yang tidak lagi dirujuk oleh kampanye mana pun.
3. **Retensi Data Sistem (Database)**:
   - Menghapus sesi login kedaluwarsa di tabel `web_sessions` (`expires_at < NOW()`).
   - Menghapus riwayat audit percobaan login gagal di tabel `audit_logs` (`login_failed`, `login_rejected`, `totp_login_failed`, `admin_stepup_failed`) yang berumur lebih dari 30 hari.
   - Menghapus notifikasi pekerja yang telah dibaca di tabel `notifications` (`read_at IS NOT NULL`) yang berumur lebih dari 90 hari.
4. **Optimasi Penyimpanan Database (`VACUUM ANALYZE`)**:
   Menjalankan `VACUUM ANALYZE` secara mandiri pada koneksi khusus dengan batas waktu kunci aman (`SET lock_timeout = '5s'`) untuk 4 tabel dengan churn tinggi: `campaign_numbers`, `ledger_entries`, `audit_logs`, dan `web_sessions`.
5. **Pengingat Retensi Kampanye Terarsip**:
   Fitur pemeriksaan kesehatan di `/admin/maintenance` mendeteksi kampanye terarsip yang berumur lebih dari 90 hari dan belum dibersihkan, menampilkan peringatan agar admin mempertimbangkan pembersihan data demi efisiensi storage VPS.

---

## 8. Cadangan Database Aman dan Pemulihan (Tahap H9)

Sesuai Keputusan M dan N pemilik, arsitektur pencadangan dan pemulihan dirancang untuk menjamin kerahasiaan penuh, integritas data finansial, serta kemudahan audit tanpa menempatkan kunci dekripsi bersama berkas cadangan:

### 8.1 Format Berkas Cadangan (.gbak)
Berkas cadangan biner berekstensi `.gbak` menggunakan format chunk streaming AES-256-GCM + kompresi gzip:
1. **Header JSON Tidak Terenkripsi (64 KB Pertama)**:
   - Magic: `GOBLAST1` (8 byte).
   - Format metadata JSON: `version` (skema DB), `created_at`, `kind` (`otomatis`, `manual`, `sebelum_restore`), `key_id` (SHA-256 8 byte pertama dari `BACKUP_KEY`), `total_rows`, `table_meta` (nama tabel, baris, bytes), dan `has_uploads`.
   - Kegunaan: Admin dapat menginspeksi versi skema, umur berkas, dan ringkasan data tanpa perlu membuka kunci enkripsi.
2. **Stream Potongan Data Terenkripsi (Chunk Streaming)**:
   - Setiap potongan berukuran 64 KB dikompresi gzip lalu dienkripsi menggunakan AES-256-GCM.
   - Nonce 12-byte unik untuk setiap potongan: 4 byte prefiks acak + 8 byte *big-endian chunk counter* (mencegah *nonce reuse*).
   - *Additional Authenticated Data* (AAD) 41 byte: Hash SHA-256 dari header + indeks chunk + flag isLast (mencegah manipulasi urutan chunk atau pemotongan berkas di tengah stream).
3. **Trailer Integritas (Trailer EOF)**:
   - Magic: `GBAKEND1` (8 byte).
   - Hash SHA-256 dari seluruh payload terenkripsi + total potongan yang ditulis.

### 8.2 Eksklusi Tabel Sensitif
Demi keamanan dan keselarasan arsitektur sistem:
1. **`whatsmeow_*`**:
   - Seluruh tabel sesi dan kunci perangkat WhatsApp (`whatsmeow_device`, `whatsmeow_identity_keys`, `whatsmeow_pre_keys`, `whatsmeow_sessions`, `whatsmeow_sender_keys`, `whatsmeow_app_state_sync_keys`, `whatsmeow_app_state_version`) **dikecualikan dari cadangan**.
   - Alasan: Sesuai Keputusan B1 dan Dokumen Konsep Bagian 11, kunci sesi perangkat WhatsApp tidak boleh bertahan atau disalin ke lingkungan lain; sesi lama harus dibersihkan dan pekerja wajib melakukan scan ulang.
2. **`web_sessions`**:
   - Sesi login berbasis cookie diabaikan demi keamanan (memaksa login ulang pasca-pemulihan).
3. **`goose_db_version`**:
   - Dikelola langsung oleh alur migrasi Goose dan CLI restore.

### 8.3 Kunci Enkripsi dan Kebijakan Cadangan
- **`BACKUP_KEY`**: 32 byte base64, terpisah dari `ENCRYPTION_KEY`. Ditolak saat startup jika bernilai sama dengan `ENCRYPTION_KEY` untuk menegakkan pemisahan domain keamanan (*security domain separation*).
- **`BACKUP_KEY_PREV`**: Kunci cadangan generasi sebelumnya untuk mendukung rotasi kunci tanpa merusak kemampuan memverifikasi atau memulihkan cadangan lama.
- Salinan kunci wajib disimpan pemilik di luar server (misal di password manager terisolasi).

### 8.4 Penjadwalan & Aturan Retensi (GFS)
- **Otomatis Harian**: Berjalan setiap pukul 03:00 WIB via background goroutine terproteksi panic (`safe.Go`).
- **Pengecekan Ruang Disk**: Sebelum cadangan dimulai, sistem memastikan sisa ruang disk bebas minimal 2x ukuran cadangan terakhir atau 100 MB.
- **Retensi Kakek-Ayah-Anak (GFS)**:
  - Menyimpan 7 cadangan harian terbaru.
  - Menyimpan 4 cadangan mingguan (cadangan hari Minggu).
  - **Garansi Keras**: Cadangan terverifikasi terakhir **DILARANG DIHAPUS** dalam kondisi apa pun oleh scheduler maupun penghapusan manual di panel admin.

### 8.5 Pemulihan Database (Restore)
1. **Perintah CLI Atomik**:
   `goblast restore --file <path_ke_file.gbak> [--force-nonempty] [--i-stopped-the-app]`
2. **Tahapan Pemulihan**:
   - Seluruh proses berjalan dalam **1 transaksi PostgreSQL tunggal** (`tx`).
   - Trigger proteksi append-only (`trg_prevent_ledger_update_delete`) dan trigger saldo (`trg_check_withdrawal_balance`) dinonaktifkan sementara di dalam transaksi.
   - Tabel dikosongkan berurutan terbalik dari dependensi foreign key.
   - Data dialirkan via `COPY FROM STDIN` streaming langsung dari pembaca AES-256-GCM.
   - Seluruh akun WhatsApp di tabel `wa_accounts` diatur ulang statusnya menjadi `'terputus'`.
   - Seluruh *sequence* ID diatur ulang ke `COALESCE(MAX(id), 0) + 1`.
   - Trigger proteksi diaktifkan kembali.
   - **5 Pemeriksaan Invarian Ketat**:
     1. Tidak ada akun pengguna bersaldo negatif.
     2. Tidak ada duplikasi upah untuk `campaign_number_id` yang sama.
     3. Tidak ada duplikasi referral untuk `campaign_number_id` yang sama.
     4. Tidak ada baris yatim (*foreign key orphan rows*).
     5. Jumlah baris yang dipulihkan cocok dengan metadata header cadangan.
   - **Pengujian Kunci Enkripsi Data Rekening**:
     Sistem memverifikasi dekripsi sampel tujuan penarikan dengan `ENCRYPTION_KEY` aktif untuk memastikan kunci yang terpasang cocok dengan data yang dipulihkan.
3. **Pemulihan via Web**:
   - Mati secara bawaan (`ALLOW_WEB_RESTORE=false`).
   - Jika diaktifkan admin, wajib dalam mode pemeliharaan (`maintenance_mode = true`), melakukan drain engine (maks 10 detik), konfirmasi ketik kata `RESTORE`, dan membuat cadangan darurat otomatis `sebelum_restore` sebelum mutasi data.

