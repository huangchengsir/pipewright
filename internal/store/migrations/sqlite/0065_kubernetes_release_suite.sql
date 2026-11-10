CREATE TABLE k8s_release_profiles (
 connection_id TEXT PRIMARY KEY COLLATE BINARY, revision INTEGER NOT NULL, enabled INTEGER NOT NULL,
 profile_json TEXT NOT NULL, updated_at INTEGER NOT NULL,
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id)
);
CREATE TABLE k8s_release_grants (
 id TEXT PRIMARY KEY COLLATE BINARY, connection_id TEXT NOT NULL COLLATE BINARY,
 project_id TEXT NOT NULL COLLATE BINARY, revision INTEGER NOT NULL, enabled INTEGER NOT NULL,
 grant_json TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id)
);
CREATE INDEX k8s_release_grants_scope ON k8s_release_grants(connection_id,project_id);
CREATE TABLE k8s_releases (
 id TEXT PRIMARY KEY COLLATE BINARY, run_id TEXT NOT NULL COLLATE BINARY,
 rollback_of TEXT NOT NULL DEFAULT '' COLLATE BINARY, reader_owner_epoch TEXT NOT NULL DEFAULT '' COLLATE BINARY,
 node_key TEXT NOT NULL COLLATE BINARY, project_id TEXT NOT NULL COLLATE BINARY,
 connection_id TEXT NOT NULL COLLATE BINARY, credential_id TEXT NOT NULL COLLATE BINARY,
 mode TEXT NOT NULL, owner_epoch TEXT NOT NULL COLLATE BINARY, revision INTEGER NOT NULL,
 state TEXT NOT NULL, cleanup_state TEXT NOT NULL, owner_fenced INTEGER NOT NULL DEFAULT 0,
 plan_cipher BLOB NOT NULL, plan_id TEXT NOT NULL COLLATE BINARY, approved_id TEXT NOT NULL DEFAULT '',
 batch_cursor INTEGER NOT NULL DEFAULT 0, reason_code TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(run_id,node_key), FOREIGN KEY(connection_id) REFERENCES k8s_connections(id),
 FOREIGN KEY(credential_id) REFERENCES k8s_credentials(id)
);
CREATE INDEX k8s_releases_run ON k8s_releases(run_id);
CREATE INDEX k8s_releases_rollback ON k8s_releases(rollback_of);
CREATE TABLE k8s_release_actions (
 id TEXT PRIMARY KEY COLLATE BINARY, release_id TEXT NOT NULL COLLATE BINARY,
 step_key TEXT NOT NULL COLLATE BINARY, resource_key TEXT NOT NULL COLLATE BINARY,
 owner_epoch TEXT NOT NULL COLLATE BINARY, revision INTEGER NOT NULL,
 state TEXT NOT NULL, intent_cipher BLOB NOT NULL, intent_id TEXT NOT NULL COLLATE BINARY,
 receipt_cipher BLOB, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(release_id,step_key), FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
);
CREATE INDEX k8s_release_actions_parent ON k8s_release_actions(release_id,state);
CREATE TABLE k8s_resource_gates (
 resource_key TEXT PRIMARY KEY COLLATE BINARY, owner_kind TEXT NOT NULL,
 owner_id TEXT NOT NULL COLLATE BINARY, owner_epoch TEXT NOT NULL COLLATE BINARY,
 created_at INTEGER NOT NULL
);
CREATE INDEX k8s_resource_gates_owner ON k8s_resource_gates(owner_kind,owner_id);
CREATE TABLE k8s_release_runtime (
 id TEXT PRIMARY KEY COLLATE BINARY, release_id TEXT COLLATE BINARY, owner_epoch TEXT NOT NULL COLLATE BINARY,
 revision INTEGER NOT NULL, cleanup_state TEXT NOT NULL, runtime_cipher BLOB NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
);
CREATE INDEX k8s_release_runtime_parent ON k8s_release_runtime(release_id,cleanup_state);
CREATE TABLE k8s_release_continuations (
 id TEXT PRIMARY KEY COLLATE BINARY, release_id TEXT NOT NULL COLLATE BINARY,
 run_id TEXT NOT NULL COLLATE BINARY, release_revision INTEGER NOT NULL,
 action TEXT NOT NULL, state TEXT NOT NULL, owner_epoch TEXT NOT NULL DEFAULT '' COLLATE BINARY, reader_owner_epoch TEXT NOT NULL DEFAULT '' COLLATE BINARY, checkpoint_cipher BLOB NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(release_id,release_revision), FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
);
CREATE INDEX k8s_release_continuations_pending ON k8s_release_continuations(state,created_at);
CREATE TABLE k8s_config_payloads (
 id TEXT PRIMARY KEY COLLATE BINARY, project_id TEXT NOT NULL COLLATE BINARY,
 node_key TEXT NOT NULL COLLATE BINARY, ciphertext BLOB NOT NULL, created_at INTEGER NOT NULL
);
CREATE INDEX k8s_config_payloads_node ON k8s_config_payloads(project_id,node_key);

CREATE TABLE k8s_release_rollback_requests (
 id TEXT PRIMARY KEY COLLATE BINARY,source_id TEXT NOT NULL COLLATE BINARY,source_revision INTEGER NOT NULL,source_plan_id TEXT NOT NULL COLLATE BINARY,
 release_id TEXT NOT NULL COLLATE BINARY,run_id TEXT NOT NULL COLLATE BINARY,created_at INTEGER NOT NULL,
 FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
);
