-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE k8s_release_profiles (
 connection_id TEXT COLLATE "C" PRIMARY KEY , revision BIGINT NOT NULL, enabled BIGINT NOT NULL,
 profile_json TEXT COLLATE "C" NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id)
);
CREATE TABLE k8s_release_grants (
 id TEXT COLLATE "C" PRIMARY KEY , connection_id TEXT COLLATE "C" NOT NULL ,
 project_id TEXT COLLATE "C" NOT NULL , revision BIGINT NOT NULL, enabled BIGINT NOT NULL,
 grant_json TEXT COLLATE "C" NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id)
);
CREATE INDEX k8s_release_grants_scope ON k8s_release_grants(connection_id,project_id);
CREATE TABLE k8s_releases (
 id TEXT COLLATE "C" PRIMARY KEY , run_id TEXT COLLATE "C" NOT NULL ,
 rollback_of TEXT COLLATE "C" NOT NULL DEFAULT '' , reader_owner_epoch TEXT COLLATE "C" NOT NULL DEFAULT '' ,
 node_key TEXT COLLATE "C" NOT NULL , project_id TEXT COLLATE "C" NOT NULL ,
 connection_id TEXT COLLATE "C" NOT NULL , credential_id TEXT COLLATE "C" NOT NULL ,
 mode TEXT COLLATE "C" NOT NULL, owner_epoch TEXT COLLATE "C" NOT NULL , revision BIGINT NOT NULL,
 state TEXT COLLATE "C" NOT NULL, cleanup_state TEXT COLLATE "C" NOT NULL, owner_fenced BIGINT NOT NULL DEFAULT 0,
 plan_cipher BYTEA NOT NULL, plan_id TEXT COLLATE "C" NOT NULL , approved_id TEXT COLLATE "C" NOT NULL DEFAULT '',
 batch_cursor BIGINT NOT NULL DEFAULT 0, reason_code TEXT COLLATE "C" NOT NULL DEFAULT '',
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE(run_id,node_key), FOREIGN KEY(connection_id) REFERENCES k8s_connections(id),
 FOREIGN KEY(credential_id) REFERENCES k8s_credentials(id)
);
CREATE INDEX k8s_releases_run ON k8s_releases(run_id);
CREATE INDEX k8s_releases_rollback ON k8s_releases(rollback_of);
CREATE TABLE k8s_release_actions (
 id TEXT COLLATE "C" PRIMARY KEY , release_id TEXT COLLATE "C" NOT NULL ,
 step_key TEXT COLLATE "C" NOT NULL , resource_key TEXT COLLATE "C" NOT NULL ,
 owner_epoch TEXT COLLATE "C" NOT NULL , revision BIGINT NOT NULL,
 state TEXT COLLATE "C" NOT NULL, intent_cipher BYTEA NOT NULL, intent_id TEXT COLLATE "C" NOT NULL ,
 receipt_cipher BYTEA, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE(release_id,step_key), FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
);
CREATE INDEX k8s_release_actions_parent ON k8s_release_actions(release_id,state);
CREATE TABLE k8s_resource_gates (
 resource_key TEXT COLLATE "C" PRIMARY KEY , owner_kind TEXT COLLATE "C" NOT NULL,
 owner_id TEXT COLLATE "C" NOT NULL , owner_epoch TEXT COLLATE "C" NOT NULL ,
 created_at BIGINT NOT NULL
);
CREATE INDEX k8s_resource_gates_owner ON k8s_resource_gates(owner_kind,owner_id);
CREATE TABLE k8s_release_runtime (
 id TEXT COLLATE "C" PRIMARY KEY , release_id TEXT COLLATE "C" , owner_epoch TEXT COLLATE "C" NOT NULL ,
 revision BIGINT NOT NULL, cleanup_state TEXT COLLATE "C" NOT NULL, runtime_cipher BYTEA NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
);
CREATE INDEX k8s_release_runtime_parent ON k8s_release_runtime(release_id,cleanup_state);
CREATE TABLE k8s_release_continuations (
 id TEXT COLLATE "C" PRIMARY KEY , release_id TEXT COLLATE "C" NOT NULL ,
 run_id TEXT COLLATE "C" NOT NULL , release_revision BIGINT NOT NULL,
 action TEXT COLLATE "C" NOT NULL, state TEXT COLLATE "C" NOT NULL, owner_epoch TEXT COLLATE "C" NOT NULL DEFAULT '' , reader_owner_epoch TEXT COLLATE "C" NOT NULL DEFAULT '' , checkpoint_cipher BYTEA NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE(release_id,release_revision), FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
);
CREATE INDEX k8s_release_continuations_pending ON k8s_release_continuations(state,created_at);
CREATE TABLE k8s_config_payloads (
 id TEXT COLLATE "C" PRIMARY KEY , project_id TEXT COLLATE "C" NOT NULL ,
 node_key TEXT COLLATE "C" NOT NULL , ciphertext BYTEA NOT NULL, created_at BIGINT NOT NULL
);
CREATE INDEX k8s_config_payloads_node ON k8s_config_payloads(project_id,node_key);

CREATE TABLE k8s_release_rollback_requests (
 id TEXT COLLATE "C" PRIMARY KEY ,source_id TEXT COLLATE "C" NOT NULL ,source_revision BIGINT NOT NULL,source_plan_id TEXT COLLATE "C" NOT NULL ,
 release_id TEXT COLLATE "C" NOT NULL ,run_id TEXT COLLATE "C" NOT NULL ,created_at BIGINT NOT NULL,
 FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
);
