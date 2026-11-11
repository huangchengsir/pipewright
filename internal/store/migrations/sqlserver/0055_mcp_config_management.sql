-- SQL Server 2019/2022/2025 explicit migration. Unicode BIN2 text; transactional DDL.
-- Cascades use multirow INSTEAD OF DELETE triggers with enforced NO ACTION FKs.
CREATE TABLE [config_control_epochs] (
 [name] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [value] BIGINT NOT NULL DEFAULT 1,
 PRIMARY KEY NONCLUSTERED ([name])
);
GO
CREATE TABLE [mcp_config_approvals] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [draft_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [token_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [base_revision] BIGINT NOT NULL,
 [policy_version] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [validator_version] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [authz_revision] BIGINT NOT NULL,
 [patch_digest] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [review_digest] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [approver] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [expires_at] BIGINT NOT NULL,
 [revoked_at] BIGINT NULL,
 [consumed_by_operation_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [mcp_config_approvals_draft] ON [mcp_config_approvals] ([draft_id], [expires_at]);
GO
CREATE TABLE [mcp_config_drafts] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_token_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [request_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [input_digest] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [base_revision] BIGINT NOT NULL,
 [policy_version] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [validator_version] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [authz_revision] BIGINT NOT NULL,
 [operations_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [patch_digest] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [expires_at] BIGINT NOT NULL,
 [applied_operation_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [mcp_config_drafts_owner] ON [mcp_config_drafts] ([owner_token_id], [project_id], [expires_at]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_mcp_config_drafts_1] ON [mcp_config_drafts] ([owner_token_id], [request_id]);
GO
CREATE TABLE [mcp_config_operations] (
 [id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [token_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [request_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [input_digest] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [draft_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [approval_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [state] NVARCHAR(24) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'pending',
 [reason] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'',
 [before_revision] BIGINT NOT NULL,
 [after_revision] BIGINT NULL,
 [result_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'{}',
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id])
);
GO
CREATE NONCLUSTERED INDEX [mcp_config_operations_draft] ON [mcp_config_operations] ([draft_id]);
GO
CREATE UNIQUE NONCLUSTERED INDEX [uq_mcp_config_operations_1] ON [mcp_config_operations] ([token_id], [request_id]);
GO
CREATE TABLE [project_activity] (
 [activity_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [run_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NULL,
 [owner_epoch] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [kind] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [phase] NVARCHAR(40) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [created_at] BIGINT NOT NULL,
 [heartbeat_at] BIGINT NOT NULL,
 [released_at] BIGINT NULL,
 PRIMARY KEY NONCLUSTERED ([activity_id])
);
GO
CREATE NONCLUSTERED INDEX [project_activity_run] ON [project_activity] ([run_id], [released_at]);
GO
CREATE NONCLUSTERED INDEX [project_activity_project] ON [project_activity] ([project_id], [released_at]);
GO
CREATE TABLE [project_config_versions] (
 [project_id] NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL DEFAULT 1,
 [deleted_at] BIGINT NULL,
 PRIMARY KEY NONCLUSTERED ([project_id])
);
GO
ALTER TABLE [mcp_tokens] ADD [config_grants_json] NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT N'[]';
GO
ALTER TABLE [mcp_tokens] ADD [authz_revision] BIGINT NOT NULL DEFAULT 0;
GO
INSERT INTO project_config_versions(project_id,revision) SELECT id,1 FROM projects;
GO
INSERT INTO config_control_epochs(name,value) VALUES ('policy',1),('secret_catalog',1),('executor',1);
GO
INSERT INTO project_activity(activity_id,project_id,run_id,owner_epoch,kind,phase,created_at,heartbeat_at)
 SELECT CONCAT(N'legacy-',id),project_id,id,'legacy-unreconciled','run','cleanup_unknown',0,0
 FROM pipeline_runs WHERE status IN ('queued','running','waiting_approval');
