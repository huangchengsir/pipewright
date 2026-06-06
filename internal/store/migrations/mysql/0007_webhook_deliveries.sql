-- 0007 webhook_deliveries(MySQL):投递去重 + pipeline_runs 目标元数据列。
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id          VARCHAR(64) PRIMARY KEY,
    project_id  VARCHAR(64) NOT NULL,
    delivery_id VARCHAR(191) NOT NULL,
    event       VARCHAR(255) NOT NULL DEFAULT '',
    branch      VARCHAR(255) NOT NULL DEFAULT '',
    commit_sha  VARCHAR(255) NOT NULL DEFAULT '',
    run_id      VARCHAR(64) NOT NULL DEFAULT '',
    outcome     VARCHAR(255) NOT NULL DEFAULT '',
    created_at  VARCHAR(32) NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    UNIQUE (project_id, delivery_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_webhook_deliveries_project_id ON webhook_deliveries (project_id);

ALTER TABLE pipeline_runs ADD COLUMN resolved_environment VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN resolved_target_server_ids LONGTEXT NOT NULL DEFAULT ('[]');
