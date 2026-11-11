-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS pipeline_configs (
    project_id TEXT COLLATE "C" PRIMARY KEY,
    spec_json  TEXT COLLATE "C" NOT NULL DEFAULT '{}',
    spec_yaml  TEXT COLLATE "C" NOT NULL DEFAULT '',
    status     TEXT COLLATE "C" NOT NULL DEFAULT 'draft',
    created_at TEXT COLLATE "C" NOT NULL,
    updated_at TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
