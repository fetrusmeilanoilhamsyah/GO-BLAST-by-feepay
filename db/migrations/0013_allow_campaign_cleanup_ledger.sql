-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION prevent_ledger_update_delete()
RETURNS TRIGGER AS $$
BEGIN
    -- Izinkan pelepasan referensi nomor kampanye yang dihapus (nilai finansial tetap abadi & tidak berubah)
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION prevent_ledger_update_delete()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only';
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
