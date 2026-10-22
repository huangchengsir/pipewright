-- Snapshots intentionally survive source run/project/server retention. Only
-- receipt ownership cascades; there is no automatic remote replay on recovery.
CREATE TABLE deployment_attempts (
 id TEXT PRIMARY KEY, run_id TEXT NOT NULL, project_id TEXT NOT NULL,
 created_at INTEGER NOT NULL, source_json TEXT NOT NULL
);
CREATE INDEX deployment_attempts_run ON deployment_attempts(run_id,created_at);
CREATE TABLE deployment_attempt_targets (
 id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL, workload_key TEXT NOT NULL,
 created_at INTEGER NOT NULL, sequence INTEGER NOT NULL DEFAULT 0,
 outcome TEXT NOT NULL, target_json TEXT NOT NULL,
 FOREIGN KEY(attempt_id) REFERENCES deployment_attempts(id) ON DELETE CASCADE
);
CREATE INDEX deployment_attempt_targets_attempt ON deployment_attempt_targets(attempt_id,created_at);
CREATE INDEX deployment_attempt_targets_workload ON deployment_attempt_targets(workload_key,created_at);
CREATE TABLE deployment_attempt_receipts (
 id TEXT PRIMARY KEY, target_id TEXT NOT NULL, sequence INTEGER NOT NULL,
 kind TEXT NOT NULL, created_at INTEGER NOT NULL, receipt_json TEXT NOT NULL,
 UNIQUE(target_id,sequence),
 FOREIGN KEY(target_id) REFERENCES deployment_attempt_targets(id) ON DELETE CASCADE
);
