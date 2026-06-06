-- 0010 pipeline_settings(MySQL):每项目构建/环境声明式配置。
CREATE TABLE IF NOT EXISTS pipeline_settings (
    project_id        VARCHAR(64) PRIMARY KEY,
    build_json        LONGTEXT NOT NULL DEFAULT ('{}'),
    environments_json LONGTEXT NOT NULL DEFAULT ('[]'),
    created_at        VARCHAR(32) NOT NULL,
    updated_at        VARCHAR(32) NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
