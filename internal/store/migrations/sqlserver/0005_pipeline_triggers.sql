-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [pipeline_triggers] (
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [webhook_token] NVARCHAR(191) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [webhook_secret_ciphertext] VARBINARY(MAX) NOT NULL,
 [events_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'{}',
 [branch_mappings_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'[]',
 [unmatched_policy] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'record',
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([project_id]),
 FOREIGN KEY ([project_id]) REFERENCES [projects] ([id]) ON DELETE NO ACTION
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_pipeline_triggers_0] ON [pipeline_triggers] ([webhook_token]);
GO
CREATE OR ALTER TRIGGER [cascade_projects] ON [projects] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [pipeline_triggers] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE p FROM [projects] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
