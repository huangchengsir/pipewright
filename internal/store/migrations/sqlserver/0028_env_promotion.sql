-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [environment_variables] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [environment] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [var_key] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [value] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [is_secret] BIGINT NOT NULL DEFAULT 0,
 [credential_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([project_id]) REFERENCES [projects] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_env_vars_proj_env] ON [environment_variables] ([project_id], [environment]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_environment_variables_1] ON [environment_variables] ([project_id], [environment], [var_key]);
GO
CREATE TABLE [project_environments] (
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [chain_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'[]',
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([project_id]),
 FOREIGN KEY ([project_id]) REFERENCES [projects] ([id]) ON DELETE NO ACTION
);
GO
CREATE TABLE [run_promotions] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [source_run_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [from_environment] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [target_environment] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'pending',
 [approval_stage] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [promoted_by] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [decided_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([source_run_id]) REFERENCES [pipeline_runs] ([id]) ON DELETE NO ACTION,
 FOREIGN KEY ([project_id]) REFERENCES [projects] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_run_promotions_run] ON [run_promotions] ([source_run_id]);
GO
CREATE NONCLUSTERED INDEX [idx_run_promotions_proj] ON [run_promotions] ([project_id]);
GO
CREATE OR ALTER TRIGGER [cascade_pipeline_runs] ON [pipeline_runs] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [diagnosis_feedback] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_approvals] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_artifacts] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_promotions] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[source_run_id]=d.[id]);
 DELETE c FROM [run_steps] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_test_reports] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE p FROM [pipeline_runs] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
GO
CREATE OR ALTER TRIGGER [cascade_projects] ON [projects] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [environment_variables] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [pipeline_configs] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [pipeline_runs] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [pipeline_settings] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [pipeline_triggers] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [project_crons] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [project_environments] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [run_promotions] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE c FROM [webhook_deliveries] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[project_id]=d.[id]);
 DELETE p FROM [projects] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
