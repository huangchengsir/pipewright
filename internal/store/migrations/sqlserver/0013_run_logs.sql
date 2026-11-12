-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [run_logs] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [seq] BIGINT NOT NULL,
 [ts] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [stream] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [step_ordinal] BIGINT NOT NULL,
 [text] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [idx_run_logs_run_seq] ON [run_logs] ([run_id], [seq]);
