-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS run_approvals (
    id         TEXT COLLATE "C" PRIMARY KEY,
    run_id     TEXT COLLATE "C" NOT NULL,
    stage_id   TEXT COLLATE "C" NOT NULL,
    stage_name TEXT COLLATE "C" NOT NULL DEFAULT '',
    status     TEXT COLLATE "C" NOT NULL DEFAULT 'pending',
    decided_by TEXT COLLATE "C" NOT NULL DEFAULT '',
    decided_at TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at TEXT COLLATE "C" NOT NULL,
    UNIQUE (run_id, stage_id),
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_run_approvals_run ON run_approvals (run_id);
