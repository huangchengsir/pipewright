-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [deploy_targets] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [server_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [server_name] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [message] NVARCHAR(2048) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [started_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [finished_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([run_id]) REFERENCES [pipeline_runs] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_deploy_targets_run] ON [deploy_targets] ([run_id]);
