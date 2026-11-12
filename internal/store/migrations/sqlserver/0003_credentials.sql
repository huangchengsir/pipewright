-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [credentials] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [name] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [type] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [scope] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [ciphertext] VARBINARY(MAX) NOT NULL,
 [masked_value] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [last_used_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NULL,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
