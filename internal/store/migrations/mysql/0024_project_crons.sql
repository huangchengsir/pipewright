-- 0024 project_crons(MySQL):项目级 cron 定时触发配置。
CREATE TABLE IF NOT EXISTS project_crons (
    project_id VARCHAR(64) PRIMARY KEY,
    expression VARCHAR(255) NOT NULL DEFAULT '',
    branch     VARCHAR(255) NOT NULL DEFAULT '',
    enabled    TINYINT NOT NULL DEFAULT 0,
    created_at VARCHAR(32) NOT NULL,
    updated_at VARCHAR(32) NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_project_crons_enabled ON project_crons (enabled);
