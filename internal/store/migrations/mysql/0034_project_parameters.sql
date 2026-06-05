-- 0034 project_parameters(MySQL):项目级类型化运行参数定义。
CREATE TABLE IF NOT EXISTS project_parameters (
    project_id VARCHAR(64) PRIMARY KEY,
    defs_json  LONGTEXT NOT NULL DEFAULT ('[]'),
    created_at VARCHAR(32) NOT NULL,
    updated_at VARCHAR(32) NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
