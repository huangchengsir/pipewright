-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [custom_nodes] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [name] NVARCHAR(191) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [description] NVARCHAR(2048) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [node_type] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [summary] NVARCHAR(2048) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [config_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'{}',
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [idx_custom_nodes_name] ON [custom_nodes] ([name]);
