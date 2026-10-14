-- +goose Up
-- +goose StatementBegin

-- 1. Indeks parsial untuk pembersih nomor menggantung (stale claims cleaner)
CREATE INDEX IF NOT EXISTS idx_campaign_numbers_claimed_at_stale
    ON campaign_numbers (claimed_at)
    WHERE status = 'dikirim';

-- 2. Indeks parsial untuk pelepasan nomor per akun WhatsApp saat terputus/berhenti
CREATE INDEX IF NOT EXISTS idx_campaign_numbers_claimed_account_release
    ON campaign_numbers (claimed_account_id)
    WHERE status = 'dikirim';

-- 3. Indeks komposit untuk hitungan status per campaign (dashboard dan GetCampaignStats)
CREATE INDEX IF NOT EXISTS idx_campaign_numbers_campaign_status
    ON campaign_numbers (campaign_id, status);

-- 4. CHECK constraints: ditambahkan dengan NOT VALID lalu di-VALIDATE
-- Integritas attempts: tidak boleh negatif
ALTER TABLE campaign_numbers
    ADD CONSTRAINT check_campaign_numbers_attempts_non_negative
    CHECK (attempts >= 0) NOT VALID;
ALTER TABLE campaign_numbers
    VALIDATE CONSTRAINT check_campaign_numbers_attempts_non_negative;

-- Integritas status 'dikirim': wajib memiliki akun pengklaim dan timestamp klaim
ALTER TABLE campaign_numbers
    ADD CONSTRAINT check_campaign_numbers_dikirim_has_claim
    CHECK (status <> 'dikirim' OR (claimed_account_id IS NOT NULL AND claimed_at IS NOT NULL)) NOT VALID;
ALTER TABLE campaign_numbers
    VALIDATE CONSTRAINT check_campaign_numbers_dikirim_has_claim;

-- Integritas status 'tersedia': akun pengklaim harus NULL
ALTER TABLE campaign_numbers
    ADD CONSTRAINT check_campaign_numbers_tersedia_unclaimed
    CHECK (status <> 'tersedia' OR claimed_account_id IS NULL) NOT VALID;
ALTER TABLE campaign_numbers
    VALIDATE CONSTRAINT check_campaign_numbers_tersedia_unclaimed;

-- 5. Konfigurasi penyimpanan tabel: fillfactor 85 dan autovacuum responsif
-- fillfactor 85 menyisakan 15% ruang kosong per halaman blok untuk pembaruan status in-place
-- (HOT - Heap-Only Tuples updates) sehingga meminimalkan bloat tabel dan overhead indeks.
-- Catatan penting: Nilai fillfactor hanya berlaku untuk halaman data baru yang ditulis setelah migrasi ini.
-- Skala autovacuum diperkecil agar vacuuming dead tuples berjalan lebih cepat saat pengiriman massal.
ALTER TABLE campaign_numbers SET (
    fillfactor = 85,
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE campaign_numbers RESET (
    fillfactor,
    autovacuum_vacuum_scale_factor,
    autovacuum_analyze_scale_factor
);

ALTER TABLE campaign_numbers DROP CONSTRAINT IF EXISTS check_campaign_numbers_tersedia_unclaimed;
ALTER TABLE campaign_numbers DROP CONSTRAINT IF EXISTS check_campaign_numbers_dikirim_has_claim;
ALTER TABLE campaign_numbers DROP CONSTRAINT IF EXISTS check_campaign_numbers_attempts_non_negative;

DROP INDEX IF EXISTS idx_campaign_numbers_campaign_status;
DROP INDEX IF EXISTS idx_campaign_numbers_claimed_account_release;
DROP INDEX IF EXISTS idx_campaign_numbers_claimed_at_stale;

-- +goose StatementEnd
