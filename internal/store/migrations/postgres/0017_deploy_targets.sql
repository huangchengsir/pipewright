-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS deploy_targets (
    id          TEXT COLLATE "C" PRIMARY KEY,
    run_id      TEXT COLLATE "C" NOT NULL,
    server_id   TEXT COLLATE "C" NOT NULL,
    server_name TEXT COLLATE "C" NOT NULL,
    status      TEXT COLLATE "C" NOT NULL,
    message     TEXT COLLATE "C" NOT NULL DEFAULT '',
    started_at  TEXT COLLATE "C" NOT NULL,
    finished_at TEXT COLLATE "C",
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id)
);


CREATE INDEX IF NOT EXISTS idx_deploy_targets_run ON deploy_targets (run_id);
