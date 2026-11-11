-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [deployment_attempts] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [source_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [deployment_attempts_run] ON [deployment_attempts] ([run_id], [created_at]);
GO
CREATE TABLE [deployment_attempt_targets] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [attempt_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [workload_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [sequence] BIGINT NOT NULL DEFAULT 0,
 [outcome] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [target_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([attempt_id]) REFERENCES [deployment_attempts] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [deployment_attempt_targets_workload] ON [deployment_attempt_targets] ([workload_key], [created_at]);
GO
CREATE NONCLUSTERED INDEX [deployment_attempt_targets_attempt] ON [deployment_attempt_targets] ([attempt_id], [created_at]);
GO
CREATE TABLE [deployment_attempt_receipts] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [target_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [sequence] BIGINT NOT NULL,
 [kind] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [receipt_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([target_id]) REFERENCES [deployment_attempt_targets] ([id]) ON DELETE NO ACTION
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_deployment_attempt_receipts_0] ON [deployment_attempt_receipts] ([target_id], [sequence]);
GO
CREATE OR ALTER TRIGGER [cascade_deployment_attempt_targets] ON [deployment_attempt_targets] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [deployment_attempt_receipts] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[target_id]=d.[id]);
 DELETE p FROM [deployment_attempt_targets] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
GO
CREATE OR ALTER TRIGGER [cascade_deployment_attempts] ON [deployment_attempts] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [deployment_attempt_targets] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[attempt_id]=d.[id]);
 DELETE p FROM [deployment_attempts] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
