-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE mcp_tokens (
    id TEXT COLLATE "C" PRIMARY KEY,
    name TEXT COLLATE "C" NOT NULL,
    token_hash TEXT COLLATE "C" NOT NULL UNIQUE,
    project_ids_json TEXT COLLATE "C" NOT NULL,
    can_trigger BIGINT NOT NULL DEFAULT 0,
    created_at TEXT COLLATE "C" NOT NULL,
    expires_at TEXT COLLATE "C" NOT NULL,
    revoked_at TEXT COLLATE "C"
);


CREATE TABLE mcp_run_requests (
    scope TEXT COLLATE "C" NOT NULL,
    request_id TEXT COLLATE "C" NOT NULL,
    fingerprint TEXT COLLATE "C" NOT NULL,
    run_id TEXT COLLATE "C" NOT NULL UNIQUE,
    dispatch_state TEXT COLLATE "C" NOT NULL DEFAULT 'unknown',
    PRIMARY KEY (scope, request_id)
);
