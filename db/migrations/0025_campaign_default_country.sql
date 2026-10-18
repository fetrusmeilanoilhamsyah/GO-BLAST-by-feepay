-- +goose Up
-- Menambahkan kolom default_country pada tabel campaigns (kode wilayah ISO 3166-1 alpha-2, default 'ID').
ALTER TABLE campaigns ADD COLUMN default_country VARCHAR(4) NOT NULL DEFAULT 'ID';

-- Inisialisasi setting default_country di tabel settings bila belum ada
INSERT INTO settings (key, value, updated_at)
VALUES ('default_country', '"ID"'::jsonb, CURRENT_TIMESTAMP)
ON CONFLICT (key) DO NOTHING;

-- +goose Down
DELETE FROM settings WHERE key = 'default_country';
ALTER TABLE campaigns DROP COLUMN IF EXISTS default_country;
