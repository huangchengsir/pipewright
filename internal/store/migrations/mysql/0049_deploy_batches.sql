CREATE TABLE deploy_batches (
    id VARCHAR(64) PRIMARY KEY,
    run_id VARCHAR(64) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    created_at VARCHAR(32) NOT NULL,
    updated_at VARCHAR(32) NOT NULL,
    UNIQUE KEY uq_deploy_batches_run_key (run_id, idempotency_key),
    KEY idx_deploy_batches_run (run_id, created_at),
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE deploy_batch_items (
    id VARCHAR(64) PRIMARY KEY,
    batch_id VARCHAR(64) NOT NULL,
    item_index INT NOT NULL,
    artifact_id VARCHAR(64) NOT NULL,
    artifact_name VARCHAR(255) NOT NULL,
    server_id VARCHAR(64) NOT NULL,
    server_name VARCHAR(255) NOT NULL,
    config_json TEXT NOT NULL,
    status VARCHAR(32) NOT NULL,
    message VARCHAR(2048) NOT NULL DEFAULT '',
    attempt INT NOT NULL DEFAULT 0,
    started_at VARCHAR(32),
    finished_at VARCHAR(32),
    UNIQUE KEY uq_deploy_batch_item_target (batch_id, item_index, server_id),
    KEY idx_deploy_batch_items_batch (batch_id, item_index),
    FOREIGN KEY (batch_id) REFERENCES deploy_batches (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
