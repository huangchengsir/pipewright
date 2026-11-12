-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE deploy_batches (
    id TEXT COLLATE "C" PRIMARY KEY,
    run_id TEXT COLLATE "C" NOT NULL,
    idempotency_key TEXT COLLATE "C" NOT NULL,
    request_hash TEXT COLLATE "C" NOT NULL,
    status TEXT COLLATE "C" NOT NULL,
    created_at TEXT COLLATE "C" NOT NULL,
    updated_at TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE,
    UNIQUE (run_id, idempotency_key)
);

CREATE INDEX idx_deploy_batches_run ON deploy_batches (run_id, created_at);

CREATE TABLE deploy_batch_items (
    id TEXT COLLATE "C" PRIMARY KEY,
    batch_id TEXT COLLATE "C" NOT NULL,
    item_index BIGINT NOT NULL,
    artifact_id TEXT COLLATE "C" NOT NULL,
    artifact_name TEXT COLLATE "C" NOT NULL,
    server_id TEXT COLLATE "C" NOT NULL,
    server_name TEXT COLLATE "C" NOT NULL,
    config_json TEXT COLLATE "C" NOT NULL,
    status TEXT COLLATE "C" NOT NULL,
    message TEXT COLLATE "C" NOT NULL DEFAULT '',
    attempt BIGINT NOT NULL DEFAULT 0,
    started_at TEXT COLLATE "C",
    finished_at TEXT COLLATE "C",
    FOREIGN KEY (batch_id) REFERENCES deploy_batches (id) ON DELETE CASCADE,
    UNIQUE (batch_id, item_index, server_id)
);

CREATE INDEX idx_deploy_batch_items_batch ON deploy_batch_items (batch_id, item_index);
