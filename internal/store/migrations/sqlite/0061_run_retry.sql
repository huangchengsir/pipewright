-- Recovery ledgers deliberately outlive retained run rows; no run/project CASCADE.
CREATE TABLE run_retry_manifests (
 root_id TEXT PRIMARY KEY COLLATE BINARY, project_id TEXT NOT NULL COLLATE BINARY,
 schema_version INTEGER NOT NULL, fingerprint TEXT NOT NULL, ciphertext BLOB NOT NULL,
 created_at INTEGER NOT NULL
);
CREATE TABLE run_retry_sources (
 root_id TEXT PRIMARY KEY COLLATE BINARY, commit_sha TEXT NOT NULL, conflicted INTEGER NOT NULL DEFAULT 0,
 first_attempt_id TEXT NOT NULL COLLATE BINARY, bound_at INTEGER NOT NULL
);
CREATE TABLE run_retry_lineages (
 identity_bytes INTEGER NOT NULL DEFAULT 0,
 root_id TEXT PRIMARY KEY COLLATE BINARY, project_id TEXT NOT NULL COLLATE BINARY,
 head_attempt_id TEXT NOT NULL COLLATE BINARY, revision INTEGER NOT NULL,
 active_attempt_id TEXT, checkpoint_bytes INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL
);
CREATE TABLE run_retry_attempts (
 initiator TEXT NOT NULL DEFAULT '',
 run_id TEXT PRIMARY KEY COLLATE BINARY, root_id TEXT NOT NULL COLLATE BINARY,
 parent_id TEXT NOT NULL COLLATE BINARY, revision INTEGER NOT NULL,
 execution_kind TEXT NOT NULL, status TEXT NOT NULL, created_at INTEGER NOT NULL,
 UNIQUE(root_id,revision)
);
CREATE INDEX run_retry_attempts_root ON run_retry_attempts(root_id,revision);
CREATE TABLE run_retry_nodes (
 attempt_id TEXT NOT NULL COLLATE BINARY, node_key TEXT NOT NULL COLLATE BINARY,
 status TEXT NOT NULL, origin TEXT NOT NULL, source_attempt_id TEXT NOT NULL COLLATE BINARY,
 source_node_key TEXT NOT NULL COLLATE BINARY, complete INTEGER NOT NULL DEFAULT 0,
 reusable INTEGER NOT NULL DEFAULT 0, valid_until INTEGER, ciphertext BLOB, owner_epoch TEXT NOT NULL DEFAULT '',
 reason_code TEXT NOT NULL DEFAULT '', resources_drained INTEGER NOT NULL DEFAULT 0,
 step_id TEXT NOT NULL DEFAULT '', step_ordinal INTEGER NOT NULL DEFAULT -1,
 updated_at INTEGER NOT NULL, PRIMARY KEY(attempt_id,node_key)
);
CREATE INDEX run_retry_nodes_source ON run_retry_nodes(source_attempt_id,source_node_key);
CREATE TABLE run_retry_requests (
 initiator TEXT NOT NULL DEFAULT '',
 root_id TEXT NOT NULL COLLATE BINARY, caller_scope TEXT NOT NULL COLLATE BINARY,
 request_id TEXT NOT NULL COLLATE BINARY, input_digest TEXT NOT NULL,
 source_attempt_id TEXT NOT NULL COLLATE BINARY, attempt_id TEXT NOT NULL COLLATE BINARY,
 state TEXT NOT NULL, reason_code TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
 PRIMARY KEY(root_id,caller_scope,request_id)
);
CREATE INDEX run_retry_requests_attempt ON run_retry_requests(attempt_id);
CREATE TABLE run_retry_dispatch (
 attempt_id TEXT PRIMARY KEY COLLATE BINARY, state TEXT NOT NULL,
 owner_epoch TEXT NOT NULL COLLATE BINARY, external_id TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL
);
CREATE TABLE run_retry_refs (
 owner_attempt_id TEXT NOT NULL COLLATE BINARY, producer_attempt_id TEXT NOT NULL COLLATE BINARY,
 producer_node_key TEXT NOT NULL COLLATE BINARY, object_key TEXT NOT NULL COLLATE BINARY,
 purpose TEXT NOT NULL, created_at INTEGER NOT NULL, released_at INTEGER,
 PRIMARY KEY(owner_attempt_id,producer_attempt_id,producer_node_key,object_key)
);
CREATE INDEX run_retry_refs_producer ON run_retry_refs(producer_attempt_id,released_at);
CREATE INDEX run_retry_refs_object ON run_retry_refs(object_key,released_at);

CREATE TABLE run_retry_effects (
 ciphertext BLOB,
 root_id TEXT NOT NULL COLLATE BINARY, effect_key TEXT NOT NULL COLLATE BINARY,
 attempt_id TEXT NOT NULL COLLATE BINARY, state TEXT NOT NULL, operation_id TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY(root_id,effect_key)
);
CREATE TABLE run_retry_external_targets (
 id TEXT PRIMARY KEY COLLATE BINARY, root_id TEXT NOT NULL COLLATE BINARY,
 node_key TEXT NOT NULL COLLATE BINARY, attempt_id TEXT NOT NULL COLLATE BINARY,
 server_id TEXT NOT NULL COLLATE BINARY, state TEXT NOT NULL, ciphertext BLOB,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(attempt_id,node_key,server_id)
);
CREATE INDEX run_retry_external_targets_root ON run_retry_external_targets(root_id,state);

CREATE TABLE run_retry_resources (
 id TEXT PRIMARY KEY COLLATE BINARY,root_id TEXT NOT NULL COLLATE BINARY,
 attempt_id TEXT NOT NULL COLLATE BINARY,node_key TEXT NOT NULL COLLATE BINARY,
 state TEXT NOT NULL,ciphertext BLOB NOT NULL,updated_at INTEGER NOT NULL
);
CREATE INDEX run_retry_resources_attempt ON run_retry_resources(attempt_id,state);
CREATE TABLE run_retry_reader_exits (
 activity_id TEXT PRIMARY KEY COLLATE BINARY,attempt_id TEXT NOT NULL COLLATE BINARY,
 owner_epoch TEXT NOT NULL COLLATE BINARY,runtime_owner TEXT NOT NULL COLLATE BINARY,exited_at INTEGER NOT NULL
);
CREATE INDEX run_retry_reader_exits_attempt ON run_retry_reader_exits(attempt_id);
