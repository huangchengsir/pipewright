-- Optional tenancy schema. Mode and principals are initialized explicitly, never by schema migration.
CREATE TABLE access_users (
 id VARCHAR(64) NOT NULL,
 username VARCHAR(128) NOT NULL,
 username_key VARCHAR(128) NOT NULL,
 display_name VARCHAR(128) NOT NULL,
 password_hash VARCHAR(512) NULL,
 enabled BIGINT NOT NULL,
 instance_admin BIGINT NOT NULL,
 must_change_password BIGINT NOT NULL,
 password_expires_at BIGINT NULL,
 password_consumed_at BIGINT NULL,
 security_generation BIGINT NOT NULL,
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL,
 PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE UNIQUE INDEX uq_access_users_0 ON access_users (username_key);
CREATE TABLE access_teams (
 id VARCHAR(64) NOT NULL,
 name VARCHAR(128) NOT NULL,
 enabled BIGINT NOT NULL,
 revision BIGINT NOT NULL,
 created_at BIGINT NOT NULL,
 PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE access_control (
 id BIGINT NOT NULL,
 enabled BIGINT NOT NULL,
 ever_enabled BIGINT NOT NULL,
 mode_epoch BIGINT NOT NULL,
 authz_revision BIGINT NOT NULL,
 catalog_version BIGINT NOT NULL DEFAULT 1,
 bootstrap_user_id VARCHAR(64) NULL,
 default_team_id VARCHAR(64) NULL,
 PRIMARY KEY (id),
 FOREIGN KEY (bootstrap_user_id) REFERENCES access_users (id),
 FOREIGN KEY (default_team_id) REFERENCES access_teams (id),
 CHECK (id=1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE access_members (
 id VARCHAR(64) NOT NULL,
 tenant_id VARCHAR(64) NOT NULL,
 user_id VARCHAR(64) NOT NULL,
 enabled BIGINT NOT NULL,
 revision BIGINT NOT NULL,
 PRIMARY KEY (id),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id) REFERENCES access_teams (id),
 FOREIGN KEY (user_id) REFERENCES access_users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE UNIQUE INDEX uq_access_members_0 ON access_members (tenant_id, user_id);
CREATE TABLE access_roles (
 id VARCHAR(64) NOT NULL,
 tenant_id VARCHAR(64) NOT NULL,
 name VARCHAR(128) NOT NULL,
 preset_key VARCHAR(64) NOT NULL,
 seed_version BIGINT NOT NULL,
 requires_selected_binding BIGINT NOT NULL,
 deleted BIGINT NOT NULL,
 revision BIGINT NOT NULL,
 pages_json LONGTEXT NOT NULL,
 grants_json LONGTEXT NOT NULL,
 PRIMARY KEY (id),
 UNIQUE (tenant_id, id),
 FOREIGN KEY (tenant_id) REFERENCES access_teams (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE access_role_seeds (
 tenant_id VARCHAR(64) NOT NULL,
 preset_key VARCHAR(64) NOT NULL,
 role_id VARCHAR(64) NOT NULL,
 seed_version BIGINT NOT NULL,
 PRIMARY KEY (tenant_id, preset_key),
 FOREIGN KEY (tenant_id) REFERENCES access_teams (id),
 FOREIGN KEY (tenant_id, role_id) REFERENCES access_roles (tenant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE access_bindings (
 id VARCHAR(64) NOT NULL,
 tenant_id VARCHAR(64) NOT NULL,
 member_id VARCHAR(64) NOT NULL,
 role_id VARCHAR(64) NOT NULL,
 scope_json LONGTEXT NOT NULL,
 PRIMARY KEY (id),
 FOREIGN KEY (tenant_id) REFERENCES access_teams (id),
 FOREIGN KEY (tenant_id, member_id) REFERENCES access_members (tenant_id, id),
 FOREIGN KEY (tenant_id, role_id) REFERENCES access_roles (tenant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE UNIQUE INDEX uq_access_bindings_0 ON access_bindings (member_id, role_id);
CREATE TABLE access_ownership (
 resource_kind VARCHAR(64) NOT NULL,
 resource_id VARCHAR(128) NOT NULL,
 tenant_id VARCHAR(64) NOT NULL,
 owner_id VARCHAR(64) NULL,
 project_id VARCHAR(64) NULL,
 environment_id VARCHAR(64) NULL,
 PRIMARY KEY (resource_kind, resource_id),
 FOREIGN KEY (tenant_id) REFERENCES access_teams (id),
 FOREIGN KEY (owner_id) REFERENCES access_users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE access_sessions (
 token_hash VARCHAR(64) NOT NULL,
 user_id VARCHAR(64) NOT NULL,
 csrf_token VARCHAR(128) NOT NULL,
 mode_epoch BIGINT NOT NULL,
 security_generation BIGINT NOT NULL,
 auth_source VARCHAR(64) NOT NULL,
 created_at BIGINT NOT NULL,
 expires_at BIGINT NOT NULL,
 last_seen_at BIGINT NOT NULL,
 password_verified_at BIGINT NULL,
 PRIMARY KEY (token_hash),
 FOREIGN KEY (user_id) REFERENCES access_users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE access_identities (
 id VARCHAR(64) NOT NULL,
 user_id VARCHAR(64) NOT NULL,
 provider VARCHAR(32) NOT NULL,
 issuer VARCHAR(512) NOT NULL,
 subject VARCHAR(256) NOT NULL,
 identity_key VARCHAR(64) NOT NULL,
 display_label VARCHAR(512) NOT NULL DEFAULT '',
 generation BIGINT NOT NULL,
 created_at BIGINT NOT NULL,
 PRIMARY KEY (id),
 FOREIGN KEY (user_id) REFERENCES access_users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE UNIQUE INDEX uq_access_identities_0 ON access_identities (identity_key);

CREATE TABLE access_environments (
 id VARCHAR(64) NOT NULL,
 tenant_id VARCHAR(64) NOT NULL,
 project_id VARCHAR(64) NOT NULL,
 name VARCHAR(128) NOT NULL,
 purpose VARCHAR(32) NOT NULL,
 revision BIGINT NOT NULL,
 PRIMARY KEY (id),
 UNIQUE (tenant_id, project_id, name),
 FOREIGN KEY (tenant_id) REFERENCES access_teams (id),
 CHECK (purpose IN ('test','production','other'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;


-- pipewright:statement
CREATE TABLE access_run_authorizations (
 mcp_token_id VARCHAR(64) NULL,
 run_id VARCHAR(64) NOT NULL,
 tenant_id VARCHAR(64) NOT NULL,
 user_id VARCHAR(64) NOT NULL,
 project_id VARCHAR(64) NOT NULL,
 mode_epoch BIGINT NOT NULL,
 user_generation BIGINT NOT NULL,
 config_revision BIGINT NOT NULL,
 catalog_version BIGINT NOT NULL,
 plan_digest VARCHAR(64) NOT NULL,
 permissions_json LONGTEXT NOT NULL,
 created_at BIGINT NOT NULL,
 revoked_at BIGINT NULL,
 PRIMARY KEY (run_id),
 FOREIGN KEY (run_id) REFERENCES pipeline_runs (id),
 FOREIGN KEY (tenant_id) REFERENCES access_teams (id),
 FOREIGN KEY (user_id) REFERENCES access_users (id),
 FOREIGN KEY (project_id) REFERENCES projects (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- Team-local reusable asset names; legacy single-admin rows retain their namespace.
ALTER TABLE pipeline_templates ADD COLUMN name_scope VARCHAR(64) NOT NULL DEFAULT '__single__';
-- pipewright:statement
DROP INDEX idx_pipeline_templates_name ON pipeline_templates;
-- pipewright:statement
CREATE UNIQUE INDEX idx_pipeline_templates_name ON pipeline_templates (name_scope,name);
-- pipewright:statement
ALTER TABLE variable_groups ADD COLUMN name_scope VARCHAR(64) NOT NULL DEFAULT '__single__';
-- pipewright:statement
DROP INDEX idx_variable_groups_name ON variable_groups;
-- pipewright:statement
CREATE UNIQUE INDEX idx_variable_groups_name ON variable_groups (name_scope,name);
-- pipewright:statement
ALTER TABLE custom_nodes ADD COLUMN name_scope VARCHAR(64) NOT NULL DEFAULT '__single__';
-- pipewright:statement
DROP INDEX idx_custom_nodes_name ON custom_nodes;
-- pipewright:statement
CREATE UNIQUE INDEX idx_custom_nodes_name ON custom_nodes (name_scope,name);
-- pipewright:statement

CREATE UNIQUE INDEX uq_access_identities_user_provider ON access_identities (user_id, provider);
-- pipewright:statement
ALTER TABLE login_flows ADD COLUMN access_user_id VARCHAR(64) NULL;
-- pipewright:statement
ALTER TABLE login_flows ADD COLUMN mode_epoch BIGINT NOT NULL DEFAULT 0;

ALTER TABLE mcp_tokens ADD COLUMN owner_user_id VARCHAR(64) NULL;
-- pipewright:statement

ALTER TABLE mcp_tokens ADD COLUMN owner_team_id VARCHAR(64) NULL;
-- pipewright:statement

ALTER TABLE mcp_tokens ADD COLUMN owner_mode_epoch BIGINT NOT NULL DEFAULT 0;
-- pipewright:statement

ALTER TABLE mcp_tokens ADD COLUMN owner_generation BIGINT NOT NULL DEFAULT 0;
-- pipewright:statement

ALTER TABLE run_approvals ADD COLUMN access_action VARCHAR(64) NULL;
-- pipewright:statement

ALTER TABLE run_approvals ADD COLUMN access_environment_ids LONGTEXT NULL;
-- pipewright:statement

-- pipewright:statement
CREATE TABLE access_execution_nodes (
 identity_key VARCHAR(64) NOT NULL,
 tenant_id VARCHAR(64) NOT NULL,
 created_at BIGINT NOT NULL,
 PRIMARY KEY (identity_key),
 FOREIGN KEY (tenant_id) REFERENCES access_teams(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
-- pipewright:statement
ALTER TABLE servers ADD COLUMN execution_binding_json LONGTEXT NULL;
-- pipewright:statement

-- pipewright:statement
CREATE TABLE access_automation_authorizations (
 source_kind VARCHAR(32) NOT NULL,
 source_id VARCHAR(64) NOT NULL,
 approval_id VARCHAR(64) NOT NULL,
 tenant_id VARCHAR(64) NOT NULL,
 user_id VARCHAR(64) NOT NULL,
 project_id VARCHAR(64) NULL,
 mode_epoch BIGINT NOT NULL,
 user_generation BIGINT NOT NULL,
 catalog_version BIGINT NOT NULL,
 config_digest VARCHAR(64) NOT NULL,
 permissions_json TEXT NOT NULL,
 created_at BIGINT NOT NULL,
 revoked_at BIGINT NULL,
 PRIMARY KEY (source_kind,source_id),
 FOREIGN KEY (tenant_id) REFERENCES access_teams(id),
 FOREIGN KEY (user_id) REFERENCES access_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- pipewright:statement
ALTER TABLE preview_envs ADD COLUMN binding_json LONGTEXT NULL;
