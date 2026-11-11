-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [release_guard_plans] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [expires_at] BIGINT NOT NULL,
 [consumed_by] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [plan_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [release_guard_plans_expiry] ON [release_guard_plans] ([expires_at]);
GO
CREATE TABLE [release_guards] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [attempt_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [plan_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [idempotency_key] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL DEFAULT 0,
 [created_at] BIGINT NOT NULL,
 [evidence_expires_at] BIGINT NOT NULL,
 [report_expires_at] BIGINT NOT NULL,
 [record_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [release_guards_expiry] ON [release_guards] ([report_expires_at]);
GO
CREATE NONCLUSTERED INDEX [release_guards_run] ON [release_guards] ([run_id], [created_at]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_release_guards_2] ON [release_guards] ([project_id], [idempotency_key]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_release_guards_3] ON [release_guards] ([plan_id]);
GO
CREATE TABLE [release_guard_analyses] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [guard_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [analysis_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([guard_id]) REFERENCES [release_guards] ([id]) ON DELETE NO ACTION
);
GO
CREATE TABLE [release_guard_events] (
 [guard_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [sequence] BIGINT NOT NULL,
 [event_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([guard_id], [sequence]),
 FOREIGN KEY ([guard_id]) REFERENCES [release_guards] ([id]) ON DELETE NO ACTION
);
GO
CREATE TABLE [release_guard_evidence] (
 [guard_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [sequence] BIGINT NOT NULL,
 [evidence_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([guard_id], [sequence]),
 FOREIGN KEY ([guard_id]) REFERENCES [release_guards] ([id]) ON DELETE NO ACTION
);
GO
CREATE OR ALTER TRIGGER [cascade_release_guards] ON [release_guards] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [release_guard_analyses] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[guard_id]=d.[id]);
 DELETE c FROM [release_guard_events] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[guard_id]=d.[id]);
 DELETE c FROM [release_guard_evidence] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[guard_id]=d.[id]);
 DELETE p FROM [release_guards] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
