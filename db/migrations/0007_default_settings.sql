-- +goose Up
-- +goose StatementBegin
INSERT INTO settings (key, value) VALUES
    ('max_accounts_per_worker', '5'::jsonb),
    ('max_active_sessions', '50'::jsonb),
    ('daily_limits', '{"fast": 1000, "medium": 500, "slow": 200, "over_slow": 100}'::jsonb),
    ('speed_delays', '{"fast": 1, "medium": 5, "slow": 15, "over_slow": 30}'::jsonb),
    ('max_attempts', '3'::jsonb),
    ('max_consecutive_failures', '5'::jsonb),
    ('claim_timeout_seconds', '300'::jsonb),
    ('rate_idr', '50'::jsonb),
    ('rate_usdt', '0.003'::jsonb)
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM settings WHERE key IN (
    'max_accounts_per_worker',
    'max_active_sessions',
    'daily_limits',
    'speed_delays',
    'max_attempts',
    'max_consecutive_failures',
    'claim_timeout_seconds',
    'rate_idr',
    'rate_usdt'
);
-- +goose StatementEnd
