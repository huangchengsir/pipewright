-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
ALTER TABLE [anomaly_alerts] ADD [rule_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'';
GO
ALTER TABLE [anomaly_alerts] ADD [ai_analysis_enabled] BIGINT NOT NULL DEFAULT 0;
GO
ALTER TABLE [anomaly_alerts] ADD [analysis_status] NVARCHAR(16) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'';
GO
ALTER TABLE [anomaly_alerts] ADD [analysis_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NULL;
GO
ALTER TABLE [anomaly_rules] ADD [ai_analysis_enabled] BIGINT NOT NULL DEFAULT 0;
