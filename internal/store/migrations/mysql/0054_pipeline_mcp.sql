CREATE TABLE mcp_tokens (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    token_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    project_ids_json TEXT NOT NULL,
    can_trigger TINYINT NOT NULL DEFAULT 0,
    created_at VARCHAR(64) NOT NULL,
    expires_at VARCHAR(64) NOT NULL,
    revoked_at VARCHAR(64) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- No run FK: retaining this tombstone prevents replay after run retention.
CREATE TABLE mcp_run_requests (
    scope VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    run_id VARCHAR(36) NOT NULL UNIQUE,
    dispatch_state VARCHAR(16) NOT NULL DEFAULT 'unknown',
    PRIMARY KEY (scope, request_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
