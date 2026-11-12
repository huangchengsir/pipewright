-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [notification_routes] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 [event] NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [channel_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [enabled] BIGINT NOT NULL DEFAULT 1,
 [created_at] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([channel_id]) REFERENCES [notification_channels] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [idx_notification_routes_event] ON [notification_routes] ([event]);
GO
CREATE OR ALTER TRIGGER [cascade_notification_channels] ON [notification_channels] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [notification_routes] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[channel_id]=d.[id]);
 DELETE p FROM [notification_channels] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
