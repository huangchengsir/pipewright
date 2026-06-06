-- 0005 pipeline_triggers(MySQL):每项目触发设置;签名密钥仅以密文 BLOB 入库。
CREATE TABLE IF NOT EXISTS pipeline_triggers (
    project_id                VARCHAR(64) PRIMARY KEY,
    webhook_token             VARCHAR(191) NOT NULL UNIQUE,
    webhook_secret_ciphertext BLOB NOT NULL,
    events_json               LONGTEXT NOT NULL DEFAULT ('{}'),
    branch_mappings_json      LONGTEXT NOT NULL DEFAULT ('[]'),
    unmatched_policy          VARCHAR(255) NOT NULL DEFAULT 'record',
    created_at                VARCHAR(32) NOT NULL,
    updated_at                VARCHAR(32) NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
