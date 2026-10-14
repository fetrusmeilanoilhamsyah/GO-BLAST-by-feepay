-- +goose Up
-- +goose StatementBegin

-- Indeks unik berbasis fungsi LOWER(email) untuk mencegah duplikasi akun dengan variasi huruf besar/kecil.
-- Hanya berlaku saat email tidak NULL (email bersifat opsional untuk pekerja sejak migrasi 0015).
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_lower
    ON users (LOWER(email))
    WHERE email IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_users_email_lower;

-- +goose StatementEnd
