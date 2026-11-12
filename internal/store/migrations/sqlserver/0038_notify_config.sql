-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [notify_config] (
 [id] BIGINT NOT NULL,
 [language] NVARCHAR(16) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'zh-CN',
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 PRIMARY KEY NONCLUSTERED ([id]),
 CHECK (id = 1)
);
GO
INSERT INTO notify_config (id, language, updated_at) VALUES (1, 'zh-CN', '');
