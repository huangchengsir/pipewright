-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [ops_chat_contexts] (
 [context_key] NVARCHAR(80) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [session_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([context_key]),
 FOREIGN KEY ([session_id]) REFERENCES [ops_chat_sessions] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [ops_chat_contexts_session] ON [ops_chat_contexts] ([session_id]);
GO
CREATE OR ALTER TRIGGER [cascade_ops_chat_sessions] ON [ops_chat_sessions] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [ops_chat_approvals] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[session_id]=d.[id]);
 DELETE c FROM [ops_chat_calls] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[session_id]=d.[id]);
 DELETE c FROM [ops_chat_contexts] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[session_id]=d.[id]);
 DELETE c FROM [ops_chat_entries] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[session_id]=d.[id]);
 DELETE c FROM [ops_chat_runs] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[session_id]=d.[id]);
 DELETE p FROM [ops_chat_sessions] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
