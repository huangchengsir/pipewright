-- 0029 project_concurrency(MySQL):项目级并发上限。
CREATE TABLE IF NOT EXISTS project_concurrency (
    project_id     VARCHAR(64) PRIMARY KEY,
    max_concurrent BIGINT NOT NULL DEFAULT 0,
    created_at     VARCHAR(32) NOT NULL,
    updated_at     VARCHAR(32) NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
