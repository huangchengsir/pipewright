-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [deploy_batches] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [idempotency_key] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [request_hash] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([run_id]) REFERENCES [pipeline_runs] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_deploy_batches_run] ON [deploy_batches] ([run_id], [created_at]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_deploy_batches_1] ON [deploy_batches] ([run_id], [idempotency_key]);
GO
CREATE TABLE [deploy_batch_items] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [batch_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [item_index] BIGINT NOT NULL,
 [artifact_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [artifact_name] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [server_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [server_name] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [config_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [message] NVARCHAR(2048) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [attempt] BIGINT NOT NULL DEFAULT 0,
 [started_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NULL,
 [finished_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([batch_id]) REFERENCES [deploy_batches] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_deploy_batch_items_batch] ON [deploy_batch_items] ([batch_id], [item_index]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_deploy_batch_items_1] ON [deploy_batch_items] ([batch_id], [item_index], [server_id]);
GO
CREATE OR ALTER TRIGGER [cascade_deploy_batches] ON [deploy_batches] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [deploy_batch_items] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[batch_id]=d.[id]);
 DELETE p FROM [deploy_batches] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
GO
CREATE OR ALTER TRIGGER [cascade_pipeline_runs] ON [pipeline_runs] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [deploy_batches] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [diagnosis_feedback] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_approvals] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_artifacts] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_promotions] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[source_run_id]=d.[id]);
 DELETE c FROM [run_steps] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE c FROM [run_test_reports] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[run_id]=d.[id]);
 DELETE p FROM [pipeline_runs] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
