-- 0032 project_runners(MySQL):项目级远程构建 runner 配置。
CREATE TABLE IF NOT EXISTS project_runners (
    project_id       VARCHAR(64) PRIMARY KEY,
    runner_server_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at       VARCHAR(32) NOT NULL,
    updated_at       VARCHAR(32) NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
