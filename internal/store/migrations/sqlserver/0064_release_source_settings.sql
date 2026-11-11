-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [release_source_settings] (
 [id] BIGINT NOT NULL,
 [source] NVARCHAR(16) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'github',
 [revision] BIGINT NOT NULL DEFAULT 1,
 PRIMARY KEY NONCLUSTERED ([id]),
 CHECK (id=1)
);
GO
INSERT INTO release_source_settings(id,source,revision) VALUES(1,'github',1);
