-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [proxy_routes] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [server_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [domain] NVARCHAR(253) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [upstream_container] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [upstream_port] BIGINT NOT NULL,
 [tls_mode] NVARCHAR(16) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'auto',
 [enabled] BIGINT NOT NULL DEFAULT 1,
 [cert_status] NVARCHAR(16) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'pending',
 [cert_detail] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NULL,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [idx_proxy_routes_server] ON [proxy_routes] ([server_id]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [idx_proxy_routes_domain] ON [proxy_routes] ([domain]);
