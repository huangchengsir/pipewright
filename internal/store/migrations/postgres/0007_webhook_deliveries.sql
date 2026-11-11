-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id          TEXT COLLATE "C" PRIMARY KEY,
    project_id  TEXT COLLATE "C" NOT NULL,
    delivery_id TEXT COLLATE "C" NOT NULL,
    event       TEXT COLLATE "C" NOT NULL DEFAULT '',
    branch      TEXT COLLATE "C" NOT NULL DEFAULT '',
    commit_sha  TEXT COLLATE "C" NOT NULL DEFAULT '',
    run_id      TEXT COLLATE "C" NOT NULL DEFAULT '',
    outcome     TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at  TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    UNIQUE (project_id, delivery_id)
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_project_id ON webhook_deliveries (project_id);

ALTER TABLE pipeline_runs ADD COLUMN resolved_environment TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN resolved_target_server_ids TEXT COLLATE "C" NOT NULL DEFAULT '[]';
