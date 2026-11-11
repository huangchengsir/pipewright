-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [project_chain_targets] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [downstream_project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [branch] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [enabled] BIGINT NOT NULL DEFAULT 1,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([downstream_project_id]) REFERENCES [projects] ([id]) ON DELETE NO ACTION,
 FOREIGN KEY ([project_id]) REFERENCES [projects] ([id]) ON DELETE NO ACTION
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_project_chain_targets] ON [project_chain_targets] ([project_id], [downstream_project_id], [branch]);
GO
CREATE NONCLUSTERED INDEX [idx_project_chain_targets_project] ON [project_chain_targets] ([project_id]);
GO
ALTER TABLE [pipeline_runs] ADD [chain_source_run_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'';
GO
ALTER TABLE [pipeline_runs] ADD [chain_depth] BIGINT NOT NULL DEFAULT 0;
GO
CREATE OR ALTER TRIGGER [cascade_projects] ON [projects] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [environment_variables] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [pipeline_configs] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [pipeline_runs] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [pipeline_settings] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [pipeline_triggers] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [project_chain_targets] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[downstream_project_id]=d.[id]);
 DELETE c FROM [project_chain_targets] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [project_concurrency] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [project_crons] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [project_environments] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [run_promotions] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [webhook_deliveries] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE p FROM [projects] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
