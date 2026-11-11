-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [audit_log] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [timestamp] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [actor] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [action] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [target_type] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [target_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 [detail_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'{}',
 [ip] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NULL,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [idx_audit_log_target_type] ON [audit_log] ([target_type]);
GO
CREATE NONCLUSTERED INDEX [idx_audit_log_action] ON [audit_log] ([action]);
GO
CREATE NONCLUSTERED INDEX [idx_audit_log_timestamp] ON [audit_log] ([timestamp] DESC, [id] DESC);
GO
CREATE OR ALTER TRIGGER [audit_log_no_update] ON [audit_log] INSTEAD OF UPDATE AS
BEGIN
 THROW 50000, 'audit_log is append-only', 1;
END;
GO
CREATE OR ALTER TRIGGER [audit_log_no_delete] ON [audit_log] INSTEAD OF DELETE AS
BEGIN
 THROW 50000, 'audit_log is append-only', 1;
END;
