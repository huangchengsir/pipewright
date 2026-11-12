-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [preview_configs] (
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [enabled] BIGINT NOT NULL DEFAULT 0,
 [dns_provider_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [base_domain] NVARCHAR(253) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([project_id])
);
GO
CREATE TABLE [preview_envs] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [pipeline_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [pr_number] BIGINT NOT NULL,
 [branch] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [server_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [route_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [subdomain] NVARCHAR(253) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [status] NVARCHAR(16) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'active',
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [reclaimed_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [idx_preview_envs_status] ON [preview_envs] ([status]);
GO
CREATE NONCLUSTERED INDEX [idx_preview_envs_project] ON [preview_envs] ([project_id]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [idx_preview_envs_project_pr] ON [preview_envs] ([project_id], [pr_number]);
