-- 0006 runs(MySQL):流水线运行 + 步骤。
CREATE TABLE IF NOT EXISTS pipeline_runs (
    id             VARCHAR(64) PRIMARY KEY,
    project_id     VARCHAR(64) NOT NULL,
    status         VARCHAR(255) NOT NULL DEFAULT 'queued',
    trigger_type   VARCHAR(255) NOT NULL DEFAULT 'manual',
    trigger_branch VARCHAR(255) NOT NULL DEFAULT '',
    trigger_commit VARCHAR(255) NOT NULL DEFAULT '',
    trigger_actor  VARCHAR(255) NOT NULL DEFAULT '',
    created_at     VARCHAR(32) NOT NULL,
    started_at     VARCHAR(32),
    finished_at    VARCHAR(32),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS run_steps (
    id          VARCHAR(64) PRIMARY KEY,
    run_id      VARCHAR(64) NOT NULL,
    name        VARCHAR(255) NOT NULL,
    status      VARCHAR(255) NOT NULL DEFAULT 'pending',
    ordinal     BIGINT NOT NULL DEFAULT 0,
    started_at  VARCHAR(32),
    finished_at VARCHAR(32),
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_pipeline_runs_project_id ON pipeline_runs (project_id);
CREATE INDEX idx_run_steps_run_id ON run_steps (run_id);
