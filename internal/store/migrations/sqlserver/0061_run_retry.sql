-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [run_retry_attempts] (
 [initiator] NVARCHAR(191) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [run_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [root_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [parent_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [execution_kind] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([run_id])
);
GO
CREATE NONCLUSTERED INDEX [run_retry_attempts_root] ON [run_retry_attempts] ([root_id], [revision]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_run_retry_attempts_1] ON [run_retry_attempts] ([root_id], [revision]);
GO
CREATE TABLE [run_retry_dispatch] (
 [attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [state] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_epoch] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [external_id] NVARCHAR(256) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([attempt_id])
);
GO
CREATE TABLE [run_retry_effects] (
 [ciphertext] VARBINARY(MAX) NULL,
 [root_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [effect_key] NVARCHAR(191) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [state] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [operation_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([root_id], [effect_key])
);
GO
CREATE TABLE [run_retry_external_targets] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [root_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [node_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [server_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [state] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [ciphertext] VARBINARY(MAX) NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [run_retry_external_targets_root] ON [run_retry_external_targets] ([root_id], [state]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_run_retry_external_targets_1] ON [run_retry_external_targets] ([attempt_id], [node_key], [server_id]);
GO
CREATE TABLE [run_retry_lineages] (
 [identity_bytes] BIGINT NOT NULL DEFAULT 0,
 [root_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [head_attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [active_attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NULL,
 [checkpoint_bytes] BIGINT NOT NULL DEFAULT 0,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([root_id])
);
GO
CREATE TABLE [run_retry_manifests] (
 [root_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [schema_version] BIGINT NOT NULL,
 [fingerprint] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [ciphertext] VARBINARY(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([root_id])
);
GO
CREATE TABLE [run_retry_nodes] (
 [attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [node_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [origin] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [source_attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [source_node_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [complete] BIGINT NOT NULL DEFAULT 0,
 [reusable] BIGINT NOT NULL DEFAULT 0,
 [valid_until] BIGINT NULL,
 [ciphertext] VARBINARY(MAX) NULL,
 [owner_epoch] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [reason_code] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [resources_drained] BIGINT NOT NULL DEFAULT 0,
 [step_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [step_ordinal] BIGINT NOT NULL DEFAULT -1,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([attempt_id], [node_key])
);
GO
CREATE NONCLUSTERED INDEX [run_retry_nodes_source] ON [run_retry_nodes] ([source_attempt_id], [source_node_key]);
GO
CREATE TABLE [run_retry_reader_exits] (
 [activity_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_epoch] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [runtime_owner] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [exited_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([activity_id])
);
GO
CREATE NONCLUSTERED INDEX [run_retry_reader_exits_attempt] ON [run_retry_reader_exits] ([attempt_id]);
GO
CREATE TABLE [run_retry_refs] (
 [owner_attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [producer_attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [producer_node_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [object_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [purpose] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [released_at] BIGINT NULL,
 PRIMARY KEY NONCLUSTERED ([owner_attempt_id], [producer_attempt_id], [producer_node_key], [object_key])
);
GO
CREATE NONCLUSTERED INDEX [run_retry_refs_object] ON [run_retry_refs] ([object_key], [released_at]);
GO
CREATE NONCLUSTERED INDEX [run_retry_refs_producer] ON [run_retry_refs] ([producer_attempt_id], [released_at]);
GO
CREATE TABLE [run_retry_requests] (
 [initiator] NVARCHAR(191) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [root_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [caller_scope] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [request_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [input_digest] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [source_attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [state] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [reason_code] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([root_id], [caller_scope], [request_id])
);
GO
CREATE NONCLUSTERED INDEX [run_retry_requests_attempt] ON [run_retry_requests] ([attempt_id]);
GO
CREATE TABLE [run_retry_resources] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [root_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [node_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [ciphertext] VARBINARY(MAX) NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [run_retry_resources_attempt] ON [run_retry_resources] ([attempt_id], [state]);
GO
CREATE TABLE [run_retry_sources] (
 [root_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [commit_sha] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [conflicted] BIGINT NOT NULL DEFAULT 0,
 [first_attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [bound_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([root_id])
);
