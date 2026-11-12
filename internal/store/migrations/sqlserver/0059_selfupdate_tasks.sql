-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [selfupdate_control] (
 [id] BIGINT NOT NULL,
 [task_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE TABLE [selfupdate_leases] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [kind] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE TABLE [selfupdate_tasks] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [instance_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [request_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [target_tag] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [active_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 [record_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_selfupdate_tasks_0] ON [selfupdate_tasks] ([instance_id], [request_id]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_selfupdate_tasks_1] ON [selfupdate_tasks] ([active_key]) WHERE [active_key] IS NOT NULL;
GO
INSERT INTO selfupdate_control(id,task_id) VALUES(1,'');
