-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [anomaly_alerts] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [server_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [server_name] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [metric] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [operator] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [threshold] FLOAT(53) NOT NULL,
 [value] FLOAT(53) NOT NULL,
 [message] NVARCHAR(2048) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [idx_anomaly_alerts_server_id] ON [anomaly_alerts] ([server_id]);
GO
CREATE NONCLUSTERED INDEX [idx_anomaly_alerts_created_at] ON [anomaly_alerts] ([created_at]);
GO
CREATE TABLE [anomaly_rules] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [metric] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [operator] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [threshold] FLOAT(53) NOT NULL,
 [server_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 [enabled] BIGINT NOT NULL DEFAULT 1,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
