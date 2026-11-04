CREATE TABLE k8s_credentials (
 id TEXT PRIMARY KEY COLLATE BINARY, name TEXT NOT NULL, revision INTEGER NOT NULL,
 enabled INTEGER NOT NULL, metadata_json TEXT NOT NULL, secret_cipher BLOB NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE k8s_connections (
 id TEXT PRIMARY KEY COLLATE BINARY, credential_id TEXT NOT NULL COLLATE BINARY,
 revision INTEGER NOT NULL, enabled INTEGER NOT NULL, connection_json TEXT NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 FOREIGN KEY(credential_id) REFERENCES k8s_credentials(id)
);
CREATE INDEX k8s_connections_credential ON k8s_connections(credential_id);
CREATE TABLE k8s_deploy_grants (
 id TEXT PRIMARY KEY COLLATE BINARY, connection_id TEXT NOT NULL COLLATE BINARY,
 scope_key TEXT NOT NULL COLLATE BINARY UNIQUE, project_id TEXT NOT NULL COLLATE BINARY,
 revision INTEGER NOT NULL, enabled INTEGER NOT NULL, grant_json TEXT NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id)
);
CREATE INDEX k8s_grants_connection ON k8s_deploy_grants(connection_id);
CREATE TABLE k8s_operations (
 id TEXT PRIMARY KEY COLLATE BINARY, run_id TEXT NOT NULL COLLATE BINARY,
 project_id TEXT NOT NULL COLLATE BINARY, node_id TEXT NOT NULL COLLATE BINARY,
 attempt INTEGER NOT NULL, connection_id TEXT NOT NULL COLLATE BINARY,
 credential_id TEXT NOT NULL COLLATE BINARY, owner_epoch TEXT NOT NULL COLLATE BINARY,
 revision INTEGER NOT NULL, business_state TEXT NOT NULL, cleanup_state TEXT NOT NULL,
 owner_fenced INTEGER NOT NULL DEFAULT 0, retired INTEGER NOT NULL DEFAULT 0,
 dispatch_fenced INTEGER NOT NULL DEFAULT 0, intent_cipher BLOB NOT NULL, receipt_cipher BLOB,
 intent_hash TEXT NOT NULL COLLATE BINARY, approved_hash TEXT NOT NULL DEFAULT '', reason_code VARCHAR(128) NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(run_id,node_id,attempt),
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id),
 FOREIGN KEY(credential_id) REFERENCES k8s_credentials(id)
);
CREATE INDEX k8s_operations_run ON k8s_operations(run_id);
CREATE INDEX k8s_operations_recovery ON k8s_operations(cleanup_state,owner_fenced);
CREATE TABLE k8s_workload_holds (
 workload_key TEXT PRIMARY KEY COLLATE BINARY, operation_id TEXT NOT NULL UNIQUE COLLATE BINARY,
 owner_epoch TEXT NOT NULL COLLATE BINARY, cluster_key TEXT NOT NULL, namespace TEXT NOT NULL,
 deployment_uid TEXT NOT NULL, created_at INTEGER NOT NULL,
 FOREIGN KEY(operation_id) REFERENCES k8s_operations(id)
);
CREATE TABLE k8s_runtime_leases (
 operation_id TEXT PRIMARY KEY COLLATE BINARY, owner_epoch TEXT NOT NULL COLLATE BINARY,
 source_operation_id TEXT COLLATE BINARY,
 revision INTEGER NOT NULL, container_name TEXT NOT NULL COLLATE BINARY UNIQUE,
 engine_id TEXT NOT NULL, container_id TEXT NOT NULL DEFAULT '', lease_json TEXT NOT NULL,
 cleanup_state TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 FOREIGN KEY(source_operation_id) REFERENCES k8s_operations(id)
);
