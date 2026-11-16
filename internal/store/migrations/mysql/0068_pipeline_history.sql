ALTER TABLE pipeline_configs ADD COLUMN history_version_id VARCHAR(64) NULL;
ALTER TABLE pipeline_runs ADD COLUMN history_version_id VARCHAR(64) NULL;
CREATE TABLE IF NOT EXISTS pipeline_config_versions (
 id VARCHAR(64) PRIMARY KEY,
 project_id VARCHAR(64) NOT NULL,
 version_seq BIGINT NOT NULL,
 created_at VARCHAR(32) NOT NULL,
 actor VARCHAR(255) NOT NULL,
 source VARCHAR(32) NOT NULL,
 change_count BIGINT NOT NULL,
 changes_json LONGTEXT NOT NULL,
 ciphertext LONGBLOB,
 UNIQUE (project_id, version_seq),
 FOREIGN KEY (project_id) REFERENCES pipeline_configs (project_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
