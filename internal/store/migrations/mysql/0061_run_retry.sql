-- Recovery ledgers deliberately outlive retained run rows; no run/project CASCADE.
CREATE TABLE run_retry_manifests (
 root_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin PRIMARY KEY, project_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 schema_version BIGINT NOT NULL, fingerprint VARCHAR(64) NOT NULL, ciphertext MEDIUMBLOB NOT NULL,
 created_at BIGINT NOT NULL
);
CREATE TABLE run_retry_sources (
 root_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin PRIMARY KEY, commit_sha VARCHAR(64) NOT NULL, conflicted BIGINT NOT NULL DEFAULT 0,
 first_attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, bound_at BIGINT NOT NULL
);
CREATE TABLE run_retry_lineages (
 identity_bytes BIGINT NOT NULL DEFAULT 0,
 root_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin PRIMARY KEY, project_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 head_attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, revision BIGINT NOT NULL,
 active_attempt_id VARCHAR(36), checkpoint_bytes BIGINT NOT NULL DEFAULT 0, updated_at BIGINT NOT NULL
);
CREATE TABLE run_retry_attempts (
 initiator VARCHAR(191) NOT NULL DEFAULT '',
 run_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin PRIMARY KEY, root_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 parent_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, revision BIGINT NOT NULL,
 execution_kind VARCHAR(64) NOT NULL, status VARCHAR(64) NOT NULL, created_at BIGINT NOT NULL,
 UNIQUE(root_id,revision)
);
CREATE INDEX run_retry_attempts_root ON run_retry_attempts(root_id,revision);
CREATE TABLE run_retry_nodes (
 attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, node_key VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 status VARCHAR(64) NOT NULL, origin VARCHAR(64) NOT NULL, source_attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 source_node_key VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, complete BIGINT NOT NULL DEFAULT 0,
 reusable BIGINT NOT NULL DEFAULT 0, valid_until BIGINT, ciphertext MEDIUMBLOB, owner_epoch VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
 reason_code VARCHAR(64) NOT NULL DEFAULT '', resources_drained BIGINT NOT NULL DEFAULT 0,
 step_id VARCHAR(36) NOT NULL DEFAULT '', step_ordinal BIGINT NOT NULL DEFAULT -1,
 updated_at BIGINT NOT NULL, PRIMARY KEY(attempt_id,node_key)
);
CREATE INDEX run_retry_nodes_source ON run_retry_nodes(source_attempt_id,source_node_key);
CREATE TABLE run_retry_requests (
 initiator VARCHAR(191) NOT NULL DEFAULT '',
 root_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, caller_scope VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 request_id VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, input_digest VARCHAR(64) NOT NULL,
 source_attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 state VARCHAR(64) NOT NULL, reason_code VARCHAR(64) NOT NULL DEFAULT '', created_at BIGINT NOT NULL,
 PRIMARY KEY(root_id,caller_scope,request_id)
);
CREATE INDEX run_retry_requests_attempt ON run_retry_requests(attempt_id);
CREATE TABLE run_retry_dispatch (
 attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin PRIMARY KEY, state VARCHAR(64) NOT NULL,
 owner_epoch VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, external_id VARCHAR(256) NOT NULL DEFAULT '',
 updated_at BIGINT NOT NULL
);
CREATE TABLE run_retry_refs (
 owner_attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, producer_attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 producer_node_key VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL, object_key VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 purpose VARCHAR(64) NOT NULL, created_at BIGINT NOT NULL, released_at BIGINT,
 PRIMARY KEY(owner_attempt_id,producer_attempt_id,producer_node_key,object_key)
);
CREATE INDEX run_retry_refs_producer ON run_retry_refs(producer_attempt_id,released_at);
CREATE INDEX run_retry_refs_object ON run_retry_refs(object_key,released_at);

CREATE TABLE run_retry_effects (
 ciphertext MEDIUMBLOB,
 root_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 effect_key VARCHAR(191) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 state VARCHAR(64) NOT NULL, operation_id VARCHAR(36) NOT NULL DEFAULT '',
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, PRIMARY KEY(root_id,effect_key)
);
CREATE TABLE run_retry_external_targets (
 id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin PRIMARY KEY,
 root_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 node_key VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 server_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 state VARCHAR(64) NOT NULL, ciphertext MEDIUMBLOB,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE(attempt_id,node_key,server_id)
);
CREATE INDEX run_retry_external_targets_root ON run_retry_external_targets(root_id,state);

CREATE TABLE run_retry_resources (
 id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin PRIMARY KEY,
 root_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 node_key VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 state VARCHAR(32) NOT NULL,ciphertext MEDIUMBLOB NOT NULL,updated_at BIGINT NOT NULL
);
CREATE INDEX run_retry_resources_attempt ON run_retry_resources(attempt_id,state);
CREATE TABLE run_retry_reader_exits (
 activity_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin PRIMARY KEY,
 attempt_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 owner_epoch VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,runtime_owner VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,exited_at BIGINT NOT NULL
);
CREATE INDEX run_retry_reader_exits_attempt ON run_retry_reader_exits(attempt_id);
