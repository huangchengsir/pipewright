-- 0008 pipeline_configs(MySQL):每项目声明式 spec(JSON + 渲染 YAML)。
CREATE TABLE IF NOT EXISTS pipeline_configs (
    project_id VARCHAR(64) PRIMARY KEY,
    spec_json  LONGTEXT NOT NULL DEFAULT ('{}'),
    spec_yaml  LONGTEXT NOT NULL DEFAULT (''),
    status     VARCHAR(255) NOT NULL DEFAULT 'draft',
    created_at VARCHAR(32) NOT NULL,
    updated_at VARCHAR(32) NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
