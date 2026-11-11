-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [projects] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [name] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [repo_url] NVARCHAR(1024) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [default_branch] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [credential_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([credential_id]) REFERENCES [credentials] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_projects_credential_id] ON [projects] ([credential_id]);
