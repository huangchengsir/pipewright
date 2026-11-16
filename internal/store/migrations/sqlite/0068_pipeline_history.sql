ALTER TABLE pipeline_configs ADD COLUMN history_version_id TEXT;
ALTER TABLE pipeline_runs ADD COLUMN history_version_id TEXT;
CREATE TABLE pipeline_config_versions (
 id TEXT PRIMARY KEY NOT NULL,
 project_id TEXT NOT NULL,
 version_seq INTEGER NOT NULL,
 created_at TEXT NOT NULL,
 actor TEXT NOT NULL,
 source TEXT NOT NULL,
 change_count INTEGER NOT NULL,
 changes_json TEXT NOT NULL,
 ciphertext BLOB,
 FOREIGN KEY (project_id) REFERENCES pipeline_configs (project_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX pipeline_config_versions_sequence ON pipeline_config_versions (project_id, version_seq);
