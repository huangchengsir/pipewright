-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [admin_user] (
 [id] BIGINT NOT NULL,
 [username] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [password_hash] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 CHECK (id = 1)
);
GO
CREATE TABLE [sessions] (
 [token] NVARCHAR(191) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [csrf_token] NVARCHAR(191) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [expires_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [last_seen_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([token])
);
