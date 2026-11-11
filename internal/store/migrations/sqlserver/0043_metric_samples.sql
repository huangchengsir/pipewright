-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [metric_samples] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [server_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [cpu_percent] FLOAT(53) NULL,
 [memory_percent] FLOAT(53) NULL,
 [disk_percent] FLOAT(53) NULL,
 [sampled_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [idx_metric_samples_time] ON [metric_samples] ([sampled_at]);
GO
CREATE NONCLUSTERED INDEX [idx_metric_samples_server_time] ON [metric_samples] ([server_id], [sampled_at]);
