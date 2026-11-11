-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [run_approvals] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [stage_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [stage_name] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [status] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'pending',
 [decided_by] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [decided_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([run_id]) REFERENCES [pipeline_runs] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_run_approvals_run] ON [run_approvals] ([run_id]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_run_approvals_1] ON [run_approvals] ([run_id], [stage_id]);
GO
CREATE OR ALTER TRIGGER [cascade_pipeline_runs] ON [pipeline_runs] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [diagnosis_feedback] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_approvals] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_artifacts] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_steps] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE p FROM [pipeline_runs] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
