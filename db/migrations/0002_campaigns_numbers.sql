-- +goose Up
-- +goose StatementBegin
CREATE TABLE campaigns (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'draf' CHECK (status IN ('draf', 'berjalan', 'selesai')),
    button_mode VARCHAR(50) NOT NULL DEFAULT 'kartu_link' CHECK (button_mode IN ('tombol_asli', 'kartu_link')),
    body TEXT NOT NULL,
    image_path TEXT,
    elements JSONB NOT NULL,
    created_by BIGINT NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP WITH TIME ZONE,
    finished_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE number_batches (
    id BIGSERIAL PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES campaigns(id),
    filename VARCHAR(255) NOT NULL,
    uploaded_by BIGINT NOT NULL REFERENCES users(id),
    total INT NOT NULL DEFAULT 0,
    accepted INT NOT NULL DEFAULT 0,
    dup_in_file INT NOT NULL DEFAULT 0,
    dup_in_campaign INT NOT NULL DEFAULT 0,
    invalid INT NOT NULL DEFAULT 0,
    ambiguous INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE campaign_numbers (
    id BIGSERIAL PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES campaigns(id),
    batch_id BIGINT NOT NULL REFERENCES number_batches(id),
    phone_e164 VARCHAR(50) NOT NULL,
    country_code VARCHAR(10) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'tersedia' CHECK (status IN ('tersedia', 'dikirim', 'terkirim', 'tidak_terdaftar', 'gagal')),
    attempts INT NOT NULL DEFAULT 0,
    claimed_account_id BIGINT,
    claimed_at TIMESTAMP WITH TIME ZONE,
    sent_at TIMESTAMP WITH TIME ZONE,
    last_error TEXT,
    CONSTRAINT unique_campaign_phone UNIQUE (campaign_id, phone_e164)
);

CREATE INDEX idx_campaign_numbers_claim ON campaign_numbers(campaign_id, id) WHERE status = 'tersedia';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS campaign_numbers;
DROP TABLE IF EXISTS number_batches;
DROP TABLE IF EXISTS campaigns;
-- +goose StatementEnd
