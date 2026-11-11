-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
ALTER TABLE [pipeline_runs] ADD [resolved_commit_sha] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'';
GO
ALTER TABLE [pipeline_runs] ADD [resolved_commit_conflict] BIGINT NOT NULL DEFAULT 0;
