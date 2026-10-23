CREATE TABLE release_guard_plans (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, expires_at INTEGER NOT NULL,
 consumed_by TEXT NOT NULL DEFAULT '', plan_json TEXT NOT NULL
);
CREATE INDEX release_guard_plans_expiry ON release_guard_plans(expires_at);
CREATE TABLE release_guards (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, run_id TEXT NOT NULL,
 attempt_id TEXT NOT NULL, plan_id TEXT NOT NULL UNIQUE,
 idempotency_key TEXT NOT NULL, status TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL,
 evidence_expires_at INTEGER NOT NULL, report_expires_at INTEGER NOT NULL,
 record_json TEXT NOT NULL, UNIQUE(project_id,idempotency_key)
);
CREATE INDEX release_guards_run ON release_guards(run_id,created_at);
CREATE INDEX release_guards_expiry ON release_guards(report_expires_at);
CREATE TABLE release_guard_evidence (
 guard_id TEXT NOT NULL, sequence INTEGER NOT NULL, evidence_json TEXT NOT NULL,
 PRIMARY KEY(guard_id,sequence),
 FOREIGN KEY(guard_id) REFERENCES release_guards(id) ON DELETE CASCADE
);
CREATE TABLE release_guard_events (
 guard_id TEXT NOT NULL, sequence INTEGER NOT NULL, event_json TEXT NOT NULL,
 PRIMARY KEY(guard_id,sequence),
 FOREIGN KEY(guard_id) REFERENCES release_guards(id) ON DELETE CASCADE
);
CREATE TABLE release_guard_analyses (
 id TEXT PRIMARY KEY, guard_id TEXT NOT NULL, status TEXT NOT NULL,
 created_at INTEGER NOT NULL, analysis_json TEXT NOT NULL,
 FOREIGN KEY(guard_id) REFERENCES release_guards(id) ON DELETE CASCADE
);
