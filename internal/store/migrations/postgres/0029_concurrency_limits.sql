-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS project_concurrency (
    project_id     TEXT COLLATE "C" PRIMARY KEY,
    max_concurrent BIGINT NOT NULL DEFAULT 0,
    created_at     TEXT COLLATE "C" NOT NULL,
    updated_at     TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
