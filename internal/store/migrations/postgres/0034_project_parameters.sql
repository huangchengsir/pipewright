-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS project_parameters (
    project_id TEXT COLLATE "C" PRIMARY KEY,
    defs_json  TEXT COLLATE "C" NOT NULL DEFAULT '[]',
    created_at TEXT COLLATE "C" NOT NULL,
    updated_at TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
