-- 0017 deploy targets(MySQL):SSH 部署每机结果。
CREATE TABLE IF NOT EXISTS deploy_targets (
    id          VARCHAR(64) PRIMARY KEY,
    run_id      VARCHAR(64) NOT NULL,
    server_id   VARCHAR(64) NOT NULL,
    server_name VARCHAR(255) NOT NULL,
    status      VARCHAR(255) NOT NULL,
    message     VARCHAR(2048) NOT NULL DEFAULT '',
    started_at  VARCHAR(32) NOT NULL,
    finished_at VARCHAR(32),
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_deploy_targets_run ON deploy_targets (run_id);
