-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [diagnosis_feedback] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [verdict] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [correct_root_cause] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [diagnosis_snapshot] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([run_id]) REFERENCES [pipeline_runs] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_diagnosis_feedback_created_at] ON [diagnosis_feedback] ([created_at]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_diagnosis_feedback_1] ON [diagnosis_feedback] ([run_id]);
GO
CREATE OR ALTER TRIGGER [cascade_pipeline_runs] ON [pipeline_runs] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [diagnosis_feedback] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_artifacts] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_steps] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE p FROM [pipeline_runs] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
