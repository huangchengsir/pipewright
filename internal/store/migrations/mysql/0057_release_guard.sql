CREATE TABLE release_guard_plans (
 id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 project_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 expires_at BIGINT NOT NULL, consumed_by VARCHAR(36) NOT NULL DEFAULT '', plan_json MEDIUMTEXT NOT NULL,
 INDEX release_guard_plans_expiry(expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE release_guards (
 id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 project_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 run_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 attempt_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 plan_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
 idempotency_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 status VARCHAR(32) NOT NULL, revision BIGINT NOT NULL DEFAULT 0,
 created_at BIGINT NOT NULL, evidence_expires_at BIGINT NOT NULL, report_expires_at BIGINT NOT NULL,
 record_json MEDIUMTEXT NOT NULL, UNIQUE KEY release_guard_request(project_id,idempotency_key),
 INDEX release_guards_run(run_id,created_at), INDEX release_guards_expiry(report_expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE release_guard_evidence (
 guard_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 sequence BIGINT NOT NULL, evidence_json MEDIUMTEXT NOT NULL,
 PRIMARY KEY(guard_id,sequence),
 FOREIGN KEY(guard_id) REFERENCES release_guards(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE release_guard_events (
 guard_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 sequence BIGINT NOT NULL, event_json TEXT NOT NULL,
 PRIMARY KEY(guard_id,sequence),
 FOREIGN KEY(guard_id) REFERENCES release_guards(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE release_guard_analyses (
 id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 guard_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 status VARCHAR(32) NOT NULL, created_at BIGINT NOT NULL, analysis_json MEDIUMTEXT NOT NULL,
 FOREIGN KEY(guard_id) REFERENCES release_guards(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
