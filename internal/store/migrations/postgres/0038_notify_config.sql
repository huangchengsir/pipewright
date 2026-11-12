-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS notify_config (
    id          BIGINT PRIMARY KEY CHECK (id = 1),
    language    TEXT COLLATE "C" NOT NULL DEFAULT 'zh-CN',
    updated_at  TEXT COLLATE "C" NOT NULL DEFAULT ''
);
INSERT INTO notify_config (id, language, updated_at) VALUES (1, 'zh-CN', '') ON CONFLICT (id) DO NOTHING;
