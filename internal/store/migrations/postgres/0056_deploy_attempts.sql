-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE deployment_attempts (
 id TEXT COLLATE "C" PRIMARY KEY, run_id TEXT COLLATE "C" NOT NULL, project_id TEXT COLLATE "C" NOT NULL,
 created_at BIGINT NOT NULL, source_json TEXT COLLATE "C" NOT NULL
);
CREATE INDEX deployment_attempts_run ON deployment_attempts(run_id,created_at);
CREATE TABLE deployment_attempt_targets (
 id TEXT COLLATE "C" PRIMARY KEY, attempt_id TEXT COLLATE "C" NOT NULL, workload_key TEXT COLLATE "C" NOT NULL,
 created_at BIGINT NOT NULL, sequence BIGINT NOT NULL DEFAULT 0,
 outcome TEXT COLLATE "C" NOT NULL, target_json TEXT COLLATE "C" NOT NULL,
 FOREIGN KEY(attempt_id) REFERENCES deployment_attempts(id) ON DELETE CASCADE
);
CREATE INDEX deployment_attempt_targets_attempt ON deployment_attempt_targets(attempt_id,created_at);
CREATE INDEX deployment_attempt_targets_workload ON deployment_attempt_targets(workload_key,created_at);
CREATE TABLE deployment_attempt_receipts (
 id TEXT COLLATE "C" PRIMARY KEY, target_id TEXT COLLATE "C" NOT NULL, sequence BIGINT NOT NULL,
 kind TEXT COLLATE "C" NOT NULL, created_at BIGINT NOT NULL, receipt_json TEXT COLLATE "C" NOT NULL,
 UNIQUE(target_id,sequence),
 FOREIGN KEY(target_id) REFERENCES deployment_attempt_targets(id) ON DELETE CASCADE
);
