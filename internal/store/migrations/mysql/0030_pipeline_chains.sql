-- 0030 pipeline_chains(MySQL):串联目标 + pipeline_runs 溯源列。
CREATE TABLE IF NOT EXISTS project_chain_targets (
    id                    VARCHAR(64) PRIMARY KEY,
    project_id            VARCHAR(64) NOT NULL,
    downstream_project_id VARCHAR(64) NOT NULL,
    branch                VARCHAR(255) NOT NULL DEFAULT '',
    enabled               TINYINT NOT NULL DEFAULT 1,
    created_at            VARCHAR(32) NOT NULL,
    updated_at            VARCHAR(32) NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    FOREIGN KEY (downstream_project_id) REFERENCES projects (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_project_chain_targets_project ON project_chain_targets (project_id);
CREATE UNIQUE INDEX uq_project_chain_targets ON project_chain_targets (project_id, downstream_project_id, branch);

ALTER TABLE pipeline_runs ADD COLUMN chain_source_run_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN chain_depth BIGINT NOT NULL DEFAULT 0;
