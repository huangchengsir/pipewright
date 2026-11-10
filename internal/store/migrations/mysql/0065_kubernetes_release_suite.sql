CREATE TABLE k8s_release_profiles (
 connection_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, revision BIGINT NOT NULL, enabled BIGINT NOT NULL,
 profile_json TEXT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE k8s_release_grants (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, connection_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 project_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, revision BIGINT NOT NULL, enabled BIGINT NOT NULL,
 grant_json MEDIUMTEXT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX k8s_release_grants_scope ON k8s_release_grants(connection_id,project_id);
CREATE TABLE k8s_releases (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, run_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 rollback_of VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '', reader_owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 node_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, project_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 connection_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, credential_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 mode VARCHAR(32) NOT NULL, owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, revision BIGINT NOT NULL,
 state VARCHAR(32) NOT NULL, cleanup_state VARCHAR(32) NOT NULL, owner_fenced BIGINT NOT NULL DEFAULT 0,
 plan_cipher LONGBLOB NOT NULL, plan_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, approved_id VARCHAR(128) NOT NULL DEFAULT '',
 batch_cursor BIGINT NOT NULL DEFAULT 0, reason_code VARCHAR(128) NOT NULL DEFAULT '',
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE(run_id,node_key), FOREIGN KEY(connection_id) REFERENCES k8s_connections(id),
 FOREIGN KEY(credential_id) REFERENCES k8s_credentials(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX k8s_releases_run ON k8s_releases(run_id);
CREATE INDEX k8s_releases_rollback ON k8s_releases(rollback_of);
CREATE TABLE k8s_release_actions (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, release_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 step_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, resource_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, revision BIGINT NOT NULL,
 state VARCHAR(32) NOT NULL, intent_cipher LONGBLOB NOT NULL, intent_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 receipt_cipher LONGBLOB, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE(release_id,step_key), FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX k8s_release_actions_parent ON k8s_release_actions(release_id,state);
CREATE TABLE k8s_resource_gates (
 resource_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, owner_kind VARCHAR(16) NOT NULL,
 owner_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_at BIGINT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX k8s_resource_gates_owner ON k8s_resource_gates(owner_kind,owner_id);
CREATE TABLE k8s_release_runtime (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, release_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL, owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 revision BIGINT NOT NULL, cleanup_state VARCHAR(32) NOT NULL, runtime_cipher LONGBLOB NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX k8s_release_runtime_parent ON k8s_release_runtime(release_id,cleanup_state);
CREATE TABLE k8s_release_continuations (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, release_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 run_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, release_revision BIGINT NOT NULL,
 action VARCHAR(32) NOT NULL, state VARCHAR(32) NOT NULL, owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '', reader_owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '', checkpoint_cipher LONGBLOB NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE(release_id,release_revision), FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX k8s_release_continuations_pending ON k8s_release_continuations(state,created_at);
CREATE TABLE k8s_config_payloads (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 project_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 node_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 ciphertext LONGBLOB NOT NULL, created_at BIGINT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX k8s_config_payloads_node ON k8s_config_payloads(project_id,node_key);

CREATE TABLE k8s_release_rollback_requests (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,source_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 source_revision BIGINT NOT NULL,source_plan_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 release_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,run_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,created_at BIGINT NOT NULL,
 FOREIGN KEY(release_id) REFERENCES k8s_releases(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
