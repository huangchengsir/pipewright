-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [run_artifacts] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [type] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [name] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [reference] NVARCHAR(2048) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [size_bytes] BIGINT NOT NULL DEFAULT 0,
 [metadata_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([run_id]) REFERENCES [pipeline_runs] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_run_artifacts_run] ON [run_artifacts] ([run_id]);
GO
CREATE OR ALTER TRIGGER [cascade_pipeline_runs] ON [pipeline_runs] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [run_artifacts] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_steps] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE p FROM [pipeline_runs] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
