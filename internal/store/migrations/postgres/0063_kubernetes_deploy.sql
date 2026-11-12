-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE k8s_credentials (
 id TEXT COLLATE "C" PRIMARY KEY , name TEXT COLLATE "C" NOT NULL, revision BIGINT NOT NULL,
 enabled BIGINT NOT NULL, metadata_json TEXT COLLATE "C" NOT NULL, secret_cipher BYTEA NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
);
CREATE TABLE k8s_connections (
 id TEXT COLLATE "C" PRIMARY KEY , credential_id TEXT COLLATE "C" NOT NULL ,
 revision BIGINT NOT NULL, enabled BIGINT NOT NULL, connection_json TEXT COLLATE "C" NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(credential_id) REFERENCES k8s_credentials(id)
);
CREATE INDEX k8s_connections_credential ON k8s_connections(credential_id);
CREATE TABLE k8s_deploy_grants (
 id TEXT COLLATE "C" PRIMARY KEY , connection_id TEXT COLLATE "C" NOT NULL ,
 scope_key TEXT COLLATE "C" NOT NULL  UNIQUE, project_id TEXT COLLATE "C" NOT NULL ,
 revision BIGINT NOT NULL, enabled BIGINT NOT NULL, grant_json TEXT COLLATE "C" NOT NULL,
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id)
);
CREATE INDEX k8s_grants_connection ON k8s_deploy_grants(connection_id);
CREATE TABLE k8s_operations (
 id TEXT COLLATE "C" PRIMARY KEY , run_id TEXT COLLATE "C" NOT NULL ,
 project_id TEXT COLLATE "C" NOT NULL , node_id TEXT COLLATE "C" NOT NULL ,
 attempt BIGINT NOT NULL, connection_id TEXT COLLATE "C" NOT NULL ,
 credential_id TEXT COLLATE "C" NOT NULL , owner_epoch TEXT COLLATE "C" NOT NULL ,
 revision BIGINT NOT NULL, business_state TEXT COLLATE "C" NOT NULL, cleanup_state TEXT COLLATE "C" NOT NULL,
 owner_fenced BIGINT NOT NULL DEFAULT 0, retired BIGINT NOT NULL DEFAULT 0,
 dispatch_fenced BIGINT NOT NULL DEFAULT 0, intent_cipher BYTEA NOT NULL, receipt_cipher BYTEA,
 intent_hash TEXT COLLATE "C" NOT NULL , approved_hash TEXT COLLATE "C" NOT NULL DEFAULT '', reason_code VARCHAR(128) NOT NULL DEFAULT '',
 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 UNIQUE(run_id,node_id,attempt),
 FOREIGN KEY(connection_id) REFERENCES k8s_connections(id),
 FOREIGN KEY(credential_id) REFERENCES k8s_credentials(id)
);
CREATE INDEX k8s_operations_run ON k8s_operations(run_id);
CREATE INDEX k8s_operations_recovery ON k8s_operations(cleanup_state,owner_fenced);
CREATE TABLE k8s_workload_holds (
 workload_key TEXT COLLATE "C" PRIMARY KEY , operation_id TEXT COLLATE "C" NOT NULL UNIQUE ,
 owner_epoch TEXT COLLATE "C" NOT NULL , cluster_key TEXT COLLATE "C" NOT NULL, namespace TEXT COLLATE "C" NOT NULL,
 deployment_uid TEXT COLLATE "C" NOT NULL, created_at BIGINT NOT NULL,
 FOREIGN KEY(operation_id) REFERENCES k8s_operations(id)
);
CREATE TABLE k8s_runtime_leases (
 operation_id TEXT COLLATE "C" PRIMARY KEY , owner_epoch TEXT COLLATE "C" NOT NULL ,
 source_operation_id TEXT COLLATE "C" ,
 revision BIGINT NOT NULL, container_name TEXT COLLATE "C" NOT NULL  UNIQUE,
 engine_id TEXT COLLATE "C" NOT NULL, container_id TEXT COLLATE "C" NOT NULL DEFAULT '', lease_json TEXT COLLATE "C" NOT NULL,
 cleanup_state TEXT COLLATE "C" NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
 FOREIGN KEY(source_operation_id) REFERENCES k8s_operations(id)
);
