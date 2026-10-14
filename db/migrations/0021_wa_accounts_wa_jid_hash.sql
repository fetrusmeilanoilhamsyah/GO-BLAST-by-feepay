-- +goose Up
-- +goose StatementBegin

-- 1. Ganti nama kolom jid_hash menjadi wa_jid_hash
ALTER TABLE wa_accounts RENAME COLUMN jid_hash TO wa_jid_hash;

-- 2. Hapus batasan NOT NULL agar akun yang baru diinisialisasi (menunggu_scan) bernilai NULL
ALTER TABLE wa_accounts ALTER COLUMN wa_jid_hash DROP NOT NULL;

-- 3. Kosongkan nilai tiruan lama (hash acak user_id+waktu bukan data nyata)
UPDATE wa_accounts SET wa_jid_hash = NULL;

-- 4. Indeks unik parsial: satu nomor WhatsApp (HMAC JID) hanya boleh terhubung ke satu akun aktif
-- (status selain 'berhenti'). Jika akun sudah berhenti, nomor yang sama dapat digunakan kembali.
CREATE UNIQUE INDEX IF NOT EXISTS idx_wa_accounts_wa_jid_hash_active
    ON wa_accounts (wa_jid_hash)
    WHERE status <> 'berhenti' AND wa_jid_hash IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_wa_accounts_wa_jid_hash_active;

-- Isi placeholder hash jika ada kolom NULL sebelum mengembalikan NOT NULL
UPDATE wa_accounts
SET wa_jid_hash = md5(id::text || clock_timestamp()::text)
WHERE wa_jid_hash IS NULL;

ALTER TABLE wa_accounts ALTER COLUMN wa_jid_hash SET NOT NULL;
ALTER TABLE wa_accounts RENAME COLUMN wa_jid_hash TO jid_hash;

-- +goose StatementEnd
