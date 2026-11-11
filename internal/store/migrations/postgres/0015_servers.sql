-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS servers (
    id            TEXT COLLATE "C" PRIMARY KEY,
    name          TEXT COLLATE "C" NOT NULL,
    host          TEXT COLLATE "C" NOT NULL,
    port          BIGINT NOT NULL DEFAULT 22,
    "user"        TEXT COLLATE "C" NOT NULL,
    credential_id TEXT COLLATE "C" NOT NULL,
    created_at    TEXT COLLATE "C" NOT NULL,
    updated_at    TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (credential_id) REFERENCES credentials (id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_servers_credential_id ON servers (credential_id);
