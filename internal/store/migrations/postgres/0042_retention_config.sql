-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS retention_config (
    id               BIGINT PRIMARY KEY CHECK (id = 1),
    enabled          BIGINT NOT NULL DEFAULT 0,
    keep_per_project BIGINT NOT NULL DEFAULT 0,
    max_age_days     BIGINT NOT NULL DEFAULT 0,
    updated_at       TEXT COLLATE "C" NOT NULL DEFAULT ''
);
INSERT INTO retention_config (id, enabled, keep_per_project, max_age_days, updated_at) VALUES (1, 0, 0, 0, '') ON CONFLICT (id) DO NOTHING;
