-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE run_retry_manifests (
 root_id TEXT COLLATE "C" PRIMARY KEY , project_id TEXT COLLATE "C" NOT NULL ,
 schema_version BIGINT NOT NULL, fingerprint TEXT COLLATE "C" NOT NULL, ciphertext BYTEA NOT NULL,
 created_at BIGINT NOT NULL
);
CREATE TABLE run_retry_sources (
 root_id TEXT COLLATE "C" PRIMARY KEY , commit_sha TEXT COLLATE "C" NOT NULL, conflicted BIGINT NOT NULL DEFAULT 0,
 first_attempt_id TEXT COLLATE "C" NOT NULL , bound_at BIGINT NOT NULL
);
CREATE TABLE run_retry_lineages (
 identity_bytes BIGINT NOT NULL DEFAULT 0,
 root_id TEXT COLLATE "C" PRIMARY KEY , project_id TEXT COLLATE "C" NOT NULL ,
 head_attempt_id TEXT COLLATE "C" NOT NULL , revision BIGINT NOT NULL,
 active_attempt_id TEXT COLLATE "C", checkpoint_bytes BIGINT NOT NULL DEFAULT 0, updated_at BIGINT NOT NULL
);
CREATE TABLE run_retry_attempts (
 initiator TEXT COLLATE "C" NOT NULL DEFAULT '',
 run_id TEXT COLLATE "C" PRIMARY KEY , root_id TEXT COLLATE "C" NOT NULL ,
 parent_id TEXT COLLATE "C" NOT NULL , revision BIGINT NOT NULL,
 execution_kind TEXT COLLATE "C" NOT NULL, status TEXT COLLATE "C" NOT NULL, created_at BIGINT NOT NULL,
 UNIQUE(root_id,revision)
);
CREATE INDEX run_retry_attempts_root ON run_retry_attempts(root_id,revision);
CREATE TABLE run_retry_nodes (
 attempt_id TEXT COLLATE "C" NOT NULL , node_key TEXT COLLATE "C" NOT NULL ,
 status TEXT COLLATE "C" NOT NULL, origin TEXT COLLATE "C" NOT NULL, source_attempt_id TEXT COLLATE "C" NOT NULL ,
 source_node_key TEXT COLLATE "C" NOT NULL , complete BIGINT NOT NULL DEFAULT 0,
 reusable BIGINT NOT NULL DEFAULT 0, valid_until BIGINT, ciphertext BYTEA, owner_epoch TEXT COLLATE "C" NOT NULL DEFAULT '',
 reason_code TEXT COLLATE "C" NOT NULL DEFAULT '', resources_drained BIGINT NOT NULL DEFAULT 0,
 step_id TEXT COLLATE "C" NOT NULL DEFAULT '', step_ordinal BIGINT NOT NULL DEFAULT -1,
 updated_at BIGINT NOT NULL, PRIMARY KEY(attempt_id,node_key)
);
CREATE INDEX run_retry_nodes_source ON run_retry_nodes(source_attempt_id,source_node_key);
CREATE TABLE run_retry_requests (
 initiator TEXT COLLATE "C" NOT NULL DEFAULT '',
 root_id TEXT COLLATE "C" NOT NULL , caller_scope TEXT COLLATE "C" NOT NULL ,
 request_id TEXT COLLATE "C" NOT NULL , input_digest TEXT COLLATE "C" NOT NULL,
 source_attempt_id TEXT COLLATE "C" NOT NULL , attempt_id TEXT COLLATE "C" NOT NULL ,
 state TEXT COLLATE "C" NOT NULL, reason_code TEXT COLLATE "C" NOT NULL DEFAULT '', created_at BIGINT NOT NULL,
 PRIMARY KEY(root_id,caller_scope,request_id)
);
CREATE INDEX run_retry_requests_attempt ON run_retry_requests(attempt_id);
CREATE TABLE run_retry_dispatch (
 attempt_id TEXT COLLATE "C" PRIMARY KEY , state TEXT COLLATE "C" NOT NULL,
 owner_epoch TEXT COLLATE "C" NOT NULL , external_id TEXT COLLATE "C" NOT NULL DEFAULT '',
 updated_at BIGINT NOT NULL
);
CREATE TABLE run_retry_refs (
 owner_attempt_id TEXT COLLATE "C" NOT NULL , producer_attempt_id TEXT COLLATE "C" NOT NULL ,
 producer_node_key TEXT COLLATE "C" NOT NULL , object_key TEXT COLLATE "C" NOT NULL ,
 purpose TEXT COLLATE "C" NOT NULL, created_at BIGINT NOT NULL, released_at BIGINT,
 PRIMARY KEY(owner_attempt_id,producer_attempt_id,producer_node_key,object_key)
);
CREATE INDEX run_retry_refs_producer ON run_retry_refs(producer_attempt_id,released_at);
CREATE INDEX run_retry_refs_object ON run_retry_refs(object_key,released_at);

CREATE TABLE run_retry_effects (
 ciphertext BYTEA,
 root_id TEXT COLLATE "C" NOT NULL , effect_key TEXT COLLATE "C" NOT NULL ,
 attempt_id TEXT COLLATE "C" NOT NULL , state TEXT COLLATE "C" NOT NULL, operation_id TEXT COLLATE "C" NOT NULL DEFAULT '',
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, PRIMARY KEY(root_id,effect_key)
);
CREATE TABLE run_retry_external_targets (
 id TEXT COLLATE "C" PRIMARY KEY , root_id TEXT COLLATE "C" NOT NULL ,
 node_key TEXT COLLATE "C" NOT NULL , attempt_id TEXT COLLATE "C" NOT NULL ,
 server_id TEXT COLLATE "C" NOT NULL , state TEXT COLLATE "C" NOT NULL, ciphertext BYTEA,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE(attempt_id,node_key,server_id)
);
CREATE INDEX run_retry_external_targets_root ON run_retry_external_targets(root_id,state);

CREATE TABLE run_retry_resources (
 id TEXT COLLATE "C" PRIMARY KEY ,root_id TEXT COLLATE "C" NOT NULL ,
 attempt_id TEXT COLLATE "C" NOT NULL ,node_key TEXT COLLATE "C" NOT NULL ,
 state TEXT COLLATE "C" NOT NULL,ciphertext BYTEA NOT NULL,updated_at BIGINT NOT NULL
);
CREATE INDEX run_retry_resources_attempt ON run_retry_resources(attempt_id,state);
CREATE TABLE run_retry_reader_exits (
 activity_id TEXT COLLATE "C" PRIMARY KEY ,attempt_id TEXT COLLATE "C" NOT NULL ,
 owner_epoch TEXT COLLATE "C" NOT NULL ,runtime_owner TEXT COLLATE "C" NOT NULL ,exited_at BIGINT NOT NULL
);
CREATE INDEX run_retry_reader_exits_attempt ON run_retry_reader_exits(attempt_id);
