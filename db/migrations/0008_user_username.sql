-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN IF NOT EXISTS username VARCHAR(50);

CREATE OR REPLACE FUNCTION users_default_username() RETURNS trigger AS $$
BEGIN
    IF NEW.username IS NULL OR NEW.username = '' THEN
        NEW.username := LOWER(REGEXP_REPLACE(SPLIT_PART(NEW.email, '@', 1), '[^a-zA-Z0-9_]', '_', 'g')) || '_' || SUBSTRING(MD5(RANDOM()::text) FROM 1 FOR 6);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_users_default_username ON users;
CREATE TRIGGER trg_users_default_username
BEFORE INSERT ON users
FOR EACH ROW
EXECUTE FUNCTION users_default_username();

-- Isi username user yang sudah ada dengan prefix email
UPDATE users 
SET username = LOWER(REGEXP_REPLACE(SPLIT_PART(email, '@', 1), '[^a-zA-Z0-9_]', '_', 'g')) 
WHERE username IS NULL;

-- Pastikan tidak ada duplikasi username pada data lama
UPDATE users u
SET username = u.username || '_' || u.id
WHERE (SELECT COUNT(*) FROM users u2 WHERE u2.username = u.username) > 1;

ALTER TABLE users ALTER COLUMN username SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username_lower ON users (LOWER(username));

-- Sinkronkan display_name dengan username jika sebelumnya masih berupa email
UPDATE users SET display_name = username WHERE display_name = email;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_users_default_username ON users;
DROP FUNCTION IF EXISTS users_default_username();
DROP INDEX IF EXISTS idx_users_username_lower;
ALTER TABLE users DROP COLUMN IF EXISTS username;
-- +goose StatementEnd
