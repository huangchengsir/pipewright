-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS dns_providers (
    id            TEXT COLLATE "C" PRIMARY KEY,
    type          TEXT COLLATE "C" NOT NULL,
    name          TEXT COLLATE "C" NOT NULL,
    credential_id TEXT COLLATE "C" NOT NULL,
    base_domain   TEXT COLLATE "C" NOT NULL,
    created_at    TEXT COLLATE "C" NOT NULL,
    updated_at    TEXT COLLATE "C" NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_dns_providers_type ON dns_providers (type);
