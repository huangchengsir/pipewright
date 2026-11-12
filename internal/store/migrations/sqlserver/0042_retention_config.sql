-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [retention_config] (
 [id] BIGINT NOT NULL,
 [enabled] BIGINT NOT NULL DEFAULT 0,
 [keep_per_project] BIGINT NOT NULL DEFAULT 0,
 [max_age_days] BIGINT NOT NULL DEFAULT 0,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 PRIMARY KEY NONCLUSTERED ([id]),
 CHECK (id = 1)
);
GO
INSERT INTO retention_config (id, enabled, keep_per_project, max_age_days, updated_at) VALUES (1, 0, 0, 0, '');
