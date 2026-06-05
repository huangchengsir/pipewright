-- 0014 run artifacts(MySQL):构建产物契约持久化。
CREATE TABLE IF NOT EXISTS run_artifacts (
    id            VARCHAR(64) PRIMARY KEY,
    run_id        VARCHAR(64) NOT NULL,
    type          VARCHAR(255) NOT NULL,
    name          VARCHAR(255) NOT NULL,
    reference     VARCHAR(2048) NOT NULL,
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    metadata_json LONGTEXT NOT NULL DEFAULT (''),
    created_at    VARCHAR(32) NOT NULL,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_run_artifacts_run ON run_artifacts (run_id);
