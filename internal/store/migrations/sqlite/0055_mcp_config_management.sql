-- Configuration control and durable request identities intentionally have no
-- cascading project/token/run foreign keys. Retention must not permit replay.
ALTER TABLE mcp_tokens ADD COLUMN config_grants_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE mcp_tokens ADD COLUMN authz_revision INTEGER NOT NULL DEFAULT 0;

CREATE TABLE project_config_versions (
 project_id TEXT PRIMARY KEY COLLATE BINARY, revision INTEGER NOT NULL DEFAULT 1,
 deleted_at INTEGER
);
INSERT INTO project_config_versions(project_id,revision) SELECT id,1 FROM projects;
CREATE TABLE config_control_epochs (name TEXT PRIMARY KEY COLLATE BINARY,value INTEGER NOT NULL DEFAULT 1);
INSERT INTO config_control_epochs(name,value) VALUES ('policy',1),('secret_catalog',1),('executor',1);

CREATE TABLE project_activity (
 activity_id TEXT PRIMARY KEY COLLATE BINARY,project_id TEXT NOT NULL COLLATE BINARY,
 run_id TEXT,owner_epoch TEXT NOT NULL COLLATE BINARY,kind TEXT NOT NULL,phase TEXT NOT NULL,
 created_at INTEGER NOT NULL,heartbeat_at INTEGER NOT NULL,released_at INTEGER
);
CREATE INDEX project_activity_project ON project_activity(project_id,released_at);
CREATE INDEX project_activity_run ON project_activity(run_id,released_at);
INSERT INTO project_activity(activity_id,project_id,run_id,owner_epoch,kind,phase,created_at,heartbeat_at)
 SELECT 'legacy-' || id,project_id,id,'legacy-unreconciled','run','cleanup_unknown',0,0
 FROM pipeline_runs WHERE status IN ('queued','running','waiting_approval');

CREATE TABLE mcp_config_drafts (
 id TEXT PRIMARY KEY COLLATE BINARY,owner_token_id TEXT NOT NULL COLLATE BINARY,
 project_id TEXT NOT NULL COLLATE BINARY,request_id TEXT NOT NULL COLLATE BINARY,
 input_digest TEXT NOT NULL,base_revision INTEGER NOT NULL,policy_version TEXT NOT NULL,
 validator_version TEXT NOT NULL,authz_revision INTEGER NOT NULL,
 operations_json TEXT NOT NULL,patch_digest TEXT NOT NULL,created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,applied_operation_id TEXT,
 UNIQUE(owner_token_id,request_id)
);
CREATE INDEX mcp_config_drafts_owner ON mcp_config_drafts(owner_token_id,project_id,expires_at);
CREATE TABLE mcp_config_approvals (
 id TEXT PRIMARY KEY COLLATE BINARY,draft_id TEXT NOT NULL COLLATE BINARY,
 token_id TEXT NOT NULL COLLATE BINARY,project_id TEXT NOT NULL COLLATE BINARY,
 base_revision INTEGER NOT NULL,policy_version TEXT NOT NULL,validator_version TEXT NOT NULL,
 authz_revision INTEGER NOT NULL,patch_digest TEXT NOT NULL,review_digest TEXT NOT NULL,
 approver TEXT NOT NULL,created_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,
 revoked_at INTEGER,consumed_by_operation_id TEXT
);
CREATE INDEX mcp_config_approvals_draft ON mcp_config_approvals(draft_id,expires_at);
CREATE TABLE mcp_config_operations (
 id TEXT PRIMARY KEY COLLATE BINARY,token_id TEXT NOT NULL COLLATE BINARY,
 project_id TEXT NOT NULL COLLATE BINARY,request_id TEXT NOT NULL COLLATE BINARY,
 input_digest TEXT NOT NULL,draft_id TEXT NOT NULL,approval_id TEXT NOT NULL,
 owner_epoch TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'pending',reason TEXT NOT NULL DEFAULT '',
 before_revision INTEGER NOT NULL,after_revision INTEGER,result_json TEXT NOT NULL DEFAULT '{}',
 created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,
 UNIQUE(token_id,request_id)
);
CREATE INDEX mcp_config_operations_draft ON mcp_config_operations(draft_id);
