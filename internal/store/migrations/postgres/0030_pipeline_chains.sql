-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS project_chain_targets (
    id                    TEXT COLLATE "C" PRIMARY KEY,
    project_id            TEXT COLLATE "C" NOT NULL,
    downstream_project_id TEXT COLLATE "C" NOT NULL,
    branch                TEXT COLLATE "C" NOT NULL DEFAULT '',
    enabled               BIGINT NOT NULL DEFAULT 1,
    created_at            TEXT COLLATE "C" NOT NULL,
    updated_at            TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    FOREIGN KEY (downstream_project_id) REFERENCES projects (id) ON DELETE CASCADE
);


CREATE INDEX IF NOT EXISTS idx_project_chain_targets_project ON project_chain_targets (project_id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_project_chain_targets
    ON project_chain_targets (project_id, downstream_project_id, branch);


ALTER TABLE pipeline_runs ADD COLUMN chain_source_run_id TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN chain_depth BIGINT NOT NULL DEFAULT 0;
