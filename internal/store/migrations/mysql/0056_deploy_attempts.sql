-- Immutable source snapshots outlive source run/project/server retention.
CREATE TABLE deployment_attempts (
 id VARCHAR(64) PRIMARY KEY, run_id VARCHAR(64) NOT NULL, project_id VARCHAR(64) NOT NULL,
 created_at BIGINT NOT NULL, source_json TEXT NOT NULL,
 KEY deployment_attempts_run(run_id,created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE deployment_attempt_targets (
 id VARCHAR(64) PRIMARY KEY, attempt_id VARCHAR(64) NOT NULL, workload_key CHAR(64) NOT NULL,
 created_at BIGINT NOT NULL, sequence BIGINT NOT NULL DEFAULT 0,
 outcome VARCHAR(32) NOT NULL, target_json TEXT NOT NULL,
 KEY deployment_attempt_targets_attempt(attempt_id,created_at),
 KEY deployment_attempt_targets_workload(workload_key,created_at),
 FOREIGN KEY(attempt_id) REFERENCES deployment_attempts(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE deployment_attempt_receipts (
 id VARCHAR(64) PRIMARY KEY, target_id VARCHAR(64) NOT NULL, sequence BIGINT NOT NULL,
 kind VARCHAR(32) NOT NULL, created_at BIGINT NOT NULL, receipt_json TEXT NOT NULL,
 UNIQUE KEY deployment_attempt_receipts_sequence(target_id,sequence),
 FOREIGN KEY(target_id) REFERENCES deployment_attempt_targets(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
