-- +goose Up
-- +goose StatementBegin

-- 1. Tambah kolom request_token di withdrawals dengan constraint unik per pengguna
ALTER TABLE withdrawals ADD COLUMN IF NOT EXISTS request_token TEXT;
UPDATE withdrawals SET request_token = 'legacy_' || id::text WHERE request_token IS NULL;
ALTER TABLE withdrawals ALTER COLUMN request_token SET DEFAULT md5(random()::text || clock_timestamp()::text);
ALTER TABLE withdrawals ALTER COLUMN request_token SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_withdrawals_user_request_token ON withdrawals (user_id, request_token);

-- 2. Tambah kolom payout_reference (untuk TXID USDT) dan reject_reason di withdrawals
ALTER TABLE withdrawals ADD COLUMN IF NOT EXISTS payout_reference TEXT;
ALTER TABLE withdrawals ADD COLUMN IF NOT EXISTS reject_reason TEXT;

-- 3. Tambah kolom withdraw_locked_until, payout_dest_hash, dan must_change_password di users
ALTER TABLE users ADD COLUMN IF NOT EXISTS withdraw_locked_until TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS payout_dest_hash TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_users_payout_dest_hash ON users (payout_dest_hash);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_users_payout_dest_hash;
ALTER TABLE users DROP COLUMN IF EXISTS must_change_password;
ALTER TABLE users DROP COLUMN IF EXISTS payout_dest_hash;
ALTER TABLE users DROP COLUMN IF EXISTS withdraw_locked_until;

ALTER TABLE withdrawals DROP COLUMN IF EXISTS reject_reason;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS payout_reference;
DROP INDEX IF EXISTS idx_withdrawals_user_request_token;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS request_token;

-- +goose StatementEnd
