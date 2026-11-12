-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
ALTER TABLE [pipeline_runs] ADD [spec_source] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'';
GO
ALTER TABLE [pipeline_runs] ADD [spec_source_ref] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'';
GO
ALTER TABLE [pipeline_runs] ADD [spec_source_file] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'';
GO
ALTER TABLE [pipeline_runs] ADD [spec_source_fallback] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'';
