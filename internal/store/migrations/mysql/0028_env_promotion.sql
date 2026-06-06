-- 0028 env_promotion(MySQL):环境链 + 逐环境变量 + 晋级记录。value 为保留字需反引号。
CREATE TABLE IF NOT EXISTS project_environments (
    project_id  VARCHAR(64) PRIMARY KEY,
    chain_json  LONGTEXT NOT NULL DEFAULT ('[]'),
    updated_at  VARCHAR(32) NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS environment_variables (
    id            VARCHAR(64) PRIMARY KEY,
    project_id    VARCHAR(64) NOT NULL,
    environment   VARCHAR(255) NOT NULL,
    var_key       VARCHAR(255) NOT NULL,
    `value`       LONGTEXT NOT NULL DEFAULT (''),
    is_secret     TINYINT NOT NULL DEFAULT 0,
    credential_id VARCHAR(64) NOT NULL DEFAULT '',
    updated_at    VARCHAR(32) NOT NULL,
    UNIQUE (project_id, environment, var_key),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_env_vars_proj_env ON environment_variables (project_id, environment);

CREATE TABLE IF NOT EXISTS run_promotions (
    id                 VARCHAR(64) PRIMARY KEY,
    project_id         VARCHAR(64) NOT NULL,
    source_run_id      VARCHAR(64) NOT NULL,
    from_environment   VARCHAR(255) NOT NULL DEFAULT '',
    target_environment VARCHAR(255) NOT NULL,
    status             VARCHAR(255) NOT NULL DEFAULT 'pending',
    approval_stage     VARCHAR(255) NOT NULL DEFAULT '',
    promoted_by        VARCHAR(255) NOT NULL DEFAULT '',
    created_at         VARCHAR(32) NOT NULL,
    decided_at         VARCHAR(32) NOT NULL DEFAULT '',
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    FOREIGN KEY (source_run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_run_promotions_proj ON run_promotions (project_id);
CREATE INDEX idx_run_promotions_run ON run_promotions (source_run_id);
