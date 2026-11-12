-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS custom_nodes (
    id          TEXT COLLATE "C" PRIMARY KEY,
    name        TEXT COLLATE "C" NOT NULL,
    description TEXT COLLATE "C" NOT NULL DEFAULT '',
    node_type   TEXT COLLATE "C" NOT NULL,
    summary     TEXT COLLATE "C" NOT NULL DEFAULT '',
    config_json TEXT COLLATE "C" NOT NULL DEFAULT '{}',
    created_at  TEXT COLLATE "C" NOT NULL,
    updated_at  TEXT COLLATE "C" NOT NULL
);


CREATE UNIQUE INDEX IF NOT EXISTS idx_custom_nodes_name ON custom_nodes (name);
