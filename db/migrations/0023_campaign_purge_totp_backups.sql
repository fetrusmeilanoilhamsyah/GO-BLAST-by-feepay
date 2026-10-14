-- +goose Up
-- +goose StatementBegin

-- 1. Siklus data campaign (persiapan H8: retensi dan pembersihan nomor usang)
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS purged_at TIMESTAMPTZ;
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS purge_summary JSONB;

-- 2. Keamanan TOTP admin & penanda step-up sesi (persiapan H5b)
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret_encrypted TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_step BIGINT;

ALTER TABLE web_sessions ADD COLUMN IF NOT EXISTS stepup_at TIMESTAMPTZ;

-- Tabel kode pemulihan admin (admin recovery codes)
CREATE TABLE IF NOT EXISTS admin_recovery_codes (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    used_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_admin_recovery_codes_user_id
    ON admin_recovery_codes (user_id);

-- 3. Pencatatan cadangan database (persiapan H9)
CREATE TABLE IF NOT EXISTS backups (
    id BIGSERIAL PRIMARY KEY,
    kind VARCHAR(50) NOT NULL CHECK (kind IN ('otomatis', 'manual', 'sebelum_restore')),
    filename TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    size_bytes BIGINT NOT NULL,
    sha256 TEXT NOT NULL,
    schema_version BIGINT NOT NULL,
    row_counts JSONB NOT NULL DEFAULT '{}'::jsonb,
    verified_at TIMESTAMPTZ,
    verify_ok BOOLEAN,
    note TEXT
);

CREATE INDEX IF NOT EXISTS idx_backups_created_at
    ON backups (created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS backups;
DROP TABLE IF EXISTS admin_recovery_codes;

ALTER TABLE web_sessions DROP COLUMN IF EXISTS stepup_at;

ALTER TABLE users DROP COLUMN IF EXISTS totp_last_step;
ALTER TABLE users DROP COLUMN IF EXISTS totp_enabled_at;
ALTER TABLE users DROP COLUMN IF EXISTS totp_secret_encrypted;

ALTER TABLE campaigns DROP COLUMN IF EXISTS purge_summary;
ALTER TABLE campaigns DROP COLUMN IF EXISTS purged_at;

-- +goose StatementEnd
