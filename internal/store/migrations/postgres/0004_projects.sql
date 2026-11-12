-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS projects (
    id             TEXT COLLATE "C" PRIMARY KEY,
    name           TEXT COLLATE "C" NOT NULL,
    repo_url       TEXT COLLATE "C" NOT NULL,
    default_branch TEXT COLLATE "C" NOT NULL DEFAULT '',
    credential_id  TEXT COLLATE "C" NOT NULL,
    created_at     TEXT COLLATE "C" NOT NULL,
    updated_at     TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (credential_id) REFERENCES credentials (id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_projects_credential_id ON projects (credential_id);
