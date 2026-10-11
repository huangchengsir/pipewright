CREATE TABLE IF NOT EXISTS ops_chat_contexts (
 context_key TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES ops_chat_sessions(id) ON DELETE CASCADE
);
CREATE INDEX ops_chat_contexts_session ON ops_chat_contexts(session_id);
