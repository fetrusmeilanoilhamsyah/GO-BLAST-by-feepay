-- +goose Up
-- +goose StatementBegin
ALTER TABLE campaign_numbers ADD COLUMN recipient_name VARCHAR(255);
ALTER TABLE campaign_numbers ADD COLUMN custom_data JSONB;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE campaign_numbers DROP COLUMN IF EXISTS custom_data;
ALTER TABLE campaign_numbers DROP COLUMN IF EXISTS recipient_name;
-- +goose StatementEnd
