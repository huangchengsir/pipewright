-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS ops_chat_sessions (
 id TEXT COLLATE "C" PRIMARY KEY, revision BIGINT NOT NULL, seq BIGINT NOT NULL DEFAULT 0,
 visible_count BIGINT NOT NULL DEFAULT 0, body_bytes BIGINT NOT NULL DEFAULT 0,
 active_run TEXT COLLATE "C" NOT NULL DEFAULT '', body BYTEA NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS ops_chat_runs (
 id TEXT COLLATE "C" PRIMARY KEY, session_id TEXT COLLATE "C" NOT NULL REFERENCES ops_chat_sessions(id) ON DELETE CASCADE,
 request_id TEXT COLLATE "C" NOT NULL UNIQUE, payload_hash TEXT COLLATE "C" NOT NULL, status TEXT COLLATE "C" NOT NULL,
 cancel_requested BIGINT NOT NULL DEFAULT 0, body BYTEA NOT NULL, created_at BIGINT NOT NULL
);
CREATE INDEX ops_chat_runs_session ON ops_chat_runs(session_id,created_at);
CREATE TABLE IF NOT EXISTS ops_chat_calls (
 id TEXT COLLATE "C" PRIMARY KEY, session_id TEXT COLLATE "C" NOT NULL REFERENCES ops_chat_sessions(id) ON DELETE CASCADE,
 run_id TEXT COLLATE "C" NOT NULL REFERENCES ops_chat_runs(id) ON DELETE CASCADE,
 server_id TEXT COLLATE "C" NOT NULL, tool_id TEXT COLLATE "C" NOT NULL, status TEXT COLLATE "C" NOT NULL,
 ordinal BIGINT NOT NULL, args_hash TEXT COLLATE "C" NOT NULL, target_hash TEXT COLLATE "C" NOT NULL, body BYTEA NOT NULL, created_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX ops_chat_calls_run ON ops_chat_calls(run_id,ordinal);
CREATE TABLE IF NOT EXISTS ops_chat_entries (
 session_id TEXT COLLATE "C" NOT NULL REFERENCES ops_chat_sessions(id) ON DELETE CASCADE, seq BIGINT NOT NULL,
 kind TEXT COLLATE "C" NOT NULL, run_id TEXT COLLATE "C" NOT NULL DEFAULT '', call_id TEXT COLLATE "C" NOT NULL DEFAULT '',
 context_allowed BIGINT NOT NULL DEFAULT 0, visible BIGINT NOT NULL DEFAULT 0,
 body BYTEA NOT NULL, body_size BIGINT NOT NULL, created_at BIGINT NOT NULL,
 PRIMARY KEY(session_id,seq)
);
CREATE TABLE IF NOT EXISTS ops_chat_approvals (
 id TEXT COLLATE "C" PRIMARY KEY, session_id TEXT COLLATE "C" NOT NULL REFERENCES ops_chat_sessions(id) ON DELETE CASCADE,
 run_id TEXT COLLATE "C" NOT NULL UNIQUE REFERENCES ops_chat_runs(id) ON DELETE CASCADE,
 status TEXT COLLATE "C" NOT NULL, instance_id TEXT COLLATE "C" NOT NULL, expires_at BIGINT NOT NULL,
 body BYTEA NOT NULL
);
CREATE TABLE IF NOT EXISTS ops_chat_state (
 id BIGINT PRIMARY KEY, active_session TEXT COLLATE "C" NOT NULL DEFAULT '',
 lease_owner TEXT COLLATE "C" NOT NULL DEFAULT '', lease_until BIGINT NOT NULL DEFAULT 0,
 keycheck BYTEA NOT NULL
);
