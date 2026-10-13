-- +goose Up
-- +goose StatementBegin

-- 1a. Tabel currency_conversions untuk pencatatan konversi saldo IDR ke USDT
CREATE TABLE IF NOT EXISTS currency_conversions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    idr_amount BIGINT NOT NULL CHECK (idr_amount > 0),
    usdt_micro BIGINT NOT NULL CHECK (usdt_micro > 0),
    kurs BIGINT NOT NULL CHECK (kurs > 0),
    request_token TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_currency_conversions_user_token UNIQUE (user_id, request_token)
);

-- 1b. Tambah kolom conversion_id dan perluas CHECK constraint kind di ledger_entries
ALTER TABLE ledger_entries ADD COLUMN IF NOT EXISTS conversion_id BIGINT REFERENCES currency_conversions(id) ON DELETE RESTRICT;

ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN ('upah', 'referral', 'penarikan', 'koreksi', 'konversi'));

-- 1c. Perbarui fungsi dan trigger saldo untuk memeriksa penarikan dan konversi (amount < 0)
CREATE OR REPLACE FUNCTION check_withdrawal_balance()
RETURNS TRIGGER AS $$
DECLARE
    current_balance BIGINT;
    locked_user RECORD;
BEGIN
    IF NEW.amount < 0 AND NEW.kind IN ('penarikan', 'konversi') THEN
        -- Kunci baris users (FOR UPDATE) untuk proteksi saldo konkuren
        SELECT * INTO locked_user FROM users WHERE id = NEW.user_id FOR UPDATE;
        
        current_balance := user_balance(NEW.user_id, NEW.currency);
        IF current_balance + NEW.amount < 0 THEN
            RAISE EXCEPTION 'insufficient balance for %', NEW.kind;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- 1d. Ubah rate_wage dan rate_referral dari JSON {IDR, USDT} menjadi bilangan bulat rupiah
UPDATE settings
SET value = (value->'IDR')::jsonb
WHERE key IN ('rate_wage', 'rate_referral')
  AND value ? 'IDR';

-- 1e. Hapus kolom users.currency
ALTER TABLE users DROP COLUMN IF EXISTS currency;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Kembalikan kolom users.currency
ALTER TABLE users ADD COLUMN IF NOT EXISTS currency VARCHAR(10) NOT NULL DEFAULT 'IDR' CHECK (currency IN ('IDR', 'USDT'));

-- Kembalikan fungsi check_withdrawal_balance ke versi hanya penarikan
CREATE OR REPLACE FUNCTION check_withdrawal_balance()
RETURNS TRIGGER AS $$
DECLARE
    current_balance BIGINT;
    locked_user RECORD;
BEGIN
    IF NEW.kind = 'penarikan' THEN
        SELECT * INTO locked_user FROM users WHERE id = NEW.user_id FOR UPDATE;
        current_balance := user_balance(NEW.user_id, NEW.currency);
        IF current_balance + NEW.amount < 0 THEN
            RAISE EXCEPTION 'insufficient balance for withdrawal';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Kembalikan CHECK constraint kind
ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN ('upah', 'referral', 'penarikan', 'koreksi'));

-- Hapus kolom conversion_id
ALTER TABLE ledger_entries DROP COLUMN IF EXISTS conversion_id;

-- Hapus tabel currency_conversions
DROP TABLE IF EXISTS currency_conversions;

-- +goose StatementEnd
