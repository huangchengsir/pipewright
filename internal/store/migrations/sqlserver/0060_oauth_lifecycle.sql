-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [oauth_authorizations] (
 [credential_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [claim_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [cipher_digest] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([credential_id]),
 FOREIGN KEY ([credential_id]) REFERENCES [credentials] ([id]) ON DELETE NO ACTION
);
GO
CREATE TABLE [oauth_credential_uses] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [credential_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [generation] BIGINT NOT NULL,
 [owner] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([credential_id]) REFERENCES [credentials] ([id]) ON DELETE NO ACTION
);
GO
CREATE NONCLUSTERED INDEX [oauth_credential_uses_credential] ON [oauth_credential_uses] ([credential_id]);
GO
CREATE TABLE [oauth_credentials] (
 [credential_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [provider] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [issuer] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [client_id] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [redirect_uri] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [app_revision] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [account_id] NVARCHAR(256) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [login] NVARCHAR(256) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [refresh_ciphertext] VARBINARY(MAX) NOT NULL,
 [expires_at] BIGINT NULL,
 [refresh_expires_at] BIGINT NULL,
 [refresh_after] BIGINT NULL,
 [revision] BIGINT NOT NULL DEFAULT 1,
 [state] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [claim_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [claim_owner] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([credential_id]),
 FOREIGN KEY ([credential_id]) REFERENCES [credentials] ([id]) ON DELETE NO ACTION
);
GO
CREATE OR ALTER TRIGGER [cascade_credentials] ON [credentials] INSTEAD OF DELETE AS
BEGIN
 SET NOCOUNT ON;
 DELETE c FROM [oauth_authorizations] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[credential_id]=d.[id]);
 DELETE c FROM [oauth_credential_uses] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[credential_id]=d.[id]);
 DELETE c FROM [oauth_credentials] c WHERE EXISTS (SELECT 1 FROM deleted d WHERE c.[credential_id]=d.[id]);
 DELETE p FROM [credentials] p WHERE EXISTS (SELECT 1 FROM deleted d WHERE p.[id]=d.[id]);
END;
