-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS notification_channels (
    id                TEXT COLLATE "C" PRIMARY KEY,
    name              TEXT COLLATE "C" NOT NULL,
    type              TEXT COLLATE "C" NOT NULL,
    enabled           BIGINT NOT NULL DEFAULT 1,
    config_json       TEXT COLLATE "C" NOT NULL DEFAULT '{}',
    secret_ciphertext BYTEA,
    created_at        TEXT COLLATE "C" NOT NULL,
    updated_at        TEXT COLLATE "C" NOT NULL
);
