ALTER TABLE pipeline_configs ADD COLUMN history_version_id TEXT COLLATE "C";
ALTER TABLE pipeline_runs ADD COLUMN history_version_id TEXT COLLATE "C";
CREATE TABLE pipeline_config_versions (
 id TEXT COLLATE "C" PRIMARY KEY,
 project_id TEXT COLLATE "C" NOT NULL,
 version_seq BIGINT NOT NULL,
 created_at TEXT NOT NULL,
 actor TEXT NOT NULL,
 source TEXT NOT NULL,
 change_count BIGINT NOT NULL,
 changes_json TEXT NOT NULL,
 ciphertext BYTEA,
 FOREIGN KEY (project_id) REFERENCES pipeline_configs (project_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX pipeline_config_versions_sequence ON pipeline_config_versions (project_id, version_seq);
