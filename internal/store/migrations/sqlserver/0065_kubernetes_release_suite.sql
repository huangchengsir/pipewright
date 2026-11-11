-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [k8s_config_payloads] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [node_key] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [ciphertext] VARBINARY(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [k8s_config_payloads_node] ON [k8s_config_payloads] ([project_id], [node_key]);
GO
CREATE TABLE [k8s_release_grants] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [connection_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
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
CREATE NONCLUSTERED INDEX [k8s_release_grants_scope] ON [k8s_release_grants] ([connection_id], [project_id]);
GO
CREATE TABLE [k8s_release_profiles] (
 [connection_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [enabled] BIGINT NOT NULL,
 [profile_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([connection_id]),
 FOREIGN KEY ([connection_id]) REFERENCES [k8s_connections] ([id]) ON DELETE NO ACTION
);
GO
CREATE TABLE [k8s_releases] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [rollback_of] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [reader_owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [node_key] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [connection_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [credential_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [mode] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [cleanup_state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_fenced] BIGINT NOT NULL DEFAULT 0,
 [plan_cipher] VARBINARY(MAX) NOT NULL,
 [plan_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [approved_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [batch_cursor] BIGINT NOT NULL DEFAULT 0,
 [reason_code] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([credential_id]) REFERENCES [k8s_credentials] ([id]) ON DELETE NO ACTION,
 FOREIGN KEY ([connection_id]) REFERENCES [k8s_connections] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [k8s_releases_rollback] ON [k8s_releases] ([rollback_of]);
GO
CREATE NONCLUSTERED INDEX [k8s_releases_run] ON [k8s_releases] ([run_id]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_k8s_releases_2] ON [k8s_releases] ([run_id], [node_key]);
GO
CREATE TABLE [k8s_resource_gates] (
 [resource_key] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_kind] NVARCHAR(16) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([resource_key])
);
GO
CREATE NONCLUSTERED INDEX [k8s_resource_gates_owner] ON [k8s_resource_gates] ([owner_kind], [owner_id]);
GO
CREATE TABLE [k8s_release_actions] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [release_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [step_key] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [resource_key] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [intent_cipher] VARBINARY(MAX) NOT NULL,
 [intent_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [receipt_cipher] VARBINARY(MAX) NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([release_id]) REFERENCES [k8s_releases] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [k8s_release_actions_parent] ON [k8s_release_actions] ([release_id], [state]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_k8s_release_actions_1] ON [k8s_release_actions] ([release_id], [step_key]);
GO
CREATE TABLE [k8s_release_continuations] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [release_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [release_revision] BIGINT NOT NULL,
 [action] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [reader_owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [checkpoint_cipher] VARBINARY(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([release_id]) REFERENCES [k8s_releases] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [k8s_release_continuations_pending] ON [k8s_release_continuations] ([state], [created_at]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_k8s_release_continuations_1] ON [k8s_release_continuations] ([release_id], [release_revision]);
GO
CREATE TABLE [k8s_release_rollback_requests] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [source_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [source_revision] BIGINT NOT NULL,
 [source_plan_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [release_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([release_id]) REFERENCES [k8s_releases] ([id]) ON DELETE NO ACTION
);
GO
CREATE TABLE [k8s_release_runtime] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [release_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NULL,
 [owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [cleanup_state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [runtime_cipher] VARBINARY(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([release_id]) REFERENCES [k8s_releases] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [k8s_release_runtime_parent] ON [k8s_release_runtime] ([release_id], [cleanup_state]);
