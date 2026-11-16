-- Optional tenancy schema. Mode and principals are initialized explicitly, never by schema migration.
GO
CREATE TABLE [access_users] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [username] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [username_key] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [display_name] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [password_hash] NVARCHAR(512) COLLATE Latin1_General_100_BIN2 NULL,
 [enabled] BIGINT NOT NULL,
 [instance_admin] BIGINT NOT NULL,
 [must_change_password] BIGINT NOT NULL,
 [password_expires_at] BIGINT NULL,
 [password_consumed_at] BIGINT NULL,
 [security_generation] BIGINT NOT NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_access_users_0] ON [access_users] ([username_key]);
GO
CREATE TABLE [access_teams] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [name] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [enabled] BIGINT NOT NULL,
 [revision] BIGINT NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE TABLE [access_control] (
 [id] BIGINT NOT NULL,
 [enabled] BIGINT NOT NULL,
 [ever_enabled] BIGINT NOT NULL,
 [mode_epoch] BIGINT NOT NULL,
 [authz_revision] BIGINT NOT NULL,
 [catalog_version] BIGINT NOT NULL DEFAULT 1,
 [bootstrap_user_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 [default_team_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([bootstrap_user_id]) REFERENCES [access_users] ([id]),
 FOREIGN KEY ([default_team_id]) REFERENCES [access_teams] ([id]),
 CHECK ([id]=1)
);
GO
CREATE TABLE [access_members] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [tenant_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [user_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [enabled] BIGINT NOT NULL,
 [revision] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 UNIQUE ([tenant_id], [id]),
 FOREIGN KEY ([tenant_id]) REFERENCES [access_teams] ([id]),
 FOREIGN KEY ([user_id]) REFERENCES [access_users] ([id])
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_access_members_0] ON [access_members] ([tenant_id], [user_id]);
GO
CREATE TABLE [access_roles] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [tenant_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [name] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [preset_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [seed_version] BIGINT NOT NULL,
 [requires_selected_binding] BIGINT NOT NULL,
 [deleted] BIGINT NOT NULL,
 [revision] BIGINT NOT NULL,
 [pages_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [grants_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 UNIQUE ([tenant_id], [id]),
 FOREIGN KEY ([tenant_id]) REFERENCES [access_teams] ([id])
);
GO
CREATE TABLE [access_role_seeds] (
 [tenant_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [preset_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [role_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [seed_version] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([tenant_id], [preset_key]),
 FOREIGN KEY ([tenant_id]) REFERENCES [access_teams] ([id]),
 FOREIGN KEY ([tenant_id], [role_id]) REFERENCES [access_roles] ([tenant_id], [id])
);
GO
CREATE TABLE [access_bindings] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [tenant_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [member_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [role_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [scope_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([tenant_id]) REFERENCES [access_teams] ([id]),
 FOREIGN KEY ([tenant_id], [member_id]) REFERENCES [access_members] ([tenant_id], [id]),
 FOREIGN KEY ([tenant_id], [role_id]) REFERENCES [access_roles] ([tenant_id], [id])
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_access_bindings_0] ON [access_bindings] ([member_id], [role_id]);
GO
CREATE TABLE [access_ownership] (
 [resource_kind] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [resource_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [tenant_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 [environment_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 PRIMARY KEY NONCLUSTERED ([resource_kind], [resource_id]),
 FOREIGN KEY ([tenant_id]) REFERENCES [access_teams] ([id]),
 FOREIGN KEY ([owner_id]) REFERENCES [access_users] ([id])
);
GO
CREATE TABLE [access_sessions] (
 [token_hash] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [user_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [csrf_token] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [mode_epoch] BIGINT NOT NULL,
 [security_generation] BIGINT NOT NULL,
 [auth_source] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [expires_at] BIGINT NOT NULL,
 [last_seen_at] BIGINT NOT NULL,
 [password_verified_at] BIGINT NULL,
 PRIMARY KEY NONCLUSTERED ([token_hash]),
 FOREIGN KEY ([user_id]) REFERENCES [access_users] ([id])
);
GO
CREATE TABLE [access_identities] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [user_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [provider] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [issuer] NVARCHAR(512) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [subject] NVARCHAR(256) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [identity_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [display_label] NVARCHAR(512) NOT NULL DEFAULT '',
 [generation] BIGINT NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([user_id]) REFERENCES [access_users] ([id])
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_access_identities_0] ON [access_identities] ([identity_key]);

GO
CREATE TABLE [access_environments] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [tenant_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [name] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [purpose] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 UNIQUE ([tenant_id], [project_id], [name]),
 FOREIGN KEY ([tenant_id]) REFERENCES [access_teams] ([id]),
 CHECK ([purpose] IN ('test','production','other'))
);


-- pipewright:statement
CREATE TABLE [access_run_authorizations] (
 [mcp_token_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 [run_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [tenant_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [user_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [mode_epoch] BIGINT NOT NULL,
 [user_generation] BIGINT NOT NULL,
 [config_revision] BIGINT NOT NULL,
 [catalog_version] BIGINT NOT NULL,
 [plan_digest] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [permissions_json] NVARCHAR(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 [revoked_at] BIGINT NULL,
 PRIMARY KEY ([run_id]),
 FOREIGN KEY ([run_id]) REFERENCES [pipeline_runs] ([id]),
 FOREIGN KEY ([tenant_id]) REFERENCES [access_teams] ([id]),
 FOREIGN KEY ([user_id]) REFERENCES [access_users] ([id]),
 FOREIGN KEY ([project_id]) REFERENCES [projects] ([id])
);

-- Team-local reusable asset names; legacy single-admin rows retain their namespace.
ALTER TABLE [pipeline_templates] ADD [name_scope] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT '__single__';
-- pipewright:statement
DROP INDEX [idx_pipeline_templates_name] ON [pipeline_templates];
-- pipewright:statement
CREATE UNIQUE INDEX [idx_pipeline_templates_name] ON [pipeline_templates] ([name_scope],[name]);
-- pipewright:statement
ALTER TABLE [variable_groups] ADD [name_scope] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT '__single__';
-- pipewright:statement
DROP INDEX [idx_variable_groups_name] ON [variable_groups];
-- pipewright:statement
CREATE UNIQUE INDEX [idx_variable_groups_name] ON [variable_groups] ([name_scope],[name]);
-- pipewright:statement
ALTER TABLE [custom_nodes] ADD [name_scope] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT '__single__';
-- pipewright:statement
DROP INDEX [idx_custom_nodes_name] ON [custom_nodes];
-- pipewright:statement
CREATE UNIQUE INDEX [idx_custom_nodes_name] ON [custom_nodes] ([name_scope],[name]);
-- pipewright:statement

CREATE UNIQUE NONCLUSTERED INDEX [uq_access_identities_user_provider] ON [access_identities] ([user_id], [provider]);
GO
ALTER TABLE [login_flows] ADD [access_user_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL;
GO
ALTER TABLE [login_flows] ADD [mode_epoch] BIGINT NOT NULL DEFAULT 0;
GO

ALTER TABLE [mcp_tokens] ADD [owner_user_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL;
GO

ALTER TABLE [mcp_tokens] ADD [owner_team_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL;
GO

ALTER TABLE [mcp_tokens] ADD [owner_mode_epoch] BIGINT NOT NULL DEFAULT 0;
GO

ALTER TABLE [mcp_tokens] ADD [owner_generation] BIGINT NOT NULL DEFAULT 0;
GO

ALTER TABLE [run_approvals] ADD [access_action] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL;
GO

ALTER TABLE [run_approvals] ADD [access_environment_ids] NVARCHAR(MAX) NULL;
GO

GO
CREATE TABLE [access_execution_nodes] (
 [identity_key] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [tenant_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([identity_key]),
 FOREIGN KEY ([tenant_id]) REFERENCES [access_teams]([id])
);
GO
ALTER TABLE [servers] ADD [execution_binding_json] NVARCHAR(MAX) NULL;
GO

-- pipewright:statement
CREATE TABLE [access_automation_authorizations] (
 [source_kind] NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [source_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [approval_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [tenant_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [user_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL,
 [mode_epoch] BIGINT NOT NULL,
 [user_generation] BIGINT NOT NULL,
 [catalog_version] BIGINT NOT NULL,
 [config_digest] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [permissions_json] NVARCHAR(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 [revoked_at] BIGINT NULL,
 PRIMARY KEY ([source_kind],[source_id]),
 FOREIGN KEY ([tenant_id]) REFERENCES [access_teams]([id]),
 FOREIGN KEY ([user_id]) REFERENCES [access_users]([id])
);
GO

-- pipewright:statement
ALTER TABLE [preview_envs] ADD [binding_json] NVARCHAR(MAX) NULL;
GO
