-- +goose Up
-- +goose StatementBegin

-- whatsmeow membuat tabel whatsmeow_* sendiri saat Connect() pertama kali.
-- Tidak perlu migrasi manual untuk tabel whatsmeow_*.

-- Tambahkan kolom consecutive_failures ke wa_accounts untuk stop otomatis
ALTER TABLE wa_accounts ADD COLUMN IF NOT EXISTS consecutive_failures INT NOT NULL DEFAULT 0;

-- Menyimpan ID perangkat di tabel whatsmeow_devices (isian oleh aplikasi setelah koneksi)
ALTER TABLE wa_accounts ADD COLUMN IF NOT EXISTS whatsmeow_device_id BIGINT;

-- Index untuk mencari akun aktif per pekerja (klaim dan status check)
CREATE INDEX IF NOT EXISTS idx_wa_accounts_worker_status ON wa_accounts(worker_id, status);
CREATE INDEX IF NOT EXISTS idx_wa_accounts_status ON wa_accounts(status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_wa_accounts_status;
DROP INDEX IF EXISTS idx_wa_accounts_worker_status;
ALTER TABLE wa_accounts DROP COLUMN IF EXISTS whatsmeow_device_id;
ALTER TABLE wa_accounts DROP COLUMN IF EXISTS consecutive_failures;
-- +goose StatementEnd
