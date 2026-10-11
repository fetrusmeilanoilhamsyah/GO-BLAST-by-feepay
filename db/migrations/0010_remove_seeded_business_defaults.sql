-- +goose Up
-- +goose StatementBegin
DELETE FROM settings WHERE key = 'daily_limits' AND value = '{"fast": 1000, "medium": 500, "slow": 200, "over_slow": 100}'::jsonb;
DELETE FROM settings WHERE key = 'max_attempts' AND value = '3'::jsonb;
DELETE FROM settings WHERE key = 'max_consecutive_failures' AND value = '5'::jsonb;
DELETE FROM settings WHERE key = 'max_accounts_per_worker' AND value = '5'::jsonb;
DELETE FROM settings WHERE key = 'max_active_sessions' AND value = '50'::jsonb;
DELETE FROM settings WHERE key = 'rate_idr' AND value = '50'::jsonb;
DELETE FROM settings WHERE key = 'rate_usdt' AND value = '0.003'::jsonb;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
INSERT INTO settings (key, value) VALUES
    ('max_accounts_per_worker', '5'::jsonb),
    ('max_active_sessions', '50'::jsonb),
    ('daily_limits', '{"fast": 1000, "medium": 500, "slow": 200, "over_slow": 100}'::jsonb),
    ('max_attempts', '3'::jsonb),
    ('max_consecutive_failures', '5'::jsonb),
    ('rate_idr', '50'::jsonb),
    ('rate_usdt', '0.003'::jsonb)
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd
