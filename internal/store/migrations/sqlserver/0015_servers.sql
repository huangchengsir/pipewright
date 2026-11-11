-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [servers] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [name] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [host] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [port] BIGINT NOT NULL DEFAULT 22,
 [user] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [credential_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([credential_id]) REFERENCES [credentials] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_servers_credential_id] ON [servers] ([credential_id]);
