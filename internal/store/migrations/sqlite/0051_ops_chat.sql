CREATE TABLE IF NOT EXISTS ops_chat_sessions (
 id TEXT PRIMARY KEY, revision INTEGER NOT NULL, seq INTEGER NOT NULL DEFAULT 0,
 visible_count INTEGER NOT NULL DEFAULT 0, body_bytes INTEGER NOT NULL DEFAULT 0,
 active_run TEXT NOT NULL DEFAULT '', body BLOB NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS ops_chat_runs (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES ops_chat_sessions(id) ON DELETE CASCADE,
 request_id TEXT NOT NULL UNIQUE, payload_hash TEXT NOT NULL, status TEXT NOT NULL,
 cancel_requested INTEGER NOT NULL DEFAULT 0, body BLOB NOT NULL, created_at BIGINT NOT NULL
);
CREATE INDEX ops_chat_runs_session ON ops_chat_runs(session_id,created_at);
CREATE TABLE IF NOT EXISTS ops_chat_calls (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES ops_chat_sessions(id) ON DELETE CASCADE,
 run_id TEXT NOT NULL REFERENCES ops_chat_runs(id) ON DELETE CASCADE,
 server_id TEXT NOT NULL, tool_id TEXT NOT NULL, status TEXT NOT NULL,
 ordinal INTEGER NOT NULL, args_hash TEXT NOT NULL, target_hash TEXT NOT NULL, body BLOB NOT NULL, created_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX ops_chat_calls_run ON ops_chat_calls(run_id,ordinal);
CREATE TABLE IF NOT EXISTS ops_chat_entries (
 session_id TEXT NOT NULL REFERENCES ops_chat_sessions(id) ON DELETE CASCADE, seq BIGINT NOT NULL,
 kind TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '', call_id TEXT NOT NULL DEFAULT '',
 context_allowed INTEGER NOT NULL DEFAULT 0, visible INTEGER NOT NULL DEFAULT 0,
 body BLOB NOT NULL, body_size INTEGER NOT NULL, created_at BIGINT NOT NULL,
 PRIMARY KEY(session_id,seq)
);
CREATE TABLE IF NOT EXISTS ops_chat_approvals (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES ops_chat_sessions(id) ON DELETE CASCADE,
 run_id TEXT NOT NULL UNIQUE REFERENCES ops_chat_runs(id) ON DELETE CASCADE,
 status TEXT NOT NULL, instance_id TEXT NOT NULL, expires_at BIGINT NOT NULL,
 body BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS ops_chat_state (
 id INTEGER PRIMARY KEY, active_session TEXT NOT NULL DEFAULT '',
 lease_owner TEXT NOT NULL DEFAULT '', lease_until BIGINT NOT NULL DEFAULT 0,
 keycheck BLOB NOT NULL
);
