-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS pipeline_triggers (
    project_id                TEXT COLLATE "C" PRIMARY KEY,
    webhook_token             TEXT COLLATE "C" NOT NULL UNIQUE,
    webhook_secret_ciphertext BYTEA NOT NULL,
    events_json               TEXT COLLATE "C" NOT NULL DEFAULT '{}',
    branch_mappings_json      TEXT COLLATE "C" NOT NULL DEFAULT '[]',
    unmatched_policy          TEXT COLLATE "C" NOT NULL DEFAULT 'record',
    created_at                TEXT COLLATE "C" NOT NULL,
    updated_at                TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
