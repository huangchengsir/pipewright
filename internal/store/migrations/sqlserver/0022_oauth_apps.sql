-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [oauth_apps] (
 [provider] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [client_id] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [client_secret_ciphertext] VARBINARY(MAX) NULL,
 [base_url] NVARCHAR(1024) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [enabled] BIGINT NOT NULL DEFAULT 0,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([provider])
);
