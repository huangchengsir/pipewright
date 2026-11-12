-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS oauth_apps (
    provider                 TEXT COLLATE "C" PRIMARY KEY,
    client_id                TEXT COLLATE "C" NOT NULL DEFAULT '',
    client_secret_ciphertext BYTEA,
    base_url                 TEXT COLLATE "C" NOT NULL DEFAULT '',
    enabled                  BIGINT NOT NULL DEFAULT 0,
    created_at               TEXT COLLATE "C" NOT NULL,
    updated_at               TEXT COLLATE "C" NOT NULL
);
