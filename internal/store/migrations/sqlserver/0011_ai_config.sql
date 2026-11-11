-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [ai_config] (
 [id] BIGINT NOT NULL,
 [provider] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [base_url] NVARCHAR(1024) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [model] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [api_key_ciphertext] VARBINARY(MAX) NULL,
 [budget_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'{"monthlyTokenLimit":null}',
 [enabled] BIGINT NOT NULL DEFAULT 0,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 CHECK (id = 1)
);
