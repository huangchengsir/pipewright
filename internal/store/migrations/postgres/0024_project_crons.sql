-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS project_crons (
    project_id TEXT COLLATE "C" PRIMARY KEY,
    expression TEXT COLLATE "C" NOT NULL DEFAULT '',
    branch     TEXT COLLATE "C" NOT NULL DEFAULT '',
    enabled    BIGINT NOT NULL DEFAULT 0,
    created_at TEXT COLLATE "C" NOT NULL,
    updated_at TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);


CREATE INDEX IF NOT EXISTS idx_project_crons_enabled ON project_crons (enabled);
