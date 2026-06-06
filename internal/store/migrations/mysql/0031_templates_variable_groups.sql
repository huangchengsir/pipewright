-- 0031 templates + variable groups(MySQL):name 唯一索引需定长 VARCHAR。
CREATE TABLE IF NOT EXISTS pipeline_templates (
    id          VARCHAR(64) PRIMARY KEY,
    name        VARCHAR(191) NOT NULL,
    description VARCHAR(2048) NOT NULL DEFAULT '',
    stages_json LONGTEXT NOT NULL DEFAULT ('[]'),
    created_at  VARCHAR(32) NOT NULL,
    updated_at  VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_pipeline_templates_name ON pipeline_templates (name);

CREATE TABLE IF NOT EXISTS variable_groups (
    id          VARCHAR(64) PRIMARY KEY,
    name        VARCHAR(191) NOT NULL,
    description VARCHAR(2048) NOT NULL DEFAULT '',
    vars_json   LONGTEXT NOT NULL DEFAULT ('[]'),
    created_at  VARCHAR(32) NOT NULL,
    updated_at  VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_variable_groups_name ON variable_groups (name);
