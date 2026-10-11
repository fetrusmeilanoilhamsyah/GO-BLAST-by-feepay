-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_campaign_numbers_sent_at ON campaign_numbers(sent_at);
CREATE INDEX IF NOT EXISTS idx_wallet_entries_currency ON wallet_entries(currency, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_wallet_entries_currency;
DROP INDEX IF EXISTS idx_campaign_numbers_sent_at;
-- +goose StatementEnd
