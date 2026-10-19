CREATE TABLE mcp_tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    project_ids_json TEXT NOT NULL,
    can_trigger INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT
);

-- A tombstone survives run retention; retries must never execute again.
CREATE TABLE mcp_run_requests (
    scope TEXT NOT NULL,
    request_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    run_id TEXT NOT NULL UNIQUE,
    dispatch_state TEXT NOT NULL DEFAULT 'unknown',
    PRIMARY KEY (scope, request_id)
);
