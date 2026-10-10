-- +goose Up
-- +goose StatementBegin
CREATE TABLE wa_accounts (
    id BIGSERIAL PRIMARY KEY,
    worker_id BIGINT NOT NULL REFERENCES users(id),
    label VARCHAR(255) NOT NULL,
    jid_hash VARCHAR(255) NOT NULL,
    speed VARCHAR(50) NOT NULL DEFAULT 'medium' CHECK (speed IN ('fast', 'medium', 'slow', 'over_slow')),
    status VARCHAR(50) NOT NULL DEFAULT 'menunggu_scan' CHECK (status IN ('menunggu_scan', 'terhubung', 'berhenti', 'terputus', 'antre')),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    connected_at TIMESTAMP WITH TIME ZONE,
    stopped_at TIMESTAMP WITH TIME ZONE,
    stop_reason TEXT
);

ALTER TABLE campaign_numbers ADD CONSTRAINT fk_campaign_numbers_account FOREIGN KEY (claimed_account_id) REFERENCES wa_accounts(id);

CREATE TABLE ledger_entries (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    kind VARCHAR(50) NOT NULL CHECK (kind IN ('upah', 'referral', 'penarikan', 'koreksi')),
    currency VARCHAR(10) NOT NULL CHECK (currency IN ('IDR', 'USDT')),
    amount BIGINT NOT NULL,
    rate_snapshot BIGINT,
    campaign_number_id BIGINT REFERENCES campaign_numbers(id),
    source_user_id BIGINT REFERENCES users(id),
    reverses_entry_id BIGINT,
    note TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_ledger_reverses FOREIGN KEY (reverses_entry_id) REFERENCES ledger_entries(id)
);

CREATE UNIQUE INDEX idx_ledger_entries_upah_unique ON ledger_entries(campaign_number_id) WHERE kind = 'upah';
CREATE INDEX idx_ledger_entries_user_curr_time ON ledger_entries(user_id, currency, created_at);

CREATE OR REPLACE FUNCTION prevent_ledger_update_delete()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only';
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_ledger_update_delete
BEFORE UPDATE OR DELETE ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION prevent_ledger_update_delete();

CREATE OR REPLACE FUNCTION user_balance(p_user_id BIGINT, p_currency VARCHAR)
RETURNS BIGINT AS $$
DECLARE
    total BIGINT;
BEGIN
    SELECT COALESCE(SUM(amount), 0) INTO total
    FROM ledger_entries
    WHERE user_id = p_user_id AND currency = p_currency;
    RETURN total;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION check_withdrawal_balance()
RETURNS TRIGGER AS $$
DECLARE
    current_balance BIGINT;
    locked_user RECORD;
BEGIN
    IF NEW.kind = 'penarikan' THEN
        -- Lock the user row to prevent concurrent withdrawals causing negative balance
        SELECT * INTO locked_user FROM users WHERE id = NEW.user_id FOR UPDATE;
        
        current_balance := user_balance(NEW.user_id, NEW.currency);
        -- amount for penarikan should be negative
        IF current_balance + NEW.amount < 0 THEN
            RAISE EXCEPTION 'insufficient balance for withdrawal';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_check_withdrawal_balance
BEFORE INSERT ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION check_withdrawal_balance();

CREATE TABLE wallet_entries (
    id BIGSERIAL PRIMARY KEY,
    currency VARCHAR(10) NOT NULL CHECK (currency IN ('IDR', 'USDT')),
    amount BIGINT NOT NULL,
    note TEXT NOT NULL,
    created_by BIGINT NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS wallet_entries;
DROP TRIGGER IF EXISTS trg_check_withdrawal_balance ON ledger_entries;
DROP FUNCTION IF EXISTS check_withdrawal_balance();
DROP FUNCTION IF EXISTS user_balance(BIGINT, VARCHAR);
DROP TRIGGER IF EXISTS trg_prevent_ledger_update_delete ON ledger_entries;
DROP FUNCTION IF EXISTS prevent_ledger_update_delete();
DROP TABLE IF EXISTS ledger_entries;
ALTER TABLE campaign_numbers DROP CONSTRAINT IF EXISTS fk_campaign_numbers_account;
DROP TABLE IF EXISTS wa_accounts;
-- +goose StatementEnd
