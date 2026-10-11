-- Seed pengembangan. Jangan jalankan di produksi.
DO $$
BEGIN
    IF current_setting('app.env', true) = 'prod' THEN
        RAISE EXCEPTION 'Seed ditolak di lingkungan produksi (APP_ENV=prod)';
    END IF;
END $$;

INSERT INTO users (id, email, password_hash, role, display_name, language, currency, referral_code, status, created_at)
VALUES 
(1, 'admin@goblast.test', 'placeholder_hash_admin', 'admin', 'Admin Feepay', 'id', 'IDR', 'ADMIN123', 'active', CURRENT_TIMESTAMP),
(2, 'worker1@goblast.test', 'placeholder_hash_worker1', 'worker', 'Pekerja Satu', 'id', 'IDR', 'WORKER01', 'active', CURRENT_TIMESTAMP),
(3, 'worker2@goblast.test', 'placeholder_hash_worker2', 'worker', 'Pekerja Dua', 'en', 'USDT', 'WORKER02', 'active', CURRENT_TIMESTAMP)
ON CONFLICT (id) DO NOTHING;

SELECT setval('users_id_seq', (SELECT MAX(id) FROM users));

INSERT INTO campaigns (id, name, status, button_mode, body, elements, created_by, created_at)
VALUES
(1, 'Campaign Dev 1', 'draf', 'kartu_link', 'Halo ini pesan tes', '{"link_web": true, "phone": false, "cs": true, "promo": false}', 1, CURRENT_TIMESTAMP)
ON CONFLICT (id) DO NOTHING;

SELECT setval('campaigns_id_seq', (SELECT MAX(id) FROM campaigns));

INSERT INTO number_batches (id, campaign_id, filename, uploaded_by, total, accepted, created_at)
VALUES
(1, 1, 'dev_numbers.csv', 1, 50, 50, CURRENT_TIMESTAMP)
ON CONFLICT (id) DO NOTHING;

SELECT setval('number_batches_id_seq', (SELECT MAX(id) FROM number_batches));

DO $$
DECLARE
    i INT;
BEGIN
    FOR i IN 1..50 LOOP
        INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, attempts)
        VALUES (1, 1, '6280000000' || LPAD(i::text, 2, '0'), '62', 'tersedia', 0)
        ON CONFLICT DO NOTHING;
    END LOOP;
END $$;

-- Pengaturan default pengembangan (tidak boleh di-seed di produksi)
INSERT INTO settings (key, value) VALUES
    ('daily_limits', '{"fast": 100, "medium": 100, "slow": 100, "over_slow": 100}'::jsonb),
    ('max_attempts', '3'::jsonb),
    ('max_consecutive_failures', '5'::jsonb),
    ('max_accounts_per_worker', '5'::jsonb),
    ('max_active_sessions', '50'::jsonb),
    ('rate_wage', '{"IDR": 50, "USDT": 3000}'::jsonb)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
