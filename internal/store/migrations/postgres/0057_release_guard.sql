-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE release_guard_plans (
 id TEXT COLLATE "C" PRIMARY KEY, project_id TEXT COLLATE "C" NOT NULL, expires_at BIGINT NOT NULL,
 consumed_by TEXT COLLATE "C" NOT NULL DEFAULT '', plan_json TEXT COLLATE "C" NOT NULL
);
CREATE INDEX release_guard_plans_expiry ON release_guard_plans(expires_at);
CREATE TABLE release_guards (
 id TEXT COLLATE "C" PRIMARY KEY, project_id TEXT COLLATE "C" NOT NULL, run_id TEXT COLLATE "C" NOT NULL,
 attempt_id TEXT COLLATE "C" NOT NULL, plan_id TEXT COLLATE "C" NOT NULL UNIQUE,
 idempotency_key TEXT COLLATE "C" NOT NULL, status TEXT COLLATE "C" NOT NULL,
 revision BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL,
 evidence_expires_at BIGINT NOT NULL, report_expires_at BIGINT NOT NULL,
 record_json TEXT COLLATE "C" NOT NULL, UNIQUE(project_id,idempotency_key)
);
CREATE INDEX release_guards_run ON release_guards(run_id,created_at);
CREATE INDEX release_guards_expiry ON release_guards(report_expires_at);
CREATE TABLE release_guard_evidence (
 guard_id TEXT COLLATE "C" NOT NULL, sequence BIGINT NOT NULL, evidence_json TEXT COLLATE "C" NOT NULL,
 PRIMARY KEY(guard_id,sequence),
 FOREIGN KEY(guard_id) REFERENCES release_guards(id) ON DELETE CASCADE
);
CREATE TABLE release_guard_events (
 guard_id TEXT COLLATE "C" NOT NULL, sequence BIGINT NOT NULL, event_json TEXT COLLATE "C" NOT NULL,
 PRIMARY KEY(guard_id,sequence),
 FOREIGN KEY(guard_id) REFERENCES release_guards(id) ON DELETE CASCADE
);
CREATE TABLE release_guard_analyses (
 id TEXT COLLATE "C" PRIMARY KEY, guard_id TEXT COLLATE "C" NOT NULL, status TEXT COLLATE "C" NOT NULL,
 created_at BIGINT NOT NULL, analysis_json TEXT COLLATE "C" NOT NULL,
 FOREIGN KEY(guard_id) REFERENCES release_guards(id) ON DELETE CASCADE
);
