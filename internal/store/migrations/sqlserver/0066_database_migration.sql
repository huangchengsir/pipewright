-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [database_identity] (
 [id] BIGINT NOT NULL,
 [instance_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 CHECK (id=1)
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_database_identity_0] ON [database_identity] ([instance_id]);
GO
CREATE TABLE [database_migration_batches] (
 [migration_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [table_name] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [batch_no] BIGINT NOT NULL,
 [row_count] BIGINT NOT NULL,
 [content_sha256] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [committed_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([migration_id], [table_name], [batch_no])
);
