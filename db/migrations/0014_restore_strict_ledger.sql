-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION prevent_ledger_update_delete()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only';
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_campaign_number_id_fkey;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_campaign_number_id_fkey 
    FOREIGN KEY (campaign_number_id) REFERENCES campaign_numbers(id) ON DELETE RESTRICT;

ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS archived_at TIMESTAMP WITH TIME ZONE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE campaigns DROP COLUMN IF EXISTS archived_at;

ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_campaign_number_id_fkey;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_campaign_number_id_fkey 
    FOREIGN KEY (campaign_number_id) REFERENCES campaign_numbers(id);

CREATE OR REPLACE FUNCTION prevent_ledger_update_delete()
RETURNS TRIGGER AS $$
BEGIN
    -- Izinkan pelepasan referensi nomor kampanye yang dihapus (perilaku migrasi 0013)
    IF TG_OP = 'UPDATE' 
       AND OLD.user_id = NEW.user_id 
       AND OLD.kind = NEW.kind 
       AND OLD.currency = NEW.currency 
       AND OLD.amount = NEW.amount 
       AND NEW.campaign_number_id IS NULL THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'ledger_entries is append-only';
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
