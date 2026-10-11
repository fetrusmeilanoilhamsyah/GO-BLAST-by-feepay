-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN IF NOT EXISTS payout_method VARCHAR(50);
ALTER TABLE users ADD COLUMN IF NOT EXISTS payout_dest_encrypted TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS payout_account_name VARCHAR(255);

ALTER TABLE withdrawals DROP CONSTRAINT IF EXISTS withdrawals_method_check;
ALTER TABLE withdrawals ADD CONSTRAINT withdrawals_method_check CHECK (method IN ('dana', 'seabank', 'bca', 'bank', 'usdt_bep20'));

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_payout_method_check;
ALTER TABLE users ADD CONSTRAINT users_payout_method_check CHECK (payout_method IS NULL OR payout_method IN ('dana', 'seabank', 'bca', 'bank', 'usdt_bep20'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_payout_method_check;
ALTER TABLE users DROP COLUMN IF EXISTS payout_account_name;
ALTER TABLE users DROP COLUMN IF EXISTS payout_dest_encrypted;
ALTER TABLE users DROP COLUMN IF EXISTS payout_method;

ALTER TABLE withdrawals DROP CONSTRAINT IF EXISTS withdrawals_method_check;
ALTER TABLE withdrawals ADD CONSTRAINT withdrawals_method_check CHECK (method IN ('dana', 'seabank', 'bank', 'usdt_bep20'));
-- +goose StatementEnd
