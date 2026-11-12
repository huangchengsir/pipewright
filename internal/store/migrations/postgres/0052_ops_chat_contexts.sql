-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS ops_chat_contexts (
 context_key TEXT COLLATE "C" PRIMARY KEY,
 session_id TEXT COLLATE "C" NOT NULL REFERENCES ops_chat_sessions(id) ON DELETE CASCADE
);
CREATE INDEX ops_chat_contexts_session ON ops_chat_contexts(session_id);
