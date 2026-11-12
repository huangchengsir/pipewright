-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [mcp_run_requests] (
 [scope] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [request_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [fingerprint] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [dispatch_state] NVARCHAR(16) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'unknown',
 PRIMARY KEY NONCLUSTERED ([scope], [request_id])
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_mcp_run_requests_1] ON [mcp_run_requests] ([run_id]);
GO
CREATE TABLE [mcp_tokens] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [name] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [token_hash] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_ids_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [can_trigger] BIGINT NOT NULL DEFAULT 0,
 [created_at] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [expires_at] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revoked_at] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_mcp_tokens_0] ON [mcp_tokens] ([token_hash]);
