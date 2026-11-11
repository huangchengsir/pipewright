-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [login_flows] (
 [flow_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [state_hash] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [browser_hash] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [purpose] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [provider] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [config_version] BIGINT NOT NULL,
 [auth_generation] BIGINT NOT NULL,
 [identity_generation] BIGINT NOT NULL,
 [security_generation] BIGINT NOT NULL,
 [session_hash] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [verifier_cipher] VARBINARY(MAX) NULL,
 [nonce_hash] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [return_to] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [status] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [expires_at] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [candidate_cipher] VARBINARY(MAX) NULL,
 [candidate_expires_at] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NULL,
 [created_at] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([flow_id])
);
GO
CREATE NONCLUSTERED INDEX [login_flows_expiry] ON [login_flows] ([expires_at]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_login_flows_1] ON [login_flows] ([state_hash]);
GO
CREATE TABLE [login_provider_settings] (
 [provider] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [visible] BIGINT NOT NULL DEFAULT 1,
 [enabled] BIGINT NOT NULL DEFAULT 0,
 [client_id] NVARCHAR(2048) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [secret_cipher] VARBINARY(MAX) NULL,
 [microsoft_tenant] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'common',
 [gitee_mode] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [config_version] BIGINT NOT NULL DEFAULT 1,
 [auth_generation] BIGINT NOT NULL DEFAULT 1,
 PRIMARY KEY NONCLUSTERED ([provider])
);
GO
CREATE TABLE [login_settings] (
 [id] BIGINT NOT NULL,
 [visibility_revision] BIGINT NOT NULL DEFAULT 1,
 PRIMARY KEY NONCLUSTERED ([id]),
 CHECK (id=1)
);
GO
CREATE TABLE [login_identities] (
 [provider] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [issuer] NVARCHAR(2048) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [subject] NVARCHAR(2048) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [display_label] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [identity_generation] BIGINT NOT NULL DEFAULT 1,
 [bound_at] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([provider]),
 FOREIGN KEY ([provider]) REFERENCES [login_provider_settings] ([provider]) ON DELETE NO ACTION
);
GO
ALTER TABLE [admin_user] ADD [security_generation] BIGINT NOT NULL DEFAULT 1;
GO
ALTER TABLE [sessions] ADD [auth_source] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'password';
GO
ALTER TABLE [sessions] ADD [provider_generation] BIGINT NOT NULL DEFAULT 0;
GO
ALTER TABLE [sessions] ADD [identity_generation] BIGINT NOT NULL DEFAULT 0;
GO
ALTER TABLE [sessions] ADD [absolute_expires_at] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NULL;
GO
ALTER TABLE [sessions] ADD [password_verified_at] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NULL;
GO
CREATE NONCLUSTERED INDEX [sessions_auth_source] ON [sessions] ([auth_source]);
GO
INSERT INTO login_settings(id) VALUES(1);
GO
INSERT INTO login_provider_settings(provider) VALUES('github'),('gitlab'),('gitee'),('google'),('microsoft');
