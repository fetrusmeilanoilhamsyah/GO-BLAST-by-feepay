-- +goose Up
-- +goose StatementBegin

-- A3: Email pekerja menjadi opsional (nullable)
-- Pengguna lama yang sudah punya email tidak terdampak
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;

-- Pastikan unique constraint pada email hanya berlaku saat email diisi (tidak NULL)
-- Indeks unik pada email NULL-safe: PostgreSQL secara bawaan sudah mengizinkan banyak NULL di UNIQUE column
-- Tidak perlu perubahan constraint, UNIQUE NOT NULL sudah berubah menjadi UNIQUE nullable

-- Sistem notifikasi: tabel untuk lonceng notifikasi pekerja
-- Menggantikan banner broadcast, semua notif sistem masuk ke sini
CREATE TABLE IF NOT EXISTS notifications (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(150) NOT NULL,
    message TEXT NOT NULL,
    -- jenis notifikasi: 'broadcast', 'ticket_reply', 'system'
    kind VARCHAR(30) NOT NULL DEFAULT 'system',
    -- referensi ke entitas asal (opsional), misal ticket ID
    ref_id BIGINT,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_unread
    ON notifications(user_id, created_at DESC)
    WHERE read_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id);

-- Tambah kolom reply_by dan reply_at di support_tickets
-- agar admin bisa balas tiket langsung dari panel
ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS reply_message TEXT;
ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS replied_by BIGINT REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS replied_at TIMESTAMPTZ;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE support_tickets DROP COLUMN IF EXISTS replied_at;
ALTER TABLE support_tickets DROP COLUMN IF EXISTS replied_by;
ALTER TABLE support_tickets DROP COLUMN IF EXISTS reply_message;

DROP TABLE IF EXISTS notifications;

ALTER TABLE users ALTER COLUMN email SET NOT NULL;
-- +goose StatementEnd
