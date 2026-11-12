-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS pipeline_runs (
    id             TEXT COLLATE "C" PRIMARY KEY,
    project_id     TEXT COLLATE "C" NOT NULL,
    status         TEXT COLLATE "C" NOT NULL DEFAULT 'queued',
    trigger_type   TEXT COLLATE "C" NOT NULL DEFAULT 'manual',
    trigger_branch TEXT COLLATE "C" NOT NULL DEFAULT '',
    trigger_commit TEXT COLLATE "C" NOT NULL DEFAULT '',
    trigger_actor  TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at     TEXT COLLATE "C" NOT NULL,
    started_at     TEXT COLLATE "C",
    finished_at    TEXT COLLATE "C",
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS run_steps (
    id          TEXT COLLATE "C" PRIMARY KEY,
    run_id      TEXT COLLATE "C" NOT NULL,
    name        TEXT COLLATE "C" NOT NULL,
    status      TEXT COLLATE "C" NOT NULL DEFAULT 'pending',
    ordinal     BIGINT NOT NULL DEFAULT 0,
    started_at  TEXT COLLATE "C",
    finished_at TEXT COLLATE "C",
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_pipeline_runs_project_id ON pipeline_runs (project_id);
CREATE INDEX IF NOT EXISTS idx_run_steps_run_id ON run_steps (run_id);
