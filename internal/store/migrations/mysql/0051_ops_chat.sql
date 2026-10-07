CREATE TABLE IF NOT EXISTS ops_chat_sessions (
 id VARCHAR(36) PRIMARY KEY, revision INTEGER NOT NULL, seq INTEGER NOT NULL DEFAULT 0,
 visible_count INTEGER NOT NULL DEFAULT 0, body_bytes INTEGER NOT NULL DEFAULT 0,
 active_run VARCHAR(36) NOT NULL DEFAULT '', body MEDIUMBLOB NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS ops_chat_runs (
 id VARCHAR(36) PRIMARY KEY, session_id VARCHAR(36) NOT NULL, FOREIGN KEY (session_id) REFERENCES ops_chat_sessions(id) ON DELETE CASCADE,
 request_id VARCHAR(36) NOT NULL UNIQUE, payload_hash VARCHAR(64) NOT NULL, status VARCHAR(40) NOT NULL,
 cancel_requested INTEGER NOT NULL DEFAULT 0, body MEDIUMBLOB NOT NULL, created_at BIGINT NOT NULL
);
CREATE INDEX ops_chat_runs_session ON ops_chat_runs(session_id,created_at);
CREATE TABLE IF NOT EXISTS ops_chat_calls (
 id VARCHAR(36) PRIMARY KEY, session_id VARCHAR(36) NOT NULL, FOREIGN KEY (session_id) REFERENCES ops_chat_sessions(id) ON DELETE CASCADE,
 run_id VARCHAR(36) NOT NULL, FOREIGN KEY (run_id) REFERENCES ops_chat_runs(id) ON DELETE CASCADE,
 server_id VARCHAR(36) NOT NULL, tool_id VARCHAR(40) NOT NULL, status VARCHAR(40) NOT NULL,
 ordinal INTEGER NOT NULL, args_hash VARCHAR(64) NOT NULL, target_hash VARCHAR(64) NOT NULL, body MEDIUMBLOB NOT NULL, created_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX ops_chat_calls_run ON ops_chat_calls(run_id,ordinal);
CREATE TABLE IF NOT EXISTS ops_chat_entries (
 session_id VARCHAR(36) NOT NULL, FOREIGN KEY (session_id) REFERENCES ops_chat_sessions(id) ON DELETE CASCADE, seq BIGINT NOT NULL,
 kind VARCHAR(40) NOT NULL, run_id VARCHAR(36) NOT NULL DEFAULT '', call_id VARCHAR(36) NOT NULL DEFAULT '',
 context_allowed INTEGER NOT NULL DEFAULT 0, visible INTEGER NOT NULL DEFAULT 0,
 body MEDIUMBLOB NOT NULL, body_size INTEGER NOT NULL, created_at BIGINT NOT NULL,
 PRIMARY KEY(session_id,seq)
);
CREATE TABLE IF NOT EXISTS ops_chat_approvals (
 id VARCHAR(36) PRIMARY KEY, session_id VARCHAR(36) NOT NULL, FOREIGN KEY (session_id) REFERENCES ops_chat_sessions(id) ON DELETE CASCADE,
 run_id VARCHAR(36) NOT NULL UNIQUE, FOREIGN KEY (run_id) REFERENCES ops_chat_runs(id) ON DELETE CASCADE,
 status VARCHAR(40) NOT NULL, instance_id VARCHAR(36) NOT NULL, expires_at BIGINT NOT NULL,
 body MEDIUMBLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS ops_chat_state (
 id INTEGER PRIMARY KEY, active_session VARCHAR(36) NOT NULL DEFAULT '',
 lease_owner VARCHAR(36) NOT NULL DEFAULT '', lease_until BIGINT NOT NULL DEFAULT 0,
 keycheck BLOB NOT NULL
);
