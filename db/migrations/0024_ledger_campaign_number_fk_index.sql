-- +goose Up
-- Indeks FK tanpa kondisi pada ledger_entries.campaign_number_id
-- Diperlukan untuk penegakan Foreign Key ON DELETE RESTRICT (migrasi 0014) secara efisien saat penghapusan nomor campaign (Purge).
-- Indeks parsial sebelumnya (idx_ledger_entries_upah_unique & idx_ledger_entries_referral_number_unique)
-- tidak dapat dipakai oleh PostgreSQL query planner untuk verifikasi referential integrity umum.
CREATE INDEX IF NOT EXISTS idx_ledger_entries_campaign_number_id ON ledger_entries (campaign_number_id);

-- +goose Down
DROP INDEX IF EXISTS idx_ledger_entries_campaign_number_id;
