-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [ops_chat_sessions] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [seq] BIGINT NOT NULL DEFAULT 0,
 [visible_count] BIGINT NOT NULL DEFAULT 0,
 [body_bytes] BIGINT NOT NULL DEFAULT 0,
 [active_run] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [body] VARBINARY(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE TABLE [ops_chat_state] (
 [id] BIGINT NOT NULL,
 [active_session] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [lease_owner] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [lease_until] BIGINT NOT NULL DEFAULT 0,
 [keycheck] VARBINARY(MAX) NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE TABLE [ops_chat_entries] (
 [session_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [seq] BIGINT NOT NULL,
 [kind] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [call_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [context_allowed] BIGINT NOT NULL DEFAULT 0,
 [visible] BIGINT NOT NULL DEFAULT 0,
 [body] VARBINARY(MAX) NOT NULL,
 [body_size] BIGINT NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([session_id], [seq]),
 FOREIGN KEY ([session_id]) REFERENCES [ops_chat_sessions] ([id]) ON DELETE NO ACTION
);
GO
CREATE TABLE [ops_chat_runs] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [session_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [request_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [payload_hash] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [cancel_requested] BIGINT NOT NULL DEFAULT 0,
 [body] VARBINARY(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([session_id]) REFERENCES [ops_chat_sessions] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [ops_chat_runs_session] ON [ops_chat_runs] ([session_id], [created_at]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_ops_chat_runs_1] ON [ops_chat_runs] ([request_id]);
GO
CREATE TABLE [ops_chat_approvals] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [session_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [instance_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [expires_at] BIGINT NOT NULL,
 [body] VARBINARY(MAX) NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([run_id]) REFERENCES [ops_chat_runs] ([id]) ON DELETE NO ACTION,
 FOREIGN KEY ([session_id]) REFERENCES [ops_chat_sessions] ([id]) ON DELETE NO ACTION
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_ops_chat_approvals_0] ON [ops_chat_approvals] ([run_id]);
GO
CREATE TABLE [ops_chat_calls] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [session_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [server_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [tool_id] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [ordinal] BIGINT NOT NULL,
 [args_hash] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [target_hash] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [body] VARBINARY(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([run_id]) REFERENCES [ops_chat_runs] ([id]) ON DELETE NO ACTION,
 FOREIGN KEY ([session_id]) REFERENCES [ops_chat_sessions] ([id]) ON DELETE NO ACTION
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [ops_chat_calls_run] ON [ops_chat_calls] ([run_id], [ordinal]);
GO
CREATE OR ALTER TRIGGER [cascade_ops_chat_runs] ON [ops_chat_runs] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [ops_chat_approvals] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [ops_chat_calls] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE p FROM [ops_chat_runs] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
GO
CREATE OR ALTER TRIGGER [cascade_ops_chat_sessions] ON [ops_chat_sessions] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [ops_chat_approvals] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[session_id]=d.[id]);
 DELETE c FROM [ops_chat_calls] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[session_id]=d.[id]);
 DELETE c FROM [ops_chat_entries] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[session_id]=d.[id]);
 DELETE c FROM [ops_chat_runs] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[session_id]=d.[id]);
 DELETE p FROM [ops_chat_sessions] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
