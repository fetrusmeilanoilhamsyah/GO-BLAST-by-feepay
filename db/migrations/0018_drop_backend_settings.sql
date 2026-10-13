-- +goose Up
-- +goose StatementBegin

-- 1. Hapus baris settings backend yang kini menggunakan konstanta limits atau env
DELETE FROM settings WHERE key IN (
    'speed_delays',
    'daily_limits',
    'max_attempts',
    'max_consecutive_failures',
    'claim_timeout_seconds',
    'max_active_sessions',
    'default_country_code'
);

-- 2. Perbarui CHECK constraint users status agar mendukung 'frozen'
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;
ALTER TABLE users ADD CONSTRAINT users_status_check CHECK (status IN ('active', 'disabled', 'frozen'));

-- 3. Kolom persetujuan syarat dan ketentuan serta alasan pembekuan di tabel users
ALTER TABLE users ADD COLUMN IF NOT EXISTS terms_accepted_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS terms_version VARCHAR(50);
ALTER TABLE users ADD COLUMN IF NOT EXISTS freeze_reason TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE users DROP COLUMN IF EXISTS freeze_reason;
ALTER TABLE users DROP COLUMN IF EXISTS terms_version;
ALTER TABLE users DROP COLUMN IF EXISTS terms_accepted_at;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;
ALTER TABLE users ADD CONSTRAINT users_status_check CHECK (status IN ('active', 'disabled'));

-- +goose StatementEnd
