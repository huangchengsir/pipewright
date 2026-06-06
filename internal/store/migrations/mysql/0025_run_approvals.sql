-- 0025 run_approvals(MySQL):人工审批门记录(run×stage 唯一)。
CREATE TABLE IF NOT EXISTS run_approvals (
    id         VARCHAR(64) PRIMARY KEY,
    run_id     VARCHAR(64) NOT NULL,
    stage_id   VARCHAR(64) NOT NULL,
    stage_name VARCHAR(255) NOT NULL DEFAULT '',
    status     VARCHAR(255) NOT NULL DEFAULT 'pending',
    decided_by VARCHAR(255) NOT NULL DEFAULT '',
    decided_at VARCHAR(32) NOT NULL DEFAULT '',
    created_at VARCHAR(32) NOT NULL,
    UNIQUE (run_id, stage_id),
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_run_approvals_run ON run_approvals (run_id);
