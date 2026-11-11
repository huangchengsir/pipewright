-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [k8s_credentials] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [name] NVARCHAR(120) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [enabled] BIGINT NOT NULL,
 [metadata_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [secret_cipher] VARBINARY(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE TABLE [k8s_connections] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [credential_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [enabled] BIGINT NOT NULL,
 [connection_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([credential_id]) REFERENCES [k8s_credentials] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [k8s_connections_credential] ON [k8s_connections] ([credential_id]);
GO
CREATE TABLE [k8s_deploy_grants] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [connection_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [scope_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [enabled] BIGINT NOT NULL,
 [grant_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([connection_id]) REFERENCES [k8s_connections] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [k8s_grants_connection] ON [k8s_deploy_grants] ([connection_id]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_k8s_deploy_grants_1] ON [k8s_deploy_grants] ([scope_key]);
GO
CREATE TABLE [k8s_operations] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [node_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [attempt] BIGINT NOT NULL,
 [connection_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [credential_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [business_state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [cleanup_state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_fenced] BIGINT NOT NULL DEFAULT 0,
 [retired] BIGINT NOT NULL DEFAULT 0,
 [dispatch_fenced] BIGINT NOT NULL DEFAULT 0,
 [intent_cipher] VARBINARY(MAX) NOT NULL,
 [receipt_cipher] VARBINARY(MAX) NULL,
 [intent_hash] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [approved_hash] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [reason_code] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([credential_id]) REFERENCES [k8s_credentials] ([id]) ON DELETE NO ACTION,
 FOREIGN KEY ([connection_id]) REFERENCES [k8s_connections] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [k8s_operations_recovery] ON [k8s_operations] ([cleanup_state], [owner_fenced]);
GO
CREATE NONCLUSTERED INDEX [k8s_operations_run] ON [k8s_operations] ([run_id]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_k8s_operations_2] ON [k8s_operations] ([run_id], [node_id], [attempt]);
GO
CREATE TABLE [k8s_runtime_leases] (
 [operation_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [source_operation_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NULL,
 [revision] BIGINT NOT NULL,
 [container_name] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [engine_id] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [container_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [lease_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [cleanup_state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([operation_id]),
 FOREIGN KEY ([source_operation_id]) REFERENCES [k8s_operations] ([id]) ON DELETE NO ACTION
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_k8s_runtime_leases_0] ON [k8s_runtime_leases] ([container_name]);
GO
CREATE TABLE [k8s_workload_holds] (
 [workload_key] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [operation_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [cluster_key] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [namespace] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [deployment_uid] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([workload_key]),
 FOREIGN KEY ([operation_id]) REFERENCES [k8s_operations] ([id]) ON DELETE NO ACTION
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_k8s_workload_holds_0] ON [k8s_workload_holds] ([operation_id]);
