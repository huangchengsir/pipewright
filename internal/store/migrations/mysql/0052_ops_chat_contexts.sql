CREATE TABLE IF NOT EXISTS ops_chat_contexts (
 context_key VARCHAR(80) PRIMARY KEY,
 session_id VARCHAR(36) NOT NULL,
 FOREIGN KEY (session_id) REFERENCES ops_chat_sessions(id) ON DELETE CASCADE
);
CREATE INDEX ops_chat_contexts_session ON ops_chat_contexts(session_id);
