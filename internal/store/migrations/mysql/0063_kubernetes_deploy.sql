CREATE TABLE k8s_credentials (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, name VARCHAR(120) NOT NULL, revision BIGINT NOT NULL,
 enabled BIGINT NOT NULL, metadata_json TEXT NOT NULL, secret_cipher MEDIUMBLOB NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE k8s_connections (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, credential_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 revision BIGINT NOT NULL, enabled BIGINT NOT NULL, connection_json TEXT NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(credential_id) REFERENCES k8s_credentials(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX k8s_connections_credential ON k8s_connections(credential_id);
CREATE TABLE k8s_deploy_grants (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, connection_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 scope_key VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE, project_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 revision BIGINT NOT NULL, enabled BIGINT NOT NULL, grant_json TEXT NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX k8s_grants_connection ON k8s_deploy_grants(connection_id);
CREATE TABLE k8s_operations (
 id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, run_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 project_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, node_id VARBINARY(128) NOT NULL,
 attempt BIGINT NOT NULL, connection_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 credential_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 revision BIGINT NOT NULL, business_state VARCHAR(32) NOT NULL, cleanup_state VARCHAR(32) NOT NULL,
 owner_fenced BIGINT NOT NULL DEFAULT 0, retired BIGINT NOT NULL DEFAULT 0,
 dispatch_fenced BIGINT NOT NULL DEFAULT 0, intent_cipher MEDIUMBLOB NOT NULL, receipt_cipher MEDIUMBLOB,
 intent_hash VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, approved_hash VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '', reason_code VARCHAR(128) NOT NULL DEFAULT '',
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE(run_id,node_id,attempt),
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id),
 FOREIGN KEY(credential_id) REFERENCES k8s_credentials(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX k8s_operations_run ON k8s_operations(run_id);
CREATE INDEX k8s_operations_recovery ON k8s_operations(cleanup_state,owner_fenced);
CREATE TABLE k8s_workload_holds (
 workload_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, operation_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
 owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, cluster_key TEXT NOT NULL, namespace TEXT NOT NULL,
 deployment_uid TEXT NOT NULL, created_at BIGINT NOT NULL,
 FOREIGN KEY(operation_id) REFERENCES k8s_operations(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE k8s_runtime_leases (
 operation_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, owner_epoch VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 source_operation_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
 revision BIGINT NOT NULL, container_name VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
 engine_id TEXT NOT NULL, container_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '', lease_json TEXT NOT NULL,
 cleanup_state VARCHAR(32) NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(source_operation_id) REFERENCES k8s_operations(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
