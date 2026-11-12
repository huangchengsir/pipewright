-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS ai_config (
    id                  BIGINT PRIMARY KEY CHECK (id = 1),
    provider            TEXT COLLATE "C" NOT NULL DEFAULT '',
    base_url            TEXT COLLATE "C" NOT NULL DEFAULT '',
    model               TEXT COLLATE "C" NOT NULL DEFAULT '',
    api_key_ciphertext  BYTEA,
    budget_json         TEXT COLLATE "C" NOT NULL DEFAULT '{"monthlyTokenLimit":null}',
    enabled             BIGINT NOT NULL DEFAULT 0,
    created_at          TEXT COLLATE "C" NOT NULL,
    updated_at          TEXT COLLATE "C" NOT NULL
);
