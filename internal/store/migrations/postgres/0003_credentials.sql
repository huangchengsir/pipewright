-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS credentials (
    id           TEXT COLLATE "C" PRIMARY KEY,
    name         TEXT COLLATE "C" NOT NULL,
    type         TEXT COLLATE "C" NOT NULL,
    scope        TEXT COLLATE "C" NOT NULL DEFAULT '',
    ciphertext   BYTEA NOT NULL,
    masked_value TEXT COLLATE "C" NOT NULL,
    last_used_at TEXT COLLATE "C",
    created_at   TEXT COLLATE "C" NOT NULL,
    updated_at   TEXT COLLATE "C" NOT NULL
);
