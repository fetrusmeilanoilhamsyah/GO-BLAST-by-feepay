-- +goose Up
-- +goose StatementBegin

-- 1. Indeks unik parsial referral di ledger_entries: sistem referral 1 tingkat (maksimal 1 bonus referral per nomor kampanye)
CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_entries_referral_unique
    ON ledger_entries (campaign_number_id)
    WHERE kind = 'referral';

-- 2. CHECK constraint referral: entri referral wajib memiliki source_user_id (pekerja pengirim) dan campaign_number_id
ALTER TABLE ledger_entries
    ADD CONSTRAINT check_ledger_entries_referral_source
    CHECK (kind <> 'referral' OR (source_user_id IS NOT NULL AND campaign_number_id IS NOT NULL)) NOT VALID;
ALTER TABLE ledger_entries
    VALIDATE CONSTRAINT check_ledger_entries_referral_source;

-- 3. Indeks pendukung FK users.referred_by untuk query daftar referer dan penghitungan bawahan
CREATE INDEX IF NOT EXISTS idx_users_referred_by
    ON users (referred_by)
    WHERE referred_by IS NOT NULL;

-- 4. Indeks retensi data untuk pembersihan berkala (H5a / H8)
-- Pembersihan sesi web kedaluwarsa
CREATE INDEX IF NOT EXISTS idx_web_sessions_expires_at
    ON web_sessions (expires_at);

-- Pencarian dan audit log rotasi/retensi berdasarkan aksi dan waktu
CREATE INDEX IF NOT EXISTS idx_audit_logs_action_created_at
    ON audit_logs (action, created_at);

-- Pembersihan notifikasi lama pekerja
CREATE INDEX IF NOT EXISTS idx_notifications_created_at
    ON notifications (created_at);

-- 5. Indeks pendukung FK number_batches.campaign_id untuk mempercepat filter batch dan penghapusan campaign
CREATE INDEX IF NOT EXISTS idx_number_batches_campaign_id
    ON number_batches (campaign_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_number_batches_campaign_id;
DROP INDEX IF EXISTS idx_notifications_created_at;
DROP INDEX IF EXISTS idx_audit_logs_action_created_at;
DROP INDEX IF EXISTS idx_web_sessions_expires_at;
DROP INDEX IF EXISTS idx_users_referred_by;

ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS check_ledger_entries_referral_source;
DROP INDEX IF EXISTS idx_ledger_entries_referral_unique;

-- +goose StatementEnd
