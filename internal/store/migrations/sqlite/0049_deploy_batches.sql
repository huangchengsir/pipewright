CREATE TABLE deploy_batches (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE,
    UNIQUE (run_id, idempotency_key)
);

CREATE INDEX idx_deploy_batches_run ON deploy_batches (run_id, created_at);

CREATE TABLE deploy_batch_items (
    id TEXT PRIMARY KEY,
    batch_id TEXT NOT NULL,
    item_index INTEGER NOT NULL,
    artifact_id TEXT NOT NULL,
    artifact_name TEXT NOT NULL,
    server_id TEXT NOT NULL,
    server_name TEXT NOT NULL,
    config_json TEXT NOT NULL,
    status TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0,
    started_at TEXT,
    finished_at TEXT,
    FOREIGN KEY (batch_id) REFERENCES deploy_batches (id) ON DELETE CASCADE,
    UNIQUE (batch_id, item_index, server_id)
);

CREATE INDEX idx_deploy_batch_items_batch ON deploy_batch_items (batch_id, item_index);
