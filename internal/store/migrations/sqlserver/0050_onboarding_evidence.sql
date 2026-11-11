-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
ALTER TABLE [pipeline_configs] ADD [saved_at] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NULL;
GO
ALTER TABLE [pipeline_runs] ADD [execution_mode] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'pending';
GO
UPDATE pipeline_runs SET execution_mode = 'legacy_unknown';
