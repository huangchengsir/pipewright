-- 0004 projects(MySQL):被纳管项目,按引用绑定凭据(credential_id FK)。
CREATE TABLE IF NOT EXISTS projects (
    id             VARCHAR(64) PRIMARY KEY,
    name           VARCHAR(255) NOT NULL,
    repo_url       VARCHAR(1024) NOT NULL,
    default_branch VARCHAR(255) NOT NULL DEFAULT '',
    credential_id  VARCHAR(64) NOT NULL,
    created_at     VARCHAR(32) NOT NULL,
    updated_at     VARCHAR(32) NOT NULL,
    FOREIGN KEY (credential_id) REFERENCES credentials (id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_projects_credential_id ON projects (credential_id);
